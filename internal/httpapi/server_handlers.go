package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/dockside-gg/game-panel/internal/store"
	"github.com/dockside-gg/game-panel/internal/templates"
	"github.com/go-chi/chi/v5"
)

var serverNamePattern = regexp.MustCompile(`^[\pL\pN][\pL\pN _.'():+#-]{1,79}$`)
var environmentNamePattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func (s *Server) listServers(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	items, err := s.store.ListServers(
		r.Context(), session.User.ID, session.User.PanelRole,
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": items})
}

func (s *Server) serverDetail(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.ServerByID(r.Context(), chi.URLParam(r, "serverID"))
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) serverActivity(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if _, err := s.store.ServerByID(r.Context(), serverID); err != nil {
		writeProblem(w, r, err)
		return
	}
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 250 {
			writeProblem(w, r, errors.Join(errBadRequest, errors.New("limit must be 1-250")))
			return
		}
		limit = parsed
	}
	events, err := s.store.ServerActivity(r.Context(), serverID, limit)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity": events})
}

func (s *Server) serverConsole(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if _, err := s.store.ServerByID(r.Context(), serverID); err != nil {
		writeProblem(w, r, err)
		return
	}
	stream, err := s.engine.OpenConsole(r.Context(), serverID, 250)
	if err != nil {
		entries, logErr := s.store.ServerOperationLogs(r.Context(), serverID, 1000)
		if logErr == nil && len(entries) > 0 {
			w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache, no-store")
			w.WriteHeader(http.StatusOK)
			encoder := json.NewEncoder(&flushingResponseWriter{ResponseWriter: w})
			for _, entry := range entries {
				if encodeErr := encoder.Encode(entry); encodeErr != nil {
					return
				}
			}
			return
		}
		writeProblem(w, r, err)
		return
	}
	defer stream.Close()
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	writer := &flushingResponseWriter{ResponseWriter: w}
	if _, err := io.CopyBuffer(writer, stream, make([]byte, 32<<10)); err != nil && r.Context().Err() == nil {
		s.logger.Warn("console proxy ended", "server_id", serverID, "error", err)
	}
}

type serverCommandRequest struct {
	Command string `json:"command"`
}

func (s *Server) serverCommand(w http.ResponseWriter, r *http.Request) {
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
	var input serverCommandRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Command = strings.TrimSpace(input.Command)
	if input.Command == "" || len(input.Command) > 2048 ||
		strings.ContainsAny(input.Command, "\x00\r\n") {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("command must be one line and at most 2048 bytes")))
		return
	}
	ready, err := s.store.ServerCommandReady(r.Context(), serverID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if !ready {
		writeProblem(w, r, errors.Join(
			store.ErrConflict,
			errors.New("the game server command transport is still starting; wait for console commands to become ready"),
		))
		return
	}
	intentionalShutdown, err := s.store.MarkIntentionalConsoleShutdown(
		r.Context(), serverID, input.Command,
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	commandResult, err := s.engine.Command(r.Context(), serverID, input.Command)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if err := s.store.RecordConsoleCommand(r.Context(), serverID, session.User.ID, len(input.Command)); err != nil {
		s.logger.Error("record console command failed", "server_id", serverID, "error", err)
	}
	if intentionalShutdown {
		s.logger.Info("intentional in-game shutdown command accepted", "server_id", serverID)
	}
	writeJSON(w, http.StatusOK, commandResult)
}

type flushingResponseWriter struct {
	http.ResponseWriter
}

func (w *flushingResponseWriter) Write(value []byte) (int, error) {
	count, err := w.ResponseWriter.Write(value)
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
	return count, err
}

type createServerRequest struct {
	TemplateVersionID      string                    `json:"template_version_id"`
	Name                   string                    `json:"name"`
	Description            string                    `json:"description"`
	Image                  string                    `json:"image"`
	Ports                  []createServerPortRequest `json:"ports"`
	CPULimitMillicores     *int                      `json:"cpu_limit_millicores"`
	MemoryLimitMB          *int64                    `json:"memory_limit_mb"`
	MemoryReservationMB    *int64                    `json:"memory_reservation_mb"`
	DiskLimitMB            *int64                    `json:"disk_limit_mb"`
	PidsLimit              *int                      `json:"pids_limit"`
	IOWeight               *int                      `json:"io_weight"`
	Variables              map[string]string         `json:"variables"`
	StartAfterProvisioning bool                      `json:"start_after_provisioning"`
}

type createServerPortRequest struct {
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
	Purpose       string `json:"purpose"`
	Environment   string `json:"environment"`
	Primary       bool   `json:"primary"`
}

func (s *Server) createServer(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	var input createServerRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	ports, err := validateCreateServerPorts(input.Ports)
	if !serverNamePattern.MatchString(input.Name) || len(input.Description) > 500 || err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid server name, description, or network ports"), err))
		return
	}
	template, err := s.store.TemplateByVersion(r.Context(), input.TemplateVersionID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	var canonical templates.CanonicalTemplate
	if err := json.Unmarshal(template.CanonicalDocument, &canonical); err != nil {
		writeProblem(w, r, fmt.Errorf("decode canonical template: %w", err))
		return
	}
	selectedImage := strings.TrimSpace(input.Image)
	if selectedImage == "" {
		selectedImage = canonical.DefaultImage
	}
	imageAllowed := false
	for _, image := range canonical.Images {
		if image == selectedImage {
			imageAllowed = true
			break
		}
	}
	if !imageAllowed {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("selected image is not part of this template version")))
		return
	}
	variables, err := validateTemplateVariables(canonical.Variables, input.Variables)
	if err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	resources, err := requestedResources(input)
	if err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	result, err := s.store.CreateServer(r.Context(), store.CreateServerParams{
		TemplateVersionID: input.TemplateVersionID,
		Name:              input.Name,
		Description:       input.Description,
		ImageReference:    selectedImage,
		Ports:             ports,
		Resources:         resources,
		Variables:         variables,
		CreatedBy:         session.User.ID,
		Start:             input.StartAfterProvisioning,
		GamePortStart:     s.cfg.GamePortStart,
		GamePortEnd:       s.cfg.GamePortEnd,
		EncryptionBox:     s.box,
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if err := s.store.AddAudit(
		r.Context(), session.User.ID, "server.create", "server", result.ServerID,
		requestIDFromContext(r.Context()), clientIP(r), r.UserAgent(),
		map[string]any{"template_version_id": input.TemplateVersionID, "operation_id": result.OperationID},
	); err != nil {
		s.logger.Error("write server creation audit failed", "error", err)
	}
	writeJSON(w, http.StatusAccepted, result)
}

func validateCreateServerPorts(input []createServerPortRequest) ([]store.ServerPort, error) {
	if len(input) < 1 || len(input) > 32 {
		return nil, errors.New("one to 32 published ports are required")
	}
	result := make([]store.ServerPort, 0, len(input))
	primaryCount := 0
	seen := make(map[string]struct{}, len(input))
	for _, source := range input {
		protocol := strings.ToLower(strings.TrimSpace(source.Protocol))
		purpose := strings.TrimSpace(source.Purpose)
		environment := strings.ToUpper(strings.TrimSpace(source.Environment))
		if source.ContainerPort < 1 || source.ContainerPort > 65535 ||
			(protocol != "tcp" && protocol != "udp") ||
			len(purpose) > 120 || len(environment) > 80 ||
			(environment != "" && !environmentNamePattern.MatchString(environment)) {
			return nil, errors.New("every port needs a valid container port and TCP or UDP protocol")
		}
		key := fmt.Sprintf("%d/%s", source.ContainerPort, protocol)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate container allocation %s", key)
		}
		seen[key] = struct{}{}
		if source.Primary {
			primaryCount++
		}
		result = append(result, store.ServerPort{
			ContainerPort: source.ContainerPort,
			Protocol:      protocol,
			Purpose:       purpose,
			Environment:   environment,
			IsPrimary:     source.Primary,
		})
	}
	if primaryCount != 1 {
		return nil, errors.New("exactly one published port must be primary")
	}
	return result, nil
}

func validateTemplateVariables(definitions []templates.Variable, supplied map[string]string) ([]store.StoredVariable, error) {
	known := make(map[string]templates.Variable, len(definitions))
	for _, definition := range definitions {
		known[definition.Environment] = definition
	}
	for name := range supplied {
		if _, exists := known[name]; !exists {
			return nil, fmt.Errorf("unknown template variable %q", name)
		}
	}
	result := make([]store.StoredVariable, 0, len(definitions))
	for _, definition := range definitions {
		value := definition.DefaultValue
		if suppliedValue, suppliedValueExists := supplied[definition.Environment]; suppliedValueExists {
			if !definition.UserEditable {
				if suppliedValue != definition.DefaultValue {
					return nil, fmt.Errorf("template variable %q is not user editable", definition.Environment)
				}
			} else {
				value = suppliedValue
			}
		}
		if len(value) > 65536 {
			return nil, fmt.Errorf("template variable %q is too long", definition.Environment)
		}
		if err := validateVariableRules(definition.Environment, value, definition.Rules); err != nil {
			return nil, err
		}
		valueCopy := value
		result = append(result, store.StoredVariable{
			Name: definition.Environment, ValueText: &valueCopy, IsSecret: definition.Secret,
		})
	}
	return result, nil
}

func validateVariableRules(name, value, rules string) error {
	for _, rule := range strings.Split(rules, "|") {
		rule = strings.TrimSpace(rule)
		switch {
		case rule == "", rule == "string", rule == "nullable", rule == "sometimes":
		case rule == "required":
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("template variable %q is required", name)
			}
		case rule == "integer", rule == "numeric":
			if value != "" {
				if _, err := strconv.ParseInt(value, 10, 64); err != nil {
					return fmt.Errorf("template variable %q must be an integer", name)
				}
			}
		case strings.HasPrefix(rule, "max:"):
			maximum, err := strconv.Atoi(strings.TrimPrefix(rule, "max:"))
			if err == nil && len(value) > maximum {
				return fmt.Errorf("template variable %q exceeds its maximum length", name)
			}
		case strings.HasPrefix(rule, "min:"):
			minimum, err := strconv.Atoi(strings.TrimPrefix(rule, "min:"))
			if err == nil && value != "" && len(value) < minimum {
				return fmt.Errorf("template variable %q is shorter than its minimum length", name)
			}
		}
	}
	return nil
}

func requestedResources(input createServerRequest) (store.ServerResources, error) {
	resources := store.ServerResources{
		CPULimitMillicores:     input.CPULimitMillicores,
		MemoryLimitBytes:       megabytes(input.MemoryLimitMB),
		MemoryReservationBytes: megabytes(input.MemoryReservationMB),
		DiskLimitBytes:         megabytes(input.DiskLimitMB),
		PidsLimit:              input.PidsLimit,
		IOWeight:               input.IOWeight,
	}
	if resources.CPULimitMillicores != nil && (*resources.CPULimitMillicores < 100 || *resources.CPULimitMillicores > 128000) {
		return resources, errors.New("CPU limit must be 100-128000 millicores or unlimited")
	}
	if resources.MemoryLimitBytes != nil && (*resources.MemoryLimitBytes < 64<<20 || *resources.MemoryLimitBytes > 1<<40) {
		return resources, errors.New("memory limit must be 64 MB-1 TB or unlimited")
	}
	if resources.MemoryReservationBytes != nil &&
		(resources.MemoryLimitBytes == nil || *resources.MemoryReservationBytes > *resources.MemoryLimitBytes) {
		return resources, errors.New("memory reservation requires a limit and cannot exceed it")
	}
	if resources.DiskLimitBytes != nil &&
		(*resources.DiskLimitBytes < 64<<20 || *resources.DiskLimitBytes > 16<<40) {
		return resources, errors.New("disk alert limit must be 64 MB-16 TB or unlimited")
	}
	if resources.PidsLimit != nil && (*resources.PidsLimit < 16 || *resources.PidsLimit > 1_000_000) {
		return resources, errors.New("PID limit must be 16-1000000 or unlimited")
	}
	if resources.IOWeight != nil && (*resources.IOWeight < 10 || *resources.IOWeight > 1000) {
		return resources, errors.New("I/O weight must be 10-1000 or unset")
	}
	return resources, nil
}

func megabytes(value *int64) *int64 {
	if value == nil {
		return nil
	}
	bytes := *value * 1024 * 1024
	return &bytes
}

type serverPowerRequest struct {
	Action string `json:"action"`
}

func (s *Server) serverPower(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canOperate(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	var input serverPowerRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Action != "start" && input.Action != "stop" && input.Action != "restart" && input.Action != "kill" {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid power action")))
		return
	}
	if session.User.PanelRole != "owner" && session.User.PanelRole != "administrator" {
		allowed, err := s.store.UserHasServerPermission(
			r.Context(), session.User.ID, serverID, "server.power."+input.Action,
		)
		if err != nil {
			writeProblem(w, r, err)
			return
		}
		if !allowed {
			writeProblem(w, r, errForbidden)
			return
		}
	}
	if err := s.store.ServerExists(r.Context(), serverID); err != nil {
		writeProblem(w, r, err)
		return
	}
	operationID, err := s.store.RequestImmediatePower(
		r.Context(), serverID, session.User.ID, input.Action,
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	powerErr := s.engine.Power(r.Context(), serverID, input.Action)
	if err := s.store.FinishPower(
		r.Context(), serverID, operationID, input.Action, powerErr,
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	if powerErr != nil {
		writeProblem(w, r, powerErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"operation_id": operationID})
}

type deleteServerRequest struct {
	ConfirmName string `json:"confirm_name"`
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	server, err := s.store.ServerByID(r.Context(), serverID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	var input deleteServerRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ConfirmName != server.Name {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("confirmation name does not match")))
		return
	}
	if err := s.engine.Delete(r.Context(), serverID); err != nil {
		writeProblem(w, r, errors.Join(store.ErrConflict, err))
		return
	}
	if err := s.store.FinalizeServerDeletion(r.Context(), serverID, session.User.ID); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func canAdminister(role string) bool {
	return role == "owner" || role == "administrator"
}

func canOperate(role string) bool {
	return canAdminister(role) || role == "operator"
}
