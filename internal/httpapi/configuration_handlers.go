package httpapi

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/dockside-gg/game-panel/internal/store"
	"github.com/go-chi/chi/v5"
)

var cpuSetPattern = regexp.MustCompile(`^(?:\d+(?:-\d+)?)(?:,\d+(?:-\d+)?)*$`)

type updateStartupRequest struct {
	Version             int64                       `json:"version"`
	Image               string                      `json:"image"`
	StartupOverride     string                      `json:"startup_override"`
	Variables           map[string]string           `json:"variables"`
	VariableDefinitions []variableDefinitionRequest `json:"variable_definitions"`
}

type variableDefinitionRequest struct {
	Environment  string `json:"environment"`
	DisplayName  string `json:"display_name"`
	Description  string `json:"description"`
	DefaultValue string `json:"default_value"`
	UserViewable bool   `json:"user_viewable"`
	UserEditable bool   `json:"user_editable"`
	Rules        string `json:"rules"`
	FieldType    string `json:"field_type"`
	Secret       bool   `json:"secret"`
}

type networkPortRequest struct {
	BindAddress   string `json:"bind_address"`
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
	Purpose       string `json:"purpose"`
	Environment   string `json:"environment"`
	IsPrimary     bool   `json:"is_primary"`
}

type updateNetworkRequest struct {
	Version int64                `json:"version"`
	Ports   []networkPortRequest `json:"ports"`
}

type updateSettingsRequest struct {
	Version             int64   `json:"version"`
	Name                string  `json:"name"`
	Description         string  `json:"description"`
	CPULimitMillicores  *int    `json:"cpu_limit_millicores"`
	CPUSet              *string `json:"cpu_set"`
	MemoryLimitMB       *int64  `json:"memory_limit_mb"`
	MemoryReservationMB *int64  `json:"memory_reservation_mb"`
	SwapLimitMB         *int64  `json:"swap_limit_mb"`
	DiskLimitMB         *int64  `json:"disk_limit_mb"`
	PidsLimit           *int    `json:"pids_limit"`
	IOWeight            *int    `json:"io_weight"`
	AutoRecoveryEnabled bool    `json:"auto_recovery_enabled"`
}

func (s *Server) serverConfiguration(w http.ResponseWriter, r *http.Request) {
	result, err := s.store.ServerConfiguration(
		r.Context(), chi.URLParam(r, "serverID"), s.box,
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) updateServerStartup(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	current, err := s.store.ServerConfiguration(r.Context(), serverID, s.box)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	var input updateStartupRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if current.Status != "stopped" {
		writeProblem(w, r, errors.Join(store.ErrConflict, errors.New("stop the server before changing startup configuration")))
		return
	}
	if input.Version != current.Version {
		writeProblem(w, r, errors.Join(store.ErrConflict, errors.New("server configuration changed; refresh and try again")))
		return
	}
	input.Image = strings.TrimSpace(input.Image)
	if len(input.Image) == 0 || len(input.Image) > 512 || !imageInTemplate(current.Images, input.Image) {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("runtime image is not allowed by this template")))
		return
	}
	override := store.NormalizeStartupOverride(input.StartupOverride)
	if override != nil && len(*override) > 32768 {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("startup command is too long")))
		return
	}
	definitions := make(map[string]store.ServerConfigurationVariable, len(current.Variables)+len(input.VariableDefinitions))
	for _, definition := range current.Variables {
		definitions[definition.Name] = definition
	}
	customDefinitions, err := validateCustomVariableDefinitions(
		input.VariableDefinitions, current.Variables, current.Ports,
	)
	if err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	for _, definition := range customDefinitions {
		definitions[definition.Name] = definition
	}
	secretNames := make(map[string]bool)
	for name, value := range input.Variables {
		definition, ok := definitions[name]
		if !ok || !definition.UserEditable {
			writeProblem(w, r, errors.Join(errBadRequest, fmt.Errorf("variable %q is not editable", name)))
			return
		}
		if len(value) > 65536 {
			writeProblem(w, r, errors.Join(errBadRequest, fmt.Errorf("variable %q is too long", name)))
			return
		}
		if err := validateVariableRules(name, value, definition.Rules); err != nil {
			writeProblem(w, r, errors.Join(errBadRequest, err))
			return
		}
		secretNames[name] = definition.Secret
	}
	oldRequest := current.RuntimeRequest()
	current.ApplyStartupCandidate(input.Image, override, input.Variables)
	result, err := s.engine.Reconfigure(r.Context(), serverID, current.RuntimeRequest())
	if err != nil {
		writeProblem(w, r, errors.Join(store.ErrConflict, err))
		return
	}
	err = s.store.CommitStartupConfiguration(
		r.Context(), serverID, session.User.ID, input.Version, input.Image,
		override, input.Variables, secretNames, customDefinitions,
		result.ContainerID, s.box,
	)
	if err != nil {
		s.rollbackRuntimeConfiguration(r, serverID, oldRequest, err)
		writeProblem(w, r, err)
		return
	}
	updated, err := s.store.ServerConfiguration(r.Context(), serverID, s.box)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func validateCustomVariableDefinitions(
	input []variableDefinitionRequest,
	existing []store.ServerConfigurationVariable,
	ports []store.ServerPort,
) ([]store.ServerConfigurationVariable, error) {
	if len(input) > 64 {
		return nil, errors.New("a server may define at most 64 custom variables")
	}
	reserved := map[string]bool{
		"STARTUP": true, "P_SERVER_UUID": true, "P_SERVER_LOCATION": true,
		"SERVER_IP": true, "SERVER_PORT": true, "SERVER_PUBLIC_PORT": true,
		"SERVER_MEMORY": true,
	}
	for _, definition := range existing {
		if !definition.Custom {
			reserved[definition.Name] = true
		}
	}
	for _, port := range ports {
		if port.Environment != "" {
			reserved[port.Environment] = true
			reserved[port.Environment+"_PUBLIC"] = true
		}
	}
	seen := make(map[string]bool, len(input))
	result := make([]store.ServerConfigurationVariable, 0, len(input))
	for _, source := range input {
		source.Environment = strings.ToUpper(strings.TrimSpace(source.Environment))
		source.DisplayName = strings.TrimSpace(source.DisplayName)
		source.Description = strings.TrimSpace(source.Description)
		source.FieldType = strings.ToLower(strings.TrimSpace(source.FieldType))
		source.Rules = strings.TrimSpace(source.Rules)
		if source.FieldType == "" {
			source.FieldType = "text"
		}
		switch source.FieldType {
		case "text", "number", "boolean", "password", "select":
		default:
			return nil, fmt.Errorf("variable %q has an unsupported field type", source.Environment)
		}
		if !environmentNamePattern.MatchString(source.Environment) ||
			strings.HasPrefix(source.Environment, "DOCKSIDE_") ||
			reserved[source.Environment] || seen[source.Environment] ||
			source.DisplayName == "" || len(source.DisplayName) > 120 ||
			len(source.Description) > 1000 || len(source.DefaultValue) > 65536 ||
			len(source.Rules) > 1000 {
			return nil, fmt.Errorf("custom variable %q is invalid, duplicated, or reserved", source.Environment)
		}
		seen[source.Environment] = true
		result = append(result, store.ServerConfigurationVariable{
			Name: source.Environment, DisplayName: source.DisplayName,
			Description: source.Description, DefaultValue: source.DefaultValue,
			Secret:       source.Secret || source.FieldType == "password",
			UserViewable: source.UserViewable, UserEditable: source.UserEditable,
			Rules: source.Rules, FieldType: source.FieldType, Custom: true,
		})
	}
	return result, nil
}

func (s *Server) updateServerNetwork(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	var input updateNetworkRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	serverID := chi.URLParam(r, "serverID")
	current, err := s.store.ServerConfiguration(r.Context(), serverID, s.box)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if current.Status != "stopped" {
		writeProblem(w, r, errors.Join(store.ErrConflict, errors.New("stop the server before changing network allocations")))
		return
	}
	if input.Version != current.Version {
		writeProblem(w, r, errors.Join(store.ErrConflict, errors.New("server configuration changed; refresh and try again")))
		return
	}
	ports, err := s.validateNetworkPorts(input.Ports)
	if err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	if err := validateTemplatePortPolicy(current.TemplateNetworkPorts, ports); err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	oldRequest := current.RuntimeRequest()
	current.ApplyPortsCandidate(ports)
	result, err := s.engine.Reconfigure(r.Context(), serverID, current.RuntimeRequest())
	if err != nil {
		writeProblem(w, r, errors.Join(store.ErrConflict, err))
		return
	}
	if err := s.store.CommitServerPorts(
		r.Context(), serverID, session.User.ID, input.Version, ports, result.ContainerID,
	); err != nil {
		s.rollbackRuntimeConfiguration(r, serverID, oldRequest, err)
		writeProblem(w, r, err)
		return
	}
	current.Version++
	writeJSON(w, http.StatusOK, current)
}

func (s *Server) updateServerSettings(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	var input updateSettingsRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if !serverNamePattern.MatchString(input.Name) || len(input.Description) > 500 {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid server name or description")))
		return
	}
	resources, err := requestedResources(createServerRequest{
		CPULimitMillicores:  input.CPULimitMillicores,
		MemoryLimitMB:       input.MemoryLimitMB,
		MemoryReservationMB: input.MemoryReservationMB,
		PidsLimit:           input.PidsLimit,
		IOWeight:            input.IOWeight,
	})
	if err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	resources.CPUSet = normalizedCPUSet(input.CPUSet)
	resources.SwapLimitBytes = megabytes(input.SwapLimitMB)
	resources.DiskLimitBytes = megabytes(input.DiskLimitMB)
	if err := validateAdditionalResources(resources); err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	serverID := chi.URLParam(r, "serverID")
	current, err := s.store.ServerConfiguration(r.Context(), serverID, s.box)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	if current.Status != "stopped" {
		writeProblem(w, r, errors.Join(store.ErrConflict, errors.New("stop the server before changing settings or resource limits")))
		return
	}
	if input.Version != current.Version {
		writeProblem(w, r, errors.Join(store.ErrConflict, errors.New("server configuration changed; refresh and try again")))
		return
	}
	oldRequest := current.RuntimeRequest()
	current.ApplySettingsCandidate(input.Name, input.Description, resources)
	result, err := s.engine.Reconfigure(r.Context(), serverID, current.RuntimeRequest())
	if err != nil {
		writeProblem(w, r, errors.Join(store.ErrConflict, err))
		return
	}
	if err := s.store.CommitServerSettings(
		r.Context(), serverID, session.User.ID, input.Version, input.Name,
		input.Description, resources, result.ContainerID, input.AutoRecoveryEnabled,
	); err != nil {
		s.rollbackRuntimeConfiguration(r, serverID, oldRequest, err)
		writeProblem(w, r, err)
		return
	}
	current.Version++
	writeJSON(w, http.StatusOK, current)
}

func (s *Server) validateNetworkPorts(input []networkPortRequest) ([]store.ServerPort, error) {
	if len(input) == 0 || len(input) > 64 {
		return nil, errors.New("configure between 1 and 64 port allocations")
	}
	primaryCount := 0
	seen := make(map[string]struct{}, len(input))
	result := make([]store.ServerPort, 0, len(input))
	for _, candidate := range input {
		candidate.BindAddress = strings.TrimSpace(candidate.BindAddress)
		if candidate.BindAddress == "" {
			candidate.BindAddress = "0.0.0.0"
		}
		ip := net.ParseIP(candidate.BindAddress)
		if ip == nil || (!ip.IsUnspecified() && !ip.IsLoopback()) {
			return nil, errors.New("bind addresses must be loopback or all interfaces")
		}
		candidate.Protocol = strings.ToLower(strings.TrimSpace(candidate.Protocol))
		candidate.Purpose = strings.TrimSpace(candidate.Purpose)
		candidate.Environment = strings.ToUpper(strings.TrimSpace(candidate.Environment))
		if candidate.HostPort < s.cfg.GamePortStart || candidate.HostPort > s.cfg.GamePortEnd ||
			candidate.ContainerPort < 1 || candidate.ContainerPort > 65535 ||
			(candidate.Protocol != "tcp" && candidate.Protocol != "udp") ||
			len(candidate.Purpose) > 120 || len(candidate.Environment) > 80 ||
			(candidate.Environment != "" && !environmentNamePattern.MatchString(candidate.Environment)) {
			return nil, errors.New("one or more port allocations are invalid")
		}
		key := fmt.Sprint(candidate.HostPort) + "/" + candidate.Protocol
		if _, exists := seen[key]; exists {
			return nil, errors.New("duplicate host port allocation")
		}
		seen[key] = struct{}{}
		if candidate.IsPrimary {
			primaryCount++
		}
		result = append(result, store.ServerPort{
			BindAddress: candidate.BindAddress, HostPort: candidate.HostPort,
			ContainerPort: candidate.ContainerPort, Protocol: candidate.Protocol,
			Purpose: candidate.Purpose, Environment: candidate.Environment,
			IsPrimary: candidate.IsPrimary,
		})
	}
	if primaryCount != 1 {
		return nil, errors.New("exactly one allocation must be primary")
	}
	return result, nil
}

func imageInTemplate(images map[string]string, requested string) bool {
	for _, image := range images {
		if image == requested {
			return true
		}
	}
	return false
}

func normalizedCPUSet(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func validateAdditionalResources(resources store.ServerResources) error {
	if resources.CPUSet != nil && (len(*resources.CPUSet) > 255 || !cpuSetPattern.MatchString(*resources.CPUSet)) {
		return errors.New("CPU set must use values like 0,2-3")
	}
	if resources.SwapLimitBytes != nil && resources.MemoryLimitBytes != nil &&
		*resources.SwapLimitBytes < *resources.MemoryLimitBytes {
		return errors.New("swap limit must be at least the memory limit or unlimited")
	}
	if resources.DiskLimitBytes != nil && *resources.DiskLimitBytes < 64<<20 {
		return errors.New("disk limit must be at least 64 MB or unlimited")
	}
	return nil
}

func (s *Server) rollbackRuntimeConfiguration(
	r *http.Request,
	serverID string,
	previous engineclient.ReconfigureRequest,
	commitErr error,
) {
	rollback, err := s.engine.Reconfigure(r.Context(), serverID, previous)
	if err != nil {
		s.logger.Error(
			"runtime configuration database commit and engine rollback both failed",
			"server_id", serverID,
			"commit_error", commitErr,
			"rollback_error", err,
		)
		return
	}
	s.logger.Warn(
		"runtime configuration database commit failed; engine rollback succeeded",
		"server_id", serverID,
		"commit_error", commitErr,
		"container_id", rollback.ContainerID,
	)
}
