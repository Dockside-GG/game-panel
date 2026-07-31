package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/go-chi/chi/v5"
)

var (
	imageReferencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*(?:(?:@sha256:)[a-f0-9]{64})?$`)
	environmentPattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
)

func (s *Server) provision(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if !uuidPattern.MatchString(serverID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server id"})
		return
	}
	var input engineclient.ProvisionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 3<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid provision request"})
		return
	}
	if err := s.validateProvision(input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if containerID, err := s.findManagedServer(r.Context(), serverID); err == nil {
		inspected, inspectErr := s.docker.ContainerInspect(r.Context(), containerID)
		if inspectErr != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "managed server exists but could not be inspected"})
			return
		}
		state := "stopped"
		if inspected.State != nil && inspected.State.Running {
			state = "running"
		}
		names := s.resourceNames(serverID)
		writeJSON(w, http.StatusOK, engineclient.ProvisionResult{
			ContainerID: containerID, VolumeName: names.volume,
			NetworkName: names.network, State: state,
		})
		return
	} else if !errors.Is(err, errNotFound) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not inspect managed containers"})
		return
	}

	result, err := s.createServerResources(r.Context(), serverID, input)
	if err != nil {
		s.logger.Error("server provisioning failed", "server_id", serverID, "error", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "server provisioning failed", "detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) installExisting(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if !uuidPattern.MatchString(serverID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server id"})
		return
	}
	var input engineclient.InstallSpec
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 3<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid installation request"})
		return
	}
	if err := s.validateRuntimeConfiguration(
		"placeholder/image:latest", "true", input.Environment,
		[]engineclient.Port{{HostIP: "0.0.0.0", HostPort: s.cfg.GamePortStart, ContainerPort: 1, Protocol: "tcp"}},
		engineclient.Resources{}, &input,
	); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	containerID, err := s.findManagedServer(r.Context(), serverID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "managed server not found"})
		return
	}
	inspected, err := s.docker.ContainerInspect(r.Context(), containerID)
	if err != nil || inspected.State == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "managed server could not be inspected"})
		return
	}
	if inspected.State.Running {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "server must be stopped before running its installer"})
		return
	}
	names := s.resourceNames(serverID)
	if err := s.runInstaller(
		r.Context(), serverID, names.volume, names.network,
		s.managedLabels(serverID), input,
	); err != nil {
		s.logger.Error("existing server installation failed", "server_id", serverID, "error", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "template installer failed", "detail": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) validateProvision(input engineclient.ProvisionRequest) error {
	return s.validateRuntimeConfiguration(
		input.Image,
		input.Startup,
		input.Environment,
		input.Ports,
		input.Resources,
		input.Install,
	)
}

func (s *Server) validateRuntimeConfiguration(
	imageReference string,
	startup string,
	environment map[string]string,
	ports []engineclient.Port,
	resources engineclient.Resources,
	install *engineclient.InstallSpec,
) error {
	if !imageReferencePattern.MatchString(imageReference) || len(imageReference) > 512 {
		return errors.New("invalid runtime image reference")
	}
	if strings.TrimSpace(startup) == "" || len(startup) > 32768 {
		return errors.New("startup command must be 1-32768 characters")
	}
	if len(environment) > 128 {
		return errors.New("too many environment variables")
	}
	for name, value := range environment {
		if !environmentPattern.MatchString(name) || len(value) > 65536 {
			return errors.New("invalid environment variable")
		}
	}
	if len(ports) == 0 || len(ports) > 64 {
		return errors.New("at least one and at most 64 ports are required")
	}
	seenPorts := make(map[string]struct{}, len(ports))
	for _, port := range ports {
		ip := net.ParseIP(port.HostIP)
		if port.HostIP == "" {
			ip = net.IPv4zero
		}
		if ip == nil || (!ip.IsUnspecified() && !ip.IsLoopback()) {
			return errors.New("port bind address must be loopback or unspecified")
		}
		if port.HostPort < s.cfg.GamePortStart || port.HostPort > s.cfg.GamePortEnd ||
			port.ContainerPort < 1 || port.ContainerPort > 65535 ||
			(port.Protocol != "tcp" && port.Protocol != "udp") {
			return errors.New("invalid port mapping")
		}
		key := port.HostIP + "/" + strconv.Itoa(port.HostPort) + "/" + port.Protocol
		if _, exists := seenPorts[key]; exists {
			return errors.New("duplicate port mapping")
		}
		seenPorts[key] = struct{}{}
	}
	if resources.CPUMillicores != nil && (*resources.CPUMillicores < 1 || *resources.CPUMillicores > 128000) {
		return errors.New("CPU limit is outside the supported range")
	}
	if resources.MemoryLimitBytes != nil && (*resources.MemoryLimitBytes < 16<<20 || *resources.MemoryLimitBytes > 1<<40) {
		return errors.New("memory limit is outside the supported range")
	}
	if resources.MemoryReservationBytes != nil && (*resources.MemoryReservationBytes < 1 || (resources.MemoryLimitBytes != nil && *resources.MemoryReservationBytes > *resources.MemoryLimitBytes)) {
		return errors.New("memory reservation is invalid")
	}
	if resources.PidsLimit != nil && (*resources.PidsLimit < 16 || *resources.PidsLimit > 1_000_000) {
		return errors.New("PID limit is outside the supported range")
	}
	if resources.IOWeight != nil && (*resources.IOWeight < 10 || *resources.IOWeight > 1000) {
		return errors.New("I/O weight must be between 10 and 1000")
	}
	if install != nil {
		if !imageReferencePattern.MatchString(install.Image) || len(install.Script) > 2<<20 {
			return errors.New("invalid installer configuration")
		}
		entrypoint := path.Base(strings.TrimSpace(install.Entrypoint))
		if entrypoint == "." || entrypoint == "" {
			entrypoint = "sh"
		}
		if entrypoint != "sh" && entrypoint != "ash" && entrypoint != "bash" {
			return errors.New("installer entrypoint must be sh, ash, or bash")
		}
		for name, value := range install.Environment {
			if !environmentPattern.MatchString(name) || len(value) > 65536 {
				return errors.New("invalid installer environment variable")
			}
		}
	}
	return nil
}

func (s *Server) reconfigure(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if !uuidPattern.MatchString(serverID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server id"})
		return
	}
	var input engineclient.ReconfigureRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 3<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid configuration request"})
		return
	}
	if err := s.validateRuntimeConfiguration(
		input.Image,
		input.Startup,
		input.Environment,
		input.Ports,
		input.Resources,
		nil,
	); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := s.replaceRuntimeContainer(r.Context(), serverID, input)
	if err != nil {
		s.logger.Error("server reconfiguration failed", "server_id", serverID, "error", err)
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "server reconfiguration failed", "detail": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) replaceRuntimeContainer(
	ctx context.Context,
	serverID string,
	input engineclient.ReconfigureRequest,
) (engineclient.ProvisionResult, error) {
	containerID, err := s.findManagedServer(ctx, serverID)
	if err != nil {
		return engineclient.ProvisionResult{}, err
	}
	inspected, err := s.docker.ContainerInspect(ctx, containerID)
	if err != nil {
		return engineclient.ProvisionResult{}, fmt.Errorf("inspect existing container: %w", err)
	}
	if inspected.State != nil && inspected.State.Running {
		return engineclient.ProvisionResult{}, errors.New("server must be stopped before changing runtime configuration")
	}
	if err := s.validateHostPortAvailability(ctx, serverID, input.Ports); err != nil {
		return engineclient.ProvisionResult{}, err
	}
	if err := s.pullImage(ctx, input.Image); err != nil {
		return engineclient.ProvisionResult{}, fmt.Errorf("pull runtime image: %w", err)
	}
	names := s.resourceNames(serverID)
	exposed, bindings, err := portConfiguration(input.Ports)
	if err != nil {
		return engineclient.ProvisionResult{}, err
	}
	environment := copyEnvironment(input.Environment)
	environment["STARTUP"] = input.Startup
	environment["P_SERVER_UUID"] = serverID
	environment["P_SERVER_LOCATION"] = "dockside"
	replacementName := names.container + "-replacement-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	created, err := s.docker.ContainerCreate(
		ctx,
		&container.Config{
			Image:        input.Image,
			Env:          environmentList(environment),
			WorkingDir:   "/home/container",
			Labels:       s.managedLabels(serverID),
			ExposedPorts: exposed,
			AttachStdin:  true,
			OpenStdin:    true,
			StdinOnce:    false,
			Tty:          false,
		},
		&container.HostConfig{
			NetworkMode:  container.NetworkMode(names.network),
			PortBindings: bindings,
			Resources:    dockerResources(input.Resources),
			Mounts: []mount.Mount{{
				Type: mount.TypeVolume, Source: names.volume, Target: "/home/container",
				VolumeOptions: &mount.VolumeOptions{NoCopy: true},
			}},
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			ReadonlyRootfs: false,
			RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			LogConfig: container.LogConfig{
				Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"},
			},
		},
		nil,
		nil,
		replacementName,
	)
	if err != nil {
		return engineclient.ProvisionResult{}, fmt.Errorf("create replacement container: %w", err)
	}
	keepReplacement := false
	oldRemoved := false
	defer func() {
		if !keepReplacement {
			_ = s.docker.ContainerRemove(
				context.WithoutCancel(ctx),
				created.ID,
				container.RemoveOptions{Force: true},
			)
		}
	}()
	if err := s.docker.ContainerRemove(ctx, containerID, container.RemoveOptions{}); err != nil {
		return engineclient.ProvisionResult{}, fmt.Errorf("remove existing stopped container: %w", err)
	}
	oldRemoved = true
	if err := s.docker.ContainerRename(ctx, created.ID, names.container); err != nil {
		if oldRemoved {
			keepReplacement = true
			s.logger.Warn(
				"replacement container retained with temporary name",
				"server_id", serverID,
				"container_id", created.ID,
				"error", err,
			)
		} else {
			return engineclient.ProvisionResult{}, fmt.Errorf("rename replacement container: %w", err)
		}
	}
	keepReplacement = true
	return engineclient.ProvisionResult{
		ContainerID: created.ID,
		VolumeName:  names.volume,
		NetworkName: names.network,
		State:       "stopped",
	}, nil
}

func (s *Server) createServerResources(ctx context.Context, serverID string, input engineclient.ProvisionRequest) (result engineclient.ProvisionResult, returnErr error) {
	hub := s.startProvisionLog(serverID)
	defer s.finishProvisionLog(serverID, hub)
	hub.emit(consoleFrame{
		Stream: "system", Phase: "provision", Message: "Provisioning started",
		ObservedAt: time.Now().UTC(),
	})
	names := s.resourceNames(serverID)
	labels := s.managedLabels(serverID)
	createdVolume := false
	createdNetwork := false
	containerID := ""
	defer func() {
		if returnErr == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if containerID != "" {
			_ = s.docker.ContainerRemove(cleanupCtx, containerID, container.RemoveOptions{Force: true})
		}
		if createdNetwork {
			_ = s.docker.NetworkRemove(cleanupCtx, names.network)
		}
		if createdVolume {
			_ = s.docker.VolumeRemove(cleanupCtx, names.volume, true)
		}
	}()
	if err := s.validateHostPortAvailability(ctx, serverID, input.Ports); err != nil {
		return result, err
	}

	hub.emit(consoleFrame{
		Stream: "system", Phase: "provision", Message: "Pulling runtime image",
		ObservedAt: time.Now().UTC(),
	})
	if err := s.pullImage(ctx, input.Image); err != nil {
		return result, fmt.Errorf("pull runtime image: %w", err)
	}
	hub.emit(consoleFrame{
		Stream: "system", Phase: "provision", Message: "Creating server storage and network",
		ObservedAt: time.Now().UTC(),
	})
	if _, err := s.docker.VolumeCreate(ctx, volume.CreateOptions{Name: names.volume, Labels: labels}); err != nil {
		return result, fmt.Errorf("create server volume: %w", err)
	}
	createdVolume = true
	if _, err := s.docker.NetworkCreate(ctx, names.network, network.CreateOptions{
		Driver: "bridge", Labels: labels,
	}); err != nil {
		return result, fmt.Errorf("create server network: %w", err)
	}
	createdNetwork = true

	if input.Install != nil && strings.TrimSpace(input.Install.Script) != "" {
		if err := s.runInstaller(ctx, serverID, names.volume, names.network, labels, *input.Install); err != nil {
			return result, err
		}
	}
	hub.emit(consoleFrame{
		Stream: "system", Phase: "provision", Message: "Finalizing server file ownership",
		ObservedAt: time.Now().UTC(),
	})
	if err := s.initializeVolumeOwnership(ctx, serverID, names.volume, labels); err != nil {
		return result, err
	}

	environment := copyEnvironment(input.Environment)
	environment["STARTUP"] = input.Startup
	environment["P_SERVER_UUID"] = serverID
	environment["P_SERVER_LOCATION"] = "dockside"
	exposed, bindings, err := portConfiguration(input.Ports)
	if err != nil {
		return result, err
	}
	resources := dockerResources(input.Resources)
	hostConfig := &container.HostConfig{
		NetworkMode:  container.NetworkMode(names.network),
		PortBindings: bindings,
		Resources:    resources,
		Mounts: []mount.Mount{{
			Type: mount.TypeVolume, Source: names.volume, Target: "/home/container",
			VolumeOptions: &mount.VolumeOptions{NoCopy: true},
		}},
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
		ReadonlyRootfs: false,
		RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
		LogConfig: container.LogConfig{
			Type:   "json-file",
			Config: map[string]string{"max-size": "10m", "max-file": "3"},
		},
	}
	created, err := s.docker.ContainerCreate(
		ctx,
		&container.Config{
			Image:        input.Image,
			Env:          environmentList(environment),
			WorkingDir:   "/home/container",
			Labels:       labels,
			ExposedPorts: exposed,
			AttachStdin:  true,
			OpenStdin:    true,
			StdinOnce:    false,
			Tty:          false,
		},
		hostConfig,
		nil,
		nil,
		names.container,
	)
	if err != nil {
		return result, fmt.Errorf("create runtime container: %w", err)
	}
	containerID = created.ID
	hub.emit(consoleFrame{
		Stream: "system", Phase: "runtime", Message: "Runtime container created",
		ObservedAt: time.Now().UTC(),
	})
	state := "stopped"
	if input.Start {
		hub.emit(consoleFrame{
			Stream: "system", Phase: "runtime", Message: "Starting game server",
			ObservedAt: time.Now().UTC(),
		})
		if err := s.docker.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
			return result, fmt.Errorf("start runtime container: %w", err)
		}
		state = "running"
	}
	return engineclient.ProvisionResult{
		ContainerID: containerID,
		VolumeName:  names.volume,
		NetworkName: names.network,
		State:       state,
	}, nil
}

func (s *Server) validateHostPortAvailability(
	ctx context.Context,
	serverID string,
	requested []engineclient.Port,
) error {
	containers, err := s.docker.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return fmt.Errorf("inspect published Docker ports: %w", err)
	}
	for _, candidate := range containers {
		if candidate.Labels["gg.dockside.server"] == serverID &&
			candidate.Labels["gg.dockside.instance"] == s.cfg.InstanceID {
			continue
		}
		for _, published := range candidate.Ports {
			if published.PublicPort == 0 {
				continue
			}
			for _, port := range requested {
				if int(published.PublicPort) == port.HostPort &&
					strings.EqualFold(published.Type, port.Protocol) {
					name := strings.TrimPrefix(strings.Join(candidate.Names, ", "), "/")
					if name == "" {
						name = candidate.ID[:12]
					}
					return fmt.Errorf(
						"host port %d/%s is already published by Docker container %s",
						port.HostPort, port.Protocol, name,
					)
				}
			}
		}
	}
	return nil
}

func (s *Server) runInstaller(ctx context.Context, serverID, volumeName, networkName string, labels map[string]string, spec engineclient.InstallSpec) error {
	hub := s.provisionLog(serverID)
	if hub != nil {
		hub.emit(consoleFrame{
			Stream: "system", Phase: "installer", Message: "Pulling installer image",
			ObservedAt: time.Now().UTC(),
		})
	}
	if err := s.pullImage(ctx, spec.Image); err != nil {
		return fmt.Errorf("pull installer image: %w", err)
	}
	entrypoint := path.Base(strings.TrimSpace(spec.Entrypoint))
	if entrypoint == "." || entrypoint == "" {
		entrypoint = "sh"
	}
	installerLabels := make(map[string]string, len(labels)+1)
	for key, value := range labels {
		installerLabels[key] = value
	}
	installerLabels["gg.dockside.kind"] = "installer"
	created, err := s.docker.ContainerCreate(
		ctx,
		&container.Config{
			Image:      spec.Image,
			Env:        environmentList(spec.Environment),
			Entrypoint: []string{entrypoint, "-c"},
			Cmd:        []string{spec.Script},
			WorkingDir: "/mnt/server",
			Labels:     installerLabels,
		},
		&container.HostConfig{
			NetworkMode: container.NetworkMode(networkName),
			Mounts: []mount.Mount{{
				Type: mount.TypeVolume, Source: volumeName, Target: "/mnt/server",
				VolumeOptions: &mount.VolumeOptions{NoCopy: true},
			}},
			CapDrop:     []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges:true"},
		},
		nil,
		nil,
		"dockside-installer-"+strings.ReplaceAll(serverID, "-", ""),
	)
	if err != nil {
		return fmt.Errorf("create installer container: %w", err)
	}
	defer s.docker.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
	if err := s.docker.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start installer container: %w", err)
	}
	var logDone chan struct{}
	if hub != nil {
		hub.emit(consoleFrame{
			Stream: "system", Phase: "installer", Message: "Installer started",
			ObservedAt: time.Now().UTC(),
		})
		logDone = make(chan struct{})
		go func() {
			defer close(logDone)
			logs, logErr := s.docker.ContainerLogs(ctx, created.ID, container.LogsOptions{
				ShowStdout: true, ShowStderr: true, Follow: true, Tail: "all",
			})
			if logErr != nil {
				hub.emit(consoleFrame{
					Stream: "system", Phase: "installer",
					Message:    "Installer logs are temporarily unavailable",
					ObservedAt: time.Now().UTC(),
				})
				return
			}
			defer logs.Close()
			stdout := &hubLineFrameWriter{hub: hub, stream: "stdout", phase: "installer"}
			stderr := &hubLineFrameWriter{hub: hub, stream: "stderr", phase: "installer"}
			_, _ = stdcopy.StdCopy(stdout, stderr, logs)
			stdout.flush()
			stderr.flush()
		}()
	}
	statusCh, errCh := s.docker.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("wait for installer: %w", err)
		}
	case status := <-statusCh:
		if logDone != nil {
			select {
			case <-logDone:
			case <-time.After(2 * time.Second):
			}
		}
		if status.StatusCode != 0 {
			logs := s.containerLogTail(ctx, created.ID)
			return fmt.Errorf("installer exited with code %d: %s", status.StatusCode, logs)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	if hub != nil {
		hub.emit(consoleFrame{
			Stream: "system", Phase: "installer", Message: "Installer completed successfully",
			ObservedAt: time.Now().UTC(),
		})
	}
	return nil
}

func (s *Server) initializeVolumeOwnership(
	ctx context.Context,
	serverID, volumeName string,
	labels map[string]string,
) error {
	if _, _, err := s.docker.ImageInspectWithRaw(ctx, fileHelperImage); err != nil {
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("inspect volume initializer image: %w", err)
		}
		if err := s.pullImage(ctx, fileHelperImage); err != nil {
			return fmt.Errorf("pull volume initializer image: %w", err)
		}
	}
	initializerLabels := make(map[string]string, len(labels)+1)
	for key, value := range labels {
		initializerLabels[key] = value
	}
	initializerLabels["gg.dockside.kind"] = "volume-initializer"
	command := fmt.Sprintf(
		"chown -R %d:%d /mnt/server && chmod 0750 /mnt/server",
		s.cfg.ServerUID,
		s.cfg.ServerGID,
	)
	created, err := s.docker.ContainerCreate(
		ctx,
		&container.Config{
			Image:      fileHelperImage,
			User:       "0:0",
			Entrypoint: []string{"sh", "-c"},
			Cmd:        []string{command},
			Labels:     initializerLabels,
		},
		&container.HostConfig{
			NetworkMode:    "none",
			ReadonlyRootfs: true,
			CapDrop:        []string{"ALL"},
			CapAdd:         []string{"CHOWN", "FOWNER", "DAC_OVERRIDE"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			Mounts: []mount.Mount{{
				Type: mount.TypeVolume, Source: volumeName, Target: "/mnt/server",
			}},
		},
		nil,
		nil,
		"",
	)
	if err != nil {
		return fmt.Errorf("create volume initializer: %w", err)
	}
	defer s.docker.ContainerRemove(
		context.WithoutCancel(ctx),
		created.ID,
		container.RemoveOptions{Force: true},
	)
	if err := s.docker.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("start volume initializer: %w", err)
	}
	statusCh, errorCh := s.docker.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case waitErr := <-errorCh:
		if waitErr != nil {
			return fmt.Errorf("wait for volume initializer: %w", waitErr)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			return fmt.Errorf("volume initializer exited with status %d", status.StatusCode)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (s *Server) pullImage(ctx context.Context, reference string) error {
	stream, err := s.docker.ImagePull(ctx, reference, image.PullOptions{})
	if err != nil {
		return err
	}
	defer stream.Close()
	decoder := json.NewDecoder(io.LimitReader(stream, 64<<20))
	for {
		var message struct {
			Error string `json:"error"`
		}
		if err := decoder.Decode(&message); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		if message.Error != "" {
			return errors.New(message.Error)
		}
	}
}

func (s *Server) containerLogTail(ctx context.Context, containerID string) string {
	stream, err := s.docker.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: "50",
	})
	if err != nil {
		return "logs unavailable"
	}
	defer stream.Close()
	value, _ := io.ReadAll(io.LimitReader(stream, 32<<10))
	return strings.TrimSpace(string(value))
}

func portConfiguration(ports []engineclient.Port) (nat.PortSet, nat.PortMap, error) {
	exposed := make(nat.PortSet, len(ports))
	bindings := make(nat.PortMap, len(ports))
	for _, configured := range ports {
		port, err := nat.NewPort(configured.Protocol, strconv.Itoa(configured.ContainerPort))
		if err != nil {
			return nil, nil, fmt.Errorf("construct port mapping: %w", err)
		}
		exposed[port] = struct{}{}
		hostIP := configured.HostIP
		if hostIP == "" {
			hostIP = "0.0.0.0"
		}
		bindings[port] = append(bindings[port], nat.PortBinding{
			HostIP: hostIP, HostPort: strconv.Itoa(configured.HostPort),
		})
	}
	return exposed, bindings, nil
}

func dockerResources(input engineclient.Resources) container.Resources {
	var resources container.Resources
	if input.CPUMillicores != nil {
		resources.NanoCPUs = int64(*input.CPUMillicores) * 1_000_000
	}
	resources.CpusetCpus = input.CPUSet
	if input.MemoryLimitBytes != nil {
		resources.Memory = *input.MemoryLimitBytes
	}
	if input.MemoryReservationBytes != nil {
		resources.MemoryReservation = *input.MemoryReservationBytes
	}
	if input.SwapLimitBytes != nil {
		resources.MemorySwap = *input.SwapLimitBytes
	}
	resources.PidsLimit = input.PidsLimit
	if input.IOWeight != nil {
		resources.BlkioWeight = uint16(*input.IOWeight)
	}
	return resources
}

func copyEnvironment(source map[string]string) map[string]string {
	result := make(map[string]string, len(source)+3)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func environmentList(environment map[string]string) []string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+environment[key])
	}
	return result
}

type resourceNameSet struct {
	container   string
	volume      string
	network     string
	dbContainer string
	dbVolume    string
}

func (s *Server) resourceNames(serverID string) resourceNameSet {
	instance := strings.ReplaceAll(s.cfg.InstanceID, "-", "")
	if len(instance) > 12 {
		instance = instance[:12]
	}
	base := "dockside-" + instance + "-" + serverID
	return resourceNameSet{
		container:   base,
		volume:      base + "-data",
		network:     base + "-net",
		dbContainer: base + "-postgres",
		dbVolume:    base + "-postgres-data",
	}
}

func (s *Server) managedLabels(serverID string) map[string]string {
	return map[string]string{
		"gg.dockside.managed":  "true",
		"gg.dockside.instance": s.cfg.InstanceID,
		"gg.dockside.server":   serverID,
		"gg.dockside.kind":     "server",
	}
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if !uuidPattern.MatchString(serverID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server id"})
		return
	}
	if err := s.removeServerResources(r.Context(), serverID); err != nil {
		s.logger.Error("delete managed server failed", "server_id", serverID, "error", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "could not fully delete managed server"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeServerResources(ctx context.Context, serverID string) error {
	names := s.resourceNames(serverID)
	if err := s.removeTransientServerContainers(ctx, serverID); err != nil {
		return err
	}
	if err := s.removeDatabaseHost(ctx, serverID, names); err != nil {
		return err
	}
	containerID, err := s.findManagedServer(ctx, serverID)
	if err == nil {
		if err := s.docker.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("remove container: %w", err)
		}
	} else if !errors.Is(err, errNotFound) {
		return err
	}
	if inspected, err := s.docker.NetworkInspect(ctx, names.network, network.InspectOptions{}); err == nil {
		if !labelsMatch(inspected.Labels, s.cfg.InstanceID, serverID) {
			return errors.New("refusing to remove network without matching managed labels")
		}
		if err := s.docker.NetworkRemove(ctx, names.network); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("remove network: %w", err)
		}
	} else if !errdefs.IsNotFound(err) {
		return fmt.Errorf("inspect network: %w", err)
	}
	if inspected, err := s.docker.VolumeInspect(ctx, names.volume); err == nil {
		if !labelsMatch(inspected.Labels, s.cfg.InstanceID, serverID) {
			return errors.New("refusing to remove volume without matching managed labels")
		}
		if err := s.docker.VolumeRemove(ctx, names.volume, true); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("remove volume: %w", err)
		}
	} else if !errdefs.IsNotFound(err) {
		return fmt.Errorf("inspect volume: %w", err)
	}
	backupDirectory := filepath.Join(s.cfg.BackupRoot, serverID)
	relativeBackup, err := filepath.Rel(s.cfg.BackupRoot, backupDirectory)
	if err != nil || relativeBackup != serverID {
		return errors.New("refusing to remove an unsafe backup directory")
	}
	if err := os.RemoveAll(backupDirectory); err != nil {
		return fmt.Errorf("remove server backups: %w", err)
	}
	return nil
}

func (s *Server) removeTransientServerContainers(ctx context.Context, serverID string) error {
	filter := filters.NewArgs(
		filters.Arg("label", "gg.dockside.managed=true"),
		filters.Arg("label", "gg.dockside.instance="+s.cfg.InstanceID),
		filters.Arg("label", "gg.dockside.server="+serverID),
	)
	found, err := s.docker.ContainerList(ctx, container.ListOptions{All: true, Filters: filter})
	if err != nil {
		return fmt.Errorf("list server helper containers: %w", err)
	}
	for _, candidate := range found {
		kind := candidate.Labels["gg.dockside.kind"]
		if kind == "server" || kind == "database" {
			continue
		}
		if err := s.docker.ContainerRemove(
			ctx, candidate.ID, container.RemoveOptions{Force: true},
		); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("remove %s helper container: %w", kind, err)
		}
	}
	return nil
}

func labelsMatch(labels map[string]string, instanceID, serverID string) bool {
	return labels["gg.dockside.managed"] == "true" &&
		labels["gg.dockside.instance"] == instanceID &&
		labels["gg.dockside.server"] == serverID
}
