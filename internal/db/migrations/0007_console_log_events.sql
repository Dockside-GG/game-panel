CREATE TABLE server_log_events_seen (
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    observed_at timestamptz NOT NULL,
    digest text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, observed_at, digest)
);
CREATE INDEX server_log_events_seen_cleanup_idx
    ON server_log_events_seen(created_at);
