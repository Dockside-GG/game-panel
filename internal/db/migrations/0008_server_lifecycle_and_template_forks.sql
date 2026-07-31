ALTER TABLE servers DROP CONSTRAINT IF EXISTS servers_status_check;

UPDATE servers
SET status = 'stopped'
WHERE status = 'crashed';

ALTER TABLE servers
    ADD CONSTRAINT servers_status_check
    CHECK (status IN (
        'installing', 'stopped', 'starting', 'running', 'restarting',
        'stopping', 'degraded', 'suspended', 'deleting', 'failed'
    ));

ALTER TABLE servers
    ADD COLUMN stop_reason text,
    ADD COLUMN auto_recovery_enabled boolean NOT NULL DEFAULT true,
    ADD COLUMN recovery_attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN recovery_window_started_at timestamptz,
    ADD COLUMN recovery_not_before timestamptz,
    ADD CONSTRAINT servers_stop_reason_check
        CHECK (stop_reason IS NULL OR stop_reason IN (
            'requested', 'clean_exit', 'unexpected_exit', 'startup_failure',
            'health_failure', 'recovery_exhausted'
        )),
    ADD CONSTRAINT servers_recovery_attempts_check
        CHECK (recovery_attempts BETWEEN 0 AND 5);

UPDATE servers
SET stop_reason = CASE
    WHEN desired_state = 'stopped' THEN 'requested'
    ELSE 'unexpected_exit'
END
WHERE status = 'stopped';

ALTER TABLE server_runtime
    ADD COLUMN command_ready boolean NOT NULL DEFAULT false;

CREATE TABLE operation_log_entries (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operation_id uuid NOT NULL REFERENCES operations(id) ON DELETE CASCADE,
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    phase text NOT NULL,
    stream text NOT NULL CHECK (stream IN ('system', 'stdout', 'stderr')),
    message text NOT NULL,
    observed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX operation_log_entries_server_sequence_idx
    ON operation_log_entries(server_id, sequence);

ALTER TABLE templates
    ADD COLUMN derived_from_version_id uuid REFERENCES template_versions(id) ON DELETE SET NULL;
