package httpapi

import (
	"net/http"
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	dashboard, err := s.store.Dashboard(
		r.Context(), session.User.ID, session.User.PanelRole,
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	host, hostErr := s.engine.Host(r.Context())
	response := map[string]any{
		"servers":         dashboard.Servers,
		"recent_activity": dashboard.RecentActivity,
		"host":            host,
	}
	if hostErr != nil {
		response["host_error"] = "Docker engine status is temporarily unavailable."
	}
	if session.User.PanelRole == "owner" || session.User.PanelRole == "administrator" {
		containers, containersErr := s.engine.SystemContainers(r.Context())
		if containersErr != nil {
			response["system_containers_error"] = "System-container health is temporarily unavailable."
		} else {
			response["system_containers"] = containers
		}
		response["can_restart_worker"] = session.User.PanelRole == "owner"
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) hostStatus(w http.ResponseWriter, r *http.Request) {
	host, err := s.engine.Host(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, host)
}

func (s *Server) listSystemContainers(w http.ResponseWriter, r *http.Request) {
	containers, err := s.engine.SystemContainers(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"containers": containers})
}

func (s *Server) restartSystemWorker(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.engine.RestartWorker(r.Context()); err != nil {
		writeProblem(w, r, err)
		return
	}
	if err := s.store.AddAudit(
		r.Context(),
		session.User.ID,
		"system.worker.restart",
		"system_container",
		"worker",
		requestIDFromContext(r.Context()),
		clientIP(r),
		r.UserAgent(),
		map[string]any{"component": "worker"},
	); err != nil {
		s.logger.Error("record worker restart audit failed", "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
}
