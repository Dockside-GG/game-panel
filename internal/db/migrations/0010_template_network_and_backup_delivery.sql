ALTER TABLE server_ports
    ADD COLUMN environment text;

CREATE TABLE backup_deliveries (
    id uuid PRIMARY KEY,
    backup_id uuid NOT NULL UNIQUE REFERENCES backups(id) ON DELETE CASCADE,
    destination_id uuid NOT NULL REFERENCES webhook_destinations(id) ON DELETE CASCADE,
    format text NOT NULL DEFAULT 'zip' CHECK (format IN ('archive', 'zip')),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'queued', 'uploading', 'delivered', 'too_large', 'failed')),
    attempts integer NOT NULL DEFAULT 0,
    response_status integer,
    last_error text,
    delivered_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX backup_deliveries_status_idx
    ON backup_deliveries(status, updated_at);
