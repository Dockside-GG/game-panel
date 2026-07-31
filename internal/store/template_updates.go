package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/dockside-gg/game-panel/internal/sanitize"
	"github.com/dockside-gg/game-panel/internal/templates"
	"github.com/jackc/pgx/v5"
)

type TemplateUpdateStatus struct {
	CurrentVersionID string `json:"current_version_id"`
	CurrentVersion   int    `json:"current_version"`
	LatestVersionID  string `json:"latest_version_id"`
	LatestVersion    int    `json:"latest_version"`
	TemplateName     string `json:"template_name"`
	UpdateAvailable  bool   `json:"update_available"`
}

type TemplateUpdateJob struct {
	OperationID     string `json:"operation_id"`
	ServerID        string `json:"server_id"`
	BackupID        string `json:"backup_id"`
	PreviousVersion string `json:"previous_version_id"`
	TargetVersion   string `json:"target_version_id"`
	Mode            string `json:"mode"`
	WasRunning      bool   `json:"was_running"`
}

func (s *Store) ServerTemplateUpdateStatus(
	ctx context.Context,
	serverID string,
) (TemplateUpdateStatus, error) {
	var result TemplateUpdateStatus
	err := s.pool.QueryRow(ctx, `
		SELECT current.id, current.version, latest.id, latest.version, template.name
		FROM servers AS server
		JOIN template_versions AS current ON current.id = server.template_version_id
		JOIN templates AS template ON template.id = current.template_id
		JOIN LATERAL (
			SELECT id, version
			FROM template_versions
			WHERE template_id = current.template_id
			ORDER BY version DESC
			LIMIT 1
		) AS latest ON true
		WHERE server.id = $1 AND server.deleted_at IS NULL
	`, serverID).Scan(
		&result.CurrentVersionID, &result.CurrentVersion,
		&result.LatestVersionID, &result.LatestVersion,
		&result.TemplateName,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	result.UpdateAvailable = result.LatestVersion > result.CurrentVersion
	return result, err
}

func (s *Store) QueueTemplateUpdate(
	ctx context.Context,
	serverID, actorID, targetVersionID, mode string,
) (TemplateUpdateJob, error) {
	if mode != "rebase" && mode != "reinstall" {
		return TemplateUpdateJob{}, fmt.Errorf("%w: invalid template update mode", ErrConflict)
	}
	operationID, err := identity.NewUUID()
	if err != nil {
		return TemplateUpdateJob{}, err
	}
	backupID, err := identity.NewUUID()
	if err != nil {
		return TemplateUpdateJob{}, err
	}
	outboxID, err := identity.NewUUID()
	if err != nil {
		return TemplateUpdateJob{}, err
	}
	activityID, err := identity.NewUUID()
	if err != nil {
		return TemplateUpdateJob{}, err
	}
	idempotencyKey, err := identity.Token(24)
	if err != nil {
		return TemplateUpdateJob{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return TemplateUpdateJob{}, err
	}
	defer tx.Rollback(ctx)
	var previousVersionID, serverName, serverStatus, desiredState string
	var currentVersion, targetVersion int
	err = tx.QueryRow(ctx, `
		SELECT current.id, current.version, target.version, server.name,
		       server.status, server.desired_state
		FROM servers AS server
		JOIN template_versions AS current ON current.id = server.template_version_id
		JOIN template_versions AS target
		  ON target.id = $2 AND target.template_id = current.template_id
		WHERE server.id = $1 AND server.deleted_at IS NULL
		FOR UPDATE OF server
	`, serverID, targetVersionID).Scan(
		&previousVersionID, &currentVersion, &targetVersion, &serverName,
		&serverStatus, &desiredState,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return TemplateUpdateJob{}, ErrNotFound
	}
	if err != nil {
		return TemplateUpdateJob{}, err
	}
	if targetVersion <= currentVersion {
		return TemplateUpdateJob{}, fmt.Errorf("%w: target template is not newer", ErrConflict)
	}
	var active bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM operations
			WHERE server_id = $1 AND status IN ('pending', 'running')
		) OR EXISTS(
			SELECT 1 FROM backups
			WHERE server_id = $1 AND status IN ('queued', 'running')
		)
	`, serverID).Scan(&active); err != nil {
		return TemplateUpdateJob{}, err
	}
	if active {
		return TemplateUpdateJob{}, fmt.Errorf("%w: another server operation is active", ErrConflict)
	}
	wasRunning := desiredState == "running" ||
		serverStatus == "running" || serverStatus == "starting" || serverStatus == "restarting"
	if _, err := tx.Exec(ctx, `
		INSERT INTO backups(
			id, server_id, name, status, include_paths, exclude_globs, created_by
		)
		VALUES ($1, $2, $3, 'queued', '[]'::jsonb, '[]'::jsonb, $4)
	`, backupID, serverID, fmt.Sprintf(
		"Pre-template-update %s %s", serverName, time.Now().UTC().Format("2006-01-02 15:04"),
	), actorID); err != nil {
		return TemplateUpdateJob{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO operations(
			id, server_id, actor_user_id, kind, idempotency_key, message
		)
		VALUES ($1, $2, $3, 'server.template_update', $4, 'Waiting for template update worker')
	`, operationID, serverID, actorID, idempotencyKey); err != nil {
		return TemplateUpdateJob{}, err
	}
	job := TemplateUpdateJob{
		OperationID: operationID, ServerID: serverID, BackupID: backupID,
		PreviousVersion: previousVersionID, TargetVersion: targetVersionID,
		Mode: mode, WasRunning: wasRunning,
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return TemplateUpdateJob{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events(id, topic, aggregate_id, payload)
		VALUES ($1, 'server.template_update', $2, $3)
	`, outboxID, serverID, payload); err != nil {
		return TemplateUpdateJob{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE servers
		SET desired_state = 'stopped',
		    status = CASE WHEN status IN ('running', 'starting', 'restarting') THEN 'stopping' ELSE status END,
		    stop_reason = 'requested', updated_at = now(), version = version + 1
		WHERE id = $1
	`, serverID); err != nil {
		return TemplateUpdateJob{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(
			id, server_id, actor_user_id, event_type, summary, data
		)
		VALUES (
			$1, $2, $3, 'server.template_update.requested',
			'Template update requested', $4
		)
	`, activityID, serverID, actorID, payload); err != nil {
		return TemplateUpdateJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TemplateUpdateJob{}, err
	}
	return job, nil
}

func (s *Store) MarkTemplateUpdateRunning(
	ctx context.Context,
	job TemplateUpdateJob,
	progress int,
	message string,
) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE operations
		SET status = 'running', started_at = COALESCE(started_at, now()),
		    progress = $2, message = $3
		WHERE id = $1 AND kind = 'server.template_update'
	`, job.OperationID, progress, message); err != nil {
		return err
	}
	return s.AppendOperationLog(
		ctx, job.OperationID, job.ServerID, "template_update", "system",
		message, time.Now().UTC(),
	)
}

func (s *Store) SetServerTemplateVersion(
	ctx context.Context,
	serverID, versionID string,
) (templates.CanonicalTemplate, error) {
	var document []byte
	tag, err := s.pool.Exec(ctx, `
		UPDATE servers AS server
		SET template_version_id = target.id,
		    image_reference = target.canonical_document->>'default_image',
		    updated_at = now(), version = version + 1
		FROM template_versions AS target, template_versions AS current
		WHERE server.id = $1 AND current.id = server.template_version_id
		  AND target.id = $2 AND target.template_id = current.template_id
	`, serverID, versionID)
	if err != nil {
		return templates.CanonicalTemplate{}, err
	}
	if tag.RowsAffected() != 1 {
		return templates.CanonicalTemplate{}, ErrConflict
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT canonical_document FROM template_versions WHERE id = $1
	`, versionID).Scan(&document); err != nil {
		return templates.CanonicalTemplate{}, err
	}
	var canonical templates.CanonicalTemplate
	if err := json.Unmarshal(document, &canonical); err != nil {
		return canonical, err
	}
	return canonical, nil
}

func (s *Store) FinishTemplateUpdate(
	ctx context.Context,
	job TemplateUpdateJob,
	containerID string,
	updateErr error,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	eventID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	desired, serverStatus := "stopped", "stopped"
	if job.WasRunning {
		desired, serverStatus = "running", "starting"
	}
	operationStatus, message := "succeeded", "Template update completed"
	eventType, severity, summary := "server.template_update.succeeded", "info", "Template update completed"
	var detail *string
	if updateErr != nil {
		value := sanitize.Text(updateErr.Error())
		detail = &value
		operationStatus, message = "failed", "Template update failed"
		eventType, severity, summary = "server.template_update.failed", "error", "Template update failed"
	}
	if _, err := tx.Exec(ctx, `
		UPDATE servers
		SET desired_state = $2, status = $3, stop_reason = NULL,
		    container_id = COALESCE(NULLIF($4, ''), container_id),
		    updated_at = now(), version = version + 1
		WHERE id = $1
	`, job.ServerID, desired, serverStatus, containerID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE operations
		SET status = $2, progress = CASE WHEN $2 = 'succeeded' THEN 100 ELSE progress END,
		    message = $3, error_code = CASE WHEN $2 = 'failed' THEN 'template_update_failed' END,
		    error_detail = $4, completed_at = now()
		WHERE id = $1
	`, job.OperationID, operationStatus, message, detail); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(
			id, server_id, event_type, severity, summary, data
		)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, eventID, job.ServerID, eventType, severity, summary, map[string]any{
		"operation_id":      job.OperationID,
		"target_version_id": job.TargetVersion,
		"backup_id":         job.BackupID,
		"mode":              job.Mode,
		"error":             detail,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
