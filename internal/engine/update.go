package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/dockside-gg/game-panel/internal/identity"
)

const (
	panelUpdateDirectory  = "panel-updates"
	panelUpdateStatusFile = "status.json"
)

var updateVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

type panelUpdatePlan struct {
	ID             string    `json:"id"`
	InstanceID     string    `json:"instance_id"`
	CurrentVersion string    `json:"current_version"`
	TargetVersion  string    `json:"target_version"`
	ReleaseURL     string    `json:"release_url"`
	ArchiveURL     string    `json:"archive_url"`
	ChecksumsURL   string    `json:"checksums_url"`
	ComposeProject string    `json:"compose_project"`
	ComposeFiles   []string  `json:"compose_files"`
	EngineImage    string    `json:"engine_image"`
	CreatedAt      time.Time `json:"created_at"`
}

func (s *Server) panelUpdateStatus(w http.ResponseWriter, _ *http.Request) {
	status, err := readPanelUpdateStatus(s.cfg.BackupRoot)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "panel update status is unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) startPanelUpdate(w http.ResponseWriter, r *http.Request) {
	var input engineclient.PanelUpdateRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid panel update request"})
		return
	}
	if err := validatePanelUpdateRequest(input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	current, err := readPanelUpdateStatus(s.cfg.BackupRoot)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "panel update status is unavailable"})
		return
	}
	if panelUpdateActive(current.State) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a panel update is already running"})
		return
	}
	plan, mounts, err := s.preparePanelUpdate(r.Context(), input)
	if err != nil {
		s.logger.Error("prepare panel update helper failed", "error", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the update helper could not be prepared"})
		return
	}
	status := newPanelUpdateStatus(plan)
	if err := writePanelUpdateStatus(s.cfg.BackupRoot, status); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "panel update status could not be initialized"})
		return
	}
	planPath := filepath.Join(s.cfg.BackupRoot, panelUpdateDirectory, "control", plan.ID+".json")
	if err := writePrivateJSON(planPath, plan); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "panel update plan could not be written"})
		return
	}
	created, err := s.docker.ContainerCreate(r.Context(), &container.Config{
		Image:      plan.EngineImage,
		User:       "0:0",
		Entrypoint: []string{"/dockside-engine"},
		Cmd:        []string{"update-helper", "/var/lib/dockside/backups/panel-updates/control/" + plan.ID + ".json"},
		Labels: map[string]string{
			"gg.dockside.system": "false", "gg.dockside.instance": s.cfg.InstanceID,
			"gg.dockside.kind": "panel-update-helper", "gg.dockside.update": plan.ID,
		},
	}, &container.HostConfig{
		AutoRemove:     true,
		NetworkMode:    "bridge",
		Mounts:         mounts,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
		ReadonlyRootfs: false,
	}, &network.NetworkingConfig{}, nil, "dockside-update-"+strings.ReplaceAll(plan.ID, "-", "")[:12])
	if err != nil {
		markPanelUpdateLaunchFailed(s.cfg.BackupRoot, status, err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the update helper could not be created"})
		return
	}
	if err := s.docker.ContainerStart(r.Context(), created.ID, container.StartOptions{}); err != nil {
		_ = s.docker.ContainerRemove(context.WithoutCancel(r.Context()), created.ID, container.RemoveOptions{Force: true})
		markPanelUpdateLaunchFailed(s.cfg.BackupRoot, status, err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "the update helper could not be started"})
		return
	}
	writeJSON(w, http.StatusAccepted, status)
}

func (s *Server) preparePanelUpdate(ctx context.Context, input engineclient.PanelUpdateRequest) (panelUpdatePlan, []mount.Mount, error) {
	managed, err := s.listSystemContainers(ctx)
	if err != nil {
		return panelUpdatePlan{}, nil, err
	}
	var engineContainerID string
	for _, summary := range managed {
		if summary.Labels["gg.dockside.component"] == "engine" {
			if engineContainerID != "" {
				return panelUpdatePlan{}, nil, errors.New("multiple engine containers found")
			}
			engineContainerID = summary.ID
		}
	}
	if engineContainerID == "" {
		return panelUpdatePlan{}, nil, errors.New("engine container not found")
	}
	inspected, err := s.docker.ContainerInspect(ctx, engineContainerID)
	if err != nil {
		return panelUpdatePlan{}, nil, err
	}
	projectRoot := strings.TrimSpace(inspected.Config.Labels["com.docker.compose.project.working_dir"])
	composeProject := strings.TrimSpace(inspected.Config.Labels["com.docker.compose.project"])
	if projectRoot == "" || composeProject == "" {
		return panelUpdatePlan{}, nil, errors.New("Docker Compose project metadata is unavailable")
	}
	composeFiles := composeFileBasenames(inspected.Config.Labels["com.docker.compose.project.config_files"])
	if len(composeFiles) == 0 {
		composeFiles = []string{"compose.yml"}
	}
	for _, name := range composeFiles {
		if name == "compose.dev.yml" || name == "compose.test.yml" {
			return panelUpdatePlan{}, nil, errors.New("in-panel updates are disabled for development and test Compose projects")
		}
	}
	mounts := []mount.Mount{
		{Type: mount.TypeBind, Source: "/var/run/docker.sock", Target: "/var/run/docker.sock"},
		{Type: mount.TypeBind, Source: projectRoot, Target: "/host/project"},
	}
	for _, item := range inspected.Mounts {
		switch item.Destination {
		case s.cfg.BackupRoot:
			mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: item.Source, Target: s.cfg.BackupRoot})
		}
	}
	if len(mounts) != 3 {
		return panelUpdatePlan{}, nil, errors.New("engine backup mount is unavailable")
	}
	id, err := identity.NewUUID()
	if err != nil {
		return panelUpdatePlan{}, nil, err
	}
	return panelUpdatePlan{
		ID: id, InstanceID: s.cfg.InstanceID, CurrentVersion: input.CurrentVersion,
		TargetVersion: input.TargetVersion, ReleaseURL: input.ReleaseURL,
		ArchiveURL: input.ArchiveURL, ChecksumsURL: input.ChecksumsURL,
		ComposeProject: composeProject, ComposeFiles: composeFiles,
		EngineImage: inspected.Config.Image, CreatedAt: time.Now().UTC(),
	}, mounts, nil
}

func validatePanelUpdateRequest(input engineclient.PanelUpdateRequest) error {
	if !updateVersionPattern.MatchString(input.CurrentVersion) || !updateVersionPattern.MatchString(input.TargetVersion) {
		return errors.New("invalid update version")
	}
	if input.CurrentVersion == input.TargetVersion {
		return errors.New("target release is already installed")
	}
	prefix := "/Dockside-GG/game-panel/releases/download/v" + input.TargetVersion + "/"
	for name, raw := range map[string]string{"archive": input.ArchiveURL, "checksums": input.ChecksumsURL} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") || !strings.HasPrefix(parsed.EscapedPath(), prefix) {
			return fmt.Errorf("invalid Dockside %s URL", name)
		}
	}
	archiveName := "dockside-game-panel-" + input.TargetVersion + ".zip"
	if !strings.HasSuffix(input.ArchiveURL, "/"+archiveName) || !strings.HasSuffix(input.ChecksumsURL, "/SHA256SUMS") {
		return errors.New("release assets do not match the target version")
	}
	release, err := url.Parse(input.ReleaseURL)
	if err != nil || release.Scheme != "https" || !strings.EqualFold(release.Hostname(), "github.com") || !strings.HasPrefix(release.EscapedPath(), "/Dockside-GG/game-panel/releases/") {
		return errors.New("invalid Dockside release URL")
	}
	return nil
}

func composeFileBasenames(raw string) []string {
	seen := map[string]bool{}
	var result []string
	for _, item := range strings.Split(raw, ",") {
		name := filepath.Base(strings.ReplaceAll(strings.TrimSpace(item), "\\", "/"))
		if name == "." || name == "" || seen[name] {
			continue
		}
		if !strings.HasPrefix(name, "compose") || !(strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")) {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	return result
}

func readPanelUpdateStatus(backupRoot string) (engineclient.PanelUpdateStatus, error) {
	path := filepath.Join(backupRoot, panelUpdateDirectory, panelUpdateStatusFile)
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return engineclient.PanelUpdateStatus{State: "idle", UpdatedAt: time.Now().UTC()}, nil
	}
	if err != nil {
		return engineclient.PanelUpdateStatus{}, err
	}
	var result engineclient.PanelUpdateStatus
	if err := json.Unmarshal(content, &result); err != nil {
		return result, err
	}
	return result, nil
}

func writePanelUpdateStatus(backupRoot string, status engineclient.PanelUpdateStatus) error {
	status.UpdatedAt = time.Now().UTC()
	return writePrivateJSON(filepath.Join(backupRoot, panelUpdateDirectory, panelUpdateStatusFile), status)
}

func writePrivateJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(content, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func newPanelUpdateStatus(plan panelUpdatePlan) engineclient.PanelUpdateStatus {
	now := time.Now().UTC()
	return engineclient.PanelUpdateStatus{
		ID: plan.ID, State: "queued", Phase: "preparing", Message: "The signed release assets are being prepared.",
		CurrentVersion: plan.CurrentVersion, TargetVersion: plan.TargetVersion,
		StartedAt: &now, UpdatedAt: now,
	}
}

func markPanelUpdateLaunchFailed(root string, status engineclient.PanelUpdateStatus, cause error) {
	now := time.Now().UTC()
	status.State, status.Phase, status.Error = "failed", "launch", cause.Error()
	status.Message, status.CompletedAt = "The update helper could not start; the running panel was not changed.", &now
	_ = writePanelUpdateStatus(root, status)
}

func panelUpdateActive(state string) bool {
	switch state {
	case "queued", "running":
		return true
	default:
		return false
	}
}
