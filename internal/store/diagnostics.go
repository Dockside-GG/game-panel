package store

import (
	"context"
	"time"

	"github.com/dockside-gg/game-panel/internal/sanitize"
)

type DiagnosticEntry struct {
	Source    string    `json:"source"`
	Severity  string    `json:"severity"`
	Code      string    `json:"code"`
	Summary   string    `json:"summary"`
	Detail    string    `json:"detail"`
	ServerID  *string   `json:"server_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) Diagnostics(ctx context.Context, limit int) ([]DiagnosticEntry, error) {
	if limit < 20 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT source, severity, code, summary, detail, server_id, created_at
		FROM (
			SELECT 'operation'::text AS source, 'error'::text AS severity,
			       COALESCE(error_code, 'operation_failed') AS code,
			       COALESCE(message, 'Operation failed') AS summary,
			       COALESCE(error_detail, '') AS detail,
			       server_id, COALESCE(completed_at, requested_at) AS created_at
			FROM operations
			WHERE status = 'failed'
			UNION ALL
			SELECT 'worker', 'error', 'outbox_delivery_failed',
			       'Background job is waiting to retry',
			       COALESCE(last_error, ''), NULL::uuid, created_at
			FROM outbox_events
			WHERE processed_at IS NULL AND last_error IS NOT NULL
			UNION ALL
			SELECT 'runtime', 'error', 'server_runtime_error',
			       'Server runtime reported an infrastructure error',
			       COALESCE(last_error, ''), server_id, observed_at
			FROM server_runtime
			WHERE last_error IS NOT NULL AND last_error <> ''
			UNION ALL
			SELECT 'template_catalog', 'warning', 'catalog_sync_failed',
			       'Dockside template catalog synchronization failed',
			       COALESCE(last_error, ''), NULL::uuid, COALESCE(checked_at, now())
			FROM template_catalog_state
			WHERE status = 'failed' AND last_error IS NOT NULL
		) diagnostic
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]DiagnosticEntry, 0, limit)
	for rows.Next() {
		var item DiagnosticEntry
		if err := rows.Scan(
			&item.Source, &item.Severity, &item.Code, &item.Summary,
			&item.Detail, &item.ServerID, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		item.Detail = sanitize.Text(item.Detail)
		result = append(result, item)
	}
	return result, rows.Err()
}
