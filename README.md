# Dockside Game Panel

> [!CAUTION]
> **Early development software — not recommended for production use.** Dockside is actively being designed, tested, and changed. Features, migrations, container behavior, and storage formats may change without notice. Back up all game and panel data independently. The project contributors and copyright holders are not liable for lost data, service interruption, security incidents, lost revenue, or other damages arising from use of this software.

Dockside is a modern, Discord-first, Docker-native game server panel for gamers and gaming communities. It is designed for groups that organize game nights, host dedicated servers, and already coordinate players and staff through Discord.

The panel makes it easy to invite Discord users with expiring single-use links, approve their accounts, require Discord MFA where appropriate, and grant panel-wide or per-server permissions without operating a separate identity system or Discord bot.

## Project status

Dockside is currently an early development and testing project. It is suitable for local development, disposable test hosts, and contributors evaluating the architecture. It should not be treated as a stable hosting product yet.

The project is licensed under the [Apache License 2.0](LICENSE). That license includes an express disclaimer of warranties and limitation of liability.

The current source follows Semantic Versioning while it evolves toward its first
stable release. See [Versioning and releases](docs/VERSIONING.md) and the
[changelog](CHANGELOG.md).

## Highlights

- Discord OAuth2 authentication using only the `identify` scope.
- Expiring, single-use invitations with owner approval.
- Owner-selectable Discord MFA policy and scoped viewer/operator access.
- Docker-isolated game servers with start, stop, restart, kill, and supervised recovery.
- Live installation/runtime console output with stdin, RCON, and template-defined HTTP REST command transports.
- Modal file editor, file/folder rename, drag-and-drop uploads, folder downloads, checksummed backups/restores, backup retention, and subscribed Discord backup delivery.
- Cron schedules for power actions, backups, console commands, delays, and notifications.
- Per-server startup variables, custom environment variables, ports, resource limits, webhooks, activity history, and private PostgreSQL databases.
- Live host, system-container, and game-container CPU, memory, disk, network, block-I/O, process, and health telemetry.
- Bundled, offline Pelican- and Pterodactyl-compatible template snapshots, plus an independently synchronized Dockside-native catalog from [Dockside-GG/game-panel-templates](https://github.com/Dockside-GG/game-panel-templates).
- Dockside template export/upload, a visual template builder, immutable local versions, server-to-template creation, compatible JSON import, and extensions for networking, command transports, resources, and backup defaults.

## Installation choices

### Release installation

Release installation is intended for people testing a published Dockside release without editing the source.

1. Download and extract the release archive.
2. Install Docker Desktop on Windows or Docker Engine with Compose v2 on Linux.
3. Create a Discord OAuth2 application and keep its client ID and client secret available.
4. Run the guided installer from the extracted release directory.

Windows PowerShell:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\install.ps1
```

Linux:

```bash
chmod +x scripts/install.sh scripts/upgrade.sh
./scripts/install.sh
```

The installer asks for the exact local or external panel URL, deployment mode, Discord credentials, MFA policy, port range, and storage locations. It generates the remaining secrets and prints the exact Discord callback URI.

See the complete [installation guide](docs/INSTALLATION.md) and [reverse-proxy guide](docs/REVERSE_PROXY.md).

### Development and contributor installation

Development mode builds the panel, worker, and engine images from the checked-out source:

```powershell
Copy-Item .env.example .env
powershell -ExecutionPolicy Bypass -File .\scripts\install.ps1
docker compose --env-file .env -f compose.yml -f compose.dev.yml up -d --build
```

Linux:

```bash
cp .env.example .env
chmod +x scripts/install.sh
./scripts/install.sh
docker compose --env-file .env -f compose.yml -f compose.dev.yml up -d --build
```

The guided installer remains the recommended way to create development secrets and Discord settings. Select a local URL such as `http://localhost:8080` unless the development host already has a reverse proxy.

Contributor commands, test setup, repository layout, development schema workflow, frontend workflow, and troubleshooting are documented in [Developer README](docs/DEVELOPMENT.md).

## Template compatibility

Dockside ships Pelican-compatible and Pterodactyl-compatible definitions inside
the release as offline snapshots; the running panel does not fetch those
definitions from their websites. The separate public Dockside catalog contains
only original Dockside-native templates and is synchronized independently.
Dockside calls all of these definitions **templates**, not eggs, throughout its
UI and documentation.

Compatibility is implemented by normalizing every definition into an immutable
Dockside canonical format. Bundled compatibility definitions and remote
catalog-managed definitions remain read-only; customization creates a local
Dockside template that synchronization never overwrites. See
[Creating Dockside templates](docs/TEMPLATES.md).

## Architecture and technologies

Dockside uses:

- Go for the application API, background worker, and isolated Docker engine service.
- React, TypeScript, Vite, and TanStack Query for the web interface.
- PostgreSQL for panel metadata, permissions, templates, jobs, audit history, and encrypted-secret references.
- Docker Engine and Compose for the panel services and managed game workloads.
- Caddy as the bundled edge proxy, with documented support for an existing reverse proxy.
- Discord OAuth2 for identity.

Only the restricted `engine` service receives the Docker socket. The internet-facing application and background worker do not mount it. Game servers receive isolated networks and named volumes.

Read [Architecture](docs/ARCHITECTURE.md) and [Security](docs/SECURITY.md) before contributing to privileged engine code.

## Documentation

- [Installation](docs/INSTALLATION.md)
- [Developer README](docs/DEVELOPMENT.md)
- [Contributing](CONTRIBUTING.md)
- [Versioning and releases](docs/VERSIONING.md)
- [Changelog](CHANGELOG.md)
- [Creating Dockside templates](docs/TEMPLATES.md)
- [Discord authentication](docs/DISCORD_AUTH.md)
- [Reverse proxies and shared web hosts](docs/REVERSE_PROXY.md)
- [Operations, backups, upgrades, and recovery](docs/OPERATIONS.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Security model](docs/SECURITY.md)
- [Product and engineering plan](docs/PROJECT_PLAN.md)

Please use the repository's structured issue forms for reproducible bugs,
feature proposals, and template-compatibility reports. Never put credentials,
webhook URLs, tokens, private server files, or other sensitive data in an issue.

## License

Copyright 2026 Dockside.GG contributors.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).
