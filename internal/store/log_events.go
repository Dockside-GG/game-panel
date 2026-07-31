package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/jackc/pgx/v5"
)

func (s *Store) RecordConsoleLogEvent(
	ctx context.Context,
	event engineclient.ServerLogEvent,
	severity, message string,
) error {
	if severity != "warning" && severity != "error" {
		return nil
	}
	digestBytes := sha256.Sum256([]byte(
		event.Stream + "\x00" + event.ObservedAt.UTC().String() + "\x00" + message,
	))
	digest := hex.EncodeToString(digestBytes[:])
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var inserted string
	err = tx.QueryRow(ctx, `
		INSERT INTO server_log_events_seen(server_id, observed_at, digest)
		SELECT $1, $2, $3
		WHERE EXISTS(
			SELECT 1 FROM servers WHERE id = $1 AND deleted_at IS NULL
		)
		ON CONFLICT DO NOTHING
		RETURNING digest
	`, event.ServerID, event.ObservedAt, digest).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	id, err := identity.NewUUID()
	if err != nil {
		return err
	}
	summary := "Game server reported a " + severity
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(
			id, server_id, event_type, severity, summary, data, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, id, event.ServerID, "server.console."+severity, severity, summary,
		map[string]any{
			"stream":  event.Stream,
			"message": strings.TrimSpace(message),
		},
		event.ObservedAt,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
