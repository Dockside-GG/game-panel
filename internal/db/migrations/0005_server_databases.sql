CREATE TABLE server_database_hosts (
    server_id uuid PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    engine text NOT NULL DEFAULT 'postgresql'
        CHECK (engine IN ('postgresql')),
    image_reference text NOT NULL,
    admin_password_encrypted text NOT NULL,
    container_id text,
    volume_name text,
    status text NOT NULL DEFAULT 'provisioning'
        CHECK (status IN ('provisioning', 'ready', 'failed')),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE server_databases (
    id uuid PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name text NOT NULL,
    username text NOT NULL,
    password_encrypted text NOT NULL,
    status text NOT NULL DEFAULT 'provisioning'
        CHECK (status IN ('provisioning', 'ready', 'failed', 'deleting')),
    last_error text,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (server_id, name),
    UNIQUE (server_id, username)
);
CREATE INDEX server_databases_server_idx
    ON server_databases(server_id, created_at);
