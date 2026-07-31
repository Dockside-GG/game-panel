package engine

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/dockside-gg/game-panel/internal/engineclient"
)

func (s *Server) serverLogEvents(w http.ResponseWriter, r *http.Request) {
	since := time.Now().UTC().Add(-15 * time.Second)
	if raw := r.URL.Query().Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || parsed.Before(time.Now().Add(-time.Hour)) ||
			parsed.After(time.Now().Add(time.Minute)) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid since time"})
			return
		}
		since = parsed.UTC()
	}
	managed, err := s.docker.ContainerList(r.Context(), container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", "gg.dockside.managed=true"),
			filters.Arg("label", "gg.dockside.instance="+s.cfg.InstanceID),
			filters.Arg("label", "gg.dockside.kind=server"),
		),
	})
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "could not list managed containers"})
		return
	}
	events := make([]engineclient.ServerLogEvent, 0)
	for _, managedContainer := range managed {
		serverID := managedContainer.Labels["gg.dockside.server"]
		if !uuidPattern.MatchString(serverID) {
			continue
		}
		stream, err := s.docker.ContainerLogs(
			r.Context(),
			managedContainer.ID,
			container.LogsOptions{
				ShowStdout: true,
				ShowStderr: true,
				Since:      since.Format(time.RFC3339Nano),
				Timestamps: true,
				Tail:       "2000",
			},
		)
		if err != nil {
			continue
		}
		var stdout, stderr bytes.Buffer
		_, copyErr := stdcopy.StdCopy(
			&stdout,
			&stderr,
			io.LimitReader(stream, 4<<20),
		)
		stream.Close()
		if copyErr != nil {
			continue
		}
		events = append(events, parseTimestampedLog(serverID, "stdout", stdout.Bytes())...)
		events = append(events, parseTimestampedLog(serverID, "stderr", stderr.Bytes())...)
		if len(events) >= 5000 {
			events = events[:5000]
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func parseTimestampedLog(
	serverID, stream string,
	input []byte,
) []engineclient.ServerLogEvent {
	result := make([]engineclient.ServerLogEvent, 0)
	scanner := bufio.NewScanner(bytes.NewReader(input))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		index := strings.IndexByte(line, ' ')
		if index < 1 {
			continue
		}
		observedAt, err := time.Parse(time.RFC3339Nano, line[:index])
		if err != nil {
			continue
		}
		message := strings.TrimSpace(line[index+1:])
		if message == "" {
			continue
		}
		if len(message) > 4000 {
			message = message[:4000]
		}
		result = append(result, engineclient.ServerLogEvent{
			ServerID: serverID, Stream: stream, Message: message,
			ObservedAt: observedAt.UTC(),
		})
	}
	return result
}
