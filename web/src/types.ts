export type SetupStatus = {
  claimed: boolean;
  public_url: string;
  discord_client_id: string;
  mfa_policy: string;
  bootstrap_enabled: boolean;
};

export type User = {
  id: string;
  discord_id: string;
  username: string;
  global_name: string | null;
  avatar_hash: string | null;
  locale: string | null;
  mfa_enabled: boolean;
  mfa_checked_at: string;
  status: "pending" | "active" | "suspended" | "rejected";
  panel_role: "owner" | "administrator" | "operator" | "viewer";
  created_at: string;
  last_login_at: string | null;
};

export type Session = {
  user: User;
  idle_expires_at: string;
  absolute_expires_at: string;
};

export type ActivityEvent = {
  id: string;
  server_id: string | null;
  actor_user_id: string | null;
  event_type: string;
  severity: string;
  summary: string;
  data: unknown;
  created_at: string;
};

export type DiagnosticEntry = {
  source: string;
  severity: "warning" | "error";
  code: string;
  summary: string;
  detail: string;
  server_id?: string | null;
  created_at: string;
};

export type SystemContainerLogs = {
  component: string;
  stdout: string;
  stderr: string;
  observed_at: string;
};

export type HostStatus = {
  connected?: boolean;
  instance_id?: string;
  engine_version?: string;
  api_version?: string;
  kernel_version?: string;
  cpus?: number;
  memory_bytes?: number;
  cpu_usage_percent?: number;
  memory_used_bytes?: number;
  memory_available_bytes?: number;
  load_1?: number;
  load_5?: number;
  load_15?: number;
  telemetry_available?: boolean;
  telemetry_scope?: "docker-host";
  data_filesystem?: FilesystemUsage;
  backup_filesystem?: FilesystemUsage;
  containers?: number;
  containers_running?: number;
  containers_stopped?: number;
  images?: number;
  operating_system?: string;
  architecture?: string;
  security_options?: string[];
  warnings?: string[];
  observed_at?: string;
};

export type FilesystemUsage = {
  path: string;
  total_bytes: number;
  used_bytes: number;
  free_bytes: number;
};

export type SystemContainer = {
  component: "gateway" | "app" | "worker" | "engine" | "postgres";
  container_id: string;
  image: string;
  state: string;
  health: string;
  cpu_percent: number;
  memory_bytes: number;
  memory_limit_bytes: number;
  network_rx_bytes: number;
  network_tx_bytes: number;
  block_read_bytes: number;
  block_write_bytes: number;
  restart_count: number;
  started_at?: string;
  observed_at: string;
  error?: string;
};

export type Dashboard = {
  servers: {
    total: number;
    running: number;
    stopped: number;
    installing: number;
    degraded: number;
    attention: number;
  };
  recent_activity: ActivityEvent[];
  host: HostStatus;
  host_error?: string;
  system_containers?: SystemContainer[];
  system_containers_error?: string;
  can_restart_worker?: boolean;
};

export type Invite = {
  id: string;
  label: string | null;
  created_by: string;
  claimed_by: string | null;
  created_at: string;
  expires_at: string;
  claimed_at: string | null;
  revoked_at: string | null;
  claimed_user?: User;
};

export type Problem = {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  request_id?: string;
  code?: string;
  retryable?: boolean;
};

export type TemplateSummary = {
  id: string;
  version_id: string;
  slug: string;
  name: string;
  category: string;
  source_kind: "pelican" | "pterodactyl" | "dockside";
  author: string | null;
  description: string;
  trust_state: string;
  version: number;
  default_image: string;
  variable_count: number;
  compatibility_report: {
    compatible: boolean;
    warnings: string[];
  };
  derived_from_version_id?: string;
  catalog_managed: boolean;
  catalog_version?: string;
};

export type TemplateCatalogStatus = {
  catalog_url: string;
  catalog_version: string | null;
  etag?: string;
  generated_at: string | null;
  checked_at: string | null;
  synced_at: string | null;
  template_count: number;
  status: "never" | "syncing" | "current" | "failed";
  last_error?: string;
};

export type TemplateVariable = {
  name: string;
  description?: string;
  environment: string;
  default_value: string;
  user_viewable: boolean;
  user_editable: boolean;
  rules?: string;
  field_type?: string;
  secret: boolean;
};

export type TemplateNetworkPort = {
  name: string;
  purpose: string;
  container_port: number;
  protocol: "tcp" | "udp" | "";
  primary: boolean;
  required: boolean;
  published: boolean;
  internal_only: boolean;
  environment?: string;
};

export type TemplateDetail = TemplateSummary & {
  canonical_document: {
    api_version: string;
    name: string;
    description?: string;
    source_kind: string;
    category: string;
    images: Record<string, string>;
    default_image: string;
    startup_command: string;
    stop_command?: string;
    install_container?: string;
    install_entrypoint?: string;
    install_script?: string;
    variables: TemplateVariable[];
    network_ports: TemplateNetworkPort[];
    command_transport: CommandTransport;
    backup_defaults: BackupDefaults;
    resource_defaults: ResourceDefaults;
    file_denylist?: string[];
    features?: unknown;
  };
  source_document: unknown;
};

export type CommandTransport = {
  type: "auto" | "stdin" | "rcon" | "http_rest" | "disabled";
  rcon_port_env?: string;
  rcon_password_env?: string;
  rest?: {
    method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
    port: number;
    port_environment?: string;
    path?: string;
    body_template?: string;
    headers?: Record<string, string>;
    accepted_status?: number[];
    timeout_seconds?: number;
    basic_auth?: {
      username: string;
      password_environment: string;
    };
    routes?: {
      command: string;
      aliases?: string[];
      usage?: string;
      min_args?: number;
      method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
      path: string;
      body_template?: string;
      headers?: Record<string, string>;
      accepted_status?: number[];
    }[];
  };
};

export type BackupDefaults = {
  include_paths?: string[];
  exclude_globs?: string[];
  retention_days?: number | null;
};

export type ResourceDefaults = {
  cpu_limit_millicores?: number | null;
  memory_limit_mb?: number | null;
  disk_alert_limit_mb?: number | null;
};

export type ServerPort = {
  id: string;
  bind_address: string;
  host_port: number;
  container_port: number;
  protocol: "tcp" | "udp";
  purpose: string;
  environment?: string;
  is_primary: boolean;
};

export type ServerResources = {
  cpu_limit_millicores: number | null;
  cpu_set: string | null;
  memory_limit_bytes: number | null;
  memory_reservation_bytes: number | null;
  swap_limit_bytes: number | null;
  disk_limit_bytes: number | null;
  pids_limit: number | null;
  io_weight: number | null;
};

export type ServerConfigurationVariable = {
  name: string;
  display_name: string;
  description: string;
  default_value: string;
  value: string | null;
  has_value: boolean;
  secret: boolean;
  user_viewable: boolean;
  user_editable: boolean;
  rules: string;
  field_type: string;
  custom: boolean;
};

export type ServerConfiguration = {
  server_id: string;
  version: number;
  status: ServerSummary["status"];
  name: string;
  description: string;
  image: string;
  images: Record<string, string>;
  template_startup: string;
  startup_override: string | null;
  effective_startup: string;
  variables: ServerConfigurationVariable[];
  ports: ServerPort[];
  resources: ServerResources;
  command_transport: CommandTransport;
  backup_defaults: BackupDefaults;
};

export type ServerFileEntry = {
  name: string;
  path: string;
  type: "file" | "directory" | "symlink" | "other";
  size: number;
  modified_at: string;
};

export type ServerFileList = {
  path: string;
  entries: ServerFileEntry[];
};

export type ServerFileContent = {
  path: string;
  content: string;
};

export type ServerBackup = {
  id: string;
  server_id: string;
  name: string;
  status: "queued" | "running" | "succeeded" | "failed" | "deleting";
  storage_kind: "local" | "s3";
  object_key: string | null;
  size_bytes: number | null;
  sha256: string | null;
  include_paths: string[];
  exclude_globs: string[];
  locked: boolean;
  retention_days: number | null;
  expires_at: string | null;
  created_by: string | null;
  created_at: string;
  completed_at: string | null;
  discord_deliveries?: {
    id: string;
    destination_id: string;
    destination_name: string;
    format: "archive" | "zip";
    status: "pending" | "queued" | "uploading" | "delivered" | "too_large" | "failed";
    attempts: number;
    response_status: number | null;
    last_error: string | null;
    delivered_at: string | null;
  }[];
};

export type ServerScheduleTask = {
  id: string;
  position: number;
  task_type: "backup" | "power" | "command" | "delay" | "notify";
  config: Record<string, unknown>;
  timeout_seconds: number;
};

export type ServerSchedule = {
  id: string;
  server_id: string;
  name: string;
  cron_expression: string;
  timezone: string;
  enabled: boolean;
  concurrency_policy: "skip" | "queue_once" | "replace";
  misfire_policy: "skip" | "run_once";
  next_run_at: string | null;
  created_by: string | null;
  created_at: string;
  updated_at: string;
  tasks: ServerScheduleTask[];
};

export type ServerWebhook = {
  id: string;
  server_id: string;
  name: string;
  kind: "discord" | "generic";
  url_preview: string;
  enabled: boolean;
  deliver_events: boolean;
  deliver_backups: boolean;
  event_filters: string[];
  has_signing_secret: boolean;
  created_by: string | null;
  created_at: string;
  updated_at: string;
};

export type WebhookDelivery = {
  id: string;
  destination_id: string;
  status: "queued" | "delivering" | "succeeded" | "retrying" | "dead";
  attempts: number;
  response_status?: number;
  last_error?: string;
  next_attempt_at?: string;
  created_at: string;
  delivered_at?: string;
};

export type ServerDatabase = {
  id: string;
  server_id: string;
  name: string;
  username: string;
  engine: "postgresql";
  host: string;
  port: number;
  status: "provisioning" | "ready" | "failed" | "deleting";
  last_error: string | null;
  created_by: string | null;
  created_at: string;
  updated_at: string;
};

export type ServerSummary = {
  id: string;
  name: string;
  description: string;
  status:
    | "installing"
    | "stopped"
    | "starting"
    | "running"
    | "restarting"
    | "stopping"
    | "degraded"
    | "suspended"
    | "deleting"
    | "failed";
  desired_state: "running" | "stopped" | "suspended" | "deleted";
  stop_reason:
    | "requested"
    | "clean_exit"
    | "unexpected_exit"
    | "startup_failure"
    | "health_failure"
    | "recovery_exhausted"
    | null;
  auto_recovery_enabled: boolean;
  recovery_attempts: number;
  container_id: string | null;
  image_reference: string;
  template_name: string;
  template_slug: string;
  template_version_id: string;
  template_version: number;
  version: number;
  primary_port: ServerPort | null;
  resources: ServerResources;
  runtime: {
    observed_state: string;
    health: string;
    cpu_percent: number | null;
    memory_bytes: number | null;
    memory_limit_bytes: number | null;
    network_rx_bytes: number | null;
    network_tx_bytes: number | null;
    block_read_bytes: number | null;
    block_write_bytes: number | null;
    disk_bytes: number | null;
    started_at: string | null;
    exit_code: number | null;
    last_error: string | null;
    observed_at: string;
  };
  created_at: string;
  updated_at: string;
};
