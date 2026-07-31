package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/go-chi/chi/v5"
)

const (
	databaseImage = "postgres:18.4-alpine"
	databaseHost  = "dockside-db"
	databasePort  = 5432
)

var databaseIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

func (s *Server) createDatabase(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	databaseID := chi.URLParam(r, "databaseID")
	if !uuidPattern.MatchString(serverID) || !uuidPattern.MatchString(databaseID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid identifier"})
		return
	}
	var input engineclient.DatabaseRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || validateDatabaseRequest(input) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid database request"})
		return
	}
	names := s.resourceNames(serverID)
	containerID, err := s.ensureDatabaseHost(r.Context(), serverID, names, input.AdminPassword)
	if err != nil {
		s.logger.Error("ensure database host failed", "server_id", serverID, "error", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "could not prepare database host"})
		return
	}
	if err := s.createPostgresDatabase(
		r.Context(), containerID, input.Name, input.Username, input.Password,
	); err != nil {
		s.logger.Error(
			"create scoped database failed",
			"server_id", serverID,
			"database_id", databaseID,
			"error", err,
		)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "could not create database"})
		return
	}
	writeJSON(w, http.StatusCreated, engineclient.DatabaseResult{
		ContainerID: containerID,
		VolumeName:  names.dbVolume,
		Host:        databaseHost,
		Port:        databasePort,
	})
}

func (s *Server) deleteDatabase(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	databaseID := chi.URLParam(r, "databaseID")
	if !uuidPattern.MatchString(serverID) || !uuidPattern.MatchString(databaseID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid identifier"})
		return
	}
	var input engineclient.DatabaseRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || validateDatabaseRequest(input) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid database request"})
		return
	}
	names := s.resourceNames(serverID)
	containerID, err := s.databaseHostContainer(r.Context(), serverID, names)
	if err != nil {
		if errors.Is(err, errNotFound) && r.URL.Query().Get("remove_host") == "true" {
			if err := s.removeDatabaseHost(r.Context(), serverID, names); err != nil {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "could not remove database host"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "database host not found"})
		return
	}
	if err := s.dropPostgresDatabase(r.Context(), containerID, input.Name, input.Username); err != nil {
		s.logger.Error(
			"drop scoped database failed",
			"server_id", serverID,
			"database_id", databaseID,
			"error", err,
		)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "could not delete database"})
		return
	}
	if r.URL.Query().Get("remove_host") == "true" {
		if err := s.removeDatabaseHost(r.Context(), serverID, names); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "database deleted but host cleanup failed"})
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateDatabasePassword(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	databaseID := chi.URLParam(r, "databaseID")
	if !uuidPattern.MatchString(serverID) || !uuidPattern.MatchString(databaseID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid identifier"})
		return
	}
	var input engineclient.DatabaseRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || validateDatabaseRequest(input) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid database request"})
		return
	}
	containerID, err := s.databaseHostContainer(r.Context(), serverID, s.resourceNames(serverID))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "database host not found"})
		return
	}
	query := "ALTER ROLE " + input.Username + " PASSWORD " + postgresLiteral(input.Password)
	if _, err := s.postgresExec(r.Context(), containerID, query); err != nil {
		s.logger.Error(
			"rotate database password failed",
			"server_id", serverID,
			"database_id", databaseID,
			"error", err,
		)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "could not rotate password"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateDatabaseRequest(input engineclient.DatabaseRequest) error {
	if !databaseIdentifierPattern.MatchString(input.Name) ||
		!databaseIdentifierPattern.MatchString(input.Username) ||
		len(input.Password) < 16 || len(input.Password) > 512 ||
		len(input.AdminPassword) < 16 || len(input.AdminPassword) > 512 ||
		strings.ContainsAny(input.Password+input.AdminPassword, "\x00\r\n") {
		return errors.New("invalid database configuration")
	}
	return nil
}

func (s *Server) ensureDatabaseHost(
	ctx context.Context,
	serverID string,
	names resourceNameSet,
	adminPassword string,
) (string, error) {
	if _, err := s.findManagedServer(ctx, serverID); err != nil {
		return "", fmt.Errorf("game server container is required: %w", err)
	}
	if existing, err := s.databaseHostContainer(ctx, serverID, names); err == nil {
		inspected, inspectErr := s.docker.ContainerInspect(ctx, existing)
		if inspectErr != nil {
			return "", inspectErr
		}
		if inspected.State == nil || !inspected.State.Running {
			if err := s.docker.ContainerStart(ctx, existing, container.StartOptions{}); err != nil {
				return "", fmt.Errorf("start database host: %w", err)
			}
		}
		if err := s.waitForPostgres(ctx, existing); err != nil {
			return "", err
		}
		return existing, nil
	} else if !errors.Is(err, errNotFound) {
		return "", err
	}
	if err := s.pullImage(ctx, databaseImage); err != nil {
		return "", fmt.Errorf("pull database image: %w", err)
	}
	labels := s.managedLabels(serverID)
	labels["gg.dockside.kind"] = "database-volume"
	if _, err := s.docker.VolumeCreate(ctx, volume.CreateOptions{
		Name: names.dbVolume, Labels: labels,
	}); err != nil {
		return "", fmt.Errorf("create database volume: %w", err)
	}
	cleanupVolume := true
	defer func() {
		if cleanupVolume {
			_ = s.docker.VolumeRemove(context.WithoutCancel(ctx), names.dbVolume, true)
		}
	}()
	if err := s.initializeDatabaseVolume(ctx, serverID, names.dbVolume); err != nil {
		return "", err
	}
	containerLabels := s.managedLabels(serverID)
	containerLabels["gg.dockside.kind"] = "database"
	created, err := s.docker.ContainerCreate(
		ctx,
		&container.Config{
			Image: databaseImage,
			User:  "70:70",
			Env: []string{
				"POSTGRES_USER=dockside_admin",
				"POSTGRES_PASSWORD=" + adminPassword,
				"POSTGRES_DB=postgres",
				"PGDATA=/var/lib/postgresql/data/pgdata",
			},
			Labels: containerLabels,
		},
		&container.HostConfig{
			NetworkMode: container.NetworkMode(names.network),
			Mounts: []mount.Mount{{
				Type: mount.TypeVolume, Source: names.dbVolume,
				Target:        "/var/lib/postgresql/data",
				VolumeOptions: &mount.VolumeOptions{NoCopy: true},
			}},
			CapDrop:       []string{"ALL"},
			SecurityOpt:   []string{"no-new-privileges:true"},
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
			LogConfig: container.LogConfig{
				Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"},
			},
		},
		&network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			names.network: {Aliases: []string{databaseHost}},
		}},
		nil,
		names.dbContainer,
	)
	if err != nil {
		return "", fmt.Errorf("create database container: %w", err)
	}
	cleanupContainer := true
	defer func() {
		if cleanupContainer {
			_ = s.docker.ContainerRemove(
				context.WithoutCancel(ctx),
				created.ID,
				container.RemoveOptions{Force: true},
			)
		}
	}()
	if err := s.docker.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start database container: %w", err)
	}
	if err := s.waitForPostgres(ctx, created.ID); err != nil {
		return "", err
	}
	cleanupContainer = false
	cleanupVolume = false
	return created.ID, nil
}

func (s *Server) initializeDatabaseVolume(
	ctx context.Context,
	serverID, volumeName string,
) error {
	labels := s.managedLabels(serverID)
	labels["gg.dockside.kind"] = "database-volume-initializer"
	created, err := s.docker.ContainerCreate(
		ctx,
		&container.Config{
			Image:      fileHelperImage,
			User:       "0:0",
			Entrypoint: []string{"sh", "-c"},
			Cmd:        []string{"chown -R 70:70 /mnt/database && chmod 0700 /mnt/database"},
			Labels:     labels,
		},
		&container.HostConfig{
			NetworkMode:    "none",
			ReadonlyRootfs: true,
			CapDrop:        []string{"ALL"},
			CapAdd:         []string{"CHOWN", "FOWNER", "DAC_OVERRIDE"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			Mounts: []mount.Mount{{
				Type: mount.TypeVolume, Source: volumeName, Target: "/mnt/database",
			}},
		},
		nil,
		nil,
		"",
	)
	if err != nil {
		return fmt.Errorf("create database volume initializer: %w", err)
	}
	defer s.docker.ContainerRemove(
		context.WithoutCancel(ctx),
		created.ID,
		container.RemoveOptions{Force: true},
	)
	if err := s.docker.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start database volume initializer: %w", err)
	}
	statusCh, errorCh := s.docker.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case waitErr := <-errorCh:
		return waitErr
	case status := <-statusCh:
		if status.StatusCode != 0 {
			return fmt.Errorf("database volume initializer exited with status %d", status.StatusCode)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (s *Server) waitForPostgres(ctx context.Context, containerID string) error {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := s.execContainer(checkCtx, containerID, []string{
			"pg_isready", "-U", "dockside_admin", "-d", "postgres",
		})
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return errors.New("database host did not become ready")
}

func (s *Server) createPostgresDatabase(
	ctx context.Context,
	containerID, name, username, password string,
) error {
	roleExists, err := s.postgresExec(
		ctx,
		containerID,
		"SELECT 1 FROM pg_roles WHERE rolname = "+postgresLiteral(username),
	)
	if err != nil {
		return err
	}
	if strings.TrimSpace(roleExists) == "1" {
		if _, err := s.postgresExec(
			ctx,
			containerID,
			"ALTER ROLE "+username+" PASSWORD "+postgresLiteral(password),
		); err != nil {
			return err
		}
	} else if _, err := s.postgresExec(
		ctx,
		containerID,
		"CREATE ROLE "+username+" LOGIN PASSWORD "+postgresLiteral(password),
	); err != nil {
		return err
	}
	databaseExists, err := s.postgresExec(
		ctx,
		containerID,
		"SELECT 1 FROM pg_database WHERE datname = "+postgresLiteral(name),
	)
	if err != nil {
		return err
	}
	if strings.TrimSpace(databaseExists) != "1" {
		if _, err := s.postgresExec(
			ctx,
			containerID,
			"CREATE DATABASE "+name+" OWNER "+username,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) dropPostgresDatabase(
	ctx context.Context,
	containerID, name, username string,
) error {
	if _, err := s.postgresExec(
		ctx,
		containerID,
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = "+
			postgresLiteral(name)+" AND pid <> pg_backend_pid()",
	); err != nil {
		return err
	}
	if _, err := s.postgresExec(ctx, containerID, "DROP DATABASE IF EXISTS "+name); err != nil {
		return err
	}
	_, err := s.postgresExec(ctx, containerID, "DROP ROLE IF EXISTS "+username)
	return err
}

func (s *Server) postgresExec(
	ctx context.Context,
	containerID, query string,
) (string, error) {
	return s.execContainer(ctx, containerID, []string{
		"psql", "-v", "ON_ERROR_STOP=1", "-U", "dockside_admin", "-d", "postgres",
		"-tAc", query,
	})
}

func (s *Server) execContainer(
	ctx context.Context,
	containerID string,
	command []string,
) (string, error) {
	created, err := s.docker.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          command,
	})
	if err != nil {
		return "", err
	}
	attached, err := s.docker.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", err
	}
	defer attached.Close()
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(
		&stdout,
		&stderr,
		io.LimitReader(attached.Reader, 1<<20),
	); err != nil {
		return "", err
	}
	inspected, err := s.docker.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return "", err
	}
	if inspected.ExitCode != 0 {
		return "", fmt.Errorf(
			"database command exited %d: %s",
			inspected.ExitCode,
			strings.TrimSpace(stderr.String()),
		)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (s *Server) databaseHostContainer(
	ctx context.Context,
	serverID string,
	names resourceNameSet,
) (string, error) {
	inspected, err := s.docker.ContainerInspect(ctx, names.dbContainer)
	if errdefs.IsNotFound(err) {
		return "", errNotFound
	}
	if err != nil {
		return "", err
	}
	if !labelsMatch(inspected.Config.Labels, s.cfg.InstanceID, serverID) ||
		inspected.Config.Labels["gg.dockside.kind"] != "database" {
		return "", errors.New("refusing database container without matching managed labels")
	}
	return inspected.ID, nil
}

func (s *Server) removeDatabaseHost(
	ctx context.Context,
	serverID string,
	names resourceNameSet,
) error {
	containerID, err := s.databaseHostContainer(ctx, serverID, names)
	if err == nil {
		if err := s.docker.ContainerRemove(
			ctx,
			containerID,
			container.RemoveOptions{Force: true},
		); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("remove database container: %w", err)
		}
	} else if !errors.Is(err, errNotFound) {
		return err
	}
	inspected, err := s.docker.VolumeInspect(ctx, names.dbVolume)
	if err == nil {
		if !labelsMatch(inspected.Labels, s.cfg.InstanceID, serverID) ||
			inspected.Labels["gg.dockside.kind"] != "database-volume" {
			return errors.New("refusing database volume without matching managed labels")
		}
		if err := s.docker.VolumeRemove(ctx, names.dbVolume, true); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("remove database volume: %w", err)
		}
	} else if !errdefs.IsNotFound(err) {
		return fmt.Errorf("inspect database volume: %w", err)
	}
	return nil
}

func postgresLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
