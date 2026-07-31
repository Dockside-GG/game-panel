package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/go-chi/chi/v5"
)

var allowedSystemComponents = map[string]bool{
	"gateway":  true,
	"app":      true,
	"worker":   true,
	"engine":   true,
	"postgres": true,
}

func (s *Server) systemContainerLogs(w http.ResponseWriter, r *http.Request) {
	component := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "component")))
	if !allowedSystemComponents[component] {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "system component not found"})
		return
	}
	tail := 250
	if value := strings.TrimSpace(r.URL.Query().Get("tail")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 20 || parsed > 2000 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tail must be 20-2000"})
			return
		}
		tail = parsed
	}
	managed, err := s.listSystemContainers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "could not list system containers"})
		return
	}
	containerID := ""
	for _, summary := range managed {
		if summary.Labels["gg.dockside.component"] == component {
			if containerID != "" {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "multiple matching system containers found"})
				return
			}
			containerID = summary.ID
		}
	}
	if containerID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "system container not found"})
		return
	}
	logs, err := s.docker.ContainerLogs(r.Context(), containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "system container logs unavailable"})
		return
	}
	defer logs.Close()
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	if _, err := stdcopy.StdCopy(stdout, stderr, io.LimitReader(logs, 4<<20)); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "system container logs could not be decoded"})
		return
	}
	writeJSON(w, http.StatusOK, engineclient.SystemContainerLogs{
		Component:  component,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		ObservedAt: time.Now().UTC(),
	})
}

func (s *Server) systemContainers(w http.ResponseWriter, r *http.Request) {
	managed, err := s.listSystemContainers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "could not list system containers"})
		return
	}
	result := make([]engineclient.SystemContainer, len(managed))
	semaphore := make(chan struct{}, 5)
	var group sync.WaitGroup
	for index, summary := range managed {
		index, summary := index, summary
		group.Add(1)
		go func() {
			defer group.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			result[index] = s.readSystemContainerStats(r.Context(), summary)
		}()
	}
	group.Wait()
	sort.Slice(result, func(i, j int) bool { return result[i].Component < result[j].Component })
	writeJSON(w, http.StatusOK, map[string]any{
		"containers":  result,
		"observed_at": time.Now().UTC(),
	})
}

func (s *Server) listSystemContainers(ctx context.Context) ([]types.Container, error) {
	filter := filters.NewArgs(
		filters.Arg("label", "gg.dockside.system=true"),
		filters.Arg("label", "gg.dockside.instance="+s.cfg.InstanceID),
	)
	found, err := s.docker.ContainerList(ctx, container.ListOptions{All: true, Filters: filter})
	if err != nil {
		return nil, err
	}
	result := found[:0]
	for _, summary := range found {
		component := summary.Labels["gg.dockside.component"]
		if allowedSystemComponents[component] {
			result = append(result, summary)
		}
	}
	return result, nil
}

func (s *Server) readSystemContainerStats(parent context.Context, summary types.Container) engineclient.SystemContainer {
	observed := engineclient.SystemContainer{
		Component:   summary.Labels["gg.dockside.component"],
		ContainerID: summary.ID,
		Image:       summary.Image,
		State:       summary.State,
		Health:      "unknown",
		ObservedAt:  time.Now().UTC(),
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	inspected, err := s.docker.ContainerInspect(ctx, summary.ID)
	if err != nil {
		observed.Error = "container inspection failed"
		return observed
	}
	observed.RestartCount = inspected.RestartCount
	if inspected.State != nil {
		observed.State = inspected.State.Status
		if inspected.State.Health != nil {
			observed.Health = inspected.State.Health.Status
		}
		if started, err := time.Parse(time.RFC3339Nano, inspected.State.StartedAt); err == nil && !started.IsZero() {
			observed.StartedAt = &started
		}
		if !inspected.State.Running {
			return observed
		}
	}
	response, err := s.docker.ContainerStatsOneShot(ctx, summary.ID)
	if err != nil {
		observed.Error = "container stats unavailable"
		return observed
	}
	defer response.Body.Close()
	var stats container.StatsResponse
	if err := json.NewDecoder(response.Body).Decode(&stats); err != nil {
		observed.Error = "container stats decode failed"
		return observed
	}
	observed.CPUPercent = calculateCPU(stats)
	usage := stats.MemoryStats.Usage
	if cache := stats.MemoryStats.Stats["inactive_file"]; usage > cache {
		usage -= cache
	}
	observed.MemoryBytes = int64(usage)
	observed.MemoryLimitBytes = int64(stats.MemoryStats.Limit)
	for _, network := range stats.Networks {
		observed.NetworkRXBytes += int64(network.RxBytes)
		observed.NetworkTXBytes += int64(network.TxBytes)
	}
	for _, entry := range stats.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(entry.Op) {
		case "read":
			observed.BlockReadBytes += int64(entry.Value)
		case "write":
			observed.BlockWriteBytes += int64(entry.Value)
		}
	}
	observed.ObservedAt = time.Now().UTC()
	return observed
}

func (s *Server) restartWorker(w http.ResponseWriter, r *http.Request) {
	managed, err := s.listSystemContainers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "could not locate worker"})
		return
	}
	workerID := ""
	for _, summary := range managed {
		if summary.Labels["gg.dockside.component"] == "worker" {
			if workerID != "" {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "multiple Dockside workers found"})
				return
			}
			workerID = summary.ID
		}
	}
	if workerID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Dockside worker not found"})
		return
	}
	timeout := 30
	if err := s.docker.ContainerRestart(r.Context(), workerID, container.StopOptions{Timeout: &timeout}); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "worker restart failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
