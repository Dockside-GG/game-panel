package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/dockside-gg/game-panel/internal/sanitize"
	"github.com/dockside-gg/game-panel/internal/secure"
	"github.com/jackc/pgx/v5"
)

type WebhookDestination struct {
	ID             string    `json:"id"`
	ServerID       string    `json:"server_id"`
	Name           string    `json:"name"`
	Kind           string    `json:"kind"`
	URLPreview     string    `json:"url_preview"`
	Enabled        bool      `json:"enabled"`
	DeliverEvents  bool      `json:"deliver_events"`
	DeliverBackups bool      `json:"deliver_backups"`
	EventFilters   []string  `json:"event_filters"`
	HasSecret      bool      `json:"has_signing_secret"`
	CreatedBy      *string   `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type WebhookDeliveryJob struct {
	DeliveryID             string
	DestinationID          string
	ServerID               string
	DestinationName        string
	Kind                   string
	URLEncrypted           string
	SigningSecretEncrypted *string
	Attempts               int
	EventID                string
	EventType              string
	Severity               string
	Summary                string
	Data                   json.RawMessage
	CreatedAt              time.Time
}

type WebhookDelivery struct {
	ID             string     `json:"id"`
	DestinationID  string     `json:"destination_id"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	ResponseStatus *int       `json:"response_status,omitempty"`
	LastError      *string    `json:"last_error,omitempty"`
	NextAttemptAt  *time.Time `json:"next_attempt_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
}

func (s *Store) CreateWebhook(
	ctx context.Context,
	serverID, actorID, name, kind, rawURL, signingSecret string,
	box *secure.Box,
) (WebhookDestination, error) {
	id, err := identity.NewUUID()
	if err != nil {
		return WebhookDestination{}, err
	}
	encryptedURL, err := box.Seal(rawURL, []byte("webhook:"+id+":url"))
	if err != nil {
		return WebhookDestination{}, err
	}
	var encryptedSecret *string
	if signingSecret != "" {
		value, err := box.Seal(signingSecret, []byte("webhook:"+id+":secret"))
		if err != nil {
			return WebhookDestination{}, err
		}
		encryptedSecret = &value
	}
	var item WebhookDestination
	var filterDocument []byte
	err = s.pool.QueryRow(ctx, `
		INSERT INTO webhook_destinations(
			id, server_id, name, kind, url_encrypted, signing_secret_encrypted,
			enabled, deliver_events, deliver_backups, event_filters, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, true, false, false, '[]', $7)
		RETURNING id, server_id, name, kind, enabled, deliver_events,
		          deliver_backups, event_filters,
		          signing_secret_encrypted IS NOT NULL, created_by, created_at, updated_at
	`, id, serverID, name, kind, encryptedURL, encryptedSecret, actorID).Scan(
		&item.ID, &item.ServerID, &item.Name, &item.Kind, &item.Enabled,
		&item.DeliverEvents, &item.DeliverBackups,
		&filterDocument, &item.HasSecret, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return WebhookDestination{}, fmt.Errorf("create webhook: %w", err)
	}
	if err := json.Unmarshal(filterDocument, &item.EventFilters); err != nil {
		return WebhookDestination{}, err
	}
	item.URLPreview = webhookURLPreview(rawURL)
	return item, nil
}

func (s *Store) ListWebhooks(ctx context.Context, serverID string, box *secure.Box) ([]WebhookDestination, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, server_id, name, kind, url_encrypted, enabled,
		       deliver_events, deliver_backups, event_filters,
		       signing_secret_encrypted IS NOT NULL, created_by, created_at, updated_at
		FROM webhook_destinations
		WHERE server_id = $1
		ORDER BY created_at DESC
	`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]WebhookDestination, 0)
	for rows.Next() {
		var item WebhookDestination
		var encryptedURL string
		var filters []byte
		if err := rows.Scan(
			&item.ID, &item.ServerID, &item.Name, &item.Kind, &encryptedURL,
			&item.Enabled, &item.DeliverEvents, &item.DeliverBackups,
			&filters, &item.HasSecret, &item.CreatedBy,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		rawURL, err := box.Open(encryptedURL, []byte("webhook:"+item.ID+":url"))
		if err != nil {
			return nil, err
		}
		item.URLPreview = webhookURLPreview(rawURL)
		if err := json.Unmarshal(filters, &item.EventFilters); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) UpdateWebhook(
	ctx context.Context,
	serverID, webhookID string,
	enabled, deliverEvents, deliverBackups *bool,
	eventFilters []string,
) error {
	filters, err := json.Marshal(eventFilters)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE webhook_destinations
		SET enabled = COALESCE($3, enabled),
		    deliver_events = COALESCE($4, deliver_events),
		    deliver_backups = COALESCE($5, deliver_backups),
		    event_filters = CASE WHEN $6 THEN $7::jsonb ELSE event_filters END,
		    updated_at = now()
		WHERE id = $1 AND server_id = $2
	`, webhookID, serverID, enabled, deliverEvents, deliverBackups,
		eventFilters != nil, string(filters))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteWebhook(ctx context.Context, serverID, webhookID string) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM webhook_destinations WHERE id = $1 AND server_id = $2
	`, webhookID, serverID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) QueueWebhookTest(
	ctx context.Context,
	serverID, actorID, webhookID string,
) (WebhookDelivery, error) {
	eventID, err := identity.NewUUID()
	if err != nil {
		return WebhookDelivery{}, err
	}
	deliveryID, err := identity.NewUUID()
	if err != nil {
		return WebhookDelivery{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WebhookDelivery{}, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM webhook_destinations WHERE id = $1 AND server_id = $2
		)
	`, webhookID, serverID).Scan(&exists); err != nil {
		return WebhookDelivery{}, err
	}
	if !exists {
		return WebhookDelivery{}, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, actor_user_id, event_type, summary, data)
		VALUES ($1, $2, $3, 'webhook.test', 'Dockside webhook test', $4)
	`, eventID, serverID, actorID, map[string]any{"destination_id": webhookID}); err != nil {
		return WebhookDelivery{}, err
	}
	// The activity trigger may not target this destination if its filters exclude tests.
	if _, err := tx.Exec(ctx, `
		INSERT INTO webhook_deliveries(id, destination_id, event_id, status, next_attempt_at)
		SELECT $1, $2, $3, 'queued', now()
		WHERE NOT EXISTS(
			SELECT 1 FROM webhook_deliveries
			WHERE destination_id = $2 AND event_id = $3
		)
	`, deliveryID, webhookID, eventID); err != nil {
		return WebhookDelivery{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WebhookDelivery{}, err
	}
	return s.WebhookDeliveryByID(ctx, serverID, webhookID, deliveryID)
}

func (s *Store) WebhookDeliveryByID(
	ctx context.Context,
	serverID, webhookID, deliveryID string,
) (WebhookDelivery, error) {
	var item WebhookDelivery
	err := s.pool.QueryRow(ctx, `
		SELECT delivery.id, delivery.destination_id, delivery.status,
		       delivery.attempts, delivery.response_status, delivery.last_error,
		       delivery.next_attempt_at, delivery.created_at, delivery.delivered_at
		FROM webhook_deliveries AS delivery
		JOIN webhook_destinations AS destination
		  ON destination.id = delivery.destination_id
		WHERE delivery.id = $1 AND destination.id = $2
		  AND destination.server_id = $3
	`, deliveryID, webhookID, serverID).Scan(
		&item.ID, &item.DestinationID, &item.Status, &item.Attempts,
		&item.ResponseStatus, &item.LastError, &item.NextAttemptAt,
		&item.CreatedAt, &item.DeliveredAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	return item, err
}

func (s *Store) RetryWebhookDelivery(
	ctx context.Context,
	serverID, webhookID, deliveryID string,
) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE webhook_deliveries AS delivery
		SET status = 'queued', next_attempt_at = now(), last_error = NULL
		FROM webhook_destinations AS destination
		WHERE delivery.id = $1
		  AND delivery.destination_id = $2
		  AND destination.id = delivery.destination_id
		  AND destination.server_id = $3
		  AND delivery.status IN ('dead', 'retrying')
	`, deliveryID, webhookID, serverID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) ClaimWebhookDelivery(ctx context.Context) (WebhookDeliveryJob, error) {
	var job WebhookDeliveryJob
	err := s.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT delivery.id
			FROM webhook_deliveries AS delivery
			WHERE (
				delivery.status IN ('queued', 'retrying')
				AND COALESCE(delivery.next_attempt_at, delivery.created_at) <= now()
			) OR (
				delivery.status = 'delivering'
				AND delivery.next_attempt_at <= now()
			)
			ORDER BY delivery.created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		),
		claimed AS (
			UPDATE webhook_deliveries AS delivery
			SET status = 'delivering', attempts = attempts + 1,
			    next_attempt_at = now() + interval '5 minutes'
			FROM candidate
			WHERE delivery.id = candidate.id
			RETURNING delivery.*
		)
		SELECT claimed.id, destination.id, destination.server_id, destination.name,
		       destination.kind, destination.url_encrypted,
		       destination.signing_secret_encrypted, claimed.attempts,
		       event.id, event.event_type, event.severity, event.summary,
		       event.data, event.created_at
		FROM claimed
		JOIN webhook_destinations AS destination ON destination.id = claimed.destination_id
		JOIN activity_events AS event ON event.id = claimed.event_id
	`).Scan(
		&job.DeliveryID, &job.DestinationID, &job.ServerID, &job.DestinationName,
		&job.Kind, &job.URLEncrypted, &job.SigningSecretEncrypted, &job.Attempts,
		&job.EventID, &job.EventType, &job.Severity, &job.Summary, &job.Data, &job.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebhookDeliveryJob{}, ErrNotFound
	}
	return job, err
}

func (s *Store) FinishWebhookDelivery(
	ctx context.Context,
	deliveryID string,
	statusCode int,
	deliveryErr error,
	retryAfter time.Duration,
	permanent bool,
) error {
	if deliveryErr == nil {
		_, err := s.pool.Exec(ctx, `
			UPDATE webhook_deliveries
			SET status = 'succeeded', response_status = $2, last_error = NULL,
			    next_attempt_at = NULL, delivered_at = now()
			WHERE id = $1
		`, deliveryID, statusCode)
		return err
	}
	state := "retrying"
	if permanent {
		state = "dead"
	}
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	if retryAfter > time.Hour {
		retryAfter = time.Hour
	}
	detail := sanitize.Text(deliveryErr.Error())
	if len(detail) > 2000 {
		detail = detail[:2000]
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE webhook_deliveries
		SET status = $2, response_status = NULLIF($3, 0), last_error = $4,
		    next_attempt_at = CASE WHEN $2 = 'retrying'
		        THEN now() + $5::interval ELSE NULL END
		WHERE id = $1
	`, deliveryID, state, statusCode, detail, fmt.Sprintf("%f seconds", retryAfter.Seconds()))
	return err
}

func webhookURLPreview(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "configured"
	}
	return parsed.Scheme + "://" + parsed.Host + "/••••••"
}
