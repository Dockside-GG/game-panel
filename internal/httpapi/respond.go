package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/dockside-gg/game-panel/internal/sanitize"
	"github.com/dockside-gg/game-panel/internal/store"
)

type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Code      string `json:"code,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
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
	code := "internal_error"
	retryable := false
	var engineError *engineclient.APIError

	switch {
	case errors.Is(err, store.ErrUnauthorized):
		status, title, detail = http.StatusUnauthorized, "Unauthorized", "Authentication is required or no longer valid."
		code = "unauthorized"
	case errors.Is(err, store.ErrMFARequired):
		status, title, detail = http.StatusForbidden, "Discord MFA required", "Enable MFA on the Discord account before signing in."
		code = "discord_mfa_required"
	case errors.Is(err, store.ErrInviteInvalid):
		status, title, detail = http.StatusGone, "Invite unavailable", "This invitation is invalid, expired, revoked, or already used."
		code = "invite_unavailable"
	case errors.Is(err, store.ErrAlreadyClaimed):
		status, title, detail = http.StatusConflict, "Installation already claimed", "The primary owner has already been configured."
		code = "installation_claimed"
	case errors.Is(err, store.ErrNotFound):
		status, title, detail = http.StatusNotFound, "Not found", "The requested resource was not found."
		code = "not_found"
	case errors.Is(err, store.ErrConflict):
		status, title, detail = http.StatusConflict, "Conflict", sanitize.Text(err.Error())
		code = "conflict"
	case errors.Is(err, errForbidden):
		status, title, detail = http.StatusForbidden, "Forbidden", "You do not have permission to perform this action."
		code = "forbidden"
	case errors.Is(err, errBadRequest):
		status, title, detail = http.StatusBadRequest, "Invalid request", sanitize.Text(err.Error())
		code = "invalid_request"
	case errors.As(err, &engineError):
		status = engineError.Status
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		title = "Runtime operation failed"
		detail = sanitize.Text(engineError.Detail)
		code = engineError.Code
		retryable = engineError.Retryable
	}

	writeJSON(w, status, problem{
		Type:      kind,
		Title:     title,
		Status:    status,
		Detail:    detail,
		RequestID: requestIDFromContext(r.Context()),
		Code:      code,
		Retryable: retryable,
	})
}
