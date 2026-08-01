package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/robfig/cron/v3"
)

var cronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

type ScheduleTask struct {
	ID             string          `json:"id"`
	Position       int             `json:"position"`
	TaskType       string          `json:"task_type"`
	Config         json.RawMessage `json:"config"`
	TimeoutSeconds int             `json:"timeout_seconds"`
}

type Schedule struct {
	ID                string         `json:"id"`
	ServerID          string         `json:"server_id"`
	Name              string         `json:"name"`
	CronExpression    string         `json:"cron_expression"`
	Timezone          string         `json:"timezone"`
	Enabled           bool           `json:"enabled"`
	ConcurrencyPolicy string         `json:"concurrency_policy"`
	MisfirePolicy     string         `json:"misfire_policy"`
	NextRunAt         *time.Time     `json:"next_run_at"`
	CreatedBy         *string        `json:"created_by"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	Tasks             []ScheduleTask `json:"tasks"`
}

type ScheduleTaskInput struct {
	TaskType       string
	Config         json.RawMessage
	TimeoutSeconds int
}

type CreateScheduleParams struct {
	ServerID       string
	Name           string
	CronExpression string
	Timezone       string
	Enabled        bool
	CreatedBy      string
	Tasks          []ScheduleTaskInput
}

type UpdateScheduleParams struct {
	ScheduleID     string
	ServerID       string
	Name           string
	CronExpression string
	Timezone       string
	Enabled        bool
	Tasks          []ScheduleTaskInput
}

type ScheduleRunJob struct {
	RunID      string
	ScheduleID string
	ServerID   string
	ActorID    string
	Name       string
	Timezone   string
	PlannedFor time.Time
	Tasks      []ScheduleTask
}

func ValidateSchedule(expression, timezone string) (time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, errors.New("invalid IANA timezone")
	}
	parsed, err := cronParser.Parse(strings.TrimSpace(expression))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron expression: %w", err)
	}
	return parsed.Next(time.Now().In(location)).UTC(), nil
}

func scheduleNextRun(expression, timezone string, enabled bool) (*time.Time, error) {
	next, err := ValidateSchedule(expression, timezone)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	return &next, nil
}

func (s *Store) CreateSchedule(ctx context.Context, input CreateScheduleParams) (Schedule, error) {
	nextRun, err := scheduleNextRun(input.CronExpression, input.Timezone, input.Enabled)
	if err != nil {
		return Schedule{}, err
	}
	scheduleID, err := identity.NewUUID()
	if err != nil {
		return Schedule{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Schedule{}, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM servers WHERE id = $1 AND deleted_at IS NULL)
	`, input.ServerID).Scan(&exists); err != nil {
		return Schedule{}, err
	}
	if !exists {
		return Schedule{}, ErrNotFound
	}
	var item Schedule
	err = tx.QueryRow(ctx, `
		INSERT INTO schedules(
			id, server_id, name, cron_expression, timezone, enabled,
			concurrency_policy, misfire_policy, next_run_at, created_by
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'skip', 'run_once', $7, $8)
		RETURNING id, server_id, name, cron_expression, timezone, enabled,
		          concurrency_policy, misfire_policy, next_run_at, created_by,
		          created_at, updated_at
	`, scheduleID, input.ServerID, input.Name, input.CronExpression, input.Timezone,
		input.Enabled, nextRun, input.CreatedBy,
	).Scan(
		&item.ID, &item.ServerID, &item.Name, &item.CronExpression, &item.Timezone,
		&item.Enabled, &item.ConcurrencyPolicy, &item.MisfirePolicy, &item.NextRunAt,
		&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return Schedule{}, err
	}
	item.Tasks = make([]ScheduleTask, 0, len(input.Tasks))
	for position, task := range input.Tasks {
		taskID, err := identity.NewUUID()
		if err != nil {
			return Schedule{}, err
		}
		var stored ScheduleTask
		err = tx.QueryRow(ctx, `
			INSERT INTO schedule_tasks(id, schedule_id, position, task_type, config, timeout_seconds)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, position, task_type, config, timeout_seconds
		`, taskID, scheduleID, position, task.TaskType, string(task.Config), task.TimeoutSeconds).Scan(
			&stored.ID, &stored.Position, &stored.TaskType, &stored.Config, &stored.TimeoutSeconds,
		)
		if err != nil {
			return Schedule{}, err
		}
		item.Tasks = append(item.Tasks, stored)
	}
	if err := tx.Commit(ctx); err != nil {
		return Schedule{}, err
	}
	return item, nil
}

func (s *Store) UpdateSchedule(ctx context.Context, input UpdateScheduleParams) (Schedule, error) {
	nextRun, err := scheduleNextRun(input.CronExpression, input.Timezone, input.Enabled)
	if err != nil {
		return Schedule{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Schedule{}, err
	}
	defer tx.Rollback(ctx)

	var item Schedule
	err = tx.QueryRow(ctx, `
		UPDATE schedules
		SET name = $3, cron_expression = $4, timezone = $5, enabled = $6,
		    next_run_at = $7, updated_at = now()
		WHERE id = $1 AND server_id = $2
		RETURNING id, server_id, name, cron_expression, timezone, enabled,
		          concurrency_policy, misfire_policy, next_run_at, created_by,
		          created_at, updated_at
	`, input.ScheduleID, input.ServerID, input.Name, input.CronExpression,
		input.Timezone, input.Enabled, nextRun,
	).Scan(
		&item.ID, &item.ServerID, &item.Name, &item.CronExpression, &item.Timezone,
		&item.Enabled, &item.ConcurrencyPolicy, &item.MisfirePolicy, &item.NextRunAt,
		&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Schedule{}, ErrNotFound
	}
	if err != nil {
		return Schedule{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM schedule_tasks WHERE schedule_id = $1`, input.ScheduleID); err != nil {
		return Schedule{}, err
	}
	item.Tasks = make([]ScheduleTask, 0, len(input.Tasks))
	for position, task := range input.Tasks {
		taskID, err := identity.NewUUID()
		if err != nil {
			return Schedule{}, err
		}
		var stored ScheduleTask
		err = tx.QueryRow(ctx, `
			INSERT INTO schedule_tasks(id, schedule_id, position, task_type, config, timeout_seconds)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, position, task_type, config, timeout_seconds
		`, taskID, input.ScheduleID, position, task.TaskType, string(task.Config), task.TimeoutSeconds).Scan(
			&stored.ID, &stored.Position, &stored.TaskType, &stored.Config, &stored.TimeoutSeconds,
		)
		if err != nil {
			return Schedule{}, err
		}
		item.Tasks = append(item.Tasks, stored)
	}
	if err := tx.Commit(ctx); err != nil {
		return Schedule{}, err
	}
	return item, nil
}

func (s *Store) ListSchedules(ctx context.Context, serverID string) ([]Schedule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, server_id, name, cron_expression, timezone, enabled,
		       concurrency_policy, misfire_policy, next_run_at, created_by,
		       created_at, updated_at
		FROM schedules
		WHERE server_id = $1
		ORDER BY created_at DESC
	`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Schedule, 0)
	for rows.Next() {
		var item Schedule
		if err := rows.Scan(
			&item.ID, &item.ServerID, &item.Name, &item.CronExpression, &item.Timezone,
			&item.Enabled, &item.ConcurrencyPolicy, &item.MisfirePolicy, &item.NextRunAt,
			&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.Tasks = make([]ScheduleTask, 0)
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range result {
		taskRows, err := s.pool.Query(ctx, `
			SELECT id, position, task_type, config, timeout_seconds
			FROM schedule_tasks WHERE schedule_id = $1 ORDER BY position
		`, result[index].ID)
		if err != nil {
			return nil, err
		}
		for taskRows.Next() {
			var task ScheduleTask
			if err := taskRows.Scan(&task.ID, &task.Position, &task.TaskType, &task.Config, &task.TimeoutSeconds); err != nil {
				taskRows.Close()
				return nil, err
			}
			result[index].Tasks = append(result[index].Tasks, task)
		}
		if err := taskRows.Err(); err != nil {
			taskRows.Close()
			return nil, err
		}
		taskRows.Close()
	}
	return result, nil
}

func (s *Store) DeleteSchedule(ctx context.Context, serverID, scheduleID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM schedules WHERE id = $1 AND server_id = $2`, scheduleID, serverID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetScheduleEnabled(ctx context.Context, serverID, scheduleID string, enabled bool) error {
	var expression, timezone string
	err := s.pool.QueryRow(ctx, `
		SELECT cron_expression, timezone FROM schedules WHERE id = $1 AND server_id = $2
	`, scheduleID, serverID).Scan(&expression, &timezone)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	next, err := scheduleNextRun(expression, timezone, enabled)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE schedules SET enabled = $3, next_run_at = $4, updated_at = now()
		WHERE id = $1 AND server_id = $2
	`, scheduleID, serverID, enabled, next)
	return err
}

func (s *Store) RunScheduleNow(ctx context.Context, serverID, scheduleID string) error {
	runID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	outboxID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM schedules WHERE id = $1 AND server_id = $2)
	`, scheduleID, serverID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO schedule_runs(id, schedule_id, planned_for, status)
		VALUES ($1, $2, now(), 'queued')
	`, runID, scheduleID); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"run_id": runID})
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events(id, topic, aggregate_id, payload)
		VALUES ($1, 'schedule.run', $2, $3)
	`, outboxID, runID, payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) EnqueueDueSchedules(ctx context.Context, limit int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT id, cron_expression, timezone, next_run_at
		FROM schedules
		WHERE enabled = true AND next_run_at <= now()
		ORDER BY next_run_at
		FOR UPDATE SKIP LOCKED
		LIMIT $1
	`, limit)
	if err != nil {
		return err
	}
	type due struct {
		id, expression, timezone string
		planned                  time.Time
	}
	items := make([]due, 0)
	for rows.Next() {
		var item due
		if err := rows.Scan(&item.id, &item.expression, &item.timezone, &item.planned); err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	rows.Close()
	for _, item := range items {
		location, err := time.LoadLocation(item.timezone)
		if err != nil {
			return err
		}
		parsed, err := cronParser.Parse(item.expression)
		if err != nil {
			return err
		}
		next := parsed.Next(time.Now().In(location)).UTC()
		var active bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM schedule_runs
				WHERE schedule_id = $1 AND status IN ('queued', 'running')
			)
		`, item.id).Scan(&active); err != nil {
			return err
		}
		runID, err := identity.NewUUID()
		if err != nil {
			return err
		}
		status := "queued"
		if active {
			status = "skipped"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO schedule_runs(id, schedule_id, planned_for, status, completed_at)
			VALUES ($1, $2, $3, $4, CASE WHEN $4 = 'skipped' THEN now() ELSE NULL END)
			ON CONFLICT(schedule_id, planned_for) DO NOTHING
		`, runID, item.id, item.planned, status); err != nil {
			return err
		}
		if status == "queued" {
			outboxID, err := identity.NewUUID()
			if err != nil {
				return err
			}
			payload, _ := json.Marshal(map[string]string{"run_id": runID})
			if _, err := tx.Exec(ctx, `
				INSERT INTO outbox_events(id, topic, aggregate_id, payload)
				VALUES ($1, 'schedule.run', $2, $3)
			`, outboxID, runID, payload); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE schedules SET next_run_at = $2, updated_at = now() WHERE id = $1
		`, item.id, next); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ScheduleRunJob(ctx context.Context, runID string) (ScheduleRunJob, error) {
	var job ScheduleRunJob
	err := s.pool.QueryRow(ctx, `
		UPDATE schedule_runs AS run
		SET status = 'running', started_at = now()
		FROM schedules AS schedule
		WHERE run.id = $1 AND run.schedule_id = schedule.id
		  AND run.status IN ('queued', 'running')
		RETURNING run.id, schedule.id, schedule.server_id, schedule.created_by, schedule.name,
		          schedule.timezone, run.planned_for
	`, runID).Scan(
		&job.RunID, &job.ScheduleID, &job.ServerID, &job.ActorID, &job.Name,
		&job.Timezone, &job.PlannedFor,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScheduleRunJob{}, ErrNotFound
	}
	if err != nil {
		return ScheduleRunJob{}, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, position, task_type, config, timeout_seconds
		FROM schedule_tasks WHERE schedule_id = $1 ORDER BY position
	`, job.ScheduleID)
	if err != nil {
		return ScheduleRunJob{}, err
	}
	defer rows.Close()
	job.Tasks = make([]ScheduleTask, 0)
	for rows.Next() {
		var task ScheduleTask
		if err := rows.Scan(&task.ID, &task.Position, &task.TaskType, &task.Config, &task.TimeoutSeconds); err != nil {
			return ScheduleRunJob{}, err
		}
		job.Tasks = append(job.Tasks, task)
	}
	return job, rows.Err()
}

func (s *Store) FinishScheduleRun(ctx context.Context, job ScheduleRunJob, runErr error) error {
	eventID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	status, severity, summary := "succeeded", "info", "Schedule completed"
	var detail *string
	if runErr != nil {
		status, severity, summary = "failed", "error", "Schedule failed"
		value := runErr.Error()
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
	if _, err := tx.Exec(ctx, `
		UPDATE schedule_runs
		SET status = $2, error_detail = $3, completed_at = now()
		WHERE id = $1
	`, job.RunID, status, detail); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, event_type, severity, summary, data)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, eventID, job.ServerID, "server.schedule."+status, severity, summary,
		map[string]any{"schedule_id": job.ScheduleID, "run_id": job.RunID, "name": job.Name},
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RecordScheduleNotification(
	ctx context.Context,
	serverID, actorID, scheduleID, message string,
) error {
	eventID, err := identity.NewUUID()
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO activity_events(id, server_id, actor_user_id, event_type, summary, data)
		VALUES ($1, $2, $3, 'server.schedule.notification', $4, $5)
	`, eventID, serverID, actorID, message, map[string]any{"schedule_id": scheduleID})
	return err
}
