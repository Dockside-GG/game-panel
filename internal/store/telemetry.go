package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/dockside-gg/game-panel/internal/identity"
)

func (s *Store) SyncRuntimeStats(ctx context.Context, snapshots []engineclient.ServerStats) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	observedServers := make(map[string]bool, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.ServerID == "" {
			continue
		}
		observedServers[snapshot.ServerID] = true
		var desiredState, previousStatus string
		var recoveryAttempts int
		var recoveryWindowStarted *time.Time
		if err := tx.QueryRow(ctx, `
			SELECT desired_state, status, recovery_attempts, recovery_window_started_at
			FROM servers
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE
		`, snapshot.ServerID).Scan(
			&desiredState, &previousStatus, &recoveryAttempts, &recoveryWindowStarted,
		); err != nil {
			continue
		}
		status := runtimeServerStatus(snapshot.State, desiredState, previousStatus)
		var stopReason *string
		running := status == "running"
		stableRunning := running && snapshot.StartedAt != nil &&
			snapshot.ObservedAt.Sub(*snapshot.StartedAt) >= 5*time.Minute
		if status == "stopped" || (status == "stopping" && desiredState == "stopped") {
			reason := "requested"
			if desiredState == "running" {
				reason = "unexpected_exit"
				if previousStatus == "restarting" {
					reason = "clean_exit"
				}
			}
			stopReason = &reason
		}
		health := snapshot.Health
		if snapshot.Error != "" {
			health = "unknown"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE server_runtime
			SET observed_state = $2, health = $3, cpu_percent = $4,
			    memory_bytes = $5, memory_limit_bytes = NULLIF($6::bigint, 0),
			    network_rx_bytes = $7, network_tx_bytes = $8,
			    block_read_bytes = $9, block_write_bytes = $10,
			    started_at = $11, exit_code = $12,
			    last_error = NULLIF($13, ''), observed_at = $14
			WHERE server_id = $1
		`, snapshot.ServerID, snapshot.State, health, snapshot.CPUPercent,
			snapshot.MemoryBytes, snapshot.MemoryLimitBytes,
			snapshot.NetworkRXBytes, snapshot.NetworkTXBytes,
			snapshot.BlockReadBytes, snapshot.BlockWriteBytes,
			snapshot.StartedAt, snapshot.ExitCode, snapshot.Error, snapshot.ObservedAt,
		); err != nil {
			return fmt.Errorf("update runtime stats for %s: %w", snapshot.ServerID, err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE servers
			SET status = $2, container_id = $3, stop_reason = $4,
			    missing_container_observations = 0,
			    recovery_attempts = CASE WHEN $5 THEN 0 ELSE recovery_attempts END,
			    recovery_window_started_at = CASE WHEN $5 THEN NULL ELSE recovery_window_started_at END,
			    recovery_not_before = CASE WHEN $2 = 'running' THEN NULL ELSE recovery_not_before END,
			    updated_at = now(),
			    version = CASE WHEN status <> $2 THEN version + 1 ELSE version END
			WHERE id = $1
		`, snapshot.ServerID, status, snapshot.ContainerID, stopReason, stableRunning); err != nil {
			return fmt.Errorf("update observed server state for %s: %w", snapshot.ServerID, err)
		}
		needsRecovery := status == "stopped" && desiredState == "running"
		plannedRestart := needsRecovery && previousStatus == "restarting"
		if needsRecovery && previousStatus != "stopped" {
			eventID, err := identity.NewUUID()
			if err != nil {
				return err
			}
			eventType, severity, summary := "server.unexpected_exit", "error", "Server stopped unexpectedly"
			if plannedRestart {
				eventType, severity, summary = "server.restart.in_progress", "info", "Game process exited for restart"
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO activity_events(
					id, server_id, event_type, severity, summary, data
				)
				VALUES ($1, $2, $3, $4, $5, $6)
			`, eventID, snapshot.ServerID, eventType, severity, summary, map[string]any{
				"exit_code": snapshot.ExitCode,
				"state":     snapshot.State,
			}); err != nil {
				return err
			}
		}
		if needsRecovery {
			now := snapshot.ObservedAt
			if recoveryWindowStarted == nil {
				recoveryAttempts = 0
				recoveryWindowStarted = &now
			}
			var pending bool
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS(
					SELECT 1 FROM outbox_events
					WHERE aggregate_id = $1 AND topic = 'server.recover'
					  AND processed_at IS NULL
				)
			`, snapshot.ServerID).Scan(&pending); err != nil {
				return err
			}
			if !pending {
				attempt := nextRecoveryAttempt(recoveryAttempts)
				delay := recoveryDelay(attempt)
				payload, _ := json.Marshal(map[string]any{
					"server_id": snapshot.ServerID, "attempt": attempt,
				})
				outboxID, err := identity.NewUUID()
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO outbox_events(
						id, topic, aggregate_id, payload, available_at
					)
					VALUES ($1, 'server.recover', $2, $3, now() + $4::interval)
				`, outboxID, snapshot.ServerID, payload,
					fmt.Sprintf("%d seconds", int(delay.Seconds()))); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `
					UPDATE servers
					SET recovery_attempts = $2,
					    recovery_window_started_at = $3,
					    recovery_not_before = now() + $4::interval
					WHERE id = $1
				`, snapshot.ServerID, attempt, recoveryWindowStarted,
					fmt.Sprintf("%d seconds", int(delay.Seconds()))); err != nil {
					return err
				}
				if attempt == 5 && recoveryAttempts < 5 {
					eventID, err := identity.NewUUID()
					if err != nil {
						return err
					}
					if _, err := tx.Exec(ctx, `
						INSERT INTO activity_events(
							id, server_id, event_type, severity, summary, data
						)
						VALUES (
							$1, $2, 'server.recovery.persistent', 'warning',
							'Server entered persistent recovery mode',
							jsonb_build_object('attempt', 5, 'retry_seconds', $3::integer)
						)
					`, eventID, snapshot.ServerID, int(delay.Seconds())); err != nil {
						return err
					}
				}
			}
		}
	}
	rows, err := tx.Query(ctx, `
		SELECT id
		FROM servers
		WHERE deleted_at IS NULL
		  AND container_id IS NOT NULL
		  AND status NOT IN ('installing', 'deleting')
		FOR UPDATE
	`)
	if err != nil {
		return err
	}
	missing := make([]string, 0)
	for rows.Next() {
		var serverID string
		if err := rows.Scan(&serverID); err != nil {
			rows.Close()
			return err
		}
		if !observedServers[serverID] {
			missing = append(missing, serverID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, serverID := range missing {
		var observations int
		if err := tx.QueryRow(ctx, `
			UPDATE servers
			SET missing_container_observations = missing_container_observations + 1
			WHERE id = $1 AND deleted_at IS NULL
			RETURNING missing_container_observations
		`, serverID).Scan(&observations); err != nil {
			return err
		}
		if observations < 2 {
			continue
		}
		eventID, err := identity.NewUUID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO activity_events(
				id, server_id, event_type, severity, summary, data
			) VALUES (
				$1, $2, 'server.external_delete', 'warning',
				'Server container was deleted outside Dockside',
				jsonb_build_object('observations', $3::integer)
			)
		`, eventID, serverID, observations); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE schedules SET enabled = false, updated_at = now()
			WHERE server_id = $1
		`, serverID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE outbox_events
			SET processed_at = now(), last_error = 'cancelled after external container deletion'
			WHERE aggregate_id = $1 AND processed_at IS NULL
		`, serverID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM server_ports WHERE server_id = $1`, serverID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE servers
			SET status = 'deleting', desired_state = 'deleted',
			    externally_deleted_at = now(), deleted_at = now(),
			    container_id = NULL, updated_at = now(), version = version + 1
			WHERE id = $1 AND deleted_at IS NULL
		`, serverID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func runtimeServerStatus(observed, desired, previous string) string {
	switch observed {
	case "running":
		if desired == "stopped" && previous == "stopping" {
			return "stopping"
		}
		return "running"
	case "created":
		if previous == "installing" {
			return previous
		}
		return "stopped"
	case "paused":
		return "suspended"
	case "restarting":
		return "restarting"
	case "removing", "dead":
		if desired == "deleted" {
			return "deleting"
		}
		return "stopped"
	case "exited":
		if desired == "running" {
			return "stopped"
		}
		return "stopped"
	default:
		return previous
	}
}

func recoveryDelay(attempt int) time.Duration {
	delays := []time.Duration{
		5 * time.Second,
		15 * time.Second,
		30 * time.Second,
		60 * time.Second,
		120 * time.Second,
	}
	if attempt < 1 {
		attempt = 1
	}
	if attempt > len(delays) {
		attempt = len(delays)
	}
	return delays[attempt-1]
}

func nextRecoveryAttempt(current int) int {
	if current < 0 {
		return 1
	}
	if current >= 5 {
		return 5
	}
	return current + 1
}
