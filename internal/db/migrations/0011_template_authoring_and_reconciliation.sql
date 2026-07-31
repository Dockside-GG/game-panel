CREATE TABLE server_variable_definitions (
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    environment text NOT NULL,
    display_name text NOT NULL,
    description text NOT NULL DEFAULT '',
    default_value text NOT NULL DEFAULT '',
    user_viewable boolean NOT NULL DEFAULT true,
    user_editable boolean NOT NULL DEFAULT true,
    rules text NOT NULL DEFAULT '',
    field_type text NOT NULL DEFAULT 'text',
    secret boolean NOT NULL DEFAULT false,
    position integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, environment),
    CONSTRAINT server_variable_definitions_environment_check
        CHECK (environment ~ '^[A-Z_][A-Z0-9_]{0,127}$'),
    CONSTRAINT server_variable_definitions_field_type_check
        CHECK (field_type IN ('text', 'number', 'boolean', 'password', 'select')),
    CONSTRAINT server_variable_definitions_length_check
        CHECK (
            length(display_name) BETWEEN 1 AND 120
            AND length(description) <= 1000
            AND length(default_value) <= 65536
            AND length(rules) <= 1000
        )
);

ALTER TABLE servers
    ADD COLUMN missing_container_observations integer NOT NULL DEFAULT 0,
    ADD COLUMN externally_deleted_at timestamptz,
    ADD CONSTRAINT servers_missing_container_observations_check
        CHECK (missing_container_observations BETWEEN 0 AND 10);

CREATE INDEX servers_reconciliation_idx
    ON servers(updated_at)
    WHERE deleted_at IS NULL AND container_id IS NOT NULL;
