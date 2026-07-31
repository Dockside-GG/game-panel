ALTER TABLE backups
    ADD COLUMN retention_days integer,
    ADD COLUMN expires_at timestamptz;

ALTER TABLE backups
    ADD CONSTRAINT backups_retention_days_check
    CHECK (retention_days IS NULL OR retention_days BETWEEN 1 AND 3650);

CREATE INDEX backups_expiration_idx
    ON backups (expires_at)
    WHERE expires_at IS NOT NULL AND locked = false;
