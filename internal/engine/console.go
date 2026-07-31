package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/dockside-gg/game-panel/internal/consolelog"
	"github.com/dockside-gg/game-panel/internal/templates"
	"github.com/go-chi/chi/v5"
)

type consoleCommandRequest struct {
	Command string `json:"command"`
}

type consoleFrame struct {
	Stream     string    `json:"stream"`
	Phase      string    `json:"phase,omitempty"`
	Severity   string    `json:"severity"`
	Message    string    `json:"message"`
	ObservedAt time.Time `json:"observed_at"`
}

func (s *Server) consoleLogs(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if !uuidPattern.MatchString(serverID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server id"})
		return
	}
	hub := s.provisionLog(serverID)
	containerID, containerErr := s.findManagedServer(r.Context(), serverID)
	if containerErr != nil && hub == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "managed server not found"})
		return
	}
	tail := strings.TrimSpace(r.URL.Query().Get("tail"))
	if tail == "" {
		tail = "250"
	}
	if !validTail(tail) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tail must be 20-5000"})
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	encoder := &synchronizedFrameEncoder{writer: w, flusher: flusher}
	_ = encoder.emit(consoleFrame{
		Stream: "system", Phase: "system", Message: "Console stream connected",
		ObservedAt: time.Now().UTC(),
	})
	if hub != nil {
		snapshot, frames, done, unsubscribe := hub.subscribe()
		for _, frame := range snapshot {
			if err := encoder.emit(frame); err != nil {
				unsubscribe()
				return
			}
		}
		for {
			select {
			case frame := <-frames:
				if err := encoder.emit(frame); err != nil {
					unsubscribe()
					return
				}
			case <-done:
				unsubscribe()
				_ = encoder.emit(consoleFrame{
					Stream: "system", Phase: "runtime",
					Message:    "Installation completed; attaching runtime console",
					ObservedAt: time.Now().UTC(),
				})
				goto runtime
			case <-r.Context().Done():
				unsubscribe()
				return
			}
		}
	}

runtime:
	if containerID == "" || containerErr != nil {
		containerID, containerErr = s.findManagedServer(r.Context(), serverID)
	}
	if containerErr != nil {
		_ = encoder.emit(consoleFrame{
			Stream: "system", Phase: "system", Message: "Runtime console is not available",
			ObservedAt: time.Now().UTC(),
		})
		return
	}
	logs, err := s.docker.ContainerLogs(r.Context(), containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       tail,
	})
	if err != nil {
		_ = encoder.emit(consoleFrame{
			Stream: "system", Phase: "system", Message: "Container logs are unavailable",
			ObservedAt: time.Now().UTC(),
		})
		return
	}
	defer logs.Close()
	stdout := &lineFrameWriter{stream: "stdout", encoder: encoder}
	stderr := &lineFrameWriter{stream: "stderr", encoder: encoder}
	stdout.phase = "runtime"
	stderr.phase = "runtime"
	_, copyErr := stdcopy.StdCopy(stdout, stderr, logs)
	stdout.flush()
	stderr.flush()
	if copyErr != nil && !errors.Is(copyErr, io.EOF) && r.Context().Err() == nil {
		s.logger.Warn("console stream ended", "server_id", serverID, "error", copyErr)
	}
}

func (s *Server) consoleCommand(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if !uuidPattern.MatchString(serverID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid server id"})
		return
	}
	var input consoleCommandRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid command request"})
		return
	}
	command := strings.TrimSpace(input.Command)
	if command == "" || len(command) > 2048 ||
		strings.ContainsAny(command, "\x00\r\n") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "command must be one line and at most 2048 bytes"})
		return
	}
	containerID, err := s.findManagedServer(r.Context(), serverID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "managed server not found"})
		return
	}
	inspected, err := s.docker.ContainerInspect(r.Context(), containerID)
	if err != nil || inspected.State == nil || !inspected.State.Running {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "server must be running"})
		return
	}
	transport, environment, err := commandTransportEnvironment(inspected.Config.Env)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "server command transport is invalid"})
		return
	}
	if transport.Type == "disabled" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "commands are disabled by this server template"})
		return
	}
	if transport.Type == "http_rest" {
		response, err := s.executeRESTCommand(
			r.Context(), serverID, containerID, command, transport, environment,
		)
		if err != nil {
			s.logger.Warn("REST game command failed", "server_id", serverID, "error", err)
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"transport": "http_rest",
			"response":  response,
		})
		return
	}
	rconEnvironment, rconReady := rconCommandEnvironment(inspected.Config.Env)
	if transport.Type == "rcon" {
		rconEnvironment, rconReady = configuredRCONEnvironment(transport, environment)
	}
	if transport.Type != "stdin" && rconReady {
		created, err := s.docker.ContainerExecCreate(
			r.Context(),
			containerID,
			container.ExecOptions{
				Cmd: []string{
					"rcon", "-s", "-a", "127.0.0.1:" + rconEnvironment.port,
					"-p", rconEnvironment.password, command,
				},
			},
		)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "could not create the RCON command"})
			return
		}
		if err := s.docker.ContainerExecStart(
			r.Context(), created.ID, container.ExecStartOptions{},
		); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "RCON is not ready yet"})
			return
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			result, err := s.docker.ContainerExecInspect(r.Context(), created.ID)
			if err != nil {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "could not inspect the RCON command"})
				return
			}
			if !result.Running {
				if result.ExitCode != 0 {
					writeJSON(w, http.StatusConflict, map[string]string{"error": "RCON rejected the command or is not ready yet"})
					return
				}
				break
			}
			if time.Now().After(deadline) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "RCON command timed out"})
				return
			}
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-r.Context().Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"transport": "rcon"})
		return
	}
	attached, err := s.docker.ContainerAttach(r.Context(), containerID, container.AttachOptions{
		Stream: true,
		Stdin:  true,
	})
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "could not attach to server console"})
		return
	}
	defer attached.Close()
	_ = attached.Conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := attached.Conn.Write([]byte(command + "\n")); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "could not write to server console"})
		return
	}
	_ = attached.CloseWrite()
	writeJSON(w, http.StatusOK, map[string]string{"transport": "stdin"})
}

type rconEnvironment struct {
	port     string
	password string
}

func commandTransportEnvironment(values []string) (
	templates.CommandTransport,
	map[string]string,
	error,
) {
	environment := make(map[string]string, len(values))
	for _, value := range values {
		name, configured, found := strings.Cut(value, "=")
		if found {
			environment[name] = configured
		}
	}
	transport := templates.CommandTransport{Type: "auto"}
	if encoded := strings.TrimSpace(environment["DOCKSIDE_COMMAND_TRANSPORT"]); encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &transport); err != nil {
			return transport, environment, err
		}
	}
	transport.Type = strings.ToLower(strings.TrimSpace(transport.Type))
	if transport.Type == "" {
		transport.Type = "auto"
	}
	return transport, environment, nil
}

func configuredRCONEnvironment(
	transport templates.CommandTransport,
	environment map[string]string,
) (rconEnvironment, bool) {
	portName := strings.TrimSpace(transport.RCONPortEnv)
	passwordName := strings.TrimSpace(transport.RCONPasswordEnv)
	if portName == "" {
		portName = "RCON_PORT"
	}
	if passwordName == "" {
		passwordName = "ADMIN_PASSWORD"
	}
	port := strings.TrimSpace(environment[portName])
	password := environment[passwordName]
	if port == "" || password == "" {
		return rconEnvironment{}, false
	}
	if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
		return rconEnvironment{}, false
	}
	return rconEnvironment{port: port, password: password}, true
}

const restCommandScript = `
set -eu
headers="$(printf '%s' "$HEADERS_B64" | base64 -d)"
length="$(printf '%s' "$BODY" | wc -c | tr -d ' ')"
{
  printf '%s %s HTTP/1.1\r\n' "$METHOD" "$REQUEST_PATH"
  printf 'Host: 127.0.0.1\r\nConnection: close\r\nContent-Length: %s\r\n' "$length"
  if [ -n "$headers" ]; then printf '%s\r\n' "$headers"; fi
  printf '\r\n'
  printf '%s' "$BODY"
} | nc -w "$TIMEOUT" 127.0.0.1 "$PORT"
`

var environmentReference = regexp.MustCompile(`\{\{ENV:([A-Z_][A-Z0-9_]*)\}\}`)

type restCommandValues struct {
	command   string
	verb      string
	args      string
	arguments []string
}

type resolvedRESTRequest struct {
	method         string
	path           string
	body           string
	headers        map[string]string
	acceptedStatus []int
}

func (s *Server) executeRESTCommand(
	ctx context.Context,
	serverID, gameContainerID, command string,
	transport templates.CommandTransport,
	environment map[string]string,
) (string, error) {
	if transport.REST == nil {
		return "", errors.New("REST command transport is not configured")
	}
	spec := transport.REST
	request, err := resolveRESTRequest(spec, command, environment)
	if err != nil {
		return "", err
	}
	port := spec.Port
	if spec.PortEnvironment != "" {
		value, err := strconv.Atoi(strings.TrimSpace(environment[spec.PortEnvironment]))
		if err != nil {
			return "", errors.New("REST command port variable is not a valid port")
		}
		port = value
	}
	if port < 1 || port > 65535 {
		return "", errors.New("REST command port is unavailable")
	}
	headers := make([]string, 0, len(request.headers)+1)
	for name, value := range request.headers {
		headers = append(headers, name+": "+value)
	}
	sort.Strings(headers)
	timeout := spec.TimeoutSeconds
	if timeout < 1 || timeout > 60 {
		timeout = 10
	}
	labels := s.managedLabels(serverID)
	labels["gg.dockside.kind"] = "command-helper"
	if _, _, err := s.docker.ImageInspectWithRaw(ctx, fileHelperImage); err != nil {
		if !errdefs.IsNotFound(err) {
			return "", fmt.Errorf("inspect REST command helper image: %w", err)
		}
		if err := s.pullImage(ctx, fileHelperImage); err != nil {
			return "", fmt.Errorf("pull REST command helper image: %w", err)
		}
	}
	created, err := s.docker.ContainerCreate(
		ctx,
		&container.Config{
			Image:      fileHelperImage,
			Entrypoint: []string{"sh", "-c"},
			Cmd:        []string{restCommandScript},
			Env: environmentList(map[string]string{
				"METHOD":       request.method,
				"REQUEST_PATH": request.path,
				"PORT":         strconv.Itoa(port),
				"TIMEOUT":      strconv.Itoa(timeout),
				"BODY":         request.body,
				"HEADERS_B64":  base64.StdEncoding.EncodeToString([]byte(strings.Join(headers, "\r\n"))),
			}),
			Labels: labels,
		},
		&container.HostConfig{
			NetworkMode:    container.NetworkMode("container:" + gameContainerID),
			ReadonlyRootfs: true,
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
		},
		nil, nil, "",
	)
	if err != nil {
		return "", fmt.Errorf("create REST command helper: %w", err)
	}
	defer s.docker.ContainerRemove(
		context.WithoutCancel(ctx), created.ID, container.RemoveOptions{Force: true},
	)
	if err := s.docker.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start REST command helper: %w", err)
	}
	statusCh, errorCh := s.docker.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	var status container.WaitResponse
	select {
	case err := <-errorCh:
		if err != nil {
			return "", fmt.Errorf("wait for REST command helper: %w", err)
		}
	case status = <-statusCh:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	logs, err := s.docker.ContainerLogs(ctx, created.ID, container.LogsOptions{
		ShowStdout: true, ShowStderr: true,
	})
	if err != nil {
		return "", fmt.Errorf("read REST command response: %w", err)
	}
	defer logs.Close()
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, logs); err != nil {
		return "", fmt.Errorf("decode REST command response: %w", err)
	}
	if status.StatusCode != 0 {
		return "", fmt.Errorf("REST command connection failed: %s", strings.TrimSpace(stderr.String()))
	}
	response := stdout.String()
	statusCode, payload, err := parseRESTResponse(response)
	if err != nil {
		return "", err
	}
	accepted := statusCode >= 200 && statusCode <= 299
	if len(request.acceptedStatus) > 0 {
		accepted = false
		for _, candidate := range request.acceptedStatus {
			accepted = accepted || candidate == statusCode
		}
	}
	if !accepted {
		return "", fmt.Errorf("REST command returned HTTP %d: %s", statusCode, truncateText(payload, 512))
	}
	return truncateText(payload, 4096), nil
}

func resolveRESTRequest(
	spec *templates.RESTCommandTransport,
	command string,
	environment map[string]string,
) (resolvedRESTRequest, error) {
	values := parseRESTCommandValues(command)
	method := spec.Method
	path := spec.Path
	bodyTemplate := spec.BodyTemplate
	headers := make(map[string]string, len(spec.Headers)+4)
	for name, value := range spec.Headers {
		headers[name] = value
	}
	acceptedStatus := spec.AcceptedStatus
	if len(spec.Routes) > 0 {
		var selected *templates.RESTCommandRoute
		for index := range spec.Routes {
			route := &spec.Routes[index]
			if route.Command == values.verb || stringInSlice(values.verb, route.Aliases) {
				selected = route
				break
			}
		}
		if selected == nil {
			commands := make([]string, 0, len(spec.Routes))
			for _, route := range spec.Routes {
				if route.Usage != "" {
					commands = append(commands, route.Usage)
				} else {
					commands = append(commands, route.Command)
				}
			}
			sort.Strings(commands)
			return resolvedRESTRequest{}, fmt.Errorf(
				"unknown REST command %q; available commands: %s",
				values.verb, strings.Join(commands, ", "),
			)
		}
		if len(values.arguments) < selected.MinArgs {
			usage := selected.Usage
			if usage == "" {
				usage = selected.Command
			}
			return resolvedRESTRequest{}, fmt.Errorf("usage: %s", usage)
		}
		method = selected.Method
		path = selected.Path
		bodyTemplate = selected.BodyTemplate
		for name, value := range selected.Headers {
			headers[name] = value
		}
		if len(selected.AcceptedStatus) > 0 {
			acceptedStatus = selected.AcceptedStatus
		}
	}
	pathValue, err := renderRESTValue(path, values, environment, true)
	if err != nil {
		return resolvedRESTRequest{}, err
	}
	if !strings.HasPrefix(pathValue, "/") || strings.ContainsAny(pathValue, "\r\n ") {
		return resolvedRESTRequest{}, errors.New("REST command path rendered to an unsafe value")
	}
	body, err := renderRESTValue(bodyTemplate, values, environment, false)
	if err != nil {
		return resolvedRESTRequest{}, err
	}
	renderedHeaders := make(map[string]string, len(headers)+1)
	for name, value := range headers {
		rendered, err := renderRESTValue(value, values, environment, false)
		if err != nil || strings.ContainsAny(rendered, "\r\n") {
			return resolvedRESTRequest{}, errors.New("REST command header could not be rendered safely")
		}
		renderedHeaders[name] = rendered
	}
	if spec.BasicAuth != nil {
		password, ok := environment[spec.BasicAuth.PasswordEnvironment]
		if !ok || password == "" {
			return resolvedRESTRequest{}, fmt.Errorf(
				"REST command requires environment variable %s",
				spec.BasicAuth.PasswordEnvironment,
			)
		}
		credentials := spec.BasicAuth.Username + ":" + password
		renderedHeaders["Authorization"] = "Basic " +
			base64.StdEncoding.EncodeToString([]byte(credentials))
	}
	return resolvedRESTRequest{
		method:         strings.ToUpper(method),
		path:           pathValue,
		body:           body,
		headers:        renderedHeaders,
		acceptedStatus: acceptedStatus,
	}, nil
}

func parseRESTCommandValues(command string) restCommandValues {
	command = strings.TrimSpace(command)
	fields := strings.Fields(command)
	verb := ""
	args := ""
	if len(fields) > 0 {
		verb = fields[0]
		args = strings.TrimSpace(command[len(verb):])
	}
	return restCommandValues{
		command:   command,
		verb:      strings.ToLower(strings.TrimSpace(verb)),
		args:      args,
		arguments: strings.Fields(args),
	}
}

func renderRESTValue(
	value string,
	values restCommandValues,
	environment map[string]string,
	escapeCommand bool,
) (string, error) {
	commandValue := values.command
	argsValue := values.args
	if escapeCommand {
		commandValue = url.QueryEscape(commandValue)
		argsValue = url.QueryEscape(argsValue)
	}
	value = strings.ReplaceAll(value, "{{COMMAND}}", commandValue)
	jsonCommand, _ := json.Marshal(values.command)
	value = strings.ReplaceAll(value, "{{COMMAND_JSON}}", string(jsonCommand))
	value = strings.ReplaceAll(value, "{{ARGS}}", argsValue)
	jsonArgs, _ := json.Marshal(values.args)
	value = strings.ReplaceAll(value, "{{ARGS_JSON}}", string(jsonArgs))
	for index := 1; index <= 32; index++ {
		argument := ""
		after := ""
		if index <= len(values.arguments) {
			argument = values.arguments[index-1]
		}
		if index < len(values.arguments) {
			after = strings.Join(values.arguments[index:], " ")
		}
		renderedArgument := argument
		renderedAfter := after
		if escapeCommand {
			renderedArgument = url.QueryEscape(argument)
			renderedAfter = url.QueryEscape(after)
		}
		value = strings.ReplaceAll(value, fmt.Sprintf("{{ARG%d}}", index), renderedArgument)
		jsonArgument, _ := json.Marshal(argument)
		value = strings.ReplaceAll(value, fmt.Sprintf("{{ARG%d_JSON}}", index), string(jsonArgument))
		integerMarker := fmt.Sprintf("{{ARG%d_INT}}", index)
		if strings.Contains(value, integerMarker) {
			integer, err := strconv.Atoi(argument)
			if err != nil {
				return "", fmt.Errorf("REST command argument %d must be an integer", index)
			}
			value = strings.ReplaceAll(value, integerMarker, strconv.Itoa(integer))
		}
		value = strings.ReplaceAll(
			value, fmt.Sprintf("{{ARGS_AFTER_%d}}", index), renderedAfter,
		)
		jsonAfter, _ := json.Marshal(after)
		value = strings.ReplaceAll(
			value, fmt.Sprintf("{{ARGS_AFTER_%d_JSON}}", index), string(jsonAfter),
		)
	}
	var missing string
	value = environmentReference.ReplaceAllStringFunc(value, func(marker string) string {
		match := environmentReference.FindStringSubmatch(marker)
		configured, ok := environment[match[1]]
		if !ok {
			missing = match[1]
			return ""
		}
		return configured
	})
	if missing != "" {
		return "", fmt.Errorf("REST command requires environment variable %s", missing)
	}
	return value, nil
}

func stringInSlice(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func parseRESTResponse(response string) (int, string, error) {
	head, body, found := strings.Cut(response, "\r\n\r\n")
	if !found {
		return 0, "", errors.New("REST command returned an invalid HTTP response")
	}
	first, _, _ := strings.Cut(head, "\r\n")
	fields := strings.Fields(first)
	if len(fields) < 2 {
		return 0, "", errors.New("REST command returned an invalid HTTP status")
	}
	status, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, "", errors.New("REST command returned an invalid HTTP status")
	}
	return status, strings.TrimSpace(body), nil
}

func truncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

func rconCommandEnvironment(values []string) (rconEnvironment, bool) {
	environment := make(map[string]string, len(values))
	for _, value := range values {
		name, configured, found := strings.Cut(value, "=")
		if found {
			environment[name] = configured
		}
	}
	startup := environment["STARTUP"]
	port := strings.TrimSpace(environment["RCON_PORT"])
	password := environment["ADMIN_PASSWORD"]
	if !strings.Contains(startup, "rcon -s -a") || port == "" || password == "" {
		return rconEnvironment{}, false
	}
	for _, character := range port {
		if character < '0' || character > '9' {
			return rconEnvironment{}, false
		}
	}
	return rconEnvironment{port: port, password: password}, true
}

func validTail(value string) bool {
	if len(value) == 0 || len(value) > 4 {
		return false
	}
	number := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
		number = number*10 + int(char-'0')
	}
	return number >= 20 && number <= 5000
}

type synchronizedFrameEncoder struct {
	mu      sync.Mutex
	writer  io.Writer
	flusher http.Flusher
}

func (e *synchronizedFrameEncoder) emit(frame consoleFrame) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if frame.Severity == "" {
		frame.Severity = consolelog.Classify(frame.Stream, frame.Message)
	}
	encoded, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if _, err := e.writer.Write(append(encoded, '\n')); err != nil {
		return err
	}
	if e.flusher != nil {
		e.flusher.Flush()
	}
	return nil
}

type lineFrameWriter struct {
	stream  string
	phase   string
	encoder *synchronizedFrameEncoder
	mu      sync.Mutex
	timer   *time.Timer
	pending []byte
}

func (w *lineFrameWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, value...)
	for {
		index := bytes.IndexByte(w.pending, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSuffix(string(w.pending[:index]), "\r")
		w.pending = w.pending[index+1:]
		if line != "" {
			if err := w.acceptLine(line); err != nil {
				return 0, err
			}
		}
	}
	if len(w.pending) > 0 {
		w.scheduleFlush()
	} else if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	return len(value), nil
}

func (w *lineFrameWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	line := string(w.pending)
	if strings.TrimSpace(line) != "" {
		_ = w.acceptLine(line)
	}
	w.pending = nil
}

func (w *lineFrameWriter) acceptLine(line string) error {
	line = stripANSI(line)
	if strings.TrimSpace(line) == "" {
		return nil
	}
	return w.encoder.emit(consoleFrame{
		Stream: w.stream, Phase: w.phase, Message: line, ObservedAt: time.Now().UTC(),
	})
}

func (w *lineFrameWriter) scheduleFlush() {
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(180*time.Millisecond, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.timer = nil
		line := string(w.pending)
		w.pending = nil
		if strings.TrimSpace(line) != "" {
			_ = w.acceptLine(line)
		}
	})
}

var ansiSequence = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)

func stripANSI(value string) string {
	return ansiSequence.ReplaceAllString(value, "")
}
