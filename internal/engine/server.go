package engine

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/dockside-gg/game-panel/internal/config"
	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/go-chi/chi/v5"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type Server struct {
	cfg             config.Config
	docker          *client.Client
	logger          *slog.Logger
	telemetryMu     sync.RWMutex
	hostTelemetry   sampledHostTelemetry
	telemetryCancel context.CancelFunc
	telemetryDone   chan struct{}
	consoleMu       sync.RWMutex
	provisionLogs   map[string]*provisionLogHub
}

func New(cfg config.Config, logger *slog.Logger) (*Server, error) {
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	server := &Server{
		cfg: cfg, docker: dockerClient, logger: logger, telemetryDone: make(chan struct{}),
		provisionLogs: make(map[string]*provisionLogHub),
	}
	ctx, cancel := context.WithCancel(context.Background())
	server.telemetryCancel = cancel
	go server.sampleHostTelemetry(ctx)
	return server, nil
}

func (s *Server) Close() error {
	if s.telemetryCancel != nil {
		s.telemetryCancel()
		<-s.telemetryDone
	}
	return s.docker.Close()
}

func (s *Server) Handler() http.Handler {
	router := chi.NewRouter()
	router.Get("/health/live", s.live)
	router.Get("/health/ready", s.ready)
	router.Group(func(api chi.Router) {
		api.Use(s.authenticate)
		api.Get("/v1/host", s.host)
		api.Get("/v1/system/containers", s.systemContainers)
		api.Post("/v1/system/containers/worker/restart", s.restartWorker)
		api.Get("/v1/servers/stats", s.serverStats)
		api.Get("/v1/servers/log-events", s.serverLogEvents)
		api.Get("/v1/servers/{serverID}/console", s.consoleLogs)
		api.Post("/v1/servers/{serverID}/console", s.consoleCommand)
		api.Get("/v1/servers/{serverID}/files", s.listFiles)
		api.Get("/v1/servers/{serverID}/files/download", s.downloadFile)
		api.Delete("/v1/servers/{serverID}/files", s.deleteFile)
		api.Get("/v1/servers/{serverID}/files/content", s.readFile)
		api.Put("/v1/servers/{serverID}/files/content", s.writeFile)
		api.Post("/v1/servers/{serverID}/files/upload", s.uploadFile)
		api.Post("/v1/servers/{serverID}/files/directories", s.createDirectory)
		api.Post("/v1/servers/{serverID}/backups/{backupID}", s.createBackup)
		api.Get("/v1/servers/{serverID}/backups/{backupID}/download", s.downloadBackup)
		api.Post("/v1/servers/{serverID}/backups/{backupID}/restore", s.restoreBackup)
		api.Delete("/v1/servers/{serverID}/backups/{backupID}", s.deleteBackup)
		api.Post("/v1/servers/{serverID}/databases/{databaseID}", s.createDatabase)
		api.Delete("/v1/servers/{serverID}/databases/{databaseID}", s.deleteDatabase)
		api.Post("/v1/servers/{serverID}/databases/{databaseID}/password", s.rotateDatabasePassword)
		api.Post("/v1/servers/{serverID}/power", s.power)
		api.Post("/v1/servers/{serverID}/provision", s.provision)
		api.Put("/v1/servers/{serverID}/configuration", s.reconfigure)
		api.Delete("/v1/servers/{serverID}", s.deleteServer)
	})
	return router
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "component": "engine"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if _, err := s.docker.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "docker": "down"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "docker": "up"})
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := "Bearer " + s.cfg.EngineToken
		actual := r.Header.Get("Authorization")
		if len(expected) != len(actual) || subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) host(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	info, err := s.docker.Info(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "docker engine unavailable"})
		return
	}
	version, err := s.docker.ServerVersion(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "docker engine version unavailable"})
		return
	}
	status := engineclient.HostStatus{
		Connected:         true,
		InstanceID:        s.cfg.InstanceID,
		EngineVersion:     version.Version,
		APIVersion:        version.APIVersion,
		OperatingSystem:   info.OperatingSystem,
		Architecture:      info.Architecture,
		KernelVersion:     info.KernelVersion,
		CPUs:              info.NCPU,
		MemoryBytes:       info.MemTotal,
		Containers:        info.Containers,
		ContainersRunning: info.ContainersRunning,
		ContainersStopped: info.ContainersStopped,
		Images:            info.Images,
		SecurityOptions:   info.SecurityOptions,
		Warnings:          info.Warnings,
		ObservedAt:        time.Now().UTC(),
	}
	s.telemetryMu.RLock()
	telemetry := s.hostTelemetry
	s.telemetryMu.RUnlock()
	if telemetry.Available {
		status.CPUUsagePercent = telemetry.CPUUsagePercent
		status.MemoryUsedBytes = telemetry.MemoryUsedBytes
		status.MemoryAvailableBytes = telemetry.MemoryAvailableBytes
		status.Load1 = telemetry.Load1
		status.Load5 = telemetry.Load5
		status.Load15 = telemetry.Load15
		status.TelemetryAvailable = true
		status.TelemetryScope = "docker-host"
		status.ObservedAt = telemetry.ObservedAt
	}
	if usage, err := filesystemUsage(s.cfg.ServerDataRoot); err == nil {
		status.DataFilesystem = &usage
	}
	if usage, err := filesystemUsage(s.cfg.BackupRoot); err == nil {
		status.BackupFilesystem = &usage
	}
	writeJSON(w, http.StatusOK, status)
}

type powerRequest struct {
	Action string `json:"action"`
}

func (s *Server) power(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if !uuidPattern.MatchString(serverID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server id"})
		return
	}
	var input powerRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	containerID, err := s.findManagedServer(r.Context(), serverID)
	if err != nil {
		if errors.Is(err, errNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "managed server container not found"})
			return
		}
		s.logger.Error("find managed server failed", "server_id", serverID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "docker operation failed"})
		return
	}
	timeout := 30
	switch input.Action {
	case "start":
		err = s.docker.ContainerStart(r.Context(), containerID, container.StartOptions{})
	case "stop":
		err = s.docker.ContainerStop(r.Context(), containerID, container.StopOptions{Timeout: &timeout})
	case "restart":
		err = s.docker.ContainerRestart(r.Context(), containerID, container.StopOptions{Timeout: &timeout})
	case "kill":
		err = s.docker.ContainerKill(r.Context(), containerID, "KILL")
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid power action"})
		return
	}
	if err != nil {
		s.logger.Error("power action failed", "server_id", serverID, "action", input.Action, "error", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "power action failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var errNotFound = errors.New("not found")

func (s *Server) findManagedServer(ctx context.Context, serverID string) (string, error) {
	filter := filters.NewArgs(
		filters.Arg("label", "gg.dockside.managed=true"),
		filters.Arg("label", "gg.dockside.instance="+s.cfg.InstanceID),
		filters.Arg("label", "gg.dockside.server="+serverID),
		filters.Arg("label", "gg.dockside.kind=server"),
	)
	containers, err := s.docker.ContainerList(ctx, container.ListOptions{All: true, Filters: filter})
	if err != nil {
		return "", err
	}
	if len(containers) != 1 {
		return "", errNotFound
	}
	return containers[0].ID, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
