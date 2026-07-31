package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dockside-gg/game-panel/internal/store"
)

type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func writeProblem(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	title := "Internal server error"
	detail := "The request could not be completed."
	kind := "about:blank"

	switch {
	case errors.Is(err, store.ErrUnauthorized):
		status, title, detail = http.StatusUnauthorized, "Unauthorized", "Authentication is required or no longer valid."
	case errors.Is(err, store.ErrMFARequired):
		status, title, detail = http.StatusForbidden, "Discord MFA required", "Enable MFA on the Discord account before signing in."
	case errors.Is(err, store.ErrInviteInvalid):
		status, title, detail = http.StatusGone, "Invite unavailable", "This invitation is invalid, expired, revoked, or already used."
	case errors.Is(err, store.ErrAlreadyClaimed):
		status, title, detail = http.StatusConflict, "Installation already claimed", "The primary owner has already been configured."
	case errors.Is(err, store.ErrNotFound):
		status, title, detail = http.StatusNotFound, "Not found", "The requested resource was not found."
	case errors.Is(err, store.ErrConflict):
		status, title, detail = http.StatusConflict, "Conflict", err.Error()
	case errors.Is(err, errForbidden):
		status, title, detail = http.StatusForbidden, "Forbidden", "You do not have permission to perform this action."
	case errors.Is(err, errBadRequest):
		status, title, detail = http.StatusBadRequest, "Invalid request", err.Error()
	}

	writeJSON(w, status, problem{
		Type:      kind,
		Title:     title,
		Status:    status,
		Detail:    detail,
		RequestID: requestIDFromContext(r.Context()),
	})
}
