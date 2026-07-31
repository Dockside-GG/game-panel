package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/jackc/pgx/v5"
)

type Backup struct {
	ID            string          `json:"id"`
	ServerID      string          `json:"server_id"`
	Name          string          `json:"name"`
	Status        string          `json:"status"`
	StorageKind   string          `json:"storage_kind"`
	ObjectKey     *string         `json:"object_key"`
	SizeBytes     *int64          `json:"size_bytes"`
	SHA256        *string         `json:"sha256"`
	IncludePaths  []string        `json:"include_paths"`
	ExcludeGlobs  []string        `json:"exclude_globs"`
	Locked        bool            `json:"locked"`
	RetentionDays *int            `json:"retention_days"`
	ExpiresAt     *time.Time      `json:"expires_at"`
	CreatedBy     *string         `json:"created_by"`
	CreatedAt     time.Time       `json:"created_at"`
	CompletedAt   *time.Time      `json:"completed_at"`
	Delivery      *BackupDelivery `json:"discord_delivery,omitempty"`
}

type BackupDelivery struct {
	ID              string     `json:"id"`
	DestinationID   string     `json:"destination_id"`
	DestinationName string     `json:"destination_name"`
	Format          string     `json:"format"`
	Status          string     `json:"status"`
	Attempts        int        `json:"attempts"`
	ResponseStatus  *int       `json:"response_status"`
	LastError       *string    `json:"last_error"`
	DeliveredAt     *time.Time `json:"delivered_at"`
}

type BackupJob struct {
	BackupID    string
	ServerID    string
	IncludePath []string
	ExcludeGlob []string
}

type BackupDeliveryJob struct {
	DeliveryID    string
	BackupID      string
	ServerID      string
	BackupName    string
	Format        string
	SizeBytes     int64
	SHA256        string
	DestinationID string
	URLEncrypted  string
	Attempts      int
}

type ExpiredBackup struct {
	BackupID string
	ServerID string
}

func (s *Store) CreateBackup(
	ctx context.Context,
	serverID, actorID, name string,
	includePaths, excludeGlobs []string,
	retentionDays *int,
	destinationID *string,
	deliveryFormat string,
) (Backup, error) {
	backupID, err := identity.NewUUID()
	if err != nil {
		return Backup{}, err
	}
	outboxID, err := identity.NewUUID()
	if err != nil {
		return Backup{}, err
	}
	eventID, err := identity.NewUUID()
	if err != nil {
		return Backup{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Backup{}, err
	}
	defer tx.Rollback(ctx)
	var exists, active bool
	if err := tx.QueryRow(ctx, `
		SELECT
			EXISTS(SELECT 1 FROM servers WHERE id = $1 AND deleted_at IS NULL),
			EXISTS(
				SELECT 1 FROM backups
				WHERE server_id = $1 AND status IN ('queued', 'running')
			)
	`, serverID).Scan(&exists, &active); err != nil {
		return Backup{}, err
	}
	if !exists {
		return Backup{}, ErrNotFound
	}
	if active {
		return Backup{}, ErrConflict
	}
	if retentionDays != nil && (*retentionDays < 1 || *retentionDays > 3650) {
		return Backup{}, ErrConflict
	}
	if destinationID != nil {
		if deliveryFormat != "archive" && deliveryFormat != "zip" {
			return Backup{}, ErrConflict
		}
		var destinationExists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM webhook_destinations
				WHERE id = $1 AND server_id = $2 AND kind = 'discord' AND enabled
			)
		`, *destinationID, serverID).Scan(&destinationExists); err != nil {
			return Backup{}, err
		}
		if !destinationExists {
			return Backup{}, ErrNotFound
		}
	}
	includeDocument, err := json.Marshal(includePaths)
	if err != nil {
		return Backup{}, err
	}
	excludeDocument, err := json.Marshal(excludeGlobs)
	if err != nil {
		return Backup{}, err
	}
	var result Backup
	var includeJSON, excludeJSON []byte
	err = tx.QueryRow(ctx, `
		INSERT INTO backups(
			id, server_id, name, status, include_paths, exclude_globs,
			retention_days, expires_at, created_by
		)
		VALUES (
			$1, $2, $3, 'queued', $4, $5, $6,
			CASE WHEN $6::integer IS NULL THEN NULL
			     ELSE now() + ($6::integer * interval '1 day') END,
			$7
		)
		RETURNING id, server_id, name, status, storage_kind, object_key,
		          size_bytes, sha256, include_paths, exclude_globs, locked,
		          retention_days, expires_at, created_by, created_at, completed_at
	`, backupID, serverID, name, string(includeDocument), string(excludeDocument),
		retentionDays, actorID).Scan(
		&result.ID, &result.ServerID, &result.Name, &result.Status, &result.StorageKind,
		&result.ObjectKey, &result.SizeBytes, &result.SHA256, &includeJSON, &excludeJSON,
		&result.Locked, &result.RetentionDays, &result.ExpiresAt,
		&result.CreatedBy, &result.CreatedAt, &result.CompletedAt,
	)
	if err != nil {
		return Backup{}, fmt.Errorf("create backup: %w", err)
	}
	if destinationID != nil {
		deliveryID, err := identity.NewUUID()
		if err != nil {
			return Backup{}, err
		}
		var destinationName string
		if err := tx.QueryRow(ctx, `
			INSERT INTO backup_deliveries(id, backup_id, destination_id, format)
			VALUES ($1, $2, $3, $4)
			RETURNING (
				SELECT name FROM webhook_destinations WHERE id = $3
			)
		`, deliveryID, backupID, *destinationID, deliveryFormat).Scan(&destinationName); err != nil {
			return Backup{}, err
		}
		result.Delivery = &BackupDelivery{
			ID: deliveryID, DestinationID: *destinationID,
			DestinationName: destinationName, Format: deliveryFormat, Status: "pending",
		}
	}
	payload, err := json.Marshal(map[string]string{"backup_id": backupID})
	if err != nil {
		return Backup{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events(id, topic, aggregate_id, payload)
		VALUES ($1, 'backup.create', $2, $3)
	`, outboxID, backupID, payload); err != nil {
		return Backup{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, actor_user_id, event_type, summary, data)
		VALUES ($1, $2, $3, 'server.backup.queued', 'Backup queued', $4)
	`, eventID, serverID, actorID, map[string]any{"backup_id": backupID, "name": name}); err != nil {
		return Backup{}, err
	}
	if err := json.Unmarshal(includeJSON, &result.IncludePaths); err != nil {
		return Backup{}, err
	}
	if err := json.Unmarshal(excludeJSON, &result.ExcludeGlobs); err != nil {
		return Backup{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Backup{}, err
	}
	return result, nil
}

func (s *Store) ListBackups(ctx context.Context, serverID string) ([]Backup, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT backup.id, backup.server_id, backup.name, backup.status,
		       backup.storage_kind, backup.object_key, backup.size_bytes, backup.sha256,
		       backup.include_paths, backup.exclude_globs, backup.locked,
		       backup.retention_days, backup.expires_at, backup.created_by,
		       backup.created_at, backup.completed_at,
		       delivery.id, delivery.destination_id, destination.name, delivery.format,
		       delivery.status, delivery.attempts, delivery.response_status,
		       delivery.last_error, delivery.delivered_at
		FROM backups AS backup
		LEFT JOIN backup_deliveries AS delivery ON delivery.backup_id = backup.id
		LEFT JOIN webhook_destinations AS destination ON destination.id = delivery.destination_id
		WHERE backup.server_id = $1
		ORDER BY backup.created_at DESC
	`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Backup, 0)
	for rows.Next() {
		var item Backup
		var includes, excludes []byte
		var deliveryID, destinationID, destinationName, format, deliveryStatus *string
		var deliveryAttempts *int
		var responseStatus *int
		var deliveryError *string
		var deliveredAt *time.Time
		if err := rows.Scan(
			&item.ID, &item.ServerID, &item.Name, &item.Status, &item.StorageKind,
			&item.ObjectKey, &item.SizeBytes, &item.SHA256, &includes, &excludes,
			&item.Locked, &item.RetentionDays, &item.ExpiresAt,
			&item.CreatedBy, &item.CreatedAt, &item.CompletedAt,
			&deliveryID, &destinationID, &destinationName, &format, &deliveryStatus,
			&deliveryAttempts, &responseStatus, &deliveryError, &deliveredAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(includes, &item.IncludePaths); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(excludes, &item.ExcludeGlobs); err != nil {
			return nil, err
		}
		if deliveryID != nil {
			item.Delivery = &BackupDelivery{
				ID: *deliveryID, DestinationID: valueOr(destinationID, ""),
				DestinationName: valueOr(destinationName, "Deleted webhook"),
				Format:          valueOr(format, "archive"), Status: valueOr(deliveryStatus, "pending"),
				Attempts: intOr(deliveryAttempts, 0), ResponseStatus: responseStatus,
				LastError: deliveryError, DeliveredAt: deliveredAt,
			}
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) BackupByID(ctx context.Context, serverID, backupID string) (Backup, error) {
	var item Backup
	var includes, excludes []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, server_id, name, status, storage_kind, object_key,
		       size_bytes, sha256, include_paths, exclude_globs, locked,
		       retention_days, expires_at, created_by, created_at, completed_at
		FROM backups
		WHERE id = $1 AND server_id = $2
	`, backupID, serverID).Scan(
		&item.ID, &item.ServerID, &item.Name, &item.Status, &item.StorageKind,
		&item.ObjectKey, &item.SizeBytes, &item.SHA256, &includes, &excludes,
		&item.Locked, &item.RetentionDays, &item.ExpiresAt,
		&item.CreatedBy, &item.CreatedAt, &item.CompletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Backup{}, ErrNotFound
	}
	if err != nil {
		return Backup{}, err
	}
	if err := json.Unmarshal(includes, &item.IncludePaths); err != nil {
		return Backup{}, err
	}
	if err := json.Unmarshal(excludes, &item.ExcludeGlobs); err != nil {
		return Backup{}, err
	}
	return item, nil
}

func (s *Store) BackupJob(ctx context.Context, backupID string) (BackupJob, error) {
	var job BackupJob
	var includes, excludes []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, server_id, include_paths, exclude_globs
		FROM backups
		WHERE id = $1 AND status IN ('queued', 'running')
	`, backupID).Scan(&job.BackupID, &job.ServerID, &includes, &excludes)
	if errors.Is(err, pgx.ErrNoRows) {
		return BackupJob{}, ErrNotFound
	}
	if err != nil {
		return BackupJob{}, err
	}
	if err := json.Unmarshal(includes, &job.IncludePath); err != nil {
		return BackupJob{}, err
	}
	if err := json.Unmarshal(excludes, &job.ExcludeGlob); err != nil {
		return BackupJob{}, err
	}
	return job, nil
}

func (s *Store) MarkBackupRunning(ctx context.Context, backupID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE backups SET status = 'running' WHERE id = $1 AND status = 'queued'
	`, backupID)
	return err
}

func (s *Store) MarkBackupSucceeded(ctx context.Context, job BackupJob, result engineclient.BackupResult) error {
	eventID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE backups
		SET status = 'succeeded', object_key = $2, size_bytes = $3,
		    sha256 = $4, completed_at = now()
		WHERE id = $1
	`, job.BackupID, result.ObjectKey, result.SizeBytes, result.SHA256); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, event_type, summary, data)
		VALUES ($1, $2, 'server.backup.succeeded', 'Backup completed', $3)
	`, eventID, job.ServerID, map[string]any{
		"backup_id": job.BackupID, "size_bytes": result.SizeBytes, "sha256": result.SHA256,
	}); err != nil {
		return err
	}
	deliveryOutboxID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		WITH queued AS (
			UPDATE backup_deliveries
			SET status = 'queued', updated_at = now()
			WHERE backup_id = $1 AND status = 'pending'
			RETURNING id
		)
		INSERT INTO outbox_events(id, topic, aggregate_id, payload)
		SELECT $2, 'backup.discord_delivery', $1, jsonb_build_object('delivery_id', queued.id)
		FROM queued
	`, job.BackupID, deliveryOutboxID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) BeginBackupDelivery(ctx context.Context, deliveryID string) (BackupDeliveryJob, error) {
	var job BackupDeliveryJob
	err := s.pool.QueryRow(ctx, `
		UPDATE backup_deliveries AS delivery
		SET status = 'uploading', attempts = attempts + 1, updated_at = now()
		FROM backups AS backup, webhook_destinations AS destination
		WHERE delivery.id = $1
		  AND backup.id = delivery.backup_id
		  AND destination.id = delivery.destination_id
		  AND delivery.status IN ('queued', 'uploading')
		  AND backup.status = 'succeeded'
		  AND destination.enabled AND destination.kind = 'discord'
		RETURNING delivery.id, backup.id, backup.server_id, backup.name,
		          delivery.format, backup.size_bytes, backup.sha256,
		          destination.id, destination.url_encrypted, delivery.attempts
	`, deliveryID).Scan(
		&job.DeliveryID, &job.BackupID, &job.ServerID, &job.BackupName,
		&job.Format, &job.SizeBytes, &job.SHA256, &job.DestinationID,
		&job.URLEncrypted, &job.Attempts,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return BackupDeliveryJob{}, ErrNotFound
	}
	return job, err
}

func (s *Store) FinishBackupDelivery(
	ctx context.Context,
	deliveryID, status string,
	responseStatus int,
	deliveryErr error,
) error {
	var detail *string
	if deliveryErr != nil {
		value := deliveryErr.Error()
		if len(value) > 2000 {
			value = value[:2000]
		}
		detail = &value
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var backupID, serverID string
	err = tx.QueryRow(ctx, `
		WITH updated AS (
			UPDATE backup_deliveries
			SET status = $2, response_status = NULLIF($3, 0), last_error = $4,
			    delivered_at = CASE WHEN $2 = 'delivered' THEN now() ELSE delivered_at END,
			    updated_at = now()
			WHERE id = $1
			RETURNING backup_id
		)
		SELECT updated.backup_id, backup.server_id
		FROM updated
		JOIN backups AS backup ON backup.id = updated.backup_id
	`, deliveryID, status, responseStatus, detail).Scan(&backupID, &serverID)
	if err != nil {
		return err
	}
	if status == "delivered" || status == "too_large" || status == "failed" {
		eventID, err := identity.NewUUID()
		if err != nil {
			return err
		}
		severity := "info"
		summary := "Backup delivered to Discord"
		if status != "delivered" {
			severity = "warning"
			summary = "Discord backup delivery " + strings.ReplaceAll(status, "_", " ")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO activity_events(
				id, server_id, event_type, severity, summary, data
			)
			VALUES ($1, $2, 'server.backup.discord.' || $3, $4, $5, $6)
		`, eventID, serverID, status, severity, summary, map[string]any{
			"backup_id": backupID, "delivery_id": deliveryID,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) MarkBackupFailed(ctx context.Context, job BackupJob, backupErr error) error {
	eventID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	detail := backupErr.Error()
	if len(detail) > 2000 {
		detail = detail[:2000]
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE backups SET status = 'failed', completed_at = now() WHERE id = $1
	`, job.BackupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, event_type, severity, summary, data)
		VALUES ($1, $2, 'server.backup.failed', 'error', 'Backup failed', $3)
	`, eventID, job.ServerID, map[string]any{"backup_id": job.BackupID, "error": detail}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) BeginBackupDeletion(ctx context.Context, serverID, backupID string) (string, error) {
	var previous string
	err := s.pool.QueryRow(ctx, `
		UPDATE backups
		SET status = 'deleting'
		WHERE id = $1 AND server_id = $2 AND locked = false
		  AND status IN ('succeeded', 'failed')
		RETURNING CASE WHEN object_key IS NULL THEN 'failed' ELSE 'succeeded' END
	`, backupID, serverID).Scan(&previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrConflict
	}
	return previous, err
}

func (s *Store) CancelBackupDeletion(ctx context.Context, serverID, backupID, previous string) error {
	if previous != "succeeded" && previous != "failed" {
		previous = "failed"
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE backups SET status = $3
		WHERE id = $1 AND server_id = $2 AND status = 'deleting'
	`, backupID, serverID, previous)
	return err
}

func (s *Store) DeleteBackupRecord(ctx context.Context, serverID, backupID, actorID string) error {
	eventID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		DELETE FROM backups
		WHERE id = $1 AND server_id = $2 AND status = 'deleting'
	`, backupID, serverID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, actor_user_id, event_type, summary, data)
		VALUES (
			$1, $2, NULLIF($3, '')::uuid, 'server.backup.deleted',
			CASE WHEN $3 = '' THEN 'Expired backup deleted' ELSE 'Backup deleted' END,
			$4
		)
	`, eventID, serverID, actorID, map[string]any{"backup_id": backupID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ExpiredBackups(ctx context.Context, limit int) ([]ExpiredBackup, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, server_id
		FROM backups
		WHERE expires_at IS NOT NULL
		  AND expires_at <= now()
		  AND locked = false
		  AND status IN ('succeeded', 'failed')
		ORDER BY expires_at
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ExpiredBackup, 0)
	for rows.Next() {
		var item ExpiredBackup
		if err := rows.Scan(&item.BackupID, &item.ServerID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) RecordBackupRestore(ctx context.Context, serverID, backupID, actorID string) error {
	eventID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, actor_user_id, event_type, summary, data)
		VALUES ($1, $2, $3, 'server.backup.restored', 'Backup restored', $4)
	`, eventID, serverID, actorID, map[string]any{"backup_id": backupID})
	return err
}

func (s *Store) SetBackupLocked(ctx context.Context, serverID, backupID, actorID string, locked bool) error {
	eventID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE backups SET locked = $3
		WHERE id = $1 AND server_id = $2
		  AND status IN ('succeeded', 'failed')
	`, backupID, serverID, locked)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, actor_user_id, event_type, summary, data)
		VALUES ($1, $2, $3, 'server.backup.lock', $4, $5)
	`, eventID, serverID, actorID, map[bool]string{true: "Backup locked", false: "Backup unlocked"}[locked],
		map[string]any{"backup_id": backupID, "locked": locked},
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
