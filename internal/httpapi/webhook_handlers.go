package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/dockside-gg/game-panel/internal/webhooks"
	"github.com/go-chi/chi/v5"
)

type createWebhookRequest struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type updateWebhookRequest struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	DeliverEvents  *bool    `json:"deliver_events,omitempty"`
	DeliverBackups *bool    `json:"deliver_backups,omitempty"`
	EventFilters   []string `json:"event_filters,omitempty"`
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
	input.URL = strings.TrimSpace(input.URL)
	if input.Name == "" || len(input.Name) > 120 {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid webhook configuration")))
		return
	}
	kind := webhookKind(input.URL)
	if err := webhooks.ValidateURL(input.URL, kind); err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	signingSecret := ""
	if kind == "generic" {
		var err error
		signingSecret, err = identity.Token(32)
		if err != nil {
			writeProblem(w, r, err)
			return
		}
	}
	item, err := s.store.CreateWebhook(
		r.Context(), serverID, session.User.ID, input.Name, kind,
		input.URL, signingSecret, s.box,
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"webhook": item})
}

func (s *Server) setWebhookEnabled(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	var input updateWebhookRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.EventFilters) > 100 {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("too many webhook filters")))
		return
	}
	for index := range input.EventFilters {
		input.EventFilters[index] = strings.TrimSpace(input.EventFilters[index])
		if input.EventFilters[index] == "" || len(input.EventFilters[index]) > 160 {
			writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid webhook event filter")))
			return
		}
	}
	if err := s.store.UpdateWebhook(
		r.Context(), chi.URLParam(r, "serverID"), chi.URLParam(r, "webhookID"),
		input.Enabled, input.DeliverEvents, input.DeliverBackups, input.EventFilters,
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func webhookKind(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "generic"
	}
	host := strings.ToLower(parsed.Hostname())
	if (host == "discord.com" || host == "discordapp.com") &&
		strings.HasPrefix(parsed.Path, "/api/webhooks/") {
		return "discord"
	}
	return "generic"
}

func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	delivery, err := s.store.QueueWebhookTest(
		r.Context(), chi.URLParam(r, "serverID"), session.User.ID, chi.URLParam(r, "webhookID"),
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"delivery": delivery})
}

func (s *Server) webhookDelivery(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.WebhookDeliveryByID(
		r.Context(), chi.URLParam(r, "serverID"), chi.URLParam(r, "webhookID"),
		chi.URLParam(r, "deliveryID"),
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delivery": item})
}

func (s *Server) retryWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RetryWebhookDelivery(
		r.Context(), chi.URLParam(r, "serverID"), chi.URLParam(r, "webhookID"),
		chi.URLParam(r, "deliveryID"),
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	item, err := s.store.WebhookDeliveryByID(
		r.Context(), chi.URLParam(r, "serverID"), chi.URLParam(r, "webhookID"),
		chi.URLParam(r, "deliveryID"),
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"delivery": item})
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
