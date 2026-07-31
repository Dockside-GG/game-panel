CREATE TABLE installations (
    id uuid PRIMARY KEY,
    public_url text NOT NULL,
    discord_client_id text NOT NULL,
    bootstrap_token_hash text,
    owner_user_id uuid,
    mfa_policy text NOT NULL DEFAULT 'administrators'
        CHECK (mfa_policy IN ('off', 'administrators', 'operators', 'everyone')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id uuid PRIMARY KEY,
    discord_id text NOT NULL UNIQUE,
    username text NOT NULL,
    global_name text,
    avatar_hash text,
    locale text,
    mfa_enabled boolean NOT NULL DEFAULT false,
    mfa_checked_at timestamptz NOT NULL DEFAULT now(),
    status text NOT NULL CHECK (status IN ('pending', 'active', 'suspended', 'rejected')),
    panel_role text NOT NULL DEFAULT 'viewer'
        CHECK (panel_role IN ('owner', 'administrator', 'operator', 'viewer')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz
);

ALTER TABLE installations
    ADD CONSTRAINT installations_owner_fk
    FOREIGN KEY (owner_user_id) REFERENCES users(id) ON DELETE RESTRICT;

CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    csrf_hash text NOT NULL,
    ip_address inet,
    user_agent text,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    idle_expires_at timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);
CREATE INDEX sessions_active_user_idx ON sessions(user_id, idle_expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE invites (
    id uuid PRIMARY KEY,
    token_hash text NOT NULL UNIQUE,
    label text,
    created_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    claimed_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    claimed_at timestamptz,
    revoked_at timestamptz
);
CREATE INDEX invites_active_idx ON invites(expires_at)
    WHERE claimed_at IS NULL AND revoked_at IS NULL;

CREATE TABLE oauth_states (
    id uuid PRIMARY KEY,
    state_hash text NOT NULL UNIQUE,
    purpose text NOT NULL CHECK (purpose IN ('login', 'claim', 'invite')),
    invite_id uuid REFERENCES invites(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz
);
CREATE INDEX oauth_states_active_idx ON oauth_states(expires_at)
    WHERE consumed_at IS NULL;

CREATE TABLE roles (
    id uuid PRIMARY KEY,
    installation_id uuid NOT NULL REFERENCES installations(id) ON DELETE CASCADE,
    name text NOT NULL,
    builtin boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (installation_id, name)
);

CREATE TABLE permissions (
    name text PRIMARY KEY,
    description text NOT NULL
);

CREATE TABLE role_permissions (
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_name text NOT NULL REFERENCES permissions(name) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_name)
);

CREATE TABLE templates (
    id uuid PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    category text NOT NULL,
    source_kind text NOT NULL CHECK (source_kind IN ('pelican', 'pterodactyl', 'dockside', 'custom')),
    upstream_url text,
    author text,
    description text,
    trust_state text NOT NULL DEFAULT 'community'
        CHECK (trust_state IN ('curated', 'community', 'untrusted', 'blocked')),
    archived_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE template_versions (
    id uuid PRIMARY KEY,
    template_id uuid NOT NULL REFERENCES templates(id) ON DELETE RESTRICT,
    version integer NOT NULL,
    api_version text NOT NULL,
    source_format text NOT NULL,
    source_digest text NOT NULL,
    source_document jsonb NOT NULL,
    canonical_document jsonb NOT NULL,
    compatibility_report jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (template_id, version),
    UNIQUE (template_id, source_digest)
);

CREATE TABLE servers (
    id uuid PRIMARY KEY,
    installation_id uuid NOT NULL REFERENCES installations(id) ON DELETE RESTRICT,
    template_version_id uuid NOT NULL REFERENCES template_versions(id) ON DELETE RESTRICT,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'installing'
        CHECK (status IN ('installing', 'stopped', 'starting', 'running', 'stopping', 'degraded', 'crashed', 'suspended', 'deleting', 'failed')),
    desired_state text NOT NULL DEFAULT 'stopped'
        CHECK (desired_state IN ('running', 'stopped', 'suspended', 'deleted')),
    container_id text,
    image_reference text NOT NULL,
    image_digest text,
    primary_address text,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (installation_id, name)
);
CREATE INDEX servers_active_idx ON servers(installation_id, status)
    WHERE deleted_at IS NULL;

CREATE TABLE server_resources (
    server_id uuid PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    cpu_limit_millicores integer,
    cpu_set text,
    memory_limit_bytes bigint,
    memory_reservation_bytes bigint,
    swap_limit_bytes bigint,
    disk_limit_bytes bigint,
    pids_limit integer,
    io_weight integer,
    CHECK (cpu_limit_millicores IS NULL OR cpu_limit_millicores > 0),
    CHECK (memory_limit_bytes IS NULL OR memory_limit_bytes > 0),
    CHECK (disk_limit_bytes IS NULL OR disk_limit_bytes > 0),
    CHECK (pids_limit IS NULL OR pids_limit > 0)
);

CREATE TABLE server_runtime (
    server_id uuid PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    observed_state text NOT NULL DEFAULT 'unknown',
    health text NOT NULL DEFAULT 'unknown',
    cpu_percent double precision,
    memory_bytes bigint,
    memory_limit_bytes bigint,
    network_rx_bytes bigint,
    network_tx_bytes bigint,
    block_read_bytes bigint,
    block_write_bytes bigint,
    disk_bytes bigint,
    started_at timestamptz,
    exit_code integer,
    last_error text,
    observed_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE server_ports (
    id uuid PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    bind_address inet NOT NULL DEFAULT '0.0.0.0',
    host_port integer NOT NULL,
    container_port integer NOT NULL,
    protocol text NOT NULL CHECK (protocol IN ('tcp', 'udp')),
    purpose text,
    is_primary boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (host_port BETWEEN 1 AND 65535),
    CHECK (container_port BETWEEN 1 AND 65535),
    UNIQUE (bind_address, host_port, protocol)
);
CREATE UNIQUE INDEX server_ports_one_primary_idx ON server_ports(server_id)
    WHERE is_primary;

CREATE TABLE server_variables (
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name text NOT NULL,
    value_text text,
    value_encrypted text,
    is_secret boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, name),
    CHECK (
        (is_secret AND value_text IS NULL AND value_encrypted IS NOT NULL)
        OR
        (NOT is_secret AND value_text IS NOT NULL AND value_encrypted IS NULL)
    )
);

CREATE TABLE role_bindings (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    server_id uuid REFERENCES servers(id) ON DELETE CASCADE,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (user_id, role_id, server_id)
);

CREATE TABLE operations (
    id uuid PRIMARY KEY,
    server_id uuid REFERENCES servers(id) ON DELETE SET NULL,
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    kind text NOT NULL,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled')),
    idempotency_key text NOT NULL UNIQUE,
    progress integer NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    message text,
    error_code text,
    error_detail text,
    requested_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz
);

CREATE TABLE outbox_events (
    id uuid PRIMARY KEY,
    topic text NOT NULL,
    aggregate_id uuid,
    payload jsonb NOT NULL,
    available_at timestamptz NOT NULL DEFAULT now(),
    attempts integer NOT NULL DEFAULT 0,
    locked_at timestamptz,
    locked_by text,
    processed_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX outbox_claim_idx ON outbox_events(available_at, created_at)
    WHERE processed_at IS NULL;

CREATE TABLE schedules (
    id uuid PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name text NOT NULL,
    cron_expression text NOT NULL,
    timezone text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    concurrency_policy text NOT NULL DEFAULT 'skip'
        CHECK (concurrency_policy IN ('skip', 'queue_once', 'replace')),
    misfire_policy text NOT NULL DEFAULT 'skip'
        CHECK (misfire_policy IN ('skip', 'run_once')),
    next_run_at timestamptz,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE schedule_tasks (
    id uuid PRIMARY KEY,
    schedule_id uuid NOT NULL REFERENCES schedules(id) ON DELETE CASCADE,
    position integer NOT NULL,
    task_type text NOT NULL CHECK (task_type IN ('backup', 'power', 'command', 'delay', 'notify')),
    config jsonb NOT NULL,
    timeout_seconds integer NOT NULL DEFAULT 300,
    UNIQUE (schedule_id, position)
);

CREATE TABLE schedule_runs (
    id uuid PRIMARY KEY,
    schedule_id uuid NOT NULL REFERENCES schedules(id) ON DELETE CASCADE,
    planned_for timestamptz NOT NULL,
    status text NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'skipped', 'cancelled')),
    started_at timestamptz,
    completed_at timestamptz,
    error_detail text,
    UNIQUE (schedule_id, planned_for)
);

CREATE TABLE backups (
    id uuid PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name text NOT NULL,
    status text NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'deleting')),
    storage_kind text NOT NULL DEFAULT 'local' CHECK (storage_kind IN ('local', 's3')),
    object_key text,
    size_bytes bigint,
    sha256 text,
    include_paths jsonb NOT NULL DEFAULT '[]'::jsonb,
    exclude_globs jsonb NOT NULL DEFAULT '[]'::jsonb,
    locked boolean NOT NULL DEFAULT false,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz
);

CREATE TABLE database_providers (
    id uuid PRIMARY KEY,
    installation_id uuid NOT NULL REFERENCES installations(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('postgres', 'mariadb')),
    name text NOT NULL,
    host text NOT NULL,
    port integer NOT NULL,
    admin_secret_encrypted text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (installation_id, name)
);

CREATE TABLE game_databases (
    id uuid PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    provider_id uuid NOT NULL REFERENCES database_providers(id) ON DELETE RESTRICT,
    database_name text NOT NULL,
    username text NOT NULL,
    password_encrypted text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    UNIQUE (provider_id, database_name),
    UNIQUE (provider_id, username)
);

CREATE TABLE webhook_destinations (
    id uuid PRIMARY KEY,
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('discord', 'generic')),
    url_encrypted text NOT NULL,
    signing_secret_encrypted text,
    enabled boolean NOT NULL DEFAULT true,
    event_filters jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE webhook_deliveries (
    id uuid PRIMARY KEY,
    destination_id uuid NOT NULL REFERENCES webhook_destinations(id) ON DELETE CASCADE,
    event_id uuid NOT NULL,
    status text NOT NULL CHECK (status IN ('queued', 'delivering', 'succeeded', 'retrying', 'dead')),
    attempts integer NOT NULL DEFAULT 0,
    response_status integer,
    last_error text,
    next_attempt_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz
);

CREATE TABLE activity_events (
    id uuid PRIMARY KEY,
    server_id uuid REFERENCES servers(id) ON DELETE SET NULL,
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    event_type text NOT NULL,
    severity text NOT NULL DEFAULT 'info' CHECK (severity IN ('info', 'warning', 'error')),
    summary text NOT NULL,
    data jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX activity_server_time_idx ON activity_events(server_id, created_at DESC);

CREATE TABLE audit_events (
    id uuid PRIMARY KEY,
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    action text NOT NULL,
    target_type text NOT NULL,
    target_id text,
    request_id text,
    ip_address inet,
    user_agent text,
    data jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_time_idx ON audit_events(created_at DESC);

CREATE TABLE metric_rollups (
    server_id uuid NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    bucket_start timestamptz NOT NULL,
    resolution_seconds integer NOT NULL,
    cpu_avg double precision,
    cpu_max double precision,
    memory_avg_bytes bigint,
    memory_max_bytes bigint,
    network_rx_bytes bigint,
    network_tx_bytes bigint,
    block_read_bytes bigint,
    block_write_bytes bigint,
    PRIMARY KEY (server_id, bucket_start, resolution_seconds)
);

INSERT INTO permissions(name, description) VALUES
    ('server.view', 'View server details'),
    ('server.power.start', 'Start a server'),
    ('server.power.stop', 'Stop a server'),
    ('server.power.restart', 'Restart a server'),
    ('server.power.kill', 'Kill a server'),
    ('server.console.read', 'Read the console'),
    ('server.console.write', 'Send console commands'),
    ('server.files.read', 'Read files'),
    ('server.files.write', 'Write files'),
    ('server.files.delete', 'Delete files'),
    ('server.backups.manage', 'Create and delete backups'),
    ('server.backups.restore', 'Restore backups'),
    ('server.schedules.manage', 'Manage schedules'),
    ('server.databases.manage', 'Manage databases'),
    ('server.network.manage', 'Manage networking'),
    ('server.startup.manage', 'Manage startup configuration'),
    ('server.resources.manage', 'Manage resource limits'),
    ('server.delete', 'Delete a server'),
    ('servers.create', 'Create servers'),
    ('templates.manage', 'Manage templates'),
    ('users.manage', 'Manage users and invitations'),
    ('installation.manage', 'Manage installation settings')
ON CONFLICT DO NOTHING;
