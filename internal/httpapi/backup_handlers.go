package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/dockside-gg/game-panel/internal/archiveutil"
	"github.com/dockside-gg/game-panel/internal/store"
	"github.com/go-chi/chi/v5"
)

type createBackupRequest struct {
	Name          string   `json:"name"`
	IncludePaths  []string `json:"include_paths"`
	ExcludeGlobs  []string `json:"exclude_globs"`
	RetentionDays *int     `json:"retention_days"`
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
	if input.Name == "" || len(input.Name) > 120 ||
		!validBackupRules(input.IncludePaths) || !validBackupRules(input.ExcludeGlobs) ||
		(input.RetentionDays != nil && (*input.RetentionDays < 1 || *input.RetentionDays > 3650)) {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid backup name, filters, or retention days")))
		return
	}
	item, err := s.store.CreateBackup(
		r.Context(), serverID, session.User.ID, input.Name, input.IncludePaths,
		input.ExcludeGlobs, input.RetentionDays,
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

func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	backupID := chi.URLParam(r, "backupID")
	item, err := s.store.BackupByID(r.Context(), serverID, backupID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if item.Status != "succeeded" || item.SHA256 == nil {
		writeProblem(w, r, errors.Join(store.ErrConflict, errors.New("backup is not ready to download")))
		return
	}
	download, err := s.engine.OpenBackup(r.Context(), serverID, backupID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	defer download.Body.Close()

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "zip"
	}
	filename := archiveutil.SafeFilename(item.Name)
	var content io.Reader = download.Body
	if format == "zip" {
		zipped := archiveutil.ZipTarGzip(download.Body)
		defer zipped.Close()
		content = zipped
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.zip"`)
	} else if format == "archive" {
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`.tar.gz"`)
		if download.ContentLength >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(download.ContentLength, 10))
		}
	} else {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("download format must be zip or archive")))
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.CopyBuffer(w, content, make([]byte, 128<<10)); err != nil &&
		r.Context().Err() == nil {
		s.logger.Warn("proxy backup download failed", "server_id", serverID, "backup_id", backupID, "error", err)
	}
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canOperate(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	backupID := chi.URLParam(r, "backupID")
	operationID, err := s.store.QueueBackupRestore(
		r.Context(), serverID, backupID, session.User.ID,
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"operation_id": operationID})
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

func (s *Server) retryBackupDelivery(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canOperate(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	if err := s.store.RetryBackupDelivery(
		r.Context(),
		chi.URLParam(r, "serverID"),
		chi.URLParam(r, "backupID"),
		chi.URLParam(r, "deliveryID"),
		session.User.ID,
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
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
