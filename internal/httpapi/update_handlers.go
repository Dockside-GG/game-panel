package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dockside-gg/game-panel/internal/buildinfo"
	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/dockside-gg/game-panel/internal/updates"
)

type panelUpdateResponse struct {
	Build  buildinfo.Info                 `json:"build"`
	Check  updates.Check                  `json:"check"`
	Status engineclient.PanelUpdateStatus `json:"status"`
}

type panelUpdateChannelRequest struct {
	IncludePrereleases bool `json:"include_prereleases"`
}

type applyPanelUpdateRequest struct {
	Version            string `json:"version"`
	IncludePrereleases bool   `json:"include_prereleases"`
}

func (s *Server) panelUpdate(w http.ResponseWriter, r *http.Request) {
	build := buildinfo.Current()
	status, err := s.engine.PanelUpdateStatus(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, panelUpdateResponse{
		Build:  build,
		Check:  s.updates.Current(build.Version, queryBoolean(r, "include_prereleases")),
		Status: status,
	})
}

func (s *Server) checkPanelUpdate(w http.ResponseWriter, r *http.Request) {
	var input panelUpdateChannelRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	s.writePanelUpdate(w, r, true, input.IncludePrereleases)
}

func (s *Server) writePanelUpdate(w http.ResponseWriter, r *http.Request, force, includePrereleases bool) {
	build := buildinfo.Current()
	check, err := s.updates.Check(r.Context(), build.Version, includePrereleases, force)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	status, err := s.engine.PanelUpdateStatus(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, panelUpdateResponse{Build: build, Check: check, Status: status})
}

func (s *Server) applyPanelUpdate(w http.ResponseWriter, r *http.Request) {
	var input applyPanelUpdateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Version = strings.TrimPrefix(strings.TrimSpace(input.Version), "v")
	build := buildinfo.Current()
	check, err := s.updates.Check(r.Context(), build.Version, input.IncludePrereleases, true)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if !check.UpdatesSupported {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New(check.Reason)))
		return
	}
	if !check.UpdateAvailable || check.Latest == nil || check.Latest.Version != input.Version {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("the requested release is not the current update for this panel and channel")))
		return
	}
	release, err := s.updates.Release(r.Context(), input.Version, input.IncludePrereleases)
	if err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	status, err := s.engine.ApplyPanelUpdate(r.Context(), engineclient.PanelUpdateRequest{
		CurrentVersion: build.Version,
		TargetVersion:  release.Version,
		ReleaseURL:     release.URL,
		ArchiveURL:     release.ArchiveURL,
		ChecksumsURL:   release.ChecksumsURL,
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	session, _ := sessionFromContext(r.Context())
	_ = s.store.AddAudit(
		r.Context(), session.User.ID, "installation.update.request",
		"installation", s.cfg.InstanceID, requestIDFromContext(r.Context()), clientIP(r), r.UserAgent(),
		map[string]any{"from": build.Version, "to": release.Version, "release": release.URL},
	)
	writeJSON(w, http.StatusAccepted, status)
}

func queryBoolean(r *http.Request, name string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
