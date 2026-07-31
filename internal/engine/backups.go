package engine

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/go-chi/chi/v5"
)

const clearVolumeScript = `
set -eu
root="$(realpath /mnt/server)"
case "$root" in /mnt/server) ;; *) exit 40 ;; esac
find "$root" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
`

type backupCreateRequest struct {
	IncludePaths []string `json:"include_paths"`
	ExcludeGlobs []string `json:"exclude_globs"`
}

type backupRestoreRequest struct {
	SHA256 string `json:"sha256"`
}

func (s *Server) createBackup(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	backupID := chi.URLParam(r, "backupID")
	if !uuidPattern.MatchString(serverID) || !uuidPattern.MatchString(backupID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server or backup id"})
		return
	}
	var input backupCreateRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid backup request"})
		return
	}
	if err := validateBackupFilters(input.IncludePaths, input.ExcludeGlobs); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := s.archiveServerVolume(
		r.Context(), serverID, backupID, input.IncludePaths, input.ExcludeGlobs,
	)
	if err != nil {
		s.logger.Error("create server backup failed", "server_id", serverID, "backup_id", backupID, "error", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "backup creation failed"})
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	backupID := chi.URLParam(r, "backupID")
	if !uuidPattern.MatchString(serverID) || !uuidPattern.MatchString(backupID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server or backup id"})
		return
	}
	var input backupRestoreRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(input.SHA256) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "valid backup SHA-256 is required"})
		return
	}
	if err := s.restoreServerVolume(r.Context(), serverID, backupID, input.SHA256); err != nil {
		s.logger.Error("restore server backup failed", "server_id", serverID, "backup_id", backupID, "error", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "backup restore failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	backupID := chi.URLParam(r, "backupID")
	if !uuidPattern.MatchString(serverID) || !uuidPattern.MatchString(backupID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server or backup id"})
		return
	}
	filename := s.backupFilename(serverID, backupID)
	if err := os.Remove(filename); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Error("delete backup archive failed", "server_id", serverID, "backup_id", backupID, "error", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "backup deletion failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	backupID := chi.URLParam(r, "backupID")
	if !uuidPattern.MatchString(serverID) || !uuidPattern.MatchString(backupID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server or backup id"})
		return
	}
	filename := s.backupFilename(serverID, backupID)
	file, err := os.Open(filename)
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "backup archive not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "backup archive unavailable"})
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "backup archive unavailable"})
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+backupID+`.tar.gz"`)
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	http.ServeContent(w, r, backupID+".tar.gz", info.ModTime(), file)
}

func (s *Server) archiveServerVolume(
	ctx context.Context,
	serverID, backupID string,
	includes, excludes []string,
) (engineclient.BackupResult, error) {
	source, _, err := s.openServerVolumeArchive(ctx, serverID, ".")
	if err != nil {
		return engineclient.BackupResult{}, fmt.Errorf("open isolated server archive: %w", err)
	}
	defer source.Close()
	directory := filepath.Join(s.cfg.BackupRoot, serverID)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return engineclient.BackupResult{}, fmt.Errorf("create backup directory: %w", err)
	}
	finalName := s.backupFilename(serverID, backupID)
	temporary, err := os.CreateTemp(directory, backupID+"-*.partial")
	if err != nil {
		return engineclient.BackupResult{}, fmt.Errorf("create temporary backup: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() {
		temporary.Close()
		_ = os.Remove(temporaryName)
	}()
	digest := sha256.New()
	counter := &countingWriter{}
	compressed := gzip.NewWriter(io.MultiWriter(temporary, digest, counter))
	archive := tar.NewWriter(compressed)
	sourceArchive := tar.NewReader(source)
	for {
		header, err := sourceArchive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return engineclient.BackupResult{}, fmt.Errorf("read server archive: %w", err)
		}
		name, err := safeArchiveName(header.Name)
		if err != nil {
			return engineclient.BackupResult{}, err
		}
		if name == "." || !backupPathSelected(name, includes, excludes) {
			continue
		}
		cloned := *header
		cloned.Name = name
		if cloned.Linkname != "" && (strings.HasPrefix(cloned.Linkname, "/") || strings.Contains(cloned.Linkname, "..")) {
			return engineclient.BackupResult{}, fmt.Errorf("unsafe archive link %q", cloned.Linkname)
		}
		if err := archive.WriteHeader(&cloned); err != nil {
			return engineclient.BackupResult{}, fmt.Errorf("write backup header: %w", err)
		}
		if header.Size > 0 {
			if _, err := io.CopyN(archive, sourceArchive, header.Size); err != nil {
				return engineclient.BackupResult{}, fmt.Errorf("write backup content: %w", err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		return engineclient.BackupResult{}, err
	}
	if err := compressed.Close(); err != nil {
		return engineclient.BackupResult{}, err
	}
	if err := temporary.Sync(); err != nil {
		return engineclient.BackupResult{}, err
	}
	if err := temporary.Close(); err != nil {
		return engineclient.BackupResult{}, err
	}
	if err := os.Rename(temporaryName, finalName); err != nil {
		return engineclient.BackupResult{}, fmt.Errorf("finalize backup: %w", err)
	}
	return engineclient.BackupResult{
		ObjectKey: path.Join(serverID, backupID+".tar.gz"),
		SizeBytes: counter.written,
		SHA256:    hex.EncodeToString(digest.Sum(nil)),
	}, nil
}

func (s *Server) restoreServerVolume(ctx context.Context, serverID, backupID, expectedHash string) error {
	containerID, err := s.findManagedServer(ctx, serverID)
	if err != nil {
		return err
	}
	inspected, err := s.docker.ContainerInspect(ctx, containerID)
	if err != nil {
		return err
	}
	if inspected.State != nil && inspected.State.Running {
		return errors.New("server must be stopped before restoring a backup")
	}
	filename := s.backupFilename(serverID, backupID)
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		file.Close()
		return err
	}
	if hex.EncodeToString(digest.Sum(nil)) != expectedHash {
		file.Close()
		return errors.New("backup checksum mismatch")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return err
	}
	if _, err := s.runVolumeHelper(ctx, serverID, clearVolumeScript, nil); err != nil {
		file.Close()
		return fmt.Errorf("clear server volume: %w", err)
	}
	uncompressed, err := gzip.NewReader(file)
	if err != nil {
		file.Close()
		return fmt.Errorf("open compressed backup: %w", err)
	}
	defer file.Close()
	defer uncompressed.Close()
	if err := s.docker.CopyToContainer(ctx, containerID, "/home/container", uncompressed, container.CopyToContainerOptions{
		CopyUIDGID: true,
	}); err != nil {
		return fmt.Errorf("restore archive into server volume: %w", err)
	}
	return nil
}

func (s *Server) backupFilename(serverID, backupID string) string {
	return filepath.Join(s.cfg.BackupRoot, serverID, backupID+".tar.gz")
}

func validateBackupFilters(includes, excludes []string) error {
	if len(includes) > 100 || len(excludes) > 100 {
		return errors.New("at most 100 include and exclude rules are allowed")
	}
	for _, rule := range append(append([]string{}, includes...), excludes...) {
		rule = strings.TrimSpace(strings.ReplaceAll(rule, "\\", "/"))
		if rule == "" || strings.HasPrefix(rule, "/") || strings.Contains(rule, "..") || len(rule) > 512 {
			return errors.New("backup filters must be safe relative paths or globs")
		}
	}
	return nil
}

func safeArchiveName(value string) (string, error) {
	value = strings.TrimPrefix(strings.ReplaceAll(value, "\\", "/"), "./")
	cleaned := path.Clean(value)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", errors.New("archive contained an unsafe path")
	}
	return cleaned, nil
}

func backupPathSelected(name string, includes, excludes []string) bool {
	selected := len(includes) == 0
	for _, rule := range includes {
		if archiveRuleMatches(strings.TrimSpace(rule), name) {
			selected = true
			break
		}
	}
	if !selected {
		return false
	}
	for _, rule := range excludes {
		if archiveRuleMatches(strings.TrimSpace(rule), name) {
			return false
		}
	}
	return true
}

func archiveRuleMatches(rule, name string) bool {
	rule = strings.TrimPrefix(strings.ReplaceAll(rule, "\\", "/"), "./")
	if strings.HasSuffix(rule, "/") {
		rule = strings.TrimSuffix(rule, "/")
	}
	if name == rule || strings.HasPrefix(name, rule+"/") {
		return true
	}
	matched, _ := path.Match(rule, name)
	return matched
}

type countingWriter struct {
	written int64
}

func (w *countingWriter) Write(value []byte) (int, error) {
	w.written += int64(len(value))
	return len(value), nil
}
