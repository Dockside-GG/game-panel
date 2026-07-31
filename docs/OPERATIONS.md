# Operations

## Game-server lifecycle and recovery

Dockside stores requested and observed server state separately. Starting, stopping, and
restarting therefore remain visible while Docker performs the operation. A stopped
server is always shown in red; an unexpected stop also includes a warning indicator
and an `unexpected_exit` activity event.

Automatic recovery is enabled by default and can be changed in each server's Settings
page. Dockside makes at most five attempts in a ten-minute window, using delays of 5,
15, 30, 60, and 120 seconds. A server that exhausts the policy remains stopped until
an operator intervenes. Intentional panel power actions and template-recognized
shutdown commands do not trigger recovery.

During provisioning, the Console page follows the installer container and then moves
to the runtime container. Command input stays unavailable while provisioning and
becomes available as soon as the server reaches its running state.

Interactive start, stop, and restart requests bypass background backup and schedule
work. Emergency kill has a separate direct path and remains available while a
server is starting, restarting, or stopping. Completion of an interrupted restart
cannot overwrite the later kill result.

Bundled templates are immutable. Use **Customize template** to create a local,
versioned Dockside fork; existing servers remain pinned to the version with which
they were provisioned.

## Service layout

```console
docker compose --env-file .env ps
docker compose --env-file .env logs --tail 200 app worker engine postgres gateway
```

- `gateway` terminates or receives HTTP and proxies only the app.
- `app` serves the UI/API and runs migrations.
- `worker` performs provisioning, schedules, backups, telemetry, log classification, and webhook delivery.
- `engine` is the only service with the Docker socket.
- `postgres` stores panel metadata and job state.

## Dashboard telemetry and system containers

The dashboard samples CPU, memory, load average, game-data storage, and backup storage from the Docker host. On Windows with Docker Desktop, “Docker host” means the Linux VM that runs the containers, not the complete physical Windows host.

Dockside system-container health is visible only to panel owners and administrators. The inventory is selected by the installation-specific `gg.dockside.system`, `gg.dockside.instance`, and `gg.dockside.component` labels; unrelated host containers are never returned.

The web UI cannot stop or kill system containers. It also cannot restart the app, gateway, engine, or PostgreSQL. An owner may restart only the background worker, with confirmation and an audit event. Use authenticated host access and Docker Compose for all other maintenance.

## Start, stop, and restart

```console
docker compose --env-file .env up -d
docker compose --env-file .env stop
docker compose --env-file .env restart app worker
```

Stopping the panel stack does not delete managed game-server containers. Power state continues according to Docker, but schedules and telemetry resume only when the worker returns.

## Upgrade

Windows:

```powershell
.\scripts\upgrade.ps1 -Version 1.2.3
```

Linux:

```bash
./scripts/upgrade.sh 1.2.3
```

The scripts create a timestamped SQL dump in `data/upgrades` before pulling/rebuilding and applying migrations. Secrets, game volumes, database volumes, and backup archives are preserved.

For a source checkout:

```powershell
.\scripts\upgrade.ps1 -Version dev -BuildFromSource
```

```bash
./scripts/upgrade.sh dev
```

## Backups

Panel-created game backups are checksummed tar-gzip archives under `data/backups/<server UUID>`. Backup reads run through an isolated, networkless helper with the game volume mounted read-only, so archive work does not use or pause the game container's Docker control path. Only one backup may run per server at a time.

A manual or scheduled backup can optionally deliver an attachment through an enabled Discord webhook belonging to that server. Dockside keeps the native `.tar.gz` as the restore object and can stream either that archive or a ZIP export without loading the complete backup into worker memory. Discord's default webhook attachment limit is 10 MiB; larger backups remain valid locally and are marked `too_large` instead of being truncated or repeatedly uploaded. Discord delivery is an external copy and may expose world data or configuration secrets to members who can access the destination channel.

Each manual or scheduled backup can specify 1–3650 retention days. Leaving retention blank keeps the archive until it is manually deleted. The worker removes expired, unlocked backups; locking an archive exempts it from retention cleanup. A restore requires the game server to be stopped and exact server/backup name confirmation.

The file manager downloads regular files without the browser editor's size/text restriction. Downloading a directory—or the current server directory—streams a complete `.tar.gz` archive.

Also back up:

- `.env`.
- `secrets`.
- `data/backups`.
- The panel PostgreSQL database/volume.

Create a metadata dump:

```console
docker compose --env-file .env exec -T postgres pg_dump -U dockside -d dockside > dockside-metadata.sql
```

Test restores on a non-production installation.

## Webhooks

Discord destinations must use Discord’s HTTPS webhook URL. Generic destinations must use public HTTPS and receive:

- JSON event body.
- `X-Dockside-Timestamp`.
- `X-Dockside-Signature-256: sha256=<HMAC>`.

The signing secret is shown once. Dockside blocks loopback, private, link-local, multicast, and redirect targets to reduce SSRF risk. Retry-After and bounded exponential retry are honored.

Console warnings/errors are classified from timestamped Docker logs, deduplicated, stripped of control characters, and redact common password/token patterns before activity/webhook delivery.

Routine stderr output is retained in the raw console but does not become an error event merely because a game wrote it to stderr. The console colors messages by classified severity.

## Game network allocations

New servers derive network questions from the immutable template version. A template can declare multiple TCP/UDP allocations, identify exactly one primary game port, keep administrative ports internal by default, and associate ports with startup variables. `SERVER_PORT` is the primary container listener and `SERVER_PUBLIC_PORT` is the published Docker host port.

Imported Pelican and Pterodactyl definitions do not consistently encode TCP/UDP metadata. Dockside infers only unambiguous administration/query ports, supplies curated defaults for supported definitions, and requires the owner to answer ambiguous ports or protocols during provisioning. Custom definitions can declare a `dockside.network_ports` array:

```json
{
  "dockside": {
    "network_ports": [
      {
        "name": "Game",
        "purpose": "Primary game traffic",
        "container_port": 7777,
        "protocol": "udp",
        "primary": true,
        "required": true,
        "published": true,
        "environment": "SERVER_PORT"
      }
    ]
  }
}
```

`0.0.0.0` is a bind address, not a client destination. Use `127.0.0.1:<host port>` locally, the host LAN address on the local network, or the configured public DNS address externally. Firewalls and routers must allow or forward the same protocol shown by Dockside.

## Removing the panel

To stop and remove only the panel service containers while preserving volumes and game data:

```console
docker compose --env-file .env down
```

`docker compose down -v` destroys panel metadata and Caddy volumes. Deleting `data`, `secrets`, or managed Docker volumes is irreversible. Take and verify backups first.

Use the panel’s exact-name server deletion workflow to remove a managed server container, network, game data volume, database container/volume, schedules, metadata, and backup directory together.
