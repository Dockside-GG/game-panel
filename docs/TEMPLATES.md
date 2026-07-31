# Creating Dockside templates

Dockside templates describe how to install, configure, start, stop, expose, command, back up, and resource-limit a game server.

The visual template builder is the recommended authoring interface. JSON import is available for version control, bulk authoring, and compatible Pelican or Pterodactyl definitions. The running panel stores template versions locally and does not download template definitions from a web catalog.

## Compatibility model

Dockside accepts the familiar top-level properties used by bundled Pelican and Pterodactyl definitions, normalizes them, and adds optional settings under a `dockside` object.

Bundled templates remain immutable. Customizing one creates a Dockside-derived template. Editing a custom template creates another immutable version so existing servers retain their original provisioning definition.

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
- `required`: prevents deselection during provisioning.
- `published`: whether it is published by default.
- `environment`: optional variable receiving the internal port.

Host ports are intentionally not stored in templates. Dockside assigns a conflict-free host port when creating a server and checks both its database reservations and existing Docker publications.

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
- `timeout_seconds`: 1–60 seconds.

REST templates may use:

- `{{COMMAND}}`: raw command, URL-encoded when used in the path.
- `{{COMMAND_JSON}}`: command encoded as a JSON string.
- `{{ENV:VARIABLE_NAME}}`: stored server environment value.

REST transports cannot specify a hostname or external URL. The request runs through a short-lived helper sharing the game container network namespace and can only contact `127.0.0.1`.

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
- Use an internal REST port rather than a published host port.
- Keep backup paths relative to `/home/container`.
- Use pinned image versions or digests for reproducible production-oriented testing.
- Test installation, first start, restart, graceful stop, command delivery, backup, restore, and a second server on the same host before sharing a template.
