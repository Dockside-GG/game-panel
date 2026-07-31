package templates

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)
var networkEnvironmentName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
var restCommandName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
var advertisedPortArgument = regexp.MustCompile(
	`(?i)(-{1,2}(?:publicport|public-port|advertisedport|advertised-port)\s*=?\s*)(\{\{SERVER_PORT\}\}|\{\{server\.allocations\.default\.port\}\}|\$\{SERVER_PORT\}|\$SERVER_PORT)`,
)

const canonicalNormalizationVersion = "10"

func Normalize(sourceKind, category, upstreamURL string, document json.RawMessage) (TemplateEntry, error) {
	var egg struct {
		Name            string            `json:"name"`
		Author          string            `json:"author"`
		Description     string            `json:"description"`
		DockerImages    json.RawMessage   `json:"docker_images"`
		Startup         string            `json:"startup"`
		StartupCommands map[string]string `json:"startup_commands"`
		Features        json.RawMessage   `json:"features"`
		FileDenylist    json.RawMessage   `json:"file_denylist"`
		Dockside        struct {
			NetworkPorts     []NetworkPort    `json:"network_ports"`
			CommandTransport CommandTransport `json:"command_transport"`
			BackupDefaults   BackupDefaults   `json:"backup_defaults"`
			ResourceDefaults ResourceDefaults `json:"resource_defaults"`
		} `json:"dockside"`
		Config  map[string]json.RawMessage `json:"config"`
		Scripts struct {
			Installation struct {
				Script     string `json:"script"`
				Container  string `json:"container"`
				Entrypoint string `json:"entrypoint"`
			} `json:"installation"`
		} `json:"scripts"`
		Variables []struct {
			Name         string          `json:"name"`
			Description  string          `json:"description"`
			Environment  string          `json:"env_variable"`
			DefaultValue interface{}     `json:"default_value"`
			UserViewable bool            `json:"user_viewable"`
			UserEditable bool            `json:"user_editable"`
			Rules        json.RawMessage `json:"rules"`
			FieldType    string          `json:"field_type"`
			Secret       bool            `json:"secret"`
		} `json:"variables"`
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := decoder.Decode(&egg); err != nil {
		return TemplateEntry{}, fmt.Errorf("decode template: %w", err)
	}
	egg.Name = strings.TrimSpace(egg.Name)
	if egg.Name == "" {
		return TemplateEntry{}, errors.New("template has no name")
	}
	images, err := normalizeImages(egg.DockerImages)
	if err != nil {
		return TemplateEntry{}, err
	}
	if len(images) == 0 {
		return TemplateEntry{}, errors.New("template has no Docker images")
	}
	startup := strings.TrimSpace(egg.Startup)
	if startup == "" && len(egg.StartupCommands) > 0 {
		if value := strings.TrimSpace(egg.StartupCommands["Default"]); value != "" {
			startup = value
		} else {
			names := make([]string, 0, len(egg.StartupCommands))
			for name := range egg.StartupCommands {
				names = append(names, name)
			}
			sort.Strings(names)
			startup = strings.TrimSpace(egg.StartupCommands[names[0]])
		}
	}
	if startup == "" {
		return TemplateEntry{}, errors.New("template has no startup command")
	}
	startup = advertisedPortArgument.ReplaceAllString(
		startup, `${1}{{SERVER_PUBLIC_PORT}}`,
	)

	imageNames := make([]string, 0, len(images))
	for name, image := range images {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(image) == "" {
			return TemplateEntry{}, errors.New("template has an empty Docker image")
		}
		imageNames = append(imageNames, name)
	}
	sort.Strings(imageNames)
	defaultImage := images[imageNames[0]]

	variables := make([]Variable, 0, len(egg.Variables))
	warnings := make([]string, 0)
	for _, source := range egg.Variables {
		if strings.TrimSpace(source.Environment) == "" {
			warnings = append(warnings, "Ignored a variable without an environment name.")
			continue
		}
		defaultValue := ""
		switch value := source.DefaultValue.(type) {
		case nil:
		case string:
			defaultValue = value
		case json.Number:
			defaultValue = value.String()
		case bool:
			defaultValue = fmt.Sprintf("%t", value)
		default:
			encoded, _ := json.Marshal(value)
			defaultValue = string(encoded)
		}
		if branded, changed := docksideServerNameDefault(
			source.Environment,
			source.Name,
			defaultValue,
		); changed {
			defaultValue = branded
			warnings = append(warnings, fmt.Sprintf(
				"Replaced the upstream server-name placeholder for %s with Dockside branding.",
				strings.TrimSpace(source.Environment),
			))
		}
		secret := source.Secret || looksSecret(source.Environment, source.Name)
		if secret && defaultValue != "" {
			defaultValue = ""
			warnings = append(warnings, fmt.Sprintf(
				"Removed the secret default for %s; secret values must be entered per server.",
				strings.TrimSpace(source.Environment),
			))
		}
		variables = append(variables, Variable{
			Name:         strings.TrimSpace(source.Name),
			Description:  strings.TrimSpace(source.Description),
			Environment:  strings.TrimSpace(source.Environment),
			DefaultValue: defaultValue,
			UserViewable: source.UserViewable,
			UserEditable: source.UserEditable,
			Rules:        normalizeRules(source.Rules),
			FieldType:    strings.TrimSpace(source.FieldType),
			Secret:       secret,
		})
	}

	stopCommand := rawString(egg.Config["stop"])
	networkPorts := inferNetworkPorts(startup, variables)
	if len(egg.Dockside.NetworkPorts) > 0 {
		networkPorts, err = normalizeExplicitNetworkPorts(egg.Dockside.NetworkPorts)
		if err != nil {
			return TemplateEntry{}, err
		}
	}
	commandTransport, err := normalizeCommandTransport(egg.Dockside.CommandTransport)
	if err != nil {
		return TemplateEntry{}, err
	}
	if err := validateBackupDefaults(egg.Dockside.BackupDefaults); err != nil {
		return TemplateEntry{}, err
	}
	if err := validateResourceDefaults(egg.Dockside.ResourceDefaults); err != nil {
		return TemplateEntry{}, err
	}
	digest := sha256.Sum256(document)
	entry := TemplateEntry{
		Name:           egg.Name,
		Category:       strings.TrimSpace(category),
		SourceKind:     sourceKind,
		UpstreamURL:    upstreamURL,
		Author:         strings.TrimSpace(egg.Author),
		Description:    strings.TrimSpace(egg.Description),
		SourceDigest:   versionedSourceDigest(hex.EncodeToString(digest[:])),
		SourceDocument: append(json.RawMessage(nil), document...),
		CanonicalDocument: CanonicalTemplate{
			APIVersion:        "dockside.gg/templates/v1",
			Name:              egg.Name,
			Description:       strings.TrimSpace(egg.Description),
			SourceKind:        sourceKind,
			Category:          strings.TrimSpace(category),
			Images:            images,
			DefaultImage:      defaultImage,
			StartupCommand:    startup,
			StopCommand:       stopCommand,
			InstallContainer:  strings.TrimSpace(egg.Scripts.Installation.Container),
			InstallEntrypoint: strings.TrimSpace(egg.Scripts.Installation.Entrypoint),
			InstallScript:     egg.Scripts.Installation.Script,
			Variables:         variables,
			NetworkPorts:      networkPorts,
			CommandTransport:  commandTransport,
			BackupDefaults:    egg.Dockside.BackupDefaults,
			ResourceDefaults:  egg.Dockside.ResourceDefaults,
			FileDenylist:      normalizeStringList(egg.FileDenylist),
			Features:          egg.Features,
		},
		CompatibilityReport: CompatibilityReport{
			Compatible: true,
			Warnings:   warnings,
		},
	}
	entry.Slug = slug(sourceKind + "-" + category + "-" + egg.Name)
	return entry, nil
}

func normalizeCommandTransport(input CommandTransport) (CommandTransport, error) {
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	if input.Type == "" {
		input.Type = "auto"
	}
	switch input.Type {
	case "auto", "stdin", "disabled":
		input.REST = nil
	case "rcon":
		input.RCONPortEnv = strings.ToUpper(strings.TrimSpace(input.RCONPortEnv))
		input.RCONPasswordEnv = strings.ToUpper(strings.TrimSpace(input.RCONPasswordEnv))
		if input.RCONPortEnv == "" {
			input.RCONPortEnv = "RCON_PORT"
		}
		if input.RCONPasswordEnv == "" {
			input.RCONPasswordEnv = "ADMIN_PASSWORD"
		}
		if !networkEnvironmentName.MatchString(input.RCONPortEnv) ||
			!networkEnvironmentName.MatchString(input.RCONPasswordEnv) {
			return CommandTransport{}, errors.New("RCON transport environment names are invalid")
		}
		input.REST = nil
	case "http_rest":
		if input.REST == nil {
			return CommandTransport{}, errors.New("HTTP REST transport requires a rest definition")
		}
		rest := input.REST
		rest.PortEnvironment = strings.ToUpper(strings.TrimSpace(rest.PortEnvironment))
		if rest.Port == 0 && rest.PortEnvironment == "" {
			return CommandTransport{}, errors.New("HTTP REST transport requires a port or port environment")
		}
		if rest.Port < 0 || rest.Port > 65535 ||
			(rest.PortEnvironment != "" && !networkEnvironmentName.MatchString(rest.PortEnvironment)) {
			return CommandTransport{}, errors.New("HTTP REST transport port is invalid")
		}
		if rest.TimeoutSeconds == 0 {
			rest.TimeoutSeconds = 10
		}
		if rest.TimeoutSeconds < 1 || rest.TimeoutSeconds > 60 {
			return CommandTransport{}, errors.New("HTTP REST transport timeout must be 1-60 seconds")
		}
		if err := normalizeRESTHeaders(rest.Headers); err != nil {
			return CommandTransport{}, err
		}
		if err := validateRESTStatuses(rest.AcceptedStatus); err != nil {
			return CommandTransport{}, err
		}
		if rest.BasicAuth != nil {
			rest.BasicAuth.Username = strings.TrimSpace(rest.BasicAuth.Username)
			rest.BasicAuth.PasswordEnvironment = strings.ToUpper(
				strings.TrimSpace(rest.BasicAuth.PasswordEnvironment),
			)
			if rest.BasicAuth.Username == "" || len(rest.BasicAuth.Username) > 256 ||
				strings.ContainsAny(rest.BasicAuth.Username, "\x00\r\n:") ||
				!networkEnvironmentName.MatchString(rest.BasicAuth.PasswordEnvironment) {
				return CommandTransport{}, errors.New("HTTP REST basic authentication is invalid")
			}
		}
		if len(rest.Routes) == 0 {
			if err := normalizeRESTRequest(
				&rest.Method, &rest.Path, rest.BodyTemplate, rest.Headers, rest.AcceptedStatus,
			); err != nil {
				return CommandTransport{}, err
			}
		} else {
			if len(rest.Routes) > 64 {
				return CommandTransport{}, errors.New("HTTP REST transport may declare at most 64 command routes")
			}
			rest.Method = ""
			rest.Path = ""
			rest.BodyTemplate = ""
			seen := make(map[string]struct{}, len(rest.Routes))
			for index := range rest.Routes {
				route := &rest.Routes[index]
				route.Command = strings.ToLower(strings.TrimSpace(route.Command))
				route.Usage = strings.TrimSpace(route.Usage)
				if !restCommandName.MatchString(route.Command) || len(route.Usage) > 256 ||
					route.MinArgs < 0 || route.MinArgs > 32 {
					return CommandTransport{}, errors.New("HTTP REST transport contains an invalid command route")
				}
				names := append([]string{route.Command}, route.Aliases...)
				route.Aliases = route.Aliases[:0]
				for nameIndex, name := range names {
					name = strings.ToLower(strings.TrimSpace(name))
					if !restCommandName.MatchString(name) {
						return CommandTransport{}, errors.New("HTTP REST transport contains an invalid command alias")
					}
					if _, exists := seen[name]; exists {
						return CommandTransport{}, fmt.Errorf("HTTP REST command name %q is duplicated", name)
					}
					seen[name] = struct{}{}
					if nameIndex > 0 {
						route.Aliases = append(route.Aliases, name)
					}
				}
				if err := normalizeRESTHeaders(route.Headers); err != nil {
					return CommandTransport{}, err
				}
				if err := normalizeRESTRequest(
					&route.Method, &route.Path, route.BodyTemplate, route.Headers, route.AcceptedStatus,
				); err != nil {
					return CommandTransport{}, fmt.Errorf("HTTP REST command %q: %w", route.Command, err)
				}
			}
		}
	default:
		return CommandTransport{}, errors.New("command transport must be auto, stdin, rcon, http_rest, or disabled")
	}
	return input, nil
}

func normalizeRESTRequest(
	method, path *string,
	body string,
	headers map[string]string,
	statuses []int,
) error {
	*method = strings.ToUpper(strings.TrimSpace(*method))
	if *method == "" {
		*method = "POST"
	}
	switch *method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return errors.New("method must be GET, POST, PUT, PATCH, or DELETE")
	}
	*path = strings.TrimSpace(*path)
	if *path == "" {
		*path = "/"
	}
	if !strings.HasPrefix(*path, "/") || strings.ContainsAny(*path, "\r\n") ||
		strings.Contains(*path, "://") || len(*path) > 2048 {
		return errors.New("path must be a local absolute path")
	}
	if len(body) > 65536 || len(headers) > 24 {
		return errors.New("request definition is too large")
	}
	return validateRESTStatuses(statuses)
}

func normalizeRESTHeaders(headers map[string]string) error {
	for name, value := range headers {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n:") ||
			strings.ContainsAny(value, "\r\n") || len(name) > 80 || len(value) > 4096 {
			return errors.New("HTTP REST transport contains an invalid header")
		}
	}
	return nil
}

func validateRESTStatuses(statuses []int) error {
	for _, status := range statuses {
		if status < 100 || status > 599 {
			return errors.New("HTTP REST transport contains an invalid accepted status")
		}
	}
	return nil
}

func validateBackupDefaults(input BackupDefaults) error {
	if len(input.IncludePaths) > 100 || len(input.ExcludeGlobs) > 100 {
		return errors.New("backup defaults may contain at most 100 include and exclude entries")
	}
	values := append(append([]string{}, input.IncludePaths...), input.ExcludeGlobs...)
	for _, value := range values {
		if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) || len(value) > 1024 {
			return errors.New("backup defaults contain an invalid path or glob")
		}
	}
	if input.RetentionDays != nil && (*input.RetentionDays < 1 || *input.RetentionDays > 3650) {
		return errors.New("backup default retention must be 1-3650 days")
	}
	return nil
}

func validateResourceDefaults(input ResourceDefaults) error {
	if input.CPULimitMillicores != nil &&
		(*input.CPULimitMillicores < 100 || *input.CPULimitMillicores > 128000) {
		return errors.New("default CPU limit must be 100-128000 millicores")
	}
	if input.MemoryLimitMB != nil && (*input.MemoryLimitMB < 64 || *input.MemoryLimitMB > 1048576) {
		return errors.New("default memory limit must be 64-1048576 MB")
	}
	if input.DiskAlertLimitMB != nil &&
		(*input.DiskAlertLimitMB < 64 || *input.DiskAlertLimitMB > 16777216) {
		return errors.New("default disk alert limit must be 64-16777216 MB")
	}
	return nil
}

func normalizeExplicitNetworkPorts(input []NetworkPort) ([]NetworkPort, error) {
	if len(input) > 32 {
		return nil, errors.New("a template may declare at most 32 network ports")
	}
	result := make([]NetworkPort, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	primaryCount := 0
	for _, port := range input {
		port.Name = strings.TrimSpace(port.Name)
		port.Purpose = strings.TrimSpace(port.Purpose)
		port.Protocol = strings.ToLower(strings.TrimSpace(port.Protocol))
		port.Environment = strings.ToUpper(strings.TrimSpace(port.Environment))
		if port.Name == "" || port.ContainerPort < 1 || port.ContainerPort > 65535 ||
			(port.Protocol != "tcp" && port.Protocol != "udp") ||
			len(port.Purpose) > 120 || len(port.Environment) > 80 ||
			(port.Environment != "" && !networkEnvironmentName.MatchString(port.Environment)) {
			return nil, errors.New("Dockside network ports need a name, valid port, and TCP or UDP protocol")
		}
		key := fmt.Sprintf("%d/%s", port.ContainerPort, port.Protocol)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate Dockside network port %s", key)
		}
		seen[key] = struct{}{}
		if port.Primary {
			if port.InternalOnly {
				return nil, errors.New("the primary Dockside network port cannot be internal-only")
			}
			primaryCount++
			port.Required = true
			port.Published = true
		}
		if port.InternalOnly && (port.Required || port.Published) {
			return nil, fmt.Errorf(
				"Dockside network port %q cannot be internal-only and published or required",
				port.Name,
			)
		}
		if port.Required {
			port.Published = true
		}
		result = append(result, port)
	}
	if primaryCount != 1 {
		return nil, errors.New("Dockside network ports must declare exactly one primary port")
	}
	return result, nil
}

func inferNetworkPorts(startup string, variables []Variable) []NetworkPort {
	ports := make([]NetworkPort, 0, 4)
	seen := make(map[string]struct{})
	add := func(port NetworkPort) {
		key := strings.ToUpper(port.Environment)
		if key == "" {
			key = fmt.Sprintf("%s:%d:%s", port.Name, port.ContainerPort, port.Protocol)
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		ports = append(ports, port)
	}

	for _, variable := range variables {
		environment := strings.ToUpper(strings.TrimSpace(variable.Environment))
		if !strings.Contains(environment, "PORT") {
			continue
		}
		defaultPort, err := strconv.Atoi(strings.TrimSpace(variable.DefaultValue))
		if err != nil || defaultPort < 1 || defaultPort > 65535 {
			continue
		}
		if !templateReferencesVariable(startup, environment) &&
			environment != "SERVER_PORT" && environment != "GAME_PORT" {
			continue
		}
		protocol := inferPortProtocol(environment, variable.Name)
		primary := environment == "SERVER_PORT" || environment == "GAME_PORT" || environment == "PORT"
		internal := strings.Contains(environment, "RCON") ||
			strings.Contains(environment, "TELNET") ||
			strings.Contains(environment, "ADMIN")
		add(NetworkPort{
			Name:          portDisplayName(environment, variable.Name),
			Purpose:       portPurpose(environment),
			ContainerPort: defaultPort,
			Protocol:      protocol,
			Primary:       primary,
			Required:      primary,
			Published:     !internal,
			Environment:   environment,
		})
	}

	hasPrimary := false
	for _, port := range ports {
		hasPrimary = hasPrimary || port.Primary
	}
	if !hasPrimary {
		add(NetworkPort{
			Name: "Game", Purpose: "Primary game traffic", Primary: true,
			Required: true, Published: true, Environment: "SERVER_PORT",
		})
	}
	sort.SliceStable(ports, func(left, right int) bool {
		if ports[left].Primary != ports[right].Primary {
			return ports[left].Primary
		}
		return ports[left].Name < ports[right].Name
	})
	return ports
}

func templateReferencesVariable(startup, environment string) bool {
	for _, marker := range []string{
		"{{" + environment + "}}", "${" + environment + "}", "$" + environment,
		"{{server.build.env." + environment + "}}",
	} {
		if strings.Contains(startup, marker) {
			return true
		}
	}
	return false
}

func inferPortProtocol(environment, name string) string {
	identity := strings.ToUpper(environment + " " + name)
	for _, fragment := range []string{"RCON", "TELNET", "HTTP", "HTTPS", "WEB", "REST", "TCP"} {
		if strings.Contains(identity, fragment) {
			return "tcp"
		}
	}
	for _, fragment := range []string{"QUERY", "BEACON", "UDP"} {
		if strings.Contains(identity, fragment) {
			return "udp"
		}
	}
	// Egg formats do not declare a protocol. Leave ambiguous ports unanswered so
	// provisioning requires an explicit choice instead of silently publishing the
	// wrong TCP/UDP transport.
	return ""
}

func portDisplayName(environment, name string) string {
	if value := strings.TrimSpace(name); value != "" {
		return value
	}
	value := strings.TrimSuffix(strings.ReplaceAll(environment, "_", " "), " PORT")
	return strings.Title(strings.ToLower(value))
}

func portPurpose(environment string) string {
	switch {
	case strings.Contains(environment, "RCON"):
		return "Remote console"
	case strings.Contains(environment, "QUERY"):
		return "Server query"
	case strings.Contains(environment, "TELNET"):
		return "Telnet administration"
	default:
		return "Additional game traffic"
	}
}

func versionedSourceDigest(sourceDigest string) string {
	digest := sha256.Sum256([]byte(sourceDigest + ":canonical-normalization:" + canonicalNormalizationVersion))
	return hex.EncodeToString(digest[:])
}

func docksideServerNameDefault(environment, name, value string) (string, bool) {
	identifier := strings.ToUpper(strings.TrimSpace(environment) + " " + strings.TrimSpace(name))
	isServerName := strings.Contains(identifier, "SERVER_NAME") ||
		strings.Contains(identifier, "SERVERNAME") ||
		strings.Contains(identifier, "SESSION_NAME") ||
		strings.Contains(identifier, "SESSIONNAME") ||
		strings.Contains(identifier, "SERVER_TITLE")
	if !isServerName {
		return value, false
	}
	lower := strings.ToLower(strings.TrimSpace(value))
	if !strings.Contains(lower, "server") ||
		(!strings.Contains(lower, "pterodactyl") && !strings.Contains(lower, "pelican")) {
		return value, false
	}
	return "A DOCKSIDE.GG Panel Server", true
}

func normalizeImages(raw json.RawMessage) (map[string]string, error) {
	images := make(map[string]string)
	if len(raw) == 0 || string(raw) == "null" {
		return images, nil
	}
	if err := json.Unmarshal(raw, &images); err == nil {
		return images, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		for _, value := range values {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				images[trimmed] = trimmed
			}
		}
		return images, nil
	}
	var entries []struct {
		Name  string `json:"name"`
		Image string `json:"image"`
	}
	if err := json.Unmarshal(raw, &entries); err == nil {
		for _, entry := range entries {
			if strings.TrimSpace(entry.Image) != "" {
				name := strings.TrimSpace(entry.Name)
				if name == "" {
					name = entry.Image
				}
				images[name] = entry.Image
			}
		}
		return images, nil
	}
	return nil, errors.New("template Docker images use an unsupported format")
}

func normalizeRules(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	var values []string
	if json.Unmarshal(raw, &values) == nil {
		return strings.Join(values, "|")
	}
	if json.Valid(raw) {
		return string(raw)
	}
	return ""
}

func normalizeStringList(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var values []string
	if json.Unmarshal(raw, &values) == nil {
		return values
	}
	return nil
}

func rawString(value json.RawMessage) string {
	if len(value) == 0 {
		return ""
	}
	var result string
	if json.Unmarshal(value, &result) == nil {
		return strings.TrimSpace(result)
	}
	return ""
}

func looksSecret(environment, name string) bool {
	value := strings.ToUpper(environment + " " + name)
	for _, fragment := range []string{"PASSWORD", "TOKEN", "SECRET", "API_KEY", "APIKEY", "PRIVATE_KEY"} {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(nonSlug.ReplaceAllString(value, "-"), "-")
	if len(value) > 110 {
		value = strings.Trim(value[:110], "-")
	}
	if value == "" {
		return "template"
	}
	return value
}
