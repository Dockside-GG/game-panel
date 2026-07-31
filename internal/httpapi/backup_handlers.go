package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dockside-gg/game-panel/internal/store"
	"github.com/go-chi/chi/v5"
)

type createBackupRequest struct {
	Name             string   `json:"name"`
	IncludePaths     []string `json:"include_paths"`
	ExcludeGlobs     []string `json:"exclude_globs"`
	RetentionDays    *int     `json:"retention_days"`
	DiscordWebhookID *string  `json:"discord_webhook_id"`
	DiscordFormat    string   `json:"discord_format"`
}

type deleteBackupRequest struct {
	ConfirmName string `json:"confirm_name"`
}

type restoreBackupRequest struct {
	ConfirmServerName string `json:"confirm_server_name"`
	ConfirmBackupName string `json:"confirm_backup_name"`
}

type lockBackupRequest struct {
	Locked bool `json:"locked"`
}

func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if _, err := s.store.ServerByID(r.Context(), serverID); err != nil {
		writeProblem(w, r, err)
		return
	}
	items, err := s.store.ListBackups(r.Context(), serverID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": items})
}

func (s *Server) createBackup(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canOperate(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	var input createBackupRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.DiscordFormat = strings.ToLower(strings.TrimSpace(input.DiscordFormat))
	if input.DiscordWebhookID != nil && input.DiscordFormat == "" {
		input.DiscordFormat = "zip"
	}
	if input.Name == "" || len(input.Name) > 120 ||
		!validBackupRules(input.IncludePaths) || !validBackupRules(input.ExcludeGlobs) ||
		(input.RetentionDays != nil && (*input.RetentionDays < 1 || *input.RetentionDays > 3650)) ||
		(input.DiscordWebhookID != nil && input.DiscordFormat != "zip" && input.DiscordFormat != "archive") {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid backup name, filters, or retention days")))
		return
	}
	item, err := s.store.CreateBackup(
		r.Context(), serverID, session.User.ID, input.Name, input.IncludePaths,
		input.ExcludeGlobs, input.RetentionDays, input.DiscordWebhookID, input.DiscordFormat,
	)
	if err != nil {
		s.logger.Error("create backup record failed", "server_id", serverID, "error", err)
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canOperate(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	backupID := chi.URLParam(r, "backupID")
	var input deleteBackupRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.store.BackupByID(r.Context(), serverID, backupID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if input.ConfirmName != item.Name {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("confirmation name does not match")))
		return
	}
	previous, err := s.store.BeginBackupDeletion(r.Context(), serverID, backupID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if err := s.engine.DeleteBackup(r.Context(), serverID, backupID); err != nil {
		_ = s.store.CancelBackupDeletion(r.Context(), serverID, backupID, previous)
		writeProblem(w, r, err)
		return
	}
	if err := s.store.DeleteBackupRecord(r.Context(), serverID, backupID, session.User.ID); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canOperate(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	backupID := chi.URLParam(r, "backupID")
	var input restoreBackupRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	server, err := s.store.ServerByID(r.Context(), serverID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	backup, err := s.store.BackupByID(r.Context(), serverID, backupID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if server.Status != "stopped" || backup.Status != "succeeded" || backup.SHA256 == nil ||
		input.ConfirmServerName != server.Name || input.ConfirmBackupName != backup.Name {
		writeProblem(w, r, errors.Join(store.ErrConflict, errors.New("server must be stopped and confirmation names must match")))
		return
	}
	if err := s.engine.RestoreBackup(r.Context(), serverID, backupID, *backup.SHA256); err != nil {
		writeProblem(w, r, err)
		return
	}
	if err := s.store.RecordBackupRestore(r.Context(), serverID, backupID, session.User.ID); err != nil {
		s.logger.Error("record backup restore failed", "server_id", serverID, "backup_id", backupID, "error", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) lockBackup(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canOperate(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	backupID := chi.URLParam(r, "backupID")
	var input lockBackupRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.SetBackupLocked(r.Context(), serverID, backupID, session.User.ID, input.Locked); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validBackupRules(rules []string) bool {
	if len(rules) > 100 {
		return false
	}
	for _, rule := range rules {
		rule = strings.TrimSpace(strings.ReplaceAll(rule, "\\", "/"))
		if rule == "" || strings.HasPrefix(rule, "/") || strings.Contains(rule, "..") || len(rule) > 512 {
			return false
		}
	}
	return true
}
