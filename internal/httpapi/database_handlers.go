package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/dockside-gg/game-panel/internal/store"
	"github.com/go-chi/chi/v5"
)

var databaseNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

type createDatabaseRequest struct {
	Name string `json:"name"`
}

type deleteDatabaseRequest struct {
	ConfirmName string `json:"confirm_name"`
}

func (s *Server) listServerDatabases(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if err := s.store.ServerExists(r.Context(), serverID); err != nil {
		writeProblem(w, r, err)
		return
	}
	items, err := s.store.ListDatabases(r.Context(), serverID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"databases": items})
}

func (s *Server) createServerDatabase(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	var input createDatabaseRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.ToLower(strings.TrimSpace(input.Name))
	if !databaseNamePattern.MatchString(input.Name) {
		writeProblem(w, r, errors.Join(
			errBadRequest,
			errors.New("database names must start with a letter and contain only lowercase letters, numbers, and underscores"),
		))
		return
	}
	provision, err := s.store.PrepareDatabase(
		r.Context(), chi.URLParam(r, "serverID"), session.User.ID, input.Name, s.box,
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	result, err := s.engine.CreateDatabase(
		r.Context(),
		provision.Database.ServerID,
		provision.Database.ID,
		provision.EngineRequest(),
	)
	if err != nil {
		if recordErr := s.store.FailDatabaseProvision(r.Context(), provision, err); recordErr != nil {
			s.logger.Error(
				"database provisioning and failure recording both failed",
				"database_id", provision.Database.ID,
				"provision_error", err,
				"record_error", recordErr,
			)
		}
		writeProblem(w, r, errors.Join(store.ErrConflict, err))
		return
	}
	if err := s.store.CompleteDatabaseProvision(r.Context(), provision, result); err != nil {
		writeProblem(w, r, err)
		return
	}
	provision.Database.Status = "ready"
	writeJSON(w, http.StatusCreated, map[string]any{
		"database": provision.Database,
		"password": provision.Password,
	})
}

func (s *Server) deleteServerDatabase(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	var input deleteDatabaseRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	serverID := chi.URLParam(r, "serverID")
	databaseID := chi.URLParam(r, "databaseID")
	job, err := s.store.DatabaseJob(r.Context(), serverID, databaseID, s.box)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if input.ConfirmName != job.Database.Name {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("confirmation name does not match")))
		return
	}
	removeHost, err := s.store.DatabaseRemovalPlan(r.Context(), serverID, databaseID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if err := s.engine.DeleteDatabase(
		r.Context(), serverID, databaseID, job.EngineRequest(), removeHost,
	); err != nil {
		writeProblem(w, r, errors.Join(store.ErrConflict, err))
		return
	}
	if err := s.store.CompleteDatabaseDeletion(
		r.Context(), serverID, databaseID, session.User.ID, removeHost,
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateServerDatabasePassword(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	databaseID := chi.URLParam(r, "databaseID")
	job, err := s.store.DatabaseJob(r.Context(), serverID, databaseID, s.box)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if job.Database.Status != "ready" {
		writeProblem(w, r, errors.Join(store.ErrConflict, errors.New("database is not ready")))
		return
	}
	password, err := identity.Token(32)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	job.Password = password
	if err := s.engine.RotateDatabasePassword(
		r.Context(), serverID, databaseID, job.EngineRequest(),
	); err != nil {
		writeProblem(w, r, errors.Join(store.ErrConflict, err))
		return
	}
	if err := s.store.RotateDatabasePassword(
		r.Context(), serverID, databaseID, session.User.ID, password, s.box,
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"password": password})
}
