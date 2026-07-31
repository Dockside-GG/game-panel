package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/go-chi/chi/v5"
)

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

type activateUserRequest struct {
	PanelRole string `json:"panel_role"`
}

type serverAccessRequest struct {
	Bindings []struct {
		ServerID string `json:"server_id"`
		Role     string `json:"role"`
	} `json:"bindings"`
}

type updateUserAccessRequest struct {
	PanelRole string `json:"panel_role"`
	Status    string `json:"status"`
}

type updateInstallationSettingsRequest struct {
	MFAPolicy string `json:"mfa_policy"`
}

func (s *Server) activateUser(w http.ResponseWriter, r *http.Request) {
	var input activateUserRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	userID := chi.URLParam(r, "userID")
	if err := s.store.ActivateUser(r.Context(), session.User.ID, userID, input.PanelRole); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rejectUser(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	userID := chi.URLParam(r, "userID")
	if err := s.store.RejectUser(r.Context(), session.User.ID, userID); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) updateUserAccess(w http.ResponseWriter, r *http.Request) {
	var input updateUserAccessRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if err := s.store.UpdateUserAccess(
		r.Context(), session.User.ID, chi.URLParam(r, "userID"),
		input.PanelRole, input.Status,
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) userServerAccess(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	bindings, err := s.store.UserServerAccess(r.Context(), userID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bindings": bindings})
}

func (s *Server) setUserServerAccess(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	userID := chi.URLParam(r, "userID")
	var input serverAccessRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.Bindings) > 500 {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("too many server access bindings")))
		return
	}
	bindings := make(map[string]string, len(input.Bindings))
	for _, binding := range input.Bindings {
		if binding.ServerID == "" ||
			(binding.Role != "administrator" && binding.Role != "operator" && binding.Role != "viewer") {
			writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid server access binding")))
			return
		}
		if _, duplicate := bindings[binding.ServerID]; duplicate {
			writeProblem(w, r, errors.Join(errBadRequest, errors.New("duplicate server access binding")))
			return
		}
		bindings[binding.ServerID] = binding.Role
	}
	if err := s.store.SetUserServerAccess(
		r.Context(), session.User.ID, userID, bindings,
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) installationSettings(w http.ResponseWriter, r *http.Request) {
	status, err := s.store.SetupStatus(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mfa_policy": status.MFAPolicy})
}

func (s *Server) updateInstallationSettings(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	var input updateInstallationSettingsRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.UpdateMFAPolicy(
		r.Context(), session.User.ID, input.MFAPolicy,
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request) {
	invites, err := s.store.ListInvites(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": invites})
}

type createInviteRequest struct {
	Label          string `json:"label"`
	ExpiresInHours int    `json:"expires_in_hours"`
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	var input createInviteRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ExpiresInHours == 0 {
		input.ExpiresInHours = 24
	}
	if input.ExpiresInHours < 1 || input.ExpiresInHours > 168 || len(strings.TrimSpace(input.Label)) > 120 {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("expiration must be 1-168 hours and label at most 120 characters")))
		return
	}
	rawToken, err := identity.Token(32)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	session, _ := sessionFromContext(r.Context())
	invite, err := s.store.CreateInvite(
		r.Context(),
		session.User.ID,
		input.Label,
		rawToken,
		time.Now().UTC().Add(time.Duration(input.ExpiresInHours)*time.Hour),
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if err := s.store.AddAudit(
		r.Context(),
		session.User.ID,
		"invite.create",
		"invite",
		invite.ID,
		requestIDFromContext(r.Context()),
		clientIP(r),
		r.UserAgent(),
		map[string]any{"expires_at": invite.ExpiresAt, "label": invite.Label},
	); err != nil {
		s.logger.Error("write invite audit failed", "error", err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"invite":     invite,
		"invite_url": s.cfg.PublicURL.String() + "/invite/" + rawToken,
	})
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request) {
	inviteID := chi.URLParam(r, "inviteID")
	if err := s.store.RevokeInvite(r.Context(), inviteID); err != nil {
		writeProblem(w, r, err)
		return
	}
	session, _ := sessionFromContext(r.Context())
	if err := s.store.AddAudit(
		r.Context(),
		session.User.ID,
		"invite.revoke",
		"invite",
		inviteID,
		requestIDFromContext(r.Context()),
		clientIP(r),
		r.UserAgent(),
		nil,
	); err != nil {
		s.logger.Error("write invite revoke audit failed", "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
}
