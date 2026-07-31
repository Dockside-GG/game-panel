package engine

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/dockside-gg/game-panel/internal/engineclient"
)

const (
	updateBackupRoot  = "/var/lib/dockside/backups"
	updateProjectRoot = "/host/project"
)

type updateHelper struct {
	plan          panelUpdatePlan
	docker        *client.Client
	logger        *slog.Logger
	status        engineclient.PanelUpdateStatus
	partial       string
	staging       string
	stopped       []string
	releaseRoot   string
	snapshotReady bool
	applyStarted  bool
	rolledBack    bool
}

type updateSnapshotManifest struct {
	Format         int               `json:"format"`
	CreatedAt      time.Time         `json:"created_at"`
	InstanceID     string            `json:"instance_id"`
	CurrentVersion string            `json:"current_version"`
	TargetVersion  string            `json:"target_version"`
	ReleaseURL     string            `json:"release_url"`
	Files          map[string]string `json:"sha256"`
	Notes          []string          `json:"notes"`
}

// MaybeRunUpdateHelper handles the short-lived privileged updater mode before
// normal engine configuration is loaded.
func MaybeRunUpdateHelper(args []string) bool {
	if len(args) != 3 || args[1] != "update-helper" {
		return false
	}
	writer := io.Writer(os.Stdout)
	if err := os.MkdirAll(filepath.Join(updateBackupRoot, panelUpdateDirectory), 0o700); err == nil {
		if logFile, openErr := os.OpenFile(
			filepath.Join(updateBackupRoot, panelUpdateDirectory, "update-helper.log"),
			os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600,
		); openErr == nil {
			defer logFile.Close()
			writer = io.MultiWriter(os.Stdout, logFile)
		}
	}
	logger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo})).With("component", "update-helper")
	if err := runUpdateHelper(args[2], logger); err != nil {
		logger.Error("panel update failed", "error", err)
		return true
	}
	logger.Info("panel update completed")
	return true
}

func runUpdateHelper(planPath string, logger *slog.Logger) (returnErr error) {
	content, err := os.ReadFile(planPath)
	if err != nil {
		return fmt.Errorf("read update plan: %w", err)
	}
	var plan panelUpdatePlan
	if err := json.Unmarshal(content, &plan); err != nil {
		return fmt.Errorf("decode update plan: %w", err)
	}
	if err := validatePanelUpdateRequest(engineclient.PanelUpdateRequest{
		CurrentVersion: plan.CurrentVersion, TargetVersion: plan.TargetVersion,
		ReleaseURL: plan.ReleaseURL, ArchiveURL: plan.ArchiveURL, ChecksumsURL: plan.ChecksumsURL,
	}); err != nil {
		return err
	}
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("create Docker client: %w", err)
	}
	defer dockerClient.Close()
	helper := &updateHelper{
		plan: plan, docker: dockerClient, logger: logger,
		status:  newPanelUpdateStatus(plan),
		partial: filepath.Join(updateBackupRoot, panelUpdateDirectory, ".partial-"+plan.ID),
		staging: filepath.Join(updateBackupRoot, panelUpdateDirectory, ".staging-"+plan.ID),
	}
	helper.status.State = "running"
	if err := helper.writeStatus("download", "Downloading and verifying the Dockside release assets."); err != nil {
		return err
	}
	defer func() {
		if returnErr == nil {
			return
		}
		helper.logger.Error("update helper step failed", "phase", helper.status.Phase, "error", returnErr)
		if helper.applyStarted && helper.snapshotReady {
			if rollbackErr := helper.rollback(context.Background()); rollbackErr != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("automatic rollback failed: %w", rollbackErr))
			} else {
				helper.rolledBack = true
			}
		}
		if !helper.applyStarted || helper.rolledBack {
			helper.startStoppedContainers(context.Background())
		}
		now := time.Now().UTC()
		helper.status.State = "failed"
		helper.status.Error = returnErr.Error()
		helper.status.CompletedAt = &now
		if helper.rolledBack {
			helper.status.Message = "The update failed, and Dockside automatically restored the prior panel version and database. The recovery snapshot remains available."
			helper.status.FailureRecovery = "Review Diagnostics and docs/UPDATES.md before retrying the release."
		} else if helper.snapshotReady {
			helper.status.Message = "The update was not completed. The pre-update recovery snapshot remains available for host-level recovery."
			helper.status.FailureRecovery = "Use docs/UPDATES.md with the pre-update snapshot before retrying."
		} else {
			helper.status.Message = "The update stopped before a recovery snapshot was finalized; the existing installation was not replaced."
		}
		_ = writePanelUpdateStatus(updateBackupRoot, helper.status)
	}()
	if err := os.MkdirAll(helper.partial, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(helper.staging, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(helper.staging)
	defer func() {
		if !helper.snapshotReady {
			_ = os.RemoveAll(helper.partial)
		}
	}()
	archivePath, err := helper.downloadAndVerify(context.Background())
	if err != nil {
		return err
	}
	if err := helper.extractRelease(archivePath); err != nil {
		return err
	}
	if err := helper.writeStatus("quiesce", "Stopping panel writers and running game servers for a consistent snapshot."); err != nil {
		return err
	}
	time.Sleep(3 * time.Second)
	if err := helper.stopWorkloads(context.Background()); err != nil {
		return err
	}
	if err := helper.writeStatus("snapshot", "Creating the PostgreSQL, panel configuration, container image, and game-server recovery snapshot."); err != nil {
		return err
	}
	if err := helper.createSnapshot(context.Background()); err != nil {
		return err
	}
	if err := helper.finalizeSnapshot(); err != nil {
		return err
	}
	helper.snapshotReady = true
	helper.status.SnapshotPath = "data/backups/panel-updates/pre-update"
	if err := helper.writeStatus("apply", "Applying the verified release and pulling exact container image tags."); err != nil {
		return err
	}
	helper.applyStarted = true
	if err := helper.applyRelease(context.Background()); err != nil {
		return err
	}
	if err := helper.writeStatus("verify", "Waiting for the updated core panel services to become healthy."); err != nil {
		return err
	}
	if err := helper.waitForPanel(context.Background(), false); err != nil {
		return err
	}
	if err := helper.writeStatus("worker", "Starting the updated background worker after core health checks passed."); err != nil {
		return err
	}
	if err := helper.startUpdatedWorker(context.Background()); err != nil {
		return err
	}
	if err := helper.waitForPanel(context.Background(), true); err != nil {
		return err
	}
	helper.startStoppedContainers(context.Background())
	now := time.Now().UTC()
	helper.status.State = "succeeded"
	helper.status.Phase = "complete"
	helper.status.Message = "Dockside was updated successfully. The newest pre-update recovery snapshot is retained."
	helper.status.CompletedAt = &now
	helper.status.Error = ""
	if err := writePanelUpdateStatus(updateBackupRoot, helper.status); err != nil {
		return err
	}
	_ = os.Remove(planPath)
	return nil
}

func (h *updateHelper) writeStatus(phase, message string) error {
	h.status.State, h.status.Phase, h.status.Message = "running", phase, message
	h.logger.Info(message, "phase", phase, "target_version", h.plan.TargetVersion)
	return writePanelUpdateStatus(updateBackupRoot, h.status)
}

func (h *updateHelper) downloadAndVerify(ctx context.Context) (string, error) {
	archiveName := "dockside-game-panel-" + h.plan.TargetVersion + ".zip"
	archivePath := filepath.Join(h.staging, archiveName)
	checksumsPath := filepath.Join(h.staging, "SHA256SUMS")
	if err := downloadUpdateAsset(ctx, h.plan.ArchiveURL, archivePath, 512<<20); err != nil {
		return "", err
	}
	if err := downloadUpdateAsset(ctx, h.plan.ChecksumsURL, checksumsPath, 1<<20); err != nil {
		return "", err
	}
	expected, err := expectedChecksum(checksumsPath, archiveName)
	if err != nil {
		return "", err
	}
	actual, err := fileSHA256(archivePath)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(expected, actual) {
		return "", errors.New("release archive SHA-256 does not match SHA256SUMS")
	}
	return archivePath, nil
}

func downloadUpdateAsset(ctx context.Context, source, destination string, limit int64) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "Dockside.GG-Game-Panel-Updater")
	httpClient := &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("too many release download redirects")
		}
		if req.URL.Scheme != "https" {
			return errors.New("release download redirected away from HTTPS")
		}
		switch strings.ToLower(req.URL.Hostname()) {
		case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		default:
			return errors.New("release download redirected to an untrusted host")
		}
		return nil
	}}
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("download release asset: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download release asset: HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, limit+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > limit {
		return errors.New("release asset exceeds the updater size limit")
	}
	return nil
}

func expectedChecksum(path, wanted string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, 1<<20))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && strings.TrimPrefix(fields[len(fields)-1], "*") == wanted {
			if len(fields[0]) != 64 {
				return "", errors.New("invalid SHA256SUMS entry")
			}
			if _, err := hex.DecodeString(fields[0]); err != nil {
				return "", errors.New("invalid SHA256SUMS digest")
			}
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", errors.New("release archive is missing from SHA256SUMS")
}

func (h *updateHelper) extractRelease(archivePath string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open release archive: %w", err)
	}
	defer reader.Close()
	prefix := "dockside-game-panel-" + h.plan.TargetVersion + "/"
	for _, file := range reader.File {
		name := filepath.ToSlash(file.Name)
		if !strings.HasPrefix(name, prefix) {
			return fmt.Errorf("release archive contains unexpected root %q", name)
		}
		relative := strings.TrimPrefix(name, prefix)
		if relative == "" {
			continue
		}
		clean := filepath.Clean(filepath.FromSlash(relative))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("release archive contains unsafe path %q", name)
		}
		target := filepath.Join(h.staging, "release", clean)
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("release archive contains unsupported link %q", name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		source, err := file.Open()
		if err != nil {
			return err
		}
		mode := file.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		destination, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			source.Close()
			return err
		}
		_, copyErr := io.Copy(destination, io.LimitReader(source, 64<<20))
		closeErr, sourceCloseErr := destination.Close(), source.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if sourceCloseErr != nil {
			return sourceCloseErr
		}
	}
	h.releaseRoot = filepath.Join(h.staging, "release")
	content, err := os.ReadFile(filepath.Join(h.releaseRoot, ".dockside-release"))
	if err != nil || strings.TrimSpace(string(content)) != h.plan.TargetVersion {
		return errors.New("release metadata does not match the target version")
	}
	return nil
}

func (h *updateHelper) managedContainers(ctx context.Context) ([]types.Container, error) {
	found, err := h.docker.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(
		filters.Arg("label", "gg.dockside.instance="+h.plan.InstanceID),
	)})
	if err != nil {
		return nil, err
	}
	result := found[:0]
	for _, item := range found {
		if item.Labels["gg.dockside.kind"] == "panel-update-helper" {
			continue
		}
		if item.Labels["gg.dockside.system"] == "true" || item.Labels["gg.dockside.kind"] == "server" {
			result = append(result, item)
		}
	}
	return result, nil
}

func (h *updateHelper) stopWorkloads(ctx context.Context) error {
	containers, err := h.managedContainers(ctx)
	if err != nil {
		return err
	}
	order := []string{"worker", "server", "app"}
	for _, kind := range order {
		for _, summary := range containers {
			component := summary.Labels["gg.dockside.component"]
			server := summary.Labels["gg.dockside.kind"] == "server"
			if (kind == "server" && !server) || (kind != "server" && component != kind) || summary.State != "running" {
				continue
			}
			timeout := 45
			if err := h.docker.ContainerStop(ctx, summary.ID, container.StopOptions{Timeout: &timeout}); err != nil {
				return fmt.Errorf("stop %s container: %w", kind, err)
			}
			h.stopped = append(h.stopped, summary.ID)
		}
	}
	return nil
}

func (h *updateHelper) startStoppedContainers(ctx context.Context) {
	for _, id := range h.stopped {
		if err := h.docker.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
			h.logger.Warn("could not restart previously running container", "container_id", id, "error", err)
		}
	}
}

func (h *updateHelper) createSnapshot(ctx context.Context) error {
	if err := archiveDirectory(updateProjectRoot, filepath.Join(h.partial, "panel-config.tar.gz"), projectSnapshotExcluded); err != nil {
		return fmt.Errorf("archive panel configuration: %w", err)
	}
	containers, err := h.managedContainers(ctx)
	if err != nil {
		return err
	}
	inspected := make([]types.ContainerJSON, 0, len(containers))
	imageSet := map[string]bool{}
	volumeTargets := map[string]string{}
	systemVolumeTargets := map[string]string{}
	for _, summary := range containers {
		item, err := h.docker.ContainerInspect(ctx, summary.ID)
		if err != nil {
			return err
		}
		inspected = append(inspected, item)
		if summary.Image != "" {
			imageSet[summary.Image] = true
		}
		if summary.Labels["gg.dockside.kind"] == "server" {
			serverID := summary.Labels["gg.dockside.server"]
			if !uuidPattern.MatchString(serverID) {
				continue
			}
			for _, mounted := range item.Mounts {
				if mounted.Type == mount.TypeVolume && mounted.Destination == "/home/container" {
					volumeTargets[mounted.Name] = serverID
				}
			}
		} else if summary.Labels["gg.dockside.system"] == "true" && summary.Labels["gg.dockside.component"] != "postgres" {
			component := summary.Labels["gg.dockside.component"]
			for _, mounted := range item.Mounts {
				if mounted.Type == mount.TypeVolume {
					target := strings.Trim(strings.ReplaceAll(mounted.Destination, "\\", "/"), "/")
					target = strings.ReplaceAll(target, "/", "-")
					if target == "" {
						target = "volume"
					}
					systemVolumeTargets[mounted.Name] = component + "/" + target
				}
			}
		}
	}
	if err := writePrivateJSON(filepath.Join(h.partial, "containers.json"), inspected); err != nil {
		return err
	}
	if err := h.saveImages(ctx, imageSet, filepath.Join(h.partial, "container-images.tar.gz")); err != nil {
		return err
	}
	if err := h.archiveVolumes(ctx, volumeTargets, "game-servers.tar.gz", "/mnt/servers"); err != nil {
		return err
	}
	if err := h.archiveVolumes(ctx, systemVolumeTargets, "system-volumes.tar.gz", "/mnt/system-volumes"); err != nil {
		return err
	}
	if err := h.dumpPostgres(ctx, containers, filepath.Join(h.partial, "postgres.sql")); err != nil {
		return err
	}
	manifest := updateSnapshotManifest{
		Format: 1, CreatedAt: time.Now().UTC(), InstanceID: h.plan.InstanceID,
		CurrentVersion: h.plan.CurrentVersion, TargetVersion: h.plan.TargetVersion,
		ReleaseURL: h.plan.ReleaseURL, Files: map[string]string{},
		Notes: []string{
			"Container images are Docker image archives; container configurations are in containers.json.",
			"Game-server named volumes are stored in game-servers.tar.gz and PostgreSQL is stored as a plain SQL dump.",
			"Non-database system named volumes, such as Caddy state, are stored in system-volumes.tar.gz.",
			"Existing panel-created game backups remain in data/backups and are not duplicated recursively.",
		},
	}
	for _, name := range []string{"panel-config.tar.gz", "containers.json", "container-images.tar.gz", "game-servers.tar.gz", "system-volumes.tar.gz", "postgres.sql"} {
		digest, err := fileSHA256(filepath.Join(h.partial, name))
		if err != nil {
			return err
		}
		manifest.Files[name] = digest
	}
	return writePrivateJSON(filepath.Join(h.partial, "manifest.json"), manifest)
}

func (h *updateHelper) saveImages(ctx context.Context, images map[string]bool, destination string) error {
	list := make([]string, 0, len(images))
	for image := range images {
		list = append(list, image)
	}
	sort.Strings(list)
	if len(list) == 0 {
		return errors.New("no managed container images were found")
	}
	stream, err := h.docker.ImageSave(ctx, list)
	if err != nil {
		return fmt.Errorf("export container images: %w", err)
	}
	defer stream.Close()
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	compressed := gzip.NewWriter(file)
	_, copyErr := io.Copy(compressed, stream)
	compressErr, closeErr := compressed.Close(), file.Close()
	if copyErr != nil {
		return copyErr
	}
	if compressErr != nil {
		return compressErr
	}
	return closeErr
}

func (h *updateHelper) archiveVolumes(ctx context.Context, volumes map[string]string, outputName, mountRoot string) error {
	if len(volumes) == 0 {
		empty := filepath.Join(h.staging, "empty-"+strings.TrimSuffix(outputName, ".tar.gz"))
		if err := os.MkdirAll(empty, 0o700); err != nil {
			return err
		}
		return archiveDirectory(empty, filepath.Join(h.partial, outputName), func(string) bool { return false })
	}
	mounts := []mount.Mount{{Type: mount.TypeBind, Source: h.partial, Target: "/snapshot"}}
	for volumeName, target := range volumes {
		mounts = append(mounts, mount.Mount{Type: mount.TypeVolume, Source: volumeName, Target: mountRoot + "/" + target, ReadOnly: true})
	}
	created, err := h.docker.ContainerCreate(ctx, &container.Config{
		Image: h.plan.EngineImage, User: "0:0", Entrypoint: []string{"tar"},
		Cmd: []string{"-czf", "/snapshot/" + outputName, "-C", mountRoot, "."},
	}, &container.HostConfig{NetworkMode: "none", Mounts: mounts, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}}, nil, nil, "")
	if err != nil {
		return fmt.Errorf("create game data snapshot helper: %w", err)
	}
	defer h.docker.ContainerRemove(context.WithoutCancel(ctx), created.ID, container.RemoveOptions{Force: true})
	if err := h.docker.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return err
	}
	statusCh, errorCh := h.docker.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errorCh:
		if err != nil {
			return err
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			return fmt.Errorf("game data snapshot helper exited %d", status.StatusCode)
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (h *updateHelper) dumpPostgres(ctx context.Context, containers []types.Container, destination string) error {
	var postgresID string
	for _, item := range containers {
		if item.Labels["gg.dockside.component"] == "postgres" {
			postgresID = item.ID
			break
		}
	}
	if postgresID == "" {
		return errors.New("PostgreSQL container not found")
	}
	inspected, err := h.docker.ContainerInspect(ctx, postgresID)
	if err != nil {
		return err
	}
	user, database := "dockside", "dockside"
	for _, value := range inspected.Config.Env {
		if strings.HasPrefix(value, "POSTGRES_USER=") {
			user = strings.TrimPrefix(value, "POSTGRES_USER=")
		}
		if strings.HasPrefix(value, "POSTGRES_DB=") {
			database = strings.TrimPrefix(value, "POSTGRES_DB=")
		}
	}
	created, err := h.docker.ContainerExecCreate(ctx, postgresID, container.ExecOptions{
		AttachStdout: true, AttachStderr: true,
		Cmd: []string{"pg_dump", "-U", user, "-d", database, "--clean", "--if-exists", "--no-owner", "--no-privileges"},
	})
	if err != nil {
		return err
	}
	attached, err := h.docker.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return err
	}
	defer attached.Close()
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	var stderr strings.Builder
	_, copyErr := stdcopy.StdCopy(file, &stderr, attached.Reader)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	result, err := h.docker.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("pg_dump exited %d: %s", result.ExitCode, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (h *updateHelper) finalizeSnapshot() error {
	root := filepath.Join(updateBackupRoot, panelUpdateDirectory)
	current := filepath.Join(root, "pre-update")
	previous := filepath.Join(root, ".previous-"+h.plan.ID)
	if _, err := os.Stat(current); err == nil {
		if err := os.Rename(current, previous); err != nil {
			return fmt.Errorf("archive previous snapshot: %w", err)
		}
	}
	if err := os.Rename(h.partial, current); err != nil {
		if _, previousErr := os.Stat(previous); previousErr == nil {
			_ = os.Rename(previous, current)
		}
		return fmt.Errorf("finalize new snapshot: %w", err)
	}
	if err := os.RemoveAll(previous); err != nil {
		h.logger.Warn("remove superseded snapshot failed", "error", err)
	}
	return nil
}

func (h *updateHelper) applyRelease(ctx context.Context) error {
	allowed := []string{".env.example", ".dockside-release", "CHANGELOG.md", "compose.yml", "compose.public.yml", "CONTRIBUTING.md", "LICENSE", "NOTICE", "README.md", "deploy", "docs", "scripts"}
	for _, name := range allowed {
		source := filepath.Join(h.releaseRoot, name)
		if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := copyUpdatePath(source, filepath.Join(updateProjectRoot, name)); err != nil {
			return err
		}
	}
	if err := setEnvironmentVersion(filepath.Join(updateProjectRoot, ".env"), h.plan.TargetVersion); err != nil {
		return err
	}
	args := h.composeArguments()
	if err := h.runCompose(ctx, append(args, "pull", "app", "worker", "engine")...); err != nil {
		return err
	}
	if err := h.runCompose(ctx, append(args, "up", "-d", "--no-build", "gateway", "app", "engine", "postgres")...); err != nil {
		return err
	}
	return nil
}

func (h *updateHelper) startUpdatedWorker(ctx context.Context) error {
	return h.runCompose(ctx, append(h.composeArguments(), "up", "-d", "--no-build", "--no-deps", "worker")...)
}

func (h *updateHelper) composeArguments() []string {
	args := []string{"compose", "--project-directory", updateProjectRoot, "--env-file", filepath.Join(updateProjectRoot, ".env"), "-p", h.plan.ComposeProject}
	for _, name := range h.plan.ComposeFiles {
		if _, err := os.Stat(filepath.Join(updateProjectRoot, name)); err == nil {
			args = append(args, "-f", filepath.Join(updateProjectRoot, name))
		}
	}
	return args
}

func (h *updateHelper) runCompose(ctx context.Context, args ...string) error {
	command := exec.CommandContext(ctx, "docker", args...)
	command.Dir = updateProjectRoot
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	h.logger.Info("Docker Compose command completed", "command", strings.Join(args, " "))
	return nil
}

func (h *updateHelper) waitForPanel(ctx context.Context, requireWorker bool) error {
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		containers, err := h.managedContainers(ctx)
		if err == nil {
			ready := map[string]bool{"app": false, "engine": false, "worker": false, "postgres": false}
			for _, summary := range containers {
				component := summary.Labels["gg.dockside.component"]
				if _, tracked := ready[component]; !tracked || summary.State != "running" {
					continue
				}
				if component == "app" || component == "engine" || component == "postgres" {
					inspected, inspectErr := h.docker.ContainerInspect(ctx, summary.ID)
					if inspectErr != nil || inspected.State == nil || inspected.State.Health == nil || inspected.State.Health.Status != "healthy" {
						continue
					}
				}
				ready[component] = true
			}
			if ready["app"] && ready["engine"] && ready["postgres"] && (!requireWorker || ready["worker"]) {
				return nil
			}
		}
		time.Sleep(3 * time.Second)
	}
	return errors.New("updated panel services did not become healthy within five minutes")
}

func (h *updateHelper) rollback(ctx context.Context) error {
	h.logger.Warn("attempting automatic panel rollback", "snapshot", "panel-updates/pre-update")
	snapshot := filepath.Join(updateBackupRoot, panelUpdateDirectory, "pre-update")
	if err := h.stopCurrentPanelWriters(ctx); err != nil {
		return err
	}
	if err := h.loadSnapshotImages(ctx, filepath.Join(snapshot, "container-images.tar.gz")); err != nil {
		return err
	}
	if err := extractTarGzip(filepath.Join(snapshot, "panel-config.tar.gz"), updateProjectRoot); err != nil {
		return fmt.Errorf("restore panel configuration: %w", err)
	}
	if err := h.restorePostgres(ctx, filepath.Join(snapshot, "postgres.sql")); err != nil {
		return fmt.Errorf("restore PostgreSQL: %w", err)
	}
	if err := h.runCompose(ctx, append(h.composeArguments(), "up", "-d", "--no-build")...); err != nil {
		return fmt.Errorf("restore prior panel containers: %w", err)
	}
	if err := h.waitForPanel(ctx, true); err != nil {
		return fmt.Errorf("verify restored panel: %w", err)
	}
	return nil
}

func (h *updateHelper) stopCurrentPanelWriters(ctx context.Context) error {
	containers, err := h.managedContainers(ctx)
	if err != nil {
		return err
	}
	for _, summary := range containers {
		component := summary.Labels["gg.dockside.component"]
		if summary.State != "running" || (component != "app" && component != "worker") {
			continue
		}
		timeout := 30
		if err := h.docker.ContainerStop(ctx, summary.ID, container.StopOptions{Timeout: &timeout}); err != nil {
			return err
		}
	}
	return nil
}

func (h *updateHelper) loadSnapshotImages(ctx context.Context, source string) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	decompressed, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer decompressed.Close()
	command := exec.CommandContext(ctx, "docker", "image", "load")
	command.Stdin = decompressed
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker image load failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (h *updateHelper) restorePostgres(ctx context.Context, source string) error {
	containers, err := h.managedContainers(ctx)
	if err != nil {
		return err
	}
	var postgresID string
	for _, item := range containers {
		if item.Labels["gg.dockside.component"] == "postgres" {
			postgresID = item.ID
			break
		}
	}
	if postgresID == "" {
		return errors.New("PostgreSQL container not found")
	}
	inspected, err := h.docker.ContainerInspect(ctx, postgresID)
	if err != nil {
		return err
	}
	user, database := "dockside", "dockside"
	for _, value := range inspected.Config.Env {
		if strings.HasPrefix(value, "POSTGRES_USER=") {
			user = strings.TrimPrefix(value, "POSTGRES_USER=")
		}
		if strings.HasPrefix(value, "POSTGRES_DB=") {
			database = strings.TrimPrefix(value, "POSTGRES_DB=")
		}
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	command := exec.CommandContext(ctx, "docker", "exec", "-i", postgresID, "psql", "-v", "ON_ERROR_STOP=1", "-U", user, "-d", database)
	command.Stdin = file
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("psql restore failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func extractTarGzip(source, destination string) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("snapshot contains unsafe path %q", header.Name)
		}
		target := filepath.Join(destination, clean)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode).Perm()); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode).Perm())
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, archive)
			closeErr := output.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("snapshot contains unsupported entry %q", header.Name)
		}
	}
}

func archiveDirectory(source, destination string, excluded func(string) bool) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	compressed := gzip.NewWriter(file)
	archive := tar.NewWriter(compressed)
	walkErr := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if excluded(relative) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relative
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(archive, input)
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	archiveErr, compressErr, closeErr := archive.Close(), compressed.Close(), file.Close()
	if walkErr != nil {
		return walkErr
	}
	if archiveErr != nil {
		return archiveErr
	}
	if compressErr != nil {
		return compressErr
	}
	return closeErr
}

func projectSnapshotExcluded(path string) bool {
	root := strings.Split(path, "/")[0]
	switch root {
	case ".git", "node_modules", "artifacts", "data":
		return true
	case "web":
		return strings.Contains(path, "/node_modules") || strings.Contains(path, "/dist")
	default:
		return false
	}
}

func copyUpdatePath(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(destination, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyUpdatePath(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func setEnvironmentVersion(path, version string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	found := false
	for index, line := range lines {
		if strings.HasPrefix(line, "DOCKSIDE_VERSION=") {
			lines[index], found = "DOCKSIDE_VERSION="+version, true
		}
	}
	if !found {
		lines = append(lines, "DOCKSIDE_VERSION="+version)
	}
	temporary := path + ".update"
	if err := os.WriteFile(temporary, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
