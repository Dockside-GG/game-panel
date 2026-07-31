package store

import (
	"context"
	"fmt"
)

func (s *Store) ServerActivity(ctx context.Context, serverID string, limit int) ([]ActivityEvent, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > 250 {
		limit = 250
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, server_id, actor_user_id, event_type, severity, summary, data, created_at
		FROM activity_events
		WHERE server_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, serverID, limit)
	if err != nil {
		return nil, fmt.Errorf("list server activity: %w", err)
	}
	defer rows.Close()
	result := make([]ActivityEvent, 0)
	for rows.Next() {
		var event ActivityEvent
		if err := rows.Scan(
			&event.ID, &event.ServerID, &event.ActorID, &event.EventType,
			&event.Severity, &event.Summary, &event.Data, &event.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
