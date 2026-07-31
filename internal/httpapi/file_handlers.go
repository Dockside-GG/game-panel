package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/dockside-gg/game-panel/internal/store"
	"github.com/go-chi/chi/v5"
)

type fileMutationRequest struct {
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
}

func (s *Server) listServerFiles(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if _, err := s.store.ServerByID(r.Context(), serverID); err != nil {
		writeProblem(w, r, err)
		return
	}
	result, err := s.engine.ListFiles(r.Context(), serverID, r.URL.Query().Get("path"))
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) readServerFile(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if _, err := s.store.ServerByID(r.Context(), serverID); err != nil {
		writeProblem(w, r, err)
		return
	}
	result, err := s.engine.ReadFile(r.Context(), serverID, r.URL.Query().Get("path"))
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) downloadServerFile(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if _, err := s.store.ServerByID(r.Context(), serverID); err != nil {
		writeProblem(w, r, err)
		return
	}
	download, err := s.engine.OpenFileDownload(
		r.Context(), serverID, r.URL.Query().Get("path"),
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	defer download.Body.Close()
	if download.ContentType != "" {
		w.Header().Set("Content-Type", download.ContentType)
	}
	if download.ContentDisposition != "" {
		w.Header().Set("Content-Disposition", download.ContentDisposition)
	}
	if download.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(download.ContentLength, 10))
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.CopyBuffer(w, download.Body, make([]byte, 128<<10)); err != nil &&
		r.Context().Err() == nil {
		s.logger.Warn("proxy server file download failed", "server_id", serverID, "error", err)
	}
}

func (s *Server) writeServerFile(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canOperate(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	if _, err := s.store.ServerByID(r.Context(), serverID); err != nil {
		writeProblem(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, (1<<20)+(16<<10))
	var input fileMutationRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	input.Path = strings.TrimSpace(input.Path)
	result, err := s.engine.WriteFile(r.Context(), serverID, input.Path, input.Content)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	s.recordFileActivity(r, serverID, session.User.ID, "write", input.Path)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) uploadServerFile(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canOperate(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	if _, err := s.store.ServerByID(r.Context(), serverID); err != nil {
		writeProblem(w, r, err)
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("path"))
	if target == "" || r.ContentLength < 0 || r.ContentLength > 2<<30 {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("upload path and content length are required; files are limited to 2 GiB")))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)
	if err := s.engine.UploadFile(
		r.Context(), serverID, target, r.Body, r.ContentLength,
	); err != nil {
		writeProblem(w, r, errors.Join(store.ErrConflict, err))
		return
	}
	s.recordFileActivity(r, serverID, session.User.ID, "upload", target)
	writeJSON(w, http.StatusCreated, map[string]string{"path": target})
}

func (s *Server) createServerDirectory(w http.ResponseWriter, r *http.Request) {
	s.mutateServerFile(w, r, "directory.create", s.engine.CreateDirectory, http.StatusCreated)
}

func (s *Server) deleteServerFile(w http.ResponseWriter, r *http.Request) {
	s.mutateServerFile(w, r, "delete", s.engine.DeleteFile, http.StatusNoContent)
}

func (s *Server) mutateServerFile(
	w http.ResponseWriter,
	r *http.Request,
	action string,
	mutation func(context.Context, string, string) error,
	status int,
) {
	session, _ := sessionFromContext(r.Context())
	if !canOperate(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	if _, err := s.store.ServerByID(r.Context(), serverID); err != nil {
		writeProblem(w, r, err)
		return
	}
	var input fileMutationRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Path = strings.TrimSpace(input.Path)
	if err := mutation(r.Context(), serverID, input.Path); err != nil {
		writeProblem(w, r, err)
		return
	}
	s.recordFileActivity(r, serverID, session.User.ID, action, input.Path)
	w.WriteHeader(status)
}

func (s *Server) recordFileActivity(r *http.Request, serverID, actorID, action, path string) {
	if err := s.store.RecordFileActivity(r.Context(), serverID, actorID, action, path); err != nil {
		s.logger.Error("record file activity failed", "server_id", serverID, "action", action, "error", err)
	}
}
