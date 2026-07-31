package engine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/go-chi/chi/v5"
)

const (
	fileHelperImage = "alpine:3.22"
	maxEditableFile = 1 << 20
)

const listFilesScript = `
set -eu
root="$(realpath /mnt/server)"
target="$root"
if [ "$TARGET" != "." ]; then target="$root/$TARGET"; fi
resolved="$(realpath "$target")"
case "$resolved" in "$root"|"$root"/*) ;; *) exit 40 ;; esac
[ -d "$resolved" ] || exit 41
set -- "$resolved"/* "$resolved"/.[!.]* "$resolved"/..?*
for item do
  if [ ! -e "$item" ] && [ ! -L "$item" ]; then continue; fi
  name="${item##*/}"
  encoded="$(printf %s "$name" | base64 | tr -d '\n')"
  if [ -L "$item" ]; then kind=symlink
  elif [ -d "$item" ]; then kind=directory
  elif [ -f "$item" ]; then kind=file
  else kind=other
  fi
  size="$(stat -c %s "$item")"
  modified="$(stat -c %Y "$item")"
  printf '%s\t%s\t%s\t%s\n' "$encoded" "$kind" "$size" "$modified"
done
`

const validateTargetScript = `
set -eu
root="$(realpath /mnt/server)"
target="$root/$TARGET"
resolved="$(realpath "$target")"
case "$resolved" in "$root"/*) ;; *) exit 40 ;; esac
[ -f "$resolved" ] || exit 41
`

const createDirectoryScript = `
set -eu
root="$(realpath /mnt/server)"
parent_rel="${TARGET%/*}"
base="${TARGET##*/}"
if [ "$parent_rel" = "$TARGET" ]; then parent_rel=.; fi
parent="$root"
if [ "$parent_rel" != "." ]; then parent="$root/$parent_rel"; fi
resolved_parent="$(realpath "$parent")"
case "$resolved_parent" in "$root"|"$root"/*) ;; *) exit 40 ;; esac
[ -d "$resolved_parent" ] || exit 41
[ -n "$base" ] && [ "$base" != "." ] && [ "$base" != ".." ] || exit 42
mkdir "$resolved_parent/$base"
`

const deletePathScript = `
set -eu
root="$(realpath /mnt/server)"
parent_rel="${TARGET%/*}"
base="${TARGET##*/}"
if [ "$parent_rel" = "$TARGET" ]; then parent_rel=.; fi
parent="$root"
if [ "$parent_rel" != "." ]; then parent="$root/$parent_rel"; fi
resolved_parent="$(realpath "$parent")"
case "$resolved_parent" in "$root"|"$root"/*) ;; *) exit 40 ;; esac
[ -n "$base" ] && [ "$base" != "." ] && [ "$base" != ".." ] || exit 42
candidate="$resolved_parent/$base"
if [ ! -e "$candidate" ] && [ ! -L "$candidate" ]; then exit 43; fi
rm -rf "$candidate"
`

const renamePathScript = `
set -eu
root="$(realpath /mnt/server)"
parent_rel="${TARGET%/*}"
base="${TARGET##*/}"
if [ "$parent_rel" = "$TARGET" ]; then parent_rel=.; fi
parent="$root"
if [ "$parent_rel" != "." ]; then parent="$root/$parent_rel"; fi
resolved_parent="$(realpath "$parent")"
case "$resolved_parent" in "$root"|"$root"/*) ;; *) exit 40 ;; esac
[ -n "$base" ] && [ "$base" != "." ] && [ "$base" != ".." ] || exit 42
source="$resolved_parent/$base"
destination="$resolved_parent/$NEW_NAME"
if [ ! -e "$source" ] && [ ! -L "$source" ]; then exit 43; fi
if [ -e "$destination" ] || [ -L "$destination" ]; then exit 44; fi
mv -- "$source" "$destination"
`

type fileWriteRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type filePathRequest struct {
	Path    string `json:"path"`
	NewName string `json:"new_name,omitempty"`
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	serverID, relative, ok := s.fileRequest(w, r, true)
	if !ok {
		return
	}
	output, err := s.runVolumeHelper(r.Context(), serverID, listFilesScript, map[string]string{"TARGET": relative})
	if err != nil {
		s.logger.Warn("list server files failed", "server_id", serverID, "path", relative, "error", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "could not list server directory"})
		return
	}
	entries, err := parseFileEntries(relative, output)
	if err != nil {
		s.logger.Error("parse helper file listing failed", "server_id", serverID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "invalid file helper response"})
		return
	}
	writeJSON(w, http.StatusOK, engineclient.FileList{Path: relative, Entries: entries})
}

func (s *Server) readFile(w http.ResponseWriter, r *http.Request) {
	serverID, relative, ok := s.fileRequest(w, r, false)
	if !ok {
		return
	}
	if _, err := s.runVolumeHelper(r.Context(), serverID, validateTargetScript, map[string]string{"TARGET": relative}); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found or unsafe"})
		return
	}
	containerID, err := s.findManagedServer(r.Context(), serverID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "managed server not found"})
		return
	}
	archive, _, err := s.docker.CopyFromContainer(r.Context(), containerID, "/home/container/"+relative)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	defer archive.Close()
	content, err := extractRegularFile(archive, maxEditableFile)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if !utf8.Valid(content) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "binary files cannot be edited in the browser"})
		return
	}
	writeJSON(w, http.StatusOK, engineclient.FileContent{Path: relative, Content: string(content)})
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	serverID, relative, ok := s.fileRequest(w, r, true)
	if !ok {
		return
	}
	archive, stat, err := s.openServerVolumeArchive(r.Context(), serverID, relative)
	if err != nil {
		s.logger.Warn("open server file download failed", "server_id", serverID, "path", relative, "error", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file or directory not found"})
		return
	}
	defer archive.Close()

	name := stat.Name
	if relative == "." || name == "" || name == "." {
		name = "server-files"
	}
	if stat.Mode.IsDir() {
		filename := name + ".tar.gz"
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		compressed := gzip.NewWriter(w)
		if _, err := io.CopyBuffer(compressed, archive, make([]byte, 128<<10)); err != nil {
			s.logger.Warn("stream directory download failed", "server_id", serverID, "path", relative, "error", err)
		}
		if err := compressed.Close(); err != nil {
			s.logger.Warn("finish directory download failed", "server_id", serverID, "path", relative, "error", err)
		}
		return
	}
	if !stat.Mode.IsRegular() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "only regular files and directories can be downloaded"})
		return
	}
	tarReader := tar.NewReader(archive)
	for {
		header, readErr := tarReader.Next()
		if errors.Is(readErr, io.EOF) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "download archive was empty"})
			return
		}
		if readErr != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "could not read file download"})
			return
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		w.Header().Set("Content-Length", strconv.FormatInt(header.Size, 10))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if _, err := io.CopyN(w, tarReader, header.Size); err != nil {
			s.logger.Warn("stream file download failed", "server_id", serverID, "path", relative, "error", err)
		}
		return
	}
}

func (s *Server) writeFile(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if !uuidPattern.MatchString(serverID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server id"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxEditableFile+(16<<10))
	var input fileWriteRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid file content request"})
		return
	}
	relative, err := safeRelativePath(input.Path, false)
	if err != nil || len(input.Content) > maxEditableFile || !utf8.ValidString(input.Content) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid path or text file exceeds 1 MiB"})
		return
	}
	parent := path.Dir(relative)
	if parent == "." {
		parent = "."
	}
	if _, err := s.runVolumeHelper(r.Context(), serverID, listFilesScript, map[string]string{"TARGET": parent}); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "file parent directory is missing or unsafe"})
		return
	}
	containerID, err := s.findManagedServer(r.Context(), serverID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "managed server not found"})
		return
	}
	var archive bytes.Buffer
	tarWriter := tar.NewWriter(&archive)
	content := []byte(input.Content)
	if err := tarWriter.WriteHeader(&tar.Header{
		Name: path.Base(relative), Mode: 0o660, Uid: s.cfg.ServerUID, Gid: s.cfg.ServerGID,
		Size: int64(len(content)), ModTime: time.Now().UTC(),
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not prepare file"})
		return
	}
	if _, err := tarWriter.Write(content); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not prepare file"})
		return
	}
	if err := tarWriter.Close(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not prepare file"})
		return
	}
	target := "/home/container"
	if parent != "." {
		target += "/" + parent
	}
	if err := s.docker.CopyToContainer(r.Context(), containerID, target, &archive, container.CopyToContainerOptions{
		CopyUIDGID: true,
	}); err != nil {
		s.logger.Warn("write server file failed", "server_id", serverID, "path", relative, "error", err)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "could not save file"})
		return
	}
	writeJSON(w, http.StatusOK, engineclient.FileContent{Path: relative, Content: input.Content})
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if !uuidPattern.MatchString(serverID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server id"})
		return
	}
	relative, err := safeRelativePath(r.URL.Query().Get("path"), false)
	if err != nil || r.ContentLength < 0 || r.ContentLength > 2<<30 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid upload path or size"})
		return
	}
	parent := path.Dir(relative)
	if _, err := s.runVolumeHelper(
		r.Context(), serverID, listFilesScript, map[string]string{"TARGET": parent},
	); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "upload parent directory is missing or unsafe"})
		return
	}
	containerID, err := s.findManagedServer(r.Context(), serverID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "managed server not found"})
		return
	}
	reader, writer := io.Pipe()
	writeDone := make(chan error, 1)
	go func() {
		archive := tar.NewWriter(writer)
		err := archive.WriteHeader(&tar.Header{
			Name: path.Base(relative), Mode: 0o660,
			Uid: s.cfg.ServerUID, Gid: s.cfg.ServerGID,
			Size: r.ContentLength, ModTime: time.Now().UTC(),
		})
		if err == nil {
			_, err = io.CopyN(archive, r.Body, r.ContentLength)
		}
		if closeErr := archive.Close(); err == nil {
			err = closeErr
		}
		_ = writer.CloseWithError(err)
		writeDone <- err
	}()
	target := "/home/container"
	if parent != "." {
		target += "/" + parent
	}
	copyErr := s.docker.CopyToContainer(
		r.Context(), containerID, target, reader,
		container.CopyToContainerOptions{CopyUIDGID: true},
	)
	if copyErr != nil {
		_ = reader.CloseWithError(copyErr)
	}
	writeErr := <-writeDone
	if copyErr != nil || writeErr != nil {
		s.logger.Warn(
			"stream server upload failed",
			"server_id", serverID, "path", relative,
			"copy_error", copyErr, "write_error", writeErr,
		)
		writeJSON(w, http.StatusConflict, map[string]string{"error": "could not upload file"})
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) createDirectory(w http.ResponseWriter, r *http.Request) {
	s.mutatePath(w, r, createDirectoryScript, http.StatusCreated)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	s.mutatePath(w, r, deletePathScript, http.StatusNoContent)
}

func (s *Server) renameFile(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if !uuidPattern.MatchString(serverID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server id"})
		return
	}
	var input filePathRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid rename request"})
		return
	}
	relative, err := safeRelativePath(input.Path, false)
	newName := strings.TrimSpace(input.NewName)
	if err != nil || !safeFileName(newName) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid file path or new name"})
		return
	}
	if path.Base(relative) == newName {
		writeJSON(w, http.StatusOK, map[string]string{"path": relative})
		return
	}
	if _, err := s.runVolumeHelper(r.Context(), serverID, renamePathScript, map[string]string{
		"TARGET": relative, "NEW_NAME": newName,
	}); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "rename failed; the destination may already exist"})
		return
	}
	newPath := newName
	if parent := path.Dir(relative); parent != "." {
		newPath = parent + "/" + newName
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": newPath})
}

func (s *Server) mutatePath(w http.ResponseWriter, r *http.Request, script string, status int) {
	serverID := chi.URLParam(r, "serverID")
	if !uuidPattern.MatchString(serverID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server id"})
		return
	}
	var input filePathRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid file path request"})
		return
	}
	relative, err := safeRelativePath(input.Path, false)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid file path"})
		return
	}
	if _, err := s.runVolumeHelper(r.Context(), serverID, script, map[string]string{"TARGET": relative}); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "file operation failed"})
		return
	}
	w.WriteHeader(status)
}

func (s *Server) fileRequest(w http.ResponseWriter, r *http.Request, allowRoot bool) (string, string, bool) {
	serverID := chi.URLParam(r, "serverID")
	if !uuidPattern.MatchString(serverID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server id"})
		return "", "", false
	}
	relative, err := safeRelativePath(r.URL.Query().Get("path"), allowRoot)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid file path"})
		return "", "", false
	}
	return serverID, relative, true
}

func safeRelativePath(value string, allowRoot bool) (string, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	if value == "" {
		value = "."
	}
	if strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") || len(value) > 1024 {
		return "", errors.New("unsafe path")
	}
	cleaned := path.Clean(value)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("unsafe path")
	}
	if cleaned == "." && !allowRoot {
		return "", errors.New("root operation forbidden")
	}
	return cleaned, nil
}

func safeFileName(value string) bool {
	return value != "" && value != "." && value != ".." &&
		len(value) <= 255 && !strings.ContainsAny(value, `/\`) &&
		!strings.ContainsRune(value, 0)
}

func (s *Server) runVolumeHelper(
	ctx context.Context,
	serverID, script string,
	environment map[string]string,
) (string, error) {
	names := s.resourceNames(serverID)
	inspected, err := s.docker.VolumeInspect(ctx, names.volume)
	if err != nil {
		return "", fmt.Errorf("inspect server volume: %w", err)
	}
	if !labelsMatch(inspected.Labels, s.cfg.InstanceID, serverID) {
		return "", errors.New("server volume labels do not match")
	}
	if _, _, err := s.docker.ImageInspectWithRaw(ctx, fileHelperImage); err != nil {
		if !errdefs.IsNotFound(err) {
			return "", fmt.Errorf("inspect file helper image: %w", err)
		}
		if err := s.pullImage(ctx, fileHelperImage); err != nil {
			return "", fmt.Errorf("pull file helper image: %w", err)
		}
	}
	labels := s.managedLabels(serverID)
	labels["gg.dockside.kind"] = "file-helper"
	created, err := s.docker.ContainerCreate(ctx, &container.Config{
		Image:      fileHelperImage,
		User:       fmt.Sprintf("%d:%d", s.cfg.ServerUID, s.cfg.ServerGID),
		Entrypoint: []string{"sh", "-c"},
		Cmd:        []string{script},
		Env:        environmentList(environment),
		Labels:     labels,
	}, &container.HostConfig{
		NetworkMode:    "none",
		ReadonlyRootfs: true,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
		Mounts: []mount.Mount{{
			Type: mount.TypeVolume, Source: names.volume, Target: "/mnt/server",
		}},
	}, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("create file helper: %w", err)
	}
	defer s.docker.ContainerRemove(context.WithoutCancel(ctx), created.ID, container.RemoveOptions{Force: true})
	if err := s.docker.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start file helper: %w", err)
	}
	statusCh, errorCh := s.docker.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	var status container.WaitResponse
	select {
	case err := <-errorCh:
		if err != nil {
			return "", fmt.Errorf("wait for file helper: %w", err)
		}
	case status = <-statusCh:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	logs, err := s.docker.ContainerLogs(ctx, created.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return "", fmt.Errorf("read file helper output: %w", err)
	}
	defer logs.Close()
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, logs); err != nil {
		return "", fmt.Errorf("decode file helper output: %w", err)
	}
	if status.StatusCode != 0 {
		return "", fmt.Errorf("file helper exited %d: %s", status.StatusCode, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

type managedArchive struct {
	io.ReadCloser
	server      *Server
	containerID string
	once        sync.Once
}

func (a *managedArchive) Close() error {
	err := a.ReadCloser.Close()
	a.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if removeErr := a.server.docker.ContainerRemove(
			ctx, a.containerID, container.RemoveOptions{Force: true},
		); removeErr != nil {
			a.server.logger.Warn("remove archive helper failed", "container_id", a.containerID, "error", removeErr)
		}
	})
	return err
}

func (s *Server) openServerVolumeArchive(
	ctx context.Context,
	serverID, relative string,
) (io.ReadCloser, container.PathStat, error) {
	var empty container.PathStat
	names := s.resourceNames(serverID)
	inspected, err := s.docker.VolumeInspect(ctx, names.volume)
	if err != nil {
		return nil, empty, fmt.Errorf("inspect server volume: %w", err)
	}
	if !labelsMatch(inspected.Labels, s.cfg.InstanceID, serverID) {
		return nil, empty, errors.New("server volume labels do not match")
	}
	if _, _, err := s.docker.ImageInspectWithRaw(ctx, fileHelperImage); err != nil {
		if !errdefs.IsNotFound(err) {
			return nil, empty, fmt.Errorf("inspect archive helper image: %w", err)
		}
		if err := s.pullImage(ctx, fileHelperImage); err != nil {
			return nil, empty, fmt.Errorf("pull archive helper image: %w", err)
		}
	}
	labels := s.managedLabels(serverID)
	labels["gg.dockside.kind"] = "archive-helper"
	created, err := s.docker.ContainerCreate(ctx, &container.Config{
		Image:      fileHelperImage,
		User:       fmt.Sprintf("%d:%d", s.cfg.ServerUID, s.cfg.ServerGID),
		Entrypoint: []string{"sleep"},
		Cmd:        []string{"3600"},
		Labels:     labels,
	}, &container.HostConfig{
		NetworkMode:    "none",
		ReadonlyRootfs: true,
		CapDrop:        []string{"ALL"},
		SecurityOpt:    []string{"no-new-privileges:true"},
		Mounts: []mount.Mount{{
			Type: mount.TypeVolume, Source: names.volume, Target: "/mnt/server", ReadOnly: true,
		}},
	}, nil, nil, "")
	if err != nil {
		return nil, empty, fmt.Errorf("create archive helper: %w", err)
	}
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.docker.ContainerRemove(cleanupCtx, created.ID, container.RemoveOptions{Force: true})
	}
	if err := s.docker.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		cleanup()
		return nil, empty, fmt.Errorf("start archive helper: %w", err)
	}
	target := "/mnt/server"
	if relative != "." {
		target += "/" + relative
	} else {
		target += "/."
	}
	stat, err := s.docker.ContainerStatPath(ctx, created.ID, target)
	if err != nil {
		cleanup()
		return nil, empty, fmt.Errorf("stat archive target: %w", err)
	}
	archive, _, err := s.docker.CopyFromContainer(ctx, created.ID, target)
	if err != nil {
		cleanup()
		return nil, empty, fmt.Errorf("open archive target: %w", err)
	}
	return &managedArchive{
		ReadCloser: archive, server: s, containerID: created.ID,
	}, stat, nil
}

func parseFileEntries(parent, output string) ([]engineclient.FileEntry, error) {
	entries := make([]engineclient.FileEntry, 0)
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			return nil, errors.New("invalid listing line")
		}
		nameBytes, err := base64.StdEncoding.DecodeString(fields[0])
		if err != nil || !utf8.Valid(nameBytes) {
			return nil, errors.New("invalid listing name")
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, err
		}
		seconds, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return nil, err
		}
		entryPath := string(nameBytes)
		if parent != "." {
			entryPath = path.Join(parent, entryPath)
		}
		entries = append(entries, engineclient.FileEntry{
			Name: string(nameBytes), Path: entryPath, Type: fields[1],
			Size: size, ModifiedAt: time.Unix(seconds, 0).UTC(),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		leftDir := entries[i].Type == "directory"
		rightDir := entries[j].Type == "directory"
		if leftDir != rightDir {
			return leftDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

func extractRegularFile(reader io.Reader, maximum int64) ([]byte, error) {
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("file archive was empty")
		}
		if err != nil {
			return nil, errors.New("could not read file archive")
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		if header.Size > maximum {
			return nil, errors.New("file exceeds the 1 MiB browser editing limit")
		}
		content, err := io.ReadAll(io.LimitReader(tarReader, maximum+1))
		if err != nil || int64(len(content)) > maximum {
			return nil, errors.New("could not read file content")
		}
		return content, nil
	}
}
