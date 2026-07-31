package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/dockside-gg/game-panel/internal/webhooks"
	"github.com/go-chi/chi/v5"
)

type createWebhookRequest struct {
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	URL           string   `json:"url"`
	SigningSecret string   `json:"signing_secret"`
	EventFilters  []string `json:"event_filters"`
}

type webhookEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if _, err := s.store.ServerByID(r.Context(), serverID); err != nil {
		writeProblem(w, r, err)
		return
	}
	items, err := s.store.ListWebhooks(r.Context(), serverID, s.box)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": items})
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	var input createWebhookRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
	input.URL = strings.TrimSpace(input.URL)
	input.SigningSecret = strings.TrimSpace(input.SigningSecret)
	if input.Name == "" || len(input.Name) > 120 ||
		(input.Kind != "discord" && input.Kind != "generic") ||
		len(input.EventFilters) > 100 || len(input.SigningSecret) > 512 {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid webhook configuration")))
		return
	}
	if err := webhooks.ValidateURL(input.URL, input.Kind); err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	for _, filter := range input.EventFilters {
		if strings.TrimSpace(filter) == "" || len(filter) > 160 {
			writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid webhook event filter")))
			return
		}
	}
	generatedSecret := ""
	if input.Kind == "generic" && input.SigningSecret == "" {
		var err error
		generatedSecret, err = identity.Token(32)
		if err != nil {
			writeProblem(w, r, err)
			return
		}
		input.SigningSecret = generatedSecret
	}
	item, err := s.store.CreateWebhook(
		r.Context(), serverID, session.User.ID, input.Name, input.Kind,
		input.URL, input.SigningSecret, input.EventFilters, s.box,
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"webhook": item, "signing_secret": generatedSecret,
	})
}

func (s *Server) setWebhookEnabled(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	var input webhookEnabledRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.SetWebhookEnabled(
		r.Context(), chi.URLParam(r, "serverID"), chi.URLParam(r, "webhookID"), input.Enabled,
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	if err := s.store.QueueWebhookTest(
		r.Context(), chi.URLParam(r, "serverID"), session.User.ID, chi.URLParam(r, "webhookID"),
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	if err := s.store.DeleteWebhook(
		r.Context(), chi.URLParam(r, "serverID"), chi.URLParam(r, "webhookID"),
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
