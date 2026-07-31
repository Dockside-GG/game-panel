package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/dockside-gg/game-panel/internal/archiveutil"
	"github.com/dockside-gg/game-panel/internal/config"
	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/dockside-gg/game-panel/internal/sanitize"
	"github.com/dockside-gg/game-panel/internal/secure"
	"github.com/dockside-gg/game-panel/internal/store"
	"github.com/dockside-gg/game-panel/internal/webhooks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Worker struct {
	pool     *pgxpool.Pool
	logger   *slog.Logger
	workerID string
	store    *store.Store
	engine   *engineclient.Client
	box      *secure.Box
	webhooks *webhooks.Client
}

var (
	consoleSecretPattern = regexp.MustCompile(`(?i)\b(password|token|secret|api[_-]?key)\b\s*[:=]\s*\S+`)
	consoleBearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{8,}`)
	consoleANSIPattern   = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
)

func New(cfg config.Config, pool *pgxpool.Pool, engine *engineclient.Client, logger *slog.Logger) (*Worker, error) {
	id, err := identity.Token(16)
	if err != nil {
		return nil, err
	}
	box, err := secure.NewBox(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	return &Worker{
		pool: pool, logger: logger, workerID: id,
		store: store.New(pool), engine: engine, box: box, webhooks: webhooks.New(),
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	workTicker := time.NewTicker(time.Second)
	telemetryTicker := time.NewTicker(5 * time.Second)
	cleanupTicker := time.NewTicker(5 * time.Minute)
	defer workTicker.Stop()
	defer telemetryTicker.Stop()
	defer cleanupTicker.Stop()

	w.logger.Info("worker started", "worker_id", w.workerID)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-workTicker.C:
			if err := w.store.EnqueueDueSchedules(ctx, 20); err != nil {
				w.logger.Error("enqueue due schedules failed", "error", err)
			}
			if err := w.processOne(ctx); err != nil {
				w.logger.Error("outbox processing failed", "error", err)
			}
			if err := w.processOneWebhook(ctx); err != nil && !errors.Is(err, store.ErrNotFound) {
				w.logger.Error("webhook processing failed", "error", err)
			}
		case <-telemetryTicker.C:
			snapshots, err := w.engine.Stats(ctx)
			if err != nil {
				w.logger.Warn("runtime telemetry unavailable", "error", err)
				continue
			}
			if err := w.store.SyncRuntimeStats(ctx, snapshots); err != nil {
				w.logger.Error("runtime telemetry synchronization failed", "error", err)
			}
		case <-cleanupTicker.C:
			if err := w.cleanup(ctx); err != nil {
				w.logger.Error("cleanup failed", "error", err)
			}
			if err := w.expireBackups(ctx); err != nil {
				w.logger.Error("backup retention cleanup failed", "error", err)
			}
		}
	}
}

func (w *Worker) processOneWebhook(ctx context.Context) error {
	job, err := w.store.ClaimWebhookDelivery(ctx)
	if err != nil {
		return err
	}
	rawURL, err := w.box.Open(job.URLEncrypted, []byte("webhook:"+job.DestinationID+":url"))
	if err != nil {
		return w.store.FinishWebhookDelivery(ctx, job.DeliveryID, 0, err, 0, true)
	}
	signingSecret := ""
	if job.SigningSecretEncrypted != nil {
		signingSecret, err = w.box.Open(
			*job.SigningSecretEncrypted,
			[]byte("webhook:"+job.DestinationID+":secret"),
		)
		if err != nil {
			return w.store.FinishWebhookDelivery(ctx, job.DeliveryID, 0, err, 0, true)
		}
	}
	status, retryAfter, permanent, deliveryErr := w.webhooks.Send(
		ctx, job, rawURL, signingSecret,
	)
	if deliveryErr != nil {
		w.logger.Warn(
			"webhook delivery failed",
			"delivery_id", job.DeliveryID,
			"destination_id", job.DestinationID,
			"attempt", job.Attempts,
			"error", sanitize.Text(deliveryErr.Error()),
		)
	}
	return w.store.FinishWebhookDelivery(
		ctx, job.DeliveryID, status, deliveryErr, retryAfter, permanent,
	)
}

type event struct {
	ID       string
	Topic    string
	Payload  []byte
	Attempts int
}

type retryableError struct {
	err   error
	after time.Duration
}

func (e retryableError) Error() string { return e.err.Error() }
func (e retryableError) Unwrap() error { return e.err }

func (w *Worker) processOne(ctx context.Context) error {
	tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var claimed event
	err = tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id
			FROM outbox_events
			WHERE processed_at IS NULL
			  AND available_at <= now()
			  AND (locked_at IS NULL OR locked_at < now() - interval '5 minutes')
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE outbox_events AS event
		SET locked_at = now(), locked_by = $1, attempts = attempts + 1
		FROM candidate
		WHERE event.id = candidate.id
		RETURNING event.id, event.topic, event.payload, event.attempts
	`, w.workerID).Scan(&claimed.ID, &claimed.Topic, &claimed.Payload, &claimed.Attempts)
	if err == pgx.ErrNoRows {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	handlerErr := w.handle(ctx, claimed)
	if handlerErr == nil {
		_, err = w.pool.Exec(ctx, `
			UPDATE outbox_events
			SET processed_at = now(), locked_at = NULL, locked_by = NULL, last_error = NULL
			WHERE id = $1 AND locked_by = $2
		`, claimed.ID, w.workerID)
		return err
	}
	backoff := time.Duration(min(300, 1<<min(claimed.Attempts, 8))) * time.Second
	var retry retryableError
	if errors.As(handlerErr, &retry) && retry.after > 0 {
		backoff = retry.after
		if backoff > time.Hour {
			backoff = time.Hour
		}
	}
	_, err = w.pool.Exec(ctx, `
		UPDATE outbox_events
		SET available_at = now() + $3::interval,
		    locked_at = NULL,
		    locked_by = NULL,
		    last_error = $4
		WHERE id = $1 AND locked_by = $2
	`, claimed.ID, w.workerID, fmt.Sprintf("%f seconds", backoff.Seconds()), truncate(handlerErr.Error(), 2000))
	return err
}

func (w *Worker) handle(ctx context.Context, event event) error {
	switch event.Topic {
	case "noop":
		return nil
	case "server.provision":
		var payload struct {
			ServerID    string `json:"server_id"`
			OperationID string `json:"operation_id"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("decode server provision event: %w", err)
		}
		if err := w.store.MarkProvisionRunning(ctx, payload.ServerID, payload.OperationID); err != nil {
			return fmt.Errorf("mark provisioning running: %w", err)
		}
		job, err := w.store.ProvisionJob(ctx, payload.ServerID, payload.OperationID, w.box)
		if err != nil {
			return err
		}
		logCtx, cancelLogs := context.WithCancel(ctx)
		logDone := make(chan struct{})
		go func() {
			defer close(logDone)
			w.persistProvisionLogs(logCtx, payload.ServerID, payload.OperationID)
		}()
		result, err := w.engine.Provision(ctx, payload.ServerID, job.Request)
		cancelLogs()
		select {
		case <-logDone:
		case <-time.After(2 * time.Second):
		}
		if err != nil {
			if event.Attempts < 3 {
				return err
			}
			if markErr := w.store.MarkProvisionFailed(ctx, job, err); markErr != nil {
				return fmt.Errorf("provision failed (%v) and recording failure failed: %w", err, markErr)
			}
			return nil
		}
		if err := w.store.MarkProvisionSucceeded(ctx, job, result); err != nil {
			return fmt.Errorf("record provision success: %w", err)
		}
		return nil
	case "server.power":
		var payload struct {
			ServerID    string `json:"server_id"`
			OperationID string `json:"operation_id"`
			Action      string `json:"action"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("decode server power event: %w", err)
		}
		powerErr := w.engine.Power(ctx, payload.ServerID, payload.Action)
		if err := w.store.FinishPower(
			ctx, payload.ServerID, payload.OperationID, payload.Action, powerErr,
		); err != nil {
			return fmt.Errorf("finish server power action: %w", err)
		}
		return nil
	case "server.recover":
		var payload struct {
			ServerID string `json:"server_id"`
			Attempt  int    `json:"attempt"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("decode server recovery event: %w", err)
		}
		proceed, err := w.store.BeginRecovery(ctx, payload.ServerID, payload.Attempt)
		if err != nil {
			return fmt.Errorf("begin server recovery: %w", err)
		}
		if !proceed {
			return nil
		}
		recoveryErr := w.engine.Power(ctx, payload.ServerID, "start")
		stillDesired, err := w.store.RecoveryStillDesired(ctx, payload.ServerID)
		if err != nil {
			return fmt.Errorf("verify server recovery intent: %w", err)
		}
		if !stillDesired {
			if recoveryErr == nil {
				if stopErr := w.engine.Power(ctx, payload.ServerID, "stop"); stopErr != nil {
					return fmt.Errorf("stop cancelled server recovery: %w", stopErr)
				}
			}
			return nil
		}
		if err := w.store.FinishRecovery(
			ctx, payload.ServerID, payload.Attempt, recoveryErr,
		); err != nil {
			return fmt.Errorf("finish server recovery: %w", err)
		}
		return nil
	case "server.template_update":
		var job store.TemplateUpdateJob
		if err := json.Unmarshal(event.Payload, &job); err != nil {
			return fmt.Errorf("decode template update event: %w", err)
		}
		if err := w.store.MarkTemplateUpdateRunning(
			ctx, job, 5, "Creating mandatory full backup",
		); err != nil {
			return err
		}
		backupJob := store.BackupJob{
			BackupID: job.BackupID, ServerID: job.ServerID,
			IncludePath: []string{}, ExcludeGlob: []string{},
		}
		if err := w.store.MarkBackupRunning(ctx, job.BackupID); err != nil {
			return err
		}
		backupResult, err := w.engine.CreateBackup(
			ctx, job.ServerID, job.BackupID, nil, nil,
		)
		if err != nil {
			if event.Attempts < 3 {
				return err
			}
			_ = w.store.MarkBackupFailed(ctx, backupJob, err)
			return w.store.FinishTemplateUpdate(ctx, job, "", fmt.Errorf("mandatory pre-update backup failed: %w", err))
		}
		if err := w.store.MarkBackupSucceeded(ctx, backupJob, backupResult); err != nil {
			return err
		}
		previousConfiguration, err := w.store.ServerConfiguration(
			ctx, job.ServerID, w.box,
		)
		if err != nil {
			return w.store.FinishTemplateUpdate(ctx, job, "", err)
		}
		if err := w.store.MarkTemplateUpdateRunning(
			ctx, job, 25, "Stopping game server",
		); err != nil {
			return err
		}
		if job.WasRunning {
			if err := w.engine.Power(ctx, job.ServerID, "stop"); err != nil {
				return w.store.FinishTemplateUpdate(ctx, job, "", fmt.Errorf("stop server for template update: %w", err))
			}
		}
		failBeforeRuntimeChange := func(updateErr error) error {
			if job.WasRunning {
				if startErr := w.engine.Power(
					context.WithoutCancel(ctx), job.ServerID, "start",
				); startErr != nil {
					updateErr = fmt.Errorf(
						"%v; returning server to its prior running state failed: %w",
						updateErr, startErr,
					)
				}
			}
			return w.store.FinishTemplateUpdate(ctx, job, "", updateErr)
		}
		if err := w.store.MarkTemplateUpdateRunning(
			ctx, job, 40, "Applying new template version",
		); err != nil {
			return failBeforeRuntimeChange(err)
		}
		canonical, err := w.store.SetServerTemplateVersion(
			ctx, job.ServerID, job.TargetVersion,
		)
		if err != nil {
			return failBeforeRuntimeChange(err)
		}
		newConfiguration, err := w.store.ServerConfiguration(
			ctx, job.ServerID, w.box,
		)
		if err == nil && job.Mode == "reinstall" {
			if install := newConfiguration.InstallSpec(canonical); install != nil {
				if markErr := w.store.MarkTemplateUpdateRunning(
					ctx, job, 55, "Running updated template installer",
				); markErr != nil {
					err = markErr
				} else {
					err = w.engine.InstallExisting(ctx, job.ServerID, *install)
				}
			}
		}
		var result engineclient.ProvisionResult
		if err == nil {
			if markErr := w.store.MarkTemplateUpdateRunning(
				ctx, job, 75, "Replacing runtime container configuration",
			); markErr != nil {
				err = markErr
			} else {
				result, err = w.engine.Reconfigure(
					ctx, job.ServerID, newConfiguration.RuntimeRequest(),
				)
			}
		}
		if err != nil {
			updateErr := err
			_, _ = w.store.SetServerTemplateVersion(
				context.WithoutCancel(ctx), job.ServerID, job.PreviousVersion,
			)
			if job.Mode == "reinstall" {
				if restoreErr := w.engine.RestoreBackup(
					context.WithoutCancel(ctx), job.ServerID, job.BackupID, backupResult.SHA256,
				); restoreErr != nil {
					updateErr = fmt.Errorf("%v; file rollback failed: %w", updateErr, restoreErr)
				}
			}
			if rollback, rollbackErr := w.engine.Reconfigure(
				context.WithoutCancel(ctx), job.ServerID, previousConfiguration.RuntimeRequest(),
			); rollbackErr != nil {
				updateErr = fmt.Errorf("%v; runtime rollback failed: %w", updateErr, rollbackErr)
			} else {
				result = rollback
			}
			if job.WasRunning {
				if startErr := w.engine.Power(context.WithoutCancel(ctx), job.ServerID, "start"); startErr != nil {
					updateErr = fmt.Errorf("%v; server restart failed: %w", updateErr, startErr)
				}
			}
			return w.store.FinishTemplateUpdate(ctx, job, result.ContainerID, updateErr)
		}
		if job.WasRunning {
			if err := w.store.MarkTemplateUpdateRunning(
				ctx, job, 90, "Starting updated game server",
			); err != nil {
				w.logger.Warn(
					"record template update start progress failed",
					"server_id", job.ServerID,
					"operation_id", job.OperationID,
					"error", err,
				)
			}
			if err := w.engine.Power(ctx, job.ServerID, "start"); err != nil {
				return w.store.FinishTemplateUpdate(ctx, job, result.ContainerID, err)
			}
		}
		return w.store.FinishTemplateUpdate(ctx, job, result.ContainerID, nil)
	case "backup.create":
		var payload struct {
			BackupID string `json:"backup_id"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("decode backup event: %w", err)
		}
		job, err := w.store.BackupJob(ctx, payload.BackupID)
		if err != nil {
			return err
		}
		if err := w.store.MarkBackupRunning(ctx, job.BackupID); err != nil {
			return fmt.Errorf("mark backup running: %w", err)
		}
		result, err := w.engine.CreateBackup(
			ctx, job.ServerID, job.BackupID, job.IncludePath, job.ExcludeGlob,
		)
		if err != nil {
			if event.Attempts < 3 {
				return err
			}
			if markErr := w.store.MarkBackupFailed(ctx, job, err); markErr != nil {
				return fmt.Errorf("backup failed (%v) and recording failure failed: %w", err, markErr)
			}
			return nil
		}
		if err := w.store.MarkBackupSucceeded(ctx, job, result); err != nil {
			return fmt.Errorf("record backup success: %w", err)
		}
		return nil
	case "backup.restore":
		var job store.BackupRestoreJob
		if err := json.Unmarshal(event.Payload, &job); err != nil {
			return fmt.Errorf("decode backup restore event: %w", err)
		}
		if err := w.store.MarkBackupRestoreRunning(
			ctx, job, 10, "Preparing server for restore",
		); err != nil {
			return err
		}
		var restoreErr error
		if job.WasRunning {
			if err := w.store.MarkBackupRestoreRunning(
				ctx, job, 20, "Stopping game server",
			); err != nil {
				return err
			}
			restoreErr = w.engine.Power(ctx, job.ServerID, "stop")
		}
		if restoreErr == nil {
			if err := w.store.MarkBackupRestoreRunning(
				ctx, job, 45, "Verifying archive and restoring files",
			); err != nil {
				restoreErr = err
			} else {
				restoreErr = w.engine.RestoreBackup(
					ctx, job.ServerID, job.BackupID, job.SHA256,
				)
			}
		}
		if job.WasRunning {
			if err := w.store.MarkBackupRestoreRunning(
				ctx, job, 85, "Starting game server",
			); err != nil {
				w.logger.Warn(
					"record backup restore start progress failed",
					"server_id", job.ServerID,
					"operation_id", job.OperationID,
					"error", err,
				)
			}
			if startErr := w.engine.Power(ctx, job.ServerID, "start"); startErr != nil {
				if restoreErr == nil {
					restoreErr = fmt.Errorf("files restored but server restart failed: %w", startErr)
				} else {
					restoreErr = fmt.Errorf("%v; returning server to running state failed: %w", restoreErr, startErr)
				}
			}
		}
		return w.store.FinishBackupRestore(ctx, job, restoreErr)
	case "backup.discord_delivery":
		var payload struct {
			DeliveryID string `json:"delivery_id"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("decode backup delivery event: %w", err)
		}
		job, err := w.store.BeginBackupDelivery(ctx, payload.DeliveryID)
		if err != nil {
			return err
		}
		rawURL, err := w.box.Open(
			job.URLEncrypted, []byte("webhook:"+job.DestinationID+":url"),
		)
		if err != nil {
			return w.store.FinishBackupDelivery(ctx, job.DeliveryID, "failed", 0, err)
		}
		download, err := w.engine.OpenBackup(ctx, job.ServerID, job.BackupID)
		if err != nil {
			if job.Attempts >= 10 {
				return w.store.FinishBackupDelivery(ctx, job.DeliveryID, "failed", 0, err)
			}
			_ = w.store.FinishBackupDelivery(ctx, job.DeliveryID, "queued", 0, err)
			return err
		}
		defer download.Body.Close()
		var content io.Reader = download.Body
		filename := archiveutil.SafeFilename(job.BackupName) + ".tar.gz"
		if job.Format == "zip" {
			zipped := archiveutil.ZipTarGzip(download.Body)
			defer zipped.Close()
			content = zipped
			filename = archiveutil.SafeFilename(job.BackupName) + ".zip"
		}
		status, retryAfter, permanent, deliveryErr := w.webhooks.SendBackup(
			ctx, job, rawURL, filename, content,
		)
		if deliveryErr == nil {
			return w.store.FinishBackupDelivery(
				ctx, job.DeliveryID, "delivered", status, nil,
			)
		}
		if permanent || job.Attempts >= 10 {
			state := "failed"
			if status == http.StatusRequestEntityTooLarge {
				state = "too_large"
			}
			return w.store.FinishBackupDelivery(
				ctx, job.DeliveryID, state, status, deliveryErr,
			)
		}
		_ = w.store.FinishBackupDelivery(
			ctx, job.DeliveryID, "queued", status, deliveryErr,
		)
		return retryableError{err: deliveryErr, after: retryAfter}
	case "schedule.run":
		var payload struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("decode schedule run event: %w", err)
		}
		job, err := w.store.ScheduleRunJob(ctx, payload.RunID)
		if err != nil {
			return err
		}
		runErr := w.executeSchedule(ctx, job)
		if err := w.store.FinishScheduleRun(ctx, job, runErr); err != nil {
			return fmt.Errorf("finish schedule run: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("no handler registered for topic %q", event.Topic)
	}
}

func (w *Worker) persistProvisionLogs(
	ctx context.Context,
	serverID, operationID string,
) {
	var stream io.ReadCloser
	for attempt := 0; attempt < 80; attempt++ {
		opened, err := w.engine.OpenConsole(ctx, serverID, 5000)
		if err == nil {
			stream = opened
			break
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	if stream == nil {
		return
	}
	defer stream.Close()
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var frame struct {
			Stream     string    `json:"stream"`
			Phase      string    `json:"phase"`
			Message    string    `json:"message"`
			ObservedAt time.Time `json:"observed_at"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil ||
			frame.Phase == "runtime" || frame.Message == "" {
			continue
		}
		if frame.ObservedAt.IsZero() {
			frame.ObservedAt = time.Now().UTC()
		}
		if err := w.store.AppendOperationLog(
			ctx, operationID, serverID, frame.Phase, frame.Stream,
			sanitizeConsoleLog(frame.Message), frame.ObservedAt,
		); err != nil && ctx.Err() == nil {
			w.logger.Warn("persist provision log failed", "server_id", serverID, "error", err)
		}
	}
}

func (w *Worker) executeSchedule(ctx context.Context, job store.ScheduleRunJob) error {
	for _, task := range job.Tasks {
		timeout := time.Duration(task.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 5 * time.Minute
		}
		taskCtx, cancel := context.WithTimeout(ctx, timeout)
		err := w.executeScheduleTask(taskCtx, job, task)
		cancel()
		if err != nil {
			return fmt.Errorf("task %d (%s): %w", task.Position+1, task.TaskType, err)
		}
	}
	return nil
}

func (w *Worker) executeScheduleTask(
	ctx context.Context,
	job store.ScheduleRunJob,
	task store.ScheduleTask,
) error {
	switch task.TaskType {
	case "backup":
		var config struct {
			Name          string   `json:"name"`
			IncludePaths  []string `json:"include_paths"`
			ExcludeGlobs  []string `json:"exclude_globs"`
			RetentionDays *int     `json:"retention_days"`
		}
		if err := json.Unmarshal(task.Config, &config); err != nil {
			return err
		}
		_, err := w.store.CreateBackup(
			ctx, job.ServerID, job.ActorID, config.Name, config.IncludePaths,
			config.ExcludeGlobs, config.RetentionDays,
		)
		return err
	case "power":
		var config struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(task.Config, &config); err != nil {
			return err
		}
		_, err := w.store.RequestPower(ctx, job.ServerID, job.ActorID, config.Action)
		return err
	case "command":
		var config struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(task.Config, &config); err != nil {
			return err
		}
		if _, err := w.engine.Command(ctx, job.ServerID, config.Command); err != nil {
			return err
		}
		return w.store.RecordConsoleCommand(ctx, job.ServerID, job.ActorID, len(config.Command))
	case "delay":
		var config struct {
			Seconds int `json:"seconds"`
		}
		if err := json.Unmarshal(task.Config, &config); err != nil {
			return err
		}
		timer := time.NewTimer(time.Duration(config.Seconds) * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	case "notify":
		var config struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(task.Config, &config); err != nil {
			return err
		}
		return w.store.RecordScheduleNotification(
			ctx, job.ServerID, job.ActorID, job.ScheduleID, config.Message,
		)
	default:
		return fmt.Errorf("unsupported schedule task %q", task.TaskType)
	}
}

func (w *Worker) cleanup(ctx context.Context) error {
	_, err := w.pool.Exec(ctx, `
		DELETE FROM oauth_states WHERE expires_at < now() - interval '1 day';
		DELETE FROM sessions
		WHERE COALESCE(revoked_at, absolute_expires_at) < now() - interval '30 days';
		DELETE FROM operation_log_entries
		WHERE observed_at < now() - interval '30 days';
	`)
	return err
}

func (w *Worker) expireBackups(ctx context.Context) error {
	items, err := w.store.ExpiredBackups(ctx, 20)
	if err != nil {
		return err
	}
	for _, item := range items {
		previous, err := w.store.BeginBackupDeletion(ctx, item.ServerID, item.BackupID)
		if errors.Is(err, store.ErrConflict) {
			continue
		}
		if err != nil {
			return err
		}
		if err := w.engine.DeleteBackup(ctx, item.ServerID, item.BackupID); err != nil {
			_ = w.store.CancelBackupDeletion(ctx, item.ServerID, item.BackupID, previous)
			return err
		}
		if err := w.store.DeleteBackupRecord(ctx, item.ServerID, item.BackupID, ""); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeConsoleLog(message string) string {
	message = consoleANSIPattern.ReplaceAllString(message, "")
	message = strings.Map(func(character rune) rune {
		if character == '\t' || character >= ' ' {
			return character
		}
		return -1
	}, message)
	message = consoleSecretPattern.ReplaceAllString(message, "$1=[REDACTED]")
	message = consoleBearerPattern.ReplaceAllString(message, "Bearer [REDACTED]")
	message = strings.TrimSpace(message)
	if len(message) > 1000 {
		message = message[:1000]
	}
	return message
}

func truncate(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}
