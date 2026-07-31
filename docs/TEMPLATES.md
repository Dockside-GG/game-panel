# Creating Dockside templates

Dockside templates describe how to install, configure, start, stop, expose, command, back up, and resource-limit a game server.

The visual template builder is the recommended authoring interface. JSON
upload/import and export are available for version control, sharing, bulk
authoring, and compatible Pelican or Pterodactyl definitions. Releases embed
offline snapshots of both compatibility libraries. The panel separately
synchronizes original Dockside-native definitions from the public
`Dockside-GG/game-panel-templates` repository and stores validated immutable
versions in PostgreSQL.

## Compatibility model

Dockside accepts the familiar top-level properties used by Pelican and Pterodactyl definitions, normalizes them, and adds optional settings under a `dockside` object.

Catalog-managed templates remain immutable. Customizing one creates a local Dockside-derived template. Editing a local template creates another immutable version so existing servers retain their original provisioning definition. Exported customized templates use the Dockside format and preserve the explicit Dockside extensions.

The panel core contains no per-game or distribution-platform provisioning
rules. It does not assume that a server uses Steam or any other storefront.
Images, installers, ports and protocols, startup behavior, command transports,
backup defaults, and environment variables must be declared by the template.
Compatibility imports can recognize generic allocation-shaped variables, but
unknown port or protocol values remain unanswered for the provisioning form
instead of being guessed from a game name.

## Catalog synchronization and local templates

The app first loads its release-bundled Pelican/Pterodactyl compatibility
snapshots from embedded files. It then attempts to download `catalog.json` from
`DOCKSIDE_TEMPLATE_CATALOG_URL` during startup and every
`DOCKSIDE_TEMPLATE_SYNC_INTERVAL` (six hours by default). That remote catalog
must contain only `source_kind: "dockside"` definitions. A remote failure is
shown in catalog status and Diagnostics, but the panel starts with bundled,
local, and previously synchronized definitions.

Remote Dockside catalog updates are applied atomically. Removed remote
definitions are archived, unchanged source digests reuse their immutable
version, and changed definitions receive a new version. Bundled compatibility
definitions are not overwritten by this sync. Templates created, imported,
customized, or saved from a server are local Dockside templates and are never
replaced by catalog synchronization.

Owners and administrators can:

- force a catalog sync from the Templates page;
- upload or drag-and-drop a JSON definition;
- export any template as Dockside JSON;
- export the original compatible source for catalog-managed Pelican or Pterodactyl definitions;
- create or customize templates through the visual editor.

## Complete example

```json
{
  "name": "Example Dedicated Server",
  "author": "Community owner",
  "description": "Installs and runs an example game server.",
  "docker_images": {
    "Default": "ghcr.io/example/game-server:latest"
  },
  "startup": "./server --port={{SERVER_PORT}} --name={{SERVER_NAME}}",
  "config": {
    "stop": "shutdown"
  },
  "scripts": {
    "installation": {
      "container": "alpine:3.22",
      "entrypoint": "sh",
      "script": "set -eu\ncd /mnt/server\n# install game files here"
    }
  },
  "variables": [
    {
      "name": "Server name",
      "description": "Name shown in the game browser.",
      "env_variable": "SERVER_NAME",
      "default_value": "A DOCKSIDE.GG Panel Server",
      "user_viewable": true,
      "user_editable": true,
      "rules": "required|string|max:120",
      "field_type": "text"
    }
  ],
  "dockside": {
    "network_ports": [
      {
        "name": "Game",
        "purpose": "Primary game traffic",
        "container_port": 27015,
        "protocol": "udp",
        "primary": true,
        "required": true,
        "published": true,
        "internal_only": false,
        "environment": "SERVER_PORT"
      }
    ],
    "command_transport": {
      "type": "rcon",
      "rcon_port_env": "RCON_PORT",
      "rcon_password_env": "ADMIN_PASSWORD"
    },
    "backup_defaults": {
      "include_paths": [
        "save/",
        "config/"
      ],
      "exclude_globs": [
        "logs/",
        "*.log"
      ],
      "retention_days": 14
    },
    "resource_defaults": {
      "cpu_limit_millicores": null,
      "memory_limit_mb": null,
      "disk_alert_limit_mb": null
    }
  }
}
```

## Base properties

### `name`

Required display name. It should identify the game or server software.

### `author`

Optional author or community name.

### `description`

Short explanation shown in the template library and provisioning workflow.

### `docker_images`

Required object mapping friendly labels to Docker image references. The first sorted label becomes the default image. A server may only select images declared in its template version.

### `startup`

Required command executed in the runtime container. Variables use `{{ENVIRONMENT_NAME}}`.

Dockside-provided values include:

- `SERVER_IP`: normally `0.0.0.0`.
- `SERVER_PORT`: primary internal/container port.
- `SERVER_PUBLIC_PORT`: primary published host port.
- `SERVER_MEMORY`: configured memory limit in MiB, or `0` when unlimited.

Each named network allocation also provides its internal value as the declared environment name and its host value as `<NAME>_PUBLIC`.

### `config.stop`

Optional graceful stop command. It also helps Dockside identify intentional in-game shutdown commands.

### `scripts.installation`

- `container`: installer image.
- `entrypoint`: `sh`, `ash`, or `bash`.
- `script`: installation script executed with `/mnt/server` as the game-data directory.

The installer runs in a temporary labeled container. It must be repeatable and should fail immediately when a required download or extraction fails.

### `variables`

Each variable supports:

- `name`: friendly label.
- `description`: user-facing help.
- `env_variable`: uppercase environment name.
- `default_value`: initial value.
- `user_viewable`: whether authorized users may know the variable exists.
- `user_editable`: whether it can be changed during provisioning or in Startup.
- `rules`: pipe-separated validation rules such as `required|string|max:120`.
- `field_type`: `text`, `number`, `boolean`, `password`, or `select`.

Names suggesting passwords, tokens, or secrets are treated as secret values. Secret values are encrypted at rest, never returned to the browser after storage, and never copied from a server into a template.

## Dockside extensions

### `dockside.network_ports`

An array of portable container allocations:

- `name`: friendly allocation label.
- `purpose`: explanation shown to the user.
- `container_port`: port used by the game process.
- `protocol`: `tcp` or `udp`.
- `primary`: exactly one allocation must be primary.
- `required`: requires this port to be published and prevents deselection during provisioning.
- `published`: whether an optional public port is selected by default.
- `internal_only`: prevents this listener from ever being published on the host.
- `environment`: optional variable receiving the internal port.

Host ports are intentionally not stored in templates. Dockside assigns a conflict-free host port when creating a server and checks both its database reservations and existing Docker publications.

The visual editor presents four exposure policies:

- **Required public:** `required: true`, `published: true`, `internal_only: false`.
- **Optional, public by default:** `required: false`, `published: true`, `internal_only: false`.
- **Optional, private by default:** `required: false`, `published: false`, `internal_only: false`. The person provisioning the server may opt to publish it.
- **Internal only:** `required: false`, `published: false`, `internal_only: true`. The API rejects attempts to publish it.

The primary game port is always required and public. An internal-only port cannot be primary, required, or published.

### `dockside.command_transport`

Supported `type` values:

- `auto`: compatibility detection for RCON, otherwise stdin.
- `stdin`: write the command to the container's attached standard input.
- `rcon`: execute the configured RCON client inside the game container.
- `http_rest`: send an HTTP request to localhost inside the game container.
- `disabled`: display console output but reject entered commands.

RCON properties:

- `rcon_port_env`: environment variable containing the RCON port.
- `rcon_password_env`: environment variable containing the password.

REST properties are placed in `rest`:

- `method`: `GET`, `POST`, `PUT`, `PATCH`, or `DELETE`.
- `port`: internal HTTP port, or `0` when using `port_environment`.
- `port_environment`: optional variable containing the internal port.
- `path`: absolute local path beginning with `/`.
- `body_template`: optional request body.
- `headers`: optional header-name/value object.
- `accepted_status`: optional exact successful status codes; otherwise all 2xx responses succeed.
- `basic_auth`: optional `username` and `password_environment`. Dockside builds
  the Basic Authorization header at execution time without storing the password
  in the template.
- `routes`: optional named command routes. When routes are present, the first
  console word selects a route and the remaining text supplies its arguments.
- `timeout_seconds`: 1–60 seconds.

Each route supports:

- `command`: primary lowercase console verb.
- `aliases`: optional alternative verbs.
- `usage`: usage text returned for missing arguments.
- `min_args`: minimum whitespace-separated argument count.
- `method`, `path`, `body_template`, `headers`, and `accepted_status`: request
  values for that command. Route headers override transport headers.

REST templates may use:

- `{{COMMAND}}`: raw command, URL-encoded when used in the path.
- `{{COMMAND_JSON}}`: command encoded as a JSON string.
- `{{ARGS}}` and `{{ARGS_JSON}}`: everything after the command verb.
- `{{ARG1}}`, `{{ARG1_JSON}}`, and `{{ARG1_INT}}`: a numbered
  whitespace-separated argument as raw text, a JSON string, or a validated
  integer.
- `{{ARGS_AFTER_1}}` and `{{ARGS_AFTER_1_JSON}}`: everything after a numbered
  argument.
- `{{ENV:VARIABLE_NAME}}`: stored server environment value.

REST transports cannot specify a hostname or external URL. The request runs through a short-lived helper sharing the game container network namespace and can only contact `127.0.0.1`.

A REST transport does not need a `network_ports` entry merely so the panel can call it. Use the transport's `port` or `port_environment` and leave the HTTP service reachable only on localhost inside the game container. If operators may optionally expose that REST endpoint, add a matching optional/private network port. If it must never be reachable through a host port, mark that network port `internal_only: true`.

## Visual and raw editing

The create, customize, and edit pages support both:

- **Visual editor:** guided forms for identity, images, install/startup behavior, network exposure, variables, command transport, backup defaults, and resource defaults.
- **Raw JSON:** the complete Dockside-compatible JSON document with formatting and server-side validation.

Switching from visual to raw JSON serializes the current form. Switching back parses the JSON into the visual fields; invalid or incomplete JSON remains in the raw editor with an error so it is not silently discarded. Compatible top-level fields that the visual form does not manage, such as `features` and `file_denylist`, are preserved.

### `dockside.backup_defaults`

- `include_paths`: files or directories normally backed up. Blank means everything.
- `exclude_globs`: paths or globs excluded after includes are evaluated.
- `retention_days`: optional 1–3650 day default.

A directory path matches every descendant. The Backups UI presents these defaults as a selectable file tree and retains an advanced glob editor.

### `dockside.resource_defaults`

- `cpu_limit_millicores`: default CPU limit; `1000` equals one core.
- `memory_limit_mb`: default memory limit.
- `disk_alert_limit_mb`: monitored disk alert threshold.

Use `null` or omit a property for unlimited behavior. Dockside resource limits are blank/unlimited by default.

## Creating a template from a server

Use **Save server as template** on the server Startup page. Dockside combines the linked immutable template with current server overrides.

The new template includes images, installer settings, startup and stop commands, custom variable definitions, non-secret defaults, internal ports, command transport, backup defaults, and resource defaults.

It excludes secret values, credentials, Discord webhook URLs, host port assignments, container IDs, runtime status, backup archives, and game data.

## Validation and safety

- Use one primary network allocation.
- Declare the correct TCP or UDP protocol.
- Keep environment names uppercase and unique.
- Never place a credential directly in JSON.
- Use an internal-only REST port rather than a published host port unless external API access is intentional.
- Keep backup paths relative to `/home/container`.
- Use pinned image versions or digests for reproducible production-oriented testing.
- Test installation, first start, restart, graceful stop, command delivery, backup, restore, and a second server on the same host before sharing a template.
