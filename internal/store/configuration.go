package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/dockside-gg/game-panel/internal/secure"
	"github.com/dockside-gg/game-panel/internal/templates"
	"github.com/jackc/pgx/v5"
)

type ServerConfigurationVariable struct {
	Name         string  `json:"name"`
	DisplayName  string  `json:"display_name"`
	Description  string  `json:"description"`
	DefaultValue string  `json:"default_value"`
	Value        *string `json:"value"`
	HasValue     bool    `json:"has_value"`
	Secret       bool    `json:"secret"`
	UserViewable bool    `json:"user_viewable"`
	UserEditable bool    `json:"user_editable"`
	Rules        string  `json:"rules"`
	FieldType    string  `json:"field_type"`
	Custom       bool    `json:"custom"`
}

type ServerConfiguration struct {
	ServerID         string                        `json:"server_id"`
	Version          int64                         `json:"version"`
	Status           string                        `json:"status"`
	Name             string                        `json:"name"`
	Description      string                        `json:"description"`
	Image            string                        `json:"image"`
	Images           map[string]string             `json:"images"`
	TemplateStartup  string                        `json:"template_startup"`
	StartupOverride  *string                       `json:"startup_override"`
	EffectiveStartup string                        `json:"effective_startup"`
	Variables        []ServerConfigurationVariable `json:"variables"`
	Ports            []ServerPort                  `json:"ports"`
	Resources        ServerResources               `json:"resources"`
	CommandTransport templates.CommandTransport    `json:"command_transport"`
	BackupDefaults   templates.BackupDefaults      `json:"backup_defaults"`

	TemplateNetworkPorts []templates.NetworkPort `json:"-"`
	environment          map[string]string
}

func (c ServerConfiguration) RuntimeRequest() engineclient.ReconfigureRequest {
	ports := make([]engineclient.Port, 0, len(c.Ports))
	for _, port := range c.Ports {
		ports = append(ports, engineclient.Port{
			HostIP:        port.BindAddress,
			HostPort:      port.HostPort,
			ContainerPort: port.ContainerPort,
			Protocol:      port.Protocol,
		})
	}
	return engineclient.ReconfigureRequest{
		Image:       c.Image,
		Startup:     c.EffectiveStartup,
		Environment: copyStringMap(c.environment),
		Ports:       ports,
		Resources: engineclient.Resources{
			CPUMillicores:          c.Resources.CPULimitMillicores,
			CPUSet:                 valueOr(c.Resources.CPUSet, ""),
			MemoryLimitBytes:       c.Resources.MemoryLimitBytes,
			MemoryReservationBytes: c.Resources.MemoryReservationBytes,
			SwapLimitBytes:         c.Resources.SwapLimitBytes,
			PidsLimit:              intToInt64(c.Resources.PidsLimit),
			IOWeight:               c.Resources.IOWeight,
		},
	}
}

func (c ServerConfiguration) InstallSpec(canonical templates.CanonicalTemplate) *engineclient.InstallSpec {
	if strings.TrimSpace(canonical.InstallContainer) == "" ||
		strings.TrimSpace(canonical.InstallScript) == "" {
		return nil
	}
	return &engineclient.InstallSpec{
		Image:       canonical.InstallContainer,
		Entrypoint:  canonical.InstallEntrypoint,
		Script:      canonical.InstallScript,
		Environment: copyStringMap(c.environment),
	}
}

func (c *ServerConfiguration) ApplyStartupCandidate(
	image string,
	override *string,
	changes map[string]string,
) {
	c.Image = image
	c.StartupOverride = override
	for name, value := range changes {
		c.environment[name] = value
		for index := range c.Variables {
			if c.Variables[index].Name != name {
				continue
			}
			c.Variables[index].HasValue = true
			if !c.Variables[index].Secret {
				copy := value
				c.Variables[index].Value = &copy
			}
		}
	}
	c.refreshEffectiveValues()
}

func (c *ServerConfiguration) ApplyPortsCandidate(ports []ServerPort) {
	c.Ports = ports
	c.refreshEffectiveValues()
}

func (c *ServerConfiguration) ApplySettingsCandidate(
	name, description string,
	resources ServerResources,
) {
	c.Name = name
	c.Description = description
	c.Resources = resources
	c.refreshEffectiveValues()
}

func (c *ServerConfiguration) refreshEffectiveValues() {
	primaryContainerPort := 0
	primaryHostPort := 0
	for _, port := range c.Ports {
		if port.Environment != "" {
			c.environment[port.Environment] = strconv.Itoa(port.ContainerPort)
			c.environment[port.Environment+"_PUBLIC"] = strconv.Itoa(port.HostPort)
		}
		if port.IsPrimary {
			primaryContainerPort = port.ContainerPort
			primaryHostPort = port.HostPort
		}
	}
	c.environment["SERVER_IP"] = "0.0.0.0"
	c.environment["SERVER_PORT"] = strconv.Itoa(primaryContainerPort)
	c.environment["SERVER_PUBLIC_PORT"] = strconv.Itoa(primaryHostPort)
	memoryMB := int64(0)
	if c.Resources.MemoryLimitBytes != nil {
		memoryMB = *c.Resources.MemoryLimitBytes / (1024 * 1024)
	}
	c.environment["SERVER_MEMORY"] = strconv.FormatInt(memoryMB, 10)
	startup := c.TemplateStartup
	if c.StartupOverride != nil {
		startup = *c.StartupOverride
	}
	c.EffectiveStartup = renderTemplate(startup, c.environment)
}

func (s *Store) ServerConfiguration(
	ctx context.Context,
	serverID string,
	box *secure.Box,
) (ServerConfiguration, error) {
	var (
		result            ServerConfiguration
		canonicalDocument []byte
	)
	err := s.pool.QueryRow(ctx, `
		SELECT
			s.id, s.version, s.status, s.name, s.description, s.image_reference,
			s.startup_override, tv.canonical_document,
			r.cpu_limit_millicores, r.cpu_set, r.memory_limit_bytes,
			r.memory_reservation_bytes, r.swap_limit_bytes, r.disk_limit_bytes,
			r.pids_limit, r.io_weight
		FROM servers AS s
		JOIN template_versions AS tv ON tv.id = s.template_version_id
		JOIN server_resources AS r ON r.server_id = s.id
		WHERE s.id = $1 AND s.deleted_at IS NULL
	`, serverID).Scan(
		&result.ServerID, &result.Version, &result.Status, &result.Name,
		&result.Description, &result.Image, &result.StartupOverride,
		&canonicalDocument,
		&result.Resources.CPULimitMillicores, &result.Resources.CPUSet,
		&result.Resources.MemoryLimitBytes, &result.Resources.MemoryReservationBytes,
		&result.Resources.SwapLimitBytes, &result.Resources.DiskLimitBytes,
		&result.Resources.PidsLimit, &result.Resources.IOWeight,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, fmt.Errorf("load server configuration: %w", err)
	}
	var canonical templates.CanonicalTemplate
	if err := json.Unmarshal(canonicalDocument, &canonical); err != nil {
		return result, fmt.Errorf("decode canonical server template: %w", err)
	}
	result.Images = canonical.Images
	if len(result.Images) == 0 {
		result.Images = map[string]string{"Current image": result.Image}
	}
	definitions := append([]templates.Variable(nil), canonical.Variables...)
	customVariables := make(map[string]bool)
	customRows, err := s.pool.Query(ctx, `
		SELECT environment, display_name, description, default_value,
		       user_viewable, user_editable, rules, field_type, secret
		FROM server_variable_definitions
		WHERE server_id = $1
		ORDER BY position, environment
	`, serverID)
	if err != nil {
		return result, err
	}
	for customRows.Next() {
		var definition templates.Variable
		if err := customRows.Scan(
			&definition.Environment, &definition.Name, &definition.Description,
			&definition.DefaultValue, &definition.UserViewable,
			&definition.UserEditable, &definition.Rules, &definition.FieldType,
			&definition.Secret,
		); err != nil {
			customRows.Close()
			return result, err
		}
		customVariables[definition.Environment] = true
		definitions = append(definitions, definition)
	}
	if err := customRows.Err(); err != nil {
		customRows.Close()
		return result, err
	}
	customRows.Close()
	result.Variables = make([]ServerConfigurationVariable, 0, len(definitions))
	result.TemplateStartup = canonical.StartupCommand
	result.environment = make(map[string]string, len(canonical.Variables)+3)
	result.CommandTransport = canonical.CommandTransport
	result.BackupDefaults = canonical.BackupDefaults
	result.TemplateNetworkPorts = append([]templates.NetworkPort(nil), canonical.NetworkPorts...)
	if encoded, err := json.Marshal(canonical.CommandTransport); err == nil {
		result.environment["DOCKSIDE_COMMAND_TRANSPORT"] = string(encoded)
	}

	stored := make(map[string]StoredVariable)
	rows, err := s.pool.Query(ctx, `
		SELECT name, value_text, value_encrypted, is_secret
		FROM server_variables
		WHERE server_id = $1
	`, serverID)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var value StoredVariable
		if err := rows.Scan(
			&value.Name, &value.ValueText, &value.ValueEncrypted, &value.IsSecret,
		); err != nil {
			rows.Close()
			return result, err
		}
		stored[value.Name] = value
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	for _, definition := range definitions {
		value := definition.DefaultValue
		hasValue := false
		if current, ok := stored[definition.Environment]; ok {
			hasValue = current.ValueText != nil || current.ValueEncrypted != nil
			switch {
			case current.IsSecret && current.ValueEncrypted != nil:
				decrypted, err := box.Open(
					*current.ValueEncrypted,
					[]byte(serverID+":"+definition.Environment),
				)
				if err != nil {
					return result, fmt.Errorf("decrypt variable %s: %w", definition.Environment, err)
				}
				value = decrypted
			case current.ValueText != nil:
				value = *current.ValueText
			}
		}
		result.environment[definition.Environment] = value
		var publicValue *string
		if !definition.Secret {
			copy := value
			publicValue = &copy
		}
		result.Variables = append(result.Variables, ServerConfigurationVariable{
			Name:         definition.Environment,
			DisplayName:  definition.Name,
			Description:  definition.Description,
			DefaultValue: definition.DefaultValue,
			Value:        publicValue,
			HasValue:     hasValue,
			Secret:       definition.Secret,
			UserViewable: definition.UserViewable,
			UserEditable: definition.UserEditable,
			Rules:        definition.Rules,
			FieldType:    definition.FieldType,
			Custom:       customVariables[definition.Environment],
		})
	}
	applyTemplatePortEnvironment(result.environment, canonical.NetworkPorts)
	result.Ports, err = s.ServerPorts(ctx, serverID)
	if err != nil {
		return result, err
	}
	result.refreshEffectiveValues()
	return result, nil
}

func applyTemplatePortEnvironment(
	environment map[string]string,
	definitions []templates.NetworkPort,
) {
	for _, port := range definitions {
		name := strings.ToUpper(strings.TrimSpace(port.Environment))
		if name == "" || port.ContainerPort < 1 || port.ContainerPort > 65535 {
			continue
		}
		environment[name] = strconv.Itoa(port.ContainerPort)
	}
}

func (s *Store) ServerPorts(ctx context.Context, serverID string) ([]ServerPort, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, host(bind_address), host_port, container_port, protocol,
		       COALESCE(purpose, ''), COALESCE(environment, ''), is_primary
		FROM server_ports
		WHERE server_id = $1
		ORDER BY is_primary DESC, host_port, protocol
	`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ServerPort, 0)
	for rows.Next() {
		var port ServerPort
		if err := rows.Scan(
			&port.ID, &port.BindAddress, &port.HostPort, &port.ContainerPort,
			&port.Protocol, &port.Purpose, &port.Environment, &port.IsPrimary,
		); err != nil {
			return nil, err
		}
		result = append(result, port)
	}
	return result, rows.Err()
}

func (s *Store) CommitStartupConfiguration(
	ctx context.Context,
	serverID, actorID string,
	expectedVersion int64,
	image string,
	override *string,
	changes map[string]string,
	secretNames map[string]bool,
	customDefinitions []ServerConfigurationVariable,
	containerID string,
	box *secure.Box,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE servers
		SET image_reference = $3, startup_override = $4, container_id = $5,
		    version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $2 AND status = 'stopped' AND deleted_at IS NULL
	`, serverID, expectedVersion, image, override, containerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM server_variable_definitions WHERE server_id = $1
	`, serverID); err != nil {
		return err
	}
	for position, definition := range customDefinitions {
		defaultValue := definition.DefaultValue
		if definition.Secret {
			defaultValue = ""
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO server_variable_definitions(
				server_id, environment, display_name, description, default_value,
				user_viewable, user_editable, rules, field_type, secret, position
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		`, serverID, definition.Name, definition.DisplayName, definition.Description,
			defaultValue, definition.UserViewable, definition.UserEditable,
			definition.Rules, definition.FieldType, definition.Secret, position); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(changes))
	for name := range changes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := changes[name]
		var textValue, encryptedValue *string
		if secretNames[name] {
			encrypted, err := box.Seal(value, []byte(serverID+":"+name))
			if err != nil {
				return err
			}
			encryptedValue = &encrypted
		} else {
			copy := value
			textValue = &copy
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO server_variables(
				server_id, name, value_text, value_encrypted, is_secret, updated_at
			)
			VALUES ($1, $2, $3, $4, $5, now())
			ON CONFLICT (server_id, name) DO UPDATE SET
				value_text = EXCLUDED.value_text,
				value_encrypted = EXCLUDED.value_encrypted,
				is_secret = EXCLUDED.is_secret,
				updated_at = now()
		`, serverID, name, textValue, encryptedValue, secretNames[name]); err != nil {
			return err
		}
	}
	return addConfigurationActivity(
		ctx, tx, serverID, actorID, "server.startup.updated",
		"Server startup configuration updated",
		map[string]any{"image": image, "variables_changed": names, "custom_startup": override != nil},
	)
}

func (s *Store) CommitServerPorts(
	ctx context.Context,
	serverID, actorID string,
	expectedVersion int64,
	ports []ServerPort,
	containerID string,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('dockside-game-port-allocation'))"); err != nil {
		return err
	}
	for _, port := range ports {
		var conflict bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM server_ports
				WHERE server_id <> $1 AND host_port = $2 AND protocol = $3
			)
		`, serverID, port.HostPort, port.Protocol).Scan(&conflict); err != nil {
			return err
		}
		if conflict {
			return fmt.Errorf(
				"host port %d/%s is already allocated: %w",
				port.HostPort, port.Protocol, ErrConflict,
			)
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE servers
		SET container_id = $3, version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $2 AND status = 'stopped' AND deleted_at IS NULL
	`, serverID, expectedVersion, containerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, "DELETE FROM server_ports WHERE server_id = $1", serverID); err != nil {
		return err
	}
	for _, port := range ports {
		id := port.ID
		if id == "" {
			id, err = identity.NewUUID()
			if err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO server_ports(
				id, server_id, bind_address, host_port, container_port,
				protocol, purpose, environment, is_primary
			)
			VALUES ($1, $2, $3::inet, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9)
		`, id, serverID, port.BindAddress, port.HostPort, port.ContainerPort,
			port.Protocol, port.Purpose, port.Environment, port.IsPrimary); err != nil {
			return err
		}
	}
	return addConfigurationActivity(
		ctx, tx, serverID, actorID, "server.network.updated",
		"Server network allocations updated",
		map[string]any{"port_count": len(ports)},
	)
}

func (s *Store) CommitServerSettings(
	ctx context.Context,
	serverID, actorID string,
	expectedVersion int64,
	name, description string,
	resources ServerResources,
	containerID string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE servers
		SET name = $3, description = $4, container_id = $5,
		    version = version + 1, updated_at = now()
		WHERE id = $1 AND version = $2 AND status = 'stopped' AND deleted_at IS NULL
	`, serverID, expectedVersion, name, description, containerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE server_resources SET
			cpu_limit_millicores = $2, cpu_set = $3, memory_limit_bytes = $4,
			memory_reservation_bytes = $5, swap_limit_bytes = $6,
			disk_limit_bytes = $7, pids_limit = $8, io_weight = $9
		WHERE server_id = $1
	`, serverID, resources.CPULimitMillicores, resources.CPUSet,
		resources.MemoryLimitBytes, resources.MemoryReservationBytes,
		resources.SwapLimitBytes, resources.DiskLimitBytes,
		resources.PidsLimit, resources.IOWeight); err != nil {
		return err
	}
	return addConfigurationActivity(
		ctx, tx, serverID, actorID, "server.settings.updated",
		"Server settings and resource limits updated",
		map[string]any{"name": name},
	)
}

func addConfigurationActivity(
	ctx context.Context,
	tx pgx.Tx,
	serverID, actorID, eventType, summary string,
	data map[string]any,
) error {
	eventID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(
			id, server_id, actor_user_id, event_type, summary, data
		) VALUES ($1, $2, $3, $4, $5, $6)
	`, eventID, serverID, actorID, eventType, summary, data); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func copyStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func NormalizeStartupOverride(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
