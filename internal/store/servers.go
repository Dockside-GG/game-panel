package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/dockside-gg/game-panel/internal/secure"
	"github.com/dockside-gg/game-panel/internal/templates"
	"github.com/jackc/pgx/v5"
)

type ServerPort struct {
	ID            string `json:"id"`
	BindAddress   string `json:"bind_address"`
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
	Purpose       string `json:"purpose"`
	Environment   string `json:"environment,omitempty"`
	IsPrimary     bool   `json:"is_primary"`
}

type ServerResources struct {
	CPULimitMillicores     *int    `json:"cpu_limit_millicores"`
	CPUSet                 *string `json:"cpu_set"`
	MemoryLimitBytes       *int64  `json:"memory_limit_bytes"`
	MemoryReservationBytes *int64  `json:"memory_reservation_bytes"`
	SwapLimitBytes         *int64  `json:"swap_limit_bytes"`
	DiskLimitBytes         *int64  `json:"disk_limit_bytes"`
	PidsLimit              *int    `json:"pids_limit"`
	IOWeight               *int    `json:"io_weight"`
}

type ServerRuntime struct {
	ObservedState   string     `json:"observed_state"`
	Health          string     `json:"health"`
	CPUPercent      *float64   `json:"cpu_percent"`
	MemoryBytes     *int64     `json:"memory_bytes"`
	MemoryLimit     *int64     `json:"memory_limit_bytes"`
	NetworkRXBytes  *int64     `json:"network_rx_bytes"`
	NetworkTXBytes  *int64     `json:"network_tx_bytes"`
	BlockReadBytes  *int64     `json:"block_read_bytes"`
	BlockWriteBytes *int64     `json:"block_write_bytes"`
	DiskBytes       *int64     `json:"disk_bytes"`
	StartedAt       *time.Time `json:"started_at"`
	ExitCode        *int       `json:"exit_code"`
	LastError       *string    `json:"last_error"`
	ObservedAt      time.Time  `json:"observed_at"`
}

type OperationLogEntry struct {
	Sequence   int64     `json:"sequence"`
	Phase      string    `json:"phase"`
	Stream     string    `json:"stream"`
	Message    string    `json:"message"`
	ObservedAt time.Time `json:"observed_at"`
}

type ServerSummary struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	Status            string          `json:"status"`
	DesiredState      string          `json:"desired_state"`
	StopReason        *string         `json:"stop_reason"`
	AutoRecovery      bool            `json:"auto_recovery_enabled"`
	RecoveryAttempts  int             `json:"recovery_attempts"`
	ContainerID       *string         `json:"container_id"`
	ImageReference    string          `json:"image_reference"`
	TemplateName      string          `json:"template_name"`
	TemplateSlug      string          `json:"template_slug"`
	TemplateVersionID string          `json:"template_version_id"`
	TemplateVersion   int             `json:"template_version"`
	PrimaryPort       *ServerPort     `json:"primary_port"`
	Resources         ServerResources `json:"resources"`
	Runtime           ServerRuntime   `json:"runtime"`
	Version           int64           `json:"version"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type StoredVariable struct {
	Name           string
	ValueText      *string
	ValueEncrypted *string
	IsSecret       bool
}

type CreateServerParams struct {
	TemplateVersionID string
	Name              string
	Description       string
	ImageReference    string
	Ports             []ServerPort
	Resources         ServerResources
	Variables         []StoredVariable
	CreatedBy         string
	Start             bool
	GamePortStart     int
	GamePortEnd       int
	EncryptionBox     *secure.Box
}

type CreateServerResult struct {
	ServerID    string       `json:"server_id"`
	OperationID string       `json:"operation_id"`
	HostPort    int          `json:"host_port"`
	Ports       []ServerPort `json:"ports"`
}

func (s *Store) CreateServer(ctx context.Context, input CreateServerParams) (CreateServerResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return CreateServerResult{}, fmt.Errorf("begin server creation: %w", err)
	}
	defer tx.Rollback(ctx)

	var installationID string
	if err := tx.QueryRow(ctx, "SELECT id FROM installations LIMIT 1").Scan(&installationID); err != nil {
		return CreateServerResult{}, fmt.Errorf("load installation: %w", err)
	}
	var templateExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM template_versions v
			JOIN templates t ON t.id = v.template_id
			WHERE v.id = $1 AND t.archived_at IS NULL
		)
	`, input.TemplateVersionID).Scan(&templateExists); err != nil {
		return CreateServerResult{}, err
	}
	if !templateExists {
		return CreateServerResult{}, ErrNotFound
	}
	var nameExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM servers
			WHERE installation_id = $1 AND lower(name) = lower($2) AND deleted_at IS NULL
		)
	`, installationID, input.Name).Scan(&nameExists); err != nil {
		return CreateServerResult{}, err
	}
	if nameExists {
		return CreateServerResult{}, ErrConflict
	}

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext('dockside-game-port-allocation'))"); err != nil {
		return CreateServerResult{}, fmt.Errorf("lock port allocation: %w", err)
	}
	allocatedPorts := make([]ServerPort, len(input.Ports))
	usedByProtocol := map[string][]int{"tcp": {}, "udp": {}}
	for index, requested := range input.Ports {
		preferred := requested.ContainerPort
		if preferred < input.GamePortStart || preferred > input.GamePortEnd {
			preferred = -1
		}
		var hostPort int
		err := tx.QueryRow(ctx, `
			SELECT candidate
			FROM generate_series($1::integer, $2::integer) AS candidate
			WHERE NOT EXISTS (
				SELECT 1 FROM server_ports
				WHERE host_port = candidate AND protocol = $3
			)
			  AND NOT (candidate = ANY($4::integer[]))
			ORDER BY CASE WHEN candidate = $5 THEN 0 ELSE 1 END, candidate
			LIMIT 1
		`, input.GamePortStart, input.GamePortEnd, requested.Protocol,
			usedByProtocol[requested.Protocol], preferred,
		).Scan(&hostPort)
		if errors.Is(err, pgx.ErrNoRows) {
			return CreateServerResult{}, fmt.Errorf("no available %s game ports: %w", requested.Protocol, ErrConflict)
		}
		if err != nil {
			return CreateServerResult{}, fmt.Errorf("allocate game port: %w", err)
		}
		requested.HostPort = hostPort
		requested.BindAddress = "0.0.0.0"
		allocatedPorts[index] = requested
		usedByProtocol[requested.Protocol] = append(usedByProtocol[requested.Protocol], hostPort)
	}

	serverID, err := identity.NewUUID()
	if err != nil {
		return CreateServerResult{}, err
	}
	desiredState := "stopped"
	if input.Start {
		desiredState = "running"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO servers(
			id, installation_id, template_version_id, name, description, status,
			desired_state, image_reference, created_by
		)
		VALUES ($1, $2, $3, $4, $5, 'installing', $6, $7, $8)
	`, serverID, installationID, input.TemplateVersionID, input.Name, input.Description,
		desiredState, input.ImageReference, input.CreatedBy,
	); err != nil {
		return CreateServerResult{}, fmt.Errorf("insert server: %w", err)
	}
	resources := input.Resources
	if _, err := tx.Exec(ctx, `
		INSERT INTO server_resources(
			server_id, cpu_limit_millicores, cpu_set, memory_limit_bytes,
			memory_reservation_bytes, swap_limit_bytes, disk_limit_bytes, pids_limit, io_weight
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, serverID, resources.CPULimitMillicores, resources.CPUSet,
		resources.MemoryLimitBytes, resources.MemoryReservationBytes,
		resources.SwapLimitBytes, resources.DiskLimitBytes,
		resources.PidsLimit, resources.IOWeight,
	); err != nil {
		return CreateServerResult{}, fmt.Errorf("insert server resources: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO server_runtime(server_id, observed_state, health)
		VALUES ($1, 'creating', 'unknown')
	`, serverID); err != nil {
		return CreateServerResult{}, fmt.Errorf("insert server runtime: %w", err)
	}
	primaryHostPort := 0
	for index := range allocatedPorts {
		portID, err := identity.NewUUID()
		if err != nil {
			return CreateServerResult{}, err
		}
		allocatedPorts[index].ID = portID
		port := allocatedPorts[index]
		if port.IsPrimary {
			primaryHostPort = port.HostPort
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO server_ports(
				id, server_id, bind_address, host_port, container_port, protocol,
				purpose, environment, is_primary
			)
			VALUES ($1, $2, '0.0.0.0', $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), $8)
		`, portID, serverID, port.HostPort, port.ContainerPort, port.Protocol,
			port.Purpose, port.Environment, port.IsPrimary); err != nil {
			return CreateServerResult{}, fmt.Errorf("insert server port: %w", err)
		}
	}
	for _, variable := range input.Variables {
		valueText := variable.ValueText
		valueEncrypted := variable.ValueEncrypted
		if variable.IsSecret {
			if input.EncryptionBox == nil || valueText == nil {
				return CreateServerResult{}, errors.New("secret variable encryption is not configured")
			}
			encrypted, err := input.EncryptionBox.Seal(*valueText, []byte(serverID+":"+variable.Name))
			if err != nil {
				return CreateServerResult{}, fmt.Errorf("encrypt server variable %s: %w", variable.Name, err)
			}
			valueText = nil
			valueEncrypted = &encrypted
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO server_variables(
				server_id, name, value_text, value_encrypted, is_secret
			)
			VALUES ($1, $2, $3, $4, $5)
		`, serverID, variable.Name, valueText, valueEncrypted, variable.IsSecret); err != nil {
			return CreateServerResult{}, fmt.Errorf("insert server variable %s: %w", variable.Name, err)
		}
	}
	operationID, err := identity.NewUUID()
	if err != nil {
		return CreateServerResult{}, err
	}
	idempotencyKey, err := identity.Token(24)
	if err != nil {
		return CreateServerResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO operations(id, server_id, actor_user_id, kind, idempotency_key, message)
		VALUES ($1, $2, $3, 'server.provision', $4, 'Waiting for provisioning worker')
	`, operationID, serverID, input.CreatedBy, idempotencyKey); err != nil {
		return CreateServerResult{}, fmt.Errorf("insert provision operation: %w", err)
	}
	eventID, err := identity.NewUUID()
	if err != nil {
		return CreateServerResult{}, err
	}
	payload, _ := json.Marshal(map[string]string{"server_id": serverID, "operation_id": operationID})
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events(id, topic, aggregate_id, payload)
		VALUES ($1, 'server.provision', $2, $3)
	`, eventID, serverID, payload); err != nil {
		return CreateServerResult{}, fmt.Errorf("enqueue server provisioning: %w", err)
	}
	activityID, err := identity.NewUUID()
	if err != nil {
		return CreateServerResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, actor_user_id, event_type, summary, data)
		VALUES ($1, $2, $3, 'server.provision.requested', 'Server provisioning requested', $4)
	`, activityID, serverID, input.CreatedBy, payload); err != nil {
		return CreateServerResult{}, fmt.Errorf("insert server activity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateServerResult{}, fmt.Errorf("commit server creation: %w", err)
	}
	return CreateServerResult{
		ServerID: serverID, OperationID: operationID,
		HostPort: primaryHostPort, Ports: allocatedPorts,
	}, nil
}

func (s *Store) ListServers(
	ctx context.Context,
	userID, panelRole string,
) ([]ServerSummary, error) {
	privileged := panelRole == "owner" || panelRole == "administrator"
	rows, err := s.pool.Query(ctx, serverSelect+`
		WHERE s.deleted_at IS NULL
		  AND (
			$2::boolean
			OR EXISTS(
				SELECT 1
				FROM role_bindings AS binding
				JOIN role_permissions AS granted ON granted.role_id = binding.role_id
				WHERE binding.user_id = $1
				  AND binding.server_id = s.id
				  AND granted.permission_name = 'server.view'
			)
		  )
		ORDER BY s.created_at DESC
	`, userID, privileged)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	defer rows.Close()
	items := make([]ServerSummary, 0)
	for rows.Next() {
		item, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ServerByID(ctx context.Context, serverID string) (ServerSummary, error) {
	row := s.pool.QueryRow(ctx, serverSelect+` WHERE s.id = $1 AND s.deleted_at IS NULL`, serverID)
	item, err := scanServer(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	return item, err
}

const serverSelect = `
	SELECT
		s.id, s.name, s.description, s.status, s.desired_state, s.stop_reason,
		s.auto_recovery_enabled, s.recovery_attempts, s.container_id,
		s.image_reference, t.name, t.slug, tv.id, tv.version,
		p.id, host(p.bind_address), p.host_port, p.container_port, p.protocol,
		COALESCE(p.purpose, ''), COALESCE(p.environment, ''), p.is_primary,
		r.cpu_limit_millicores, r.cpu_set, r.memory_limit_bytes,
		r.memory_reservation_bytes, r.swap_limit_bytes, r.disk_limit_bytes,
		r.pids_limit, r.io_weight,
		rt.observed_state, rt.health, rt.cpu_percent, rt.memory_bytes,
		rt.memory_limit_bytes, rt.network_rx_bytes, rt.network_tx_bytes,
		rt.block_read_bytes, rt.block_write_bytes, rt.disk_bytes,
		rt.started_at, rt.exit_code, rt.last_error, rt.observed_at,
		s.version, s.created_at, s.updated_at
	FROM servers s
	JOIN template_versions tv ON tv.id = s.template_version_id
	JOIN templates t ON t.id = tv.template_id
	JOIN server_resources r ON r.server_id = s.id
	JOIN server_runtime rt ON rt.server_id = s.id
	LEFT JOIN LATERAL (
		SELECT * FROM server_ports
		WHERE server_id = s.id AND is_primary
		LIMIT 1
	) p ON true
`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanServer(row rowScanner) (ServerSummary, error) {
	var item ServerSummary
	var port ServerPort
	var portID *string
	var bindAddress *string
	var hostPort, containerPort *int
	var protocol, purpose, environment *string
	var primary *bool
	err := row.Scan(
		&item.ID, &item.Name, &item.Description, &item.Status, &item.DesiredState,
		&item.StopReason, &item.AutoRecovery, &item.RecoveryAttempts,
		&item.ContainerID, &item.ImageReference, &item.TemplateName,
		&item.TemplateSlug, &item.TemplateVersionID, &item.TemplateVersion,
		&portID, &bindAddress, &hostPort, &containerPort, &protocol, &purpose,
		&environment, &primary,
		&item.Resources.CPULimitMillicores, &item.Resources.CPUSet,
		&item.Resources.MemoryLimitBytes, &item.Resources.MemoryReservationBytes,
		&item.Resources.SwapLimitBytes, &item.Resources.DiskLimitBytes,
		&item.Resources.PidsLimit, &item.Resources.IOWeight,
		&item.Runtime.ObservedState, &item.Runtime.Health, &item.Runtime.CPUPercent,
		&item.Runtime.MemoryBytes, &item.Runtime.MemoryLimit,
		&item.Runtime.NetworkRXBytes, &item.Runtime.NetworkTXBytes,
		&item.Runtime.BlockReadBytes, &item.Runtime.BlockWriteBytes,
		&item.Runtime.DiskBytes, &item.Runtime.StartedAt, &item.Runtime.ExitCode,
		&item.Runtime.LastError, &item.Runtime.ObservedAt,
		&item.Version, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return item, err
	}
	if portID != nil {
		port.ID = *portID
		port.BindAddress = valueOr(bindAddress, "0.0.0.0")
		port.HostPort = intOr(hostPort, 0)
		port.ContainerPort = intOr(containerPort, 0)
		port.Protocol = valueOr(protocol, "")
		port.Purpose = valueOr(purpose, "")
		port.Environment = valueOr(environment, "")
		port.IsPrimary = primary != nil && *primary
		item.PrimaryPort = &port
	}
	return item, nil
}

func valueOr(value *string, fallback string) string {
	if value == nil {
		return fallback
	}
	return *value
}

func intOr(value *int, fallback int) int {
	if value == nil {
		return fallback
	}
	return *value
}

type ProvisionJob struct {
	ServerID    string
	OperationID string
	Request     engineclient.ProvisionRequest
}

func (s *Store) ProvisionJob(ctx context.Context, serverID, operationID string, box *secure.Box) (ProvisionJob, error) {
	var (
		canonicalDocument []byte
		imageReference    string
		desiredState      string
		resources         ServerResources
	)
	err := s.pool.QueryRow(ctx, `
		SELECT
			tv.canonical_document, s.image_reference, s.desired_state,
			r.cpu_limit_millicores, r.cpu_set, r.memory_limit_bytes,
			r.memory_reservation_bytes, r.swap_limit_bytes, r.pids_limit, r.io_weight
		FROM servers s
		JOIN template_versions tv ON tv.id = s.template_version_id
		JOIN server_resources r ON r.server_id = s.id
		WHERE s.id = $1 AND s.deleted_at IS NULL AND s.status = 'installing'
	`, serverID).Scan(
		&canonicalDocument, &imageReference, &desiredState,
		&resources.CPULimitMillicores, &resources.CPUSet,
		&resources.MemoryLimitBytes, &resources.MemoryReservationBytes,
		&resources.SwapLimitBytes, &resources.PidsLimit, &resources.IOWeight,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProvisionJob{}, ErrNotFound
	}
	if err != nil {
		return ProvisionJob{}, fmt.Errorf("load provision job: %w", err)
	}
	var canonical templates.CanonicalTemplate
	if err := json.Unmarshal(canonicalDocument, &canonical); err != nil {
		return ProvisionJob{}, fmt.Errorf("decode provision template: %w", err)
	}
	ports, err := s.ServerPorts(ctx, serverID)
	if err != nil || len(ports) == 0 {
		if err == nil {
			err = ErrNotFound
		}
		return ProvisionJob{}, fmt.Errorf("load provision ports: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT name, value_text, value_encrypted, is_secret
		FROM server_variables
		WHERE server_id = $1
		ORDER BY name
	`, serverID)
	if err != nil {
		return ProvisionJob{}, err
	}
	defer rows.Close()
	environment := make(map[string]string)
	for rows.Next() {
		var name string
		var textValue, encryptedValue *string
		var secret bool
		if err := rows.Scan(&name, &textValue, &encryptedValue, &secret); err != nil {
			return ProvisionJob{}, err
		}
		if secret {
			if encryptedValue == nil {
				return ProvisionJob{}, fmt.Errorf("secret variable %s has no ciphertext", name)
			}
			value, err := box.Open(*encryptedValue, []byte(serverID+":"+name))
			if err != nil {
				return ProvisionJob{}, fmt.Errorf("decrypt variable %s: %w", name, err)
			}
			environment[name] = value
		} else if textValue != nil {
			environment[name] = *textValue
		}
	}
	if err := rows.Err(); err != nil {
		return ProvisionJob{}, err
	}
	environment["SERVER_IP"] = "0.0.0.0"
	applyTemplatePortEnvironment(environment, canonical.NetworkPorts)
	var primary ServerPort
	enginePorts := make([]engineclient.Port, 0, len(ports))
	for _, port := range ports {
		if port.IsPrimary {
			primary = port
		}
		if port.Environment != "" {
			environment[port.Environment] = strconv.Itoa(port.ContainerPort)
			environment[port.Environment+"_PUBLIC"] = strconv.Itoa(port.HostPort)
		}
		enginePorts = append(enginePorts, engineclient.Port{
			HostIP: port.BindAddress, HostPort: port.HostPort,
			ContainerPort: port.ContainerPort, Protocol: port.Protocol,
		})
	}
	environment["SERVER_PORT"] = strconv.Itoa(primary.ContainerPort)
	environment["SERVER_PUBLIC_PORT"] = strconv.Itoa(primary.HostPort)
	memoryMB := int64(0)
	if resources.MemoryLimitBytes != nil {
		memoryMB = *resources.MemoryLimitBytes / (1024 * 1024)
	}
	environment["SERVER_MEMORY"] = strconv.FormatInt(memoryMB, 10)
	if commandTransport, err := json.Marshal(canonical.CommandTransport); err == nil {
		environment["DOCKSIDE_COMMAND_TRANSPORT"] = string(commandTransport)
	}
	startup := renderTemplate(canonical.StartupCommand, environment)

	var install *engineclient.InstallSpec
	if canonical.InstallContainer != "" && canonical.InstallScript != "" {
		install = &engineclient.InstallSpec{
			Image: canonical.InstallContainer, Entrypoint: canonical.InstallEntrypoint,
			Script: canonical.InstallScript, Environment: environment,
		}
	}
	request := engineclient.ProvisionRequest{
		Image: imageReference, Startup: startup, Environment: environment,
		Ports: enginePorts,
		Resources: engineclient.Resources{
			CPUMillicores:          resources.CPULimitMillicores,
			CPUSet:                 valueOr(resources.CPUSet, ""),
			MemoryLimitBytes:       resources.MemoryLimitBytes,
			MemoryReservationBytes: resources.MemoryReservationBytes,
			SwapLimitBytes:         resources.SwapLimitBytes,
			PidsLimit:              intToInt64(resources.PidsLimit),
			IOWeight:               resources.IOWeight,
		},
		Install: install,
		Start:   desiredState == "running",
	}
	return ProvisionJob{ServerID: serverID, OperationID: operationID, Request: request}, nil
}

func renderTemplate(value string, variables map[string]string) string {
	for name, replacement := range variables {
		value = strings.ReplaceAll(value, "{{"+name+"}}", replacement)
		value = strings.ReplaceAll(value, "{{server.build.env."+name+"}}", replacement)
	}
	value = strings.ReplaceAll(value, "{{server.allocations.default.port}}", variables["SERVER_PORT"])
	value = strings.ReplaceAll(value, "{{server.allocations.default.ip}}", variables["SERVER_IP"])
	return value
}

func intToInt64(value *int) *int64 {
	if value == nil {
		return nil
	}
	converted := int64(*value)
	return &converted
}

func (s *Store) MarkProvisionRunning(ctx context.Context, serverID, operationID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE operations
		SET status = 'running', started_at = COALESCE(started_at, now()),
		    progress = 10, message = 'Provisioning container resources'
		WHERE id = $1 AND server_id = $2
	`, operationID, serverID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE server_runtime
		SET observed_state = 'creating', observed_at = now()
		WHERE server_id = $1
	`, serverID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AppendOperationLog(
	ctx context.Context,
	operationID, serverID, phase, stream, message string,
	observedAt time.Time,
) error {
	if stream != "system" && stream != "stdout" && stream != "stderr" {
		stream = "system"
	}
	if strings.TrimSpace(phase) == "" {
		phase = "provision"
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	if len(message) > 8000 {
		message = message[:8000]
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO operation_log_entries(
			operation_id, server_id, phase, stream, message, observed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, operationID, serverID, phase, stream, message, observedAt)
	return err
}

func (s *Store) ServerOperationLogs(
	ctx context.Context,
	serverID string,
	limit int,
) ([]OperationLogEntry, error) {
	if limit < 20 || limit > 5000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT sequence, phase, stream, message, observed_at
		FROM (
			SELECT sequence, phase, stream, message, observed_at
			FROM operation_log_entries
			WHERE server_id = $1
			ORDER BY sequence DESC
			LIMIT $2
		) recent
		ORDER BY sequence
	`, serverID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]OperationLogEntry, 0, limit)
	for rows.Next() {
		var entry OperationLogEntry
		if err := rows.Scan(
			&entry.Sequence, &entry.Phase, &entry.Stream,
			&entry.Message, &entry.ObservedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func (s *Store) MarkProvisionSucceeded(ctx context.Context, job ProvisionJob, result engineclient.ProvisionResult) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	status := "stopped"
	if result.State == "running" {
		status = "running"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE servers
		SET status = $2, container_id = $3, updated_at = now(), version = version + 1
		WHERE id = $1
	`, job.ServerID, status, result.ContainerID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE server_runtime
		SET observed_state = $2, health = 'unknown',
		    started_at = CASE WHEN $2 = 'running' THEN now() ELSE NULL END,
		    last_error = NULL, observed_at = now()
		WHERE server_id = $1
	`, job.ServerID, status); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE operations
		SET status = 'succeeded', progress = 100, message = 'Provisioning completed',
		    completed_at = now()
		WHERE id = $1
	`, job.OperationID); err != nil {
		return err
	}
	eventID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, event_type, summary, data)
		VALUES ($1, $2, 'server.provision.succeeded', 'Server provisioning completed', $3)
	`, eventID, job.ServerID, map[string]any{
		"container_id": result.ContainerID,
		"volume_name":  result.VolumeName,
		"network_name": result.NetworkName,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) MarkProvisionFailed(ctx context.Context, job ProvisionJob, provisionErr error) error {
	detail := provisionErr.Error()
	if len(detail) > 4000 {
		detail = detail[:4000]
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE servers
		SET status = 'failed', updated_at = now(), version = version + 1
		WHERE id = $1
	`, job.ServerID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE server_runtime
		SET observed_state = 'failed', health = 'unhealthy',
		    last_error = $2, observed_at = now()
		WHERE server_id = $1
	`, job.ServerID, detail); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE operations
		SET status = 'failed', error_code = 'provision_failed',
		    error_detail = $1, message = 'Provisioning failed', completed_at = now()
		WHERE id = $2
	`, detail, job.OperationID); err != nil {
		return err
	}
	eventID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, event_type, severity, summary, data)
		VALUES ($1, $2, 'server.provision.failed', 'error', 'Server provisioning failed', $3)
	`, eventID, job.ServerID, map[string]any{"error": detail}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RequestPower(ctx context.Context, serverID, actorID, action string) (string, error) {
	return s.requestPower(ctx, serverID, actorID, action, true)
}

func (s *Store) RequestImmediatePower(
	ctx context.Context,
	serverID, actorID, action string,
) (string, error) {
	return s.requestPower(ctx, serverID, actorID, action, false)
}

func (s *Store) requestPower(
	ctx context.Context,
	serverID, actorID, action string,
	enqueue bool,
) (string, error) {
	transition := map[string]string{
		"start": "starting", "stop": "stopping", "restart": "restarting", "kill": "stopping",
	}[action]
	desired := map[string]string{
		"start": "running", "stop": "stopped", "restart": "running", "kill": "stopped",
	}[action]
	if transition == "" {
		return "", ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var current string
	if err := tx.QueryRow(ctx, `
		SELECT status
		FROM servers
		WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE
	`, serverID).Scan(&current); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", err
	}
	if current == "installing" || current == "deleting" || current == "suspended" {
		return "", ErrConflict
	}
	if action != "kill" &&
		(current == "starting" || current == "restarting" || current == "stopping") {
		return "", ErrConflict
	}
	if (action == "start" && current == "running") ||
		((action == "stop" || action == "kill") && current == "stopped") {
		return "", ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE servers
		SET status = $2, desired_state = $3,
		    stop_reason = CASE WHEN $3 = 'stopped' THEN 'requested' ELSE NULL END,
		    updated_at = now(), version = version + 1
		WHERE id = $1 AND deleted_at IS NULL
	`, serverID, transition, desired); err != nil {
		return "", err
	}
	operationID, err := identity.NewUUID()
	if err != nil {
		return "", err
	}
	idempotencyKey, err := identity.Token(24)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO operations(
			id, server_id, actor_user_id, kind, status, idempotency_key,
			message, started_at
		)
		VALUES (
			$1, $2, NULLIF($3, '')::uuid, 'server.power.' || $4, 'running', $5,
			'Server ' || $4 || ' in progress', now()
		)
	`, operationID, serverID, actorID, action, idempotencyKey); err != nil {
		return "", err
	}
	activityID, err := identity.NewUUID()
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, actor_user_id, event_type, summary, data)
		VALUES ($1, $2, NULLIF($3, '')::uuid, 'server.power.' || $4, 'Server ' || $4 || ' requested', $5)
	`, activityID, serverID, actorID, action, map[string]string{
		"action": action, "operation_id": operationID,
	}); err != nil {
		return "", err
	}
	if enqueue {
		payload, _ := json.Marshal(map[string]string{
			"server_id": serverID, "operation_id": operationID, "action": action,
		})
		outboxID, err := identity.NewUUID()
		if err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_events(id, topic, aggregate_id, payload)
			VALUES ($1, 'server.power', $2, $3)
		`, outboxID, serverID, payload); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return operationID, nil
}

func (s *Store) FinishPower(
	ctx context.Context,
	serverID, operationID, action string,
	powerErr error,
) error {
	status := map[string]string{
		"start": "running", "restart": "running", "stop": "stopped", "kill": "stopped",
	}[action]
	expectedDesired := map[string]string{
		"start": "running", "restart": "running", "stop": "stopped", "kill": "stopped",
	}[action]
	if status == "" {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if powerErr != nil {
		detail := truncateStoreError(powerErr.Error(), 2000)
		fallback := "failed"
		if action == "stop" || action == "kill" {
			fallback = "running"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE servers
			SET status = $2,
			    desired_state = CASE WHEN $2 = 'running' THEN 'running' ELSE desired_state END,
			    stop_reason = CASE WHEN $2 = 'running' THEN NULL ELSE stop_reason END,
			    updated_at = now(), version = version + 1
			WHERE id = $1 AND desired_state = $3
		`, serverID, fallback, expectedDesired); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE operations
			SET status = 'failed', message = 'Power action failed',
			    error_code = 'power_failed', error_detail = $2, completed_at = now()
			WHERE id = $1
		`, operationID, detail); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE servers
		SET status = $2,
		    stop_reason = CASE WHEN $2 = 'stopped' THEN 'requested' ELSE NULL END,
		    recovery_attempts = CASE WHEN $2 = 'running' THEN 0 ELSE recovery_attempts END,
		    recovery_window_started_at = CASE WHEN $2 = 'running' THEN NULL ELSE recovery_window_started_at END,
		    recovery_not_before = NULL,
		    updated_at = now(), version = version + 1
		WHERE id = $1 AND desired_state = $3
	`, serverID, status, expectedDesired); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE server_runtime
		SET observed_state = $2,
		    started_at = CASE WHEN $2 = 'running' THEN now() ELSE started_at END,
		    observed_at = now()
		WHERE server_id = $1
		  AND EXISTS (
			  SELECT 1 FROM servers
			  WHERE id = $1 AND desired_state = $3
		  )
	`, serverID, status, expectedDesired); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE operations
		SET status = 'succeeded', progress = 100, message = 'Power action completed',
		    completed_at = now()
		WHERE id = $1
	`, operationID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) BeginRecovery(ctx context.Context, serverID string, attempt int) (bool, error) {
	command, err := s.pool.Exec(ctx, `
		UPDATE servers
		SET status = 'starting', recovery_not_before = NULL,
		    updated_at = now(), version = version + 1
		WHERE id = $1 AND deleted_at IS NULL
		  AND desired_state = 'running'
		  AND status = 'stopped'
		  AND auto_recovery_enabled
		  AND recovery_attempts = $2
	`, serverID, attempt)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

func (s *Store) FinishRecovery(
	ctx context.Context,
	serverID string,
	attempt int,
	recoveryErr error,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if recoveryErr != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE servers
			SET status = 'stopped', stop_reason = 'unexpected_exit',
			    updated_at = now(), version = version + 1
			WHERE id = $1 AND desired_state = 'running'
		`, serverID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE servers
			SET status = 'starting', updated_at = now(), version = version + 1
			WHERE id = $1 AND desired_state = 'running'
		`, serverID); err != nil {
			return err
		}
	}
	eventID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	severity := "info"
	summary := fmt.Sprintf("Automatic recovery attempt %d started", attempt)
	data := map[string]any{"attempt": attempt}
	if recoveryErr != nil {
		severity = "error"
		summary = fmt.Sprintf("Automatic recovery attempt %d failed", attempt)
		data["error"] = truncateStoreError(recoveryErr.Error(), 1000)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(
			id, server_id, event_type, severity, summary, data
		)
		VALUES ($1, $2, 'server.recovery.attempt', $3, $4, $5)
	`, eventID, serverID, severity, summary, data); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) MarkIntentionalConsoleShutdown(
	ctx context.Context,
	serverID, command string,
) (bool, error) {
	var stopCommand string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(tv.canonical_document->>'stop_command', '')
		FROM servers s
		JOIN template_versions tv ON tv.id = s.template_version_id
		WHERE s.id = $1 AND s.deleted_at IS NULL
	`, serverID).Scan(&stopCommand)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	expected := strings.Fields(strings.ToLower(strings.TrimSpace(stopCommand)))
	actual := strings.Fields(strings.ToLower(strings.TrimSpace(command)))
	if len(expected) == 0 || len(actual) == 0 || expected[0] != actual[0] {
		return false, nil
	}
	switch expected[0] {
	case "shutdown", "quit", "exit", "stop", "saveandexit":
	default:
		return false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `
		UPDATE servers
		SET status = 'stopping', desired_state = 'stopped',
		    stop_reason = 'requested', updated_at = now(), version = version + 1
		WHERE id = $1 AND deleted_at IS NULL
	`, serverID); err != nil {
		return false, err
	}
	outboxID, err := identity.NewUUID()
	if err != nil {
		return false, err
	}
	payload, _ := json.Marshal(map[string]string{"server_id": serverID})
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events(id, topic, aggregate_id, payload, available_at)
		VALUES ($1, 'server.enforce_stop', $2, $3, now() + interval '30 seconds')
	`, outboxID, serverID, payload); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ServerCommandReady(ctx context.Context, serverID string) (bool, error) {
	var ready bool
	err := s.pool.QueryRow(ctx, `
		SELECT s.status = 'running'
		FROM servers s
		WHERE s.id = $1 AND s.deleted_at IS NULL
	`, serverID).Scan(&ready)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return ready, err
}

func (s *Store) IntentionalStopPending(ctx context.Context, serverID string) (bool, error) {
	var pending bool
	err := s.pool.QueryRow(ctx, `
		SELECT desired_state = 'stopped' AND status = 'stopping'
		FROM servers
		WHERE id = $1 AND deleted_at IS NULL
	`, serverID).Scan(&pending)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return pending, err
}

func (s *Store) FinishIntentionalStop(ctx context.Context, serverID string, stopErr error) error {
	if stopErr != nil {
		_, err := s.pool.Exec(ctx, `
			UPDATE servers
			SET status = 'failed', updated_at = now(), version = version + 1
			WHERE id = $1 AND desired_state = 'stopped'
		`, serverID)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE servers
		SET status = 'stopped', stop_reason = 'requested',
		    updated_at = now(), version = version + 1
		WHERE id = $1 AND desired_state = 'stopped'
	`, serverID)
	return err
}

func truncateStoreError(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}

func (s *Store) RecordConsoleCommand(ctx context.Context, serverID, actorID string, length int) error {
	eventID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, actor_user_id, event_type, summary, data)
		VALUES ($1, $2, $3, 'server.console.command', 'Console command sent', $4)
	`, eventID, serverID, actorID, map[string]any{"length": length})
	return err
}

func (s *Store) RecordFileActivity(ctx context.Context, serverID, actorID, action, path string) error {
	eventID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, actor_user_id, event_type, summary, data)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, eventID, serverID, actorID, "server.file."+action, "Server file "+action,
		map[string]any{"path": path},
	)
	return err
}

func (s *Store) ServerExists(ctx context.Context, serverID string) error {
	var exists bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM servers WHERE id = $1 AND deleted_at IS NULL)
	`, serverID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func (s *Store) FinalizeServerDeletion(ctx context.Context, serverID, actorID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE servers
		SET status = 'deleting', desired_state = 'deleted', deleted_at = now(),
		    container_id = NULL, updated_at = now(), version = version + 1
		WHERE id = $1 AND deleted_at IS NULL
	`, serverID)
	if err != nil {
		return err
	}
	return s.AddAudit(ctx, actorID, "server.delete", "server", serverID, "", net.IP(nil), "", nil)
}
