# Dockside developer README

> [!CAUTION]
> Dockside is early development software and is not recommended for production hosting. Development migrations and storage behavior may change. Use disposable test servers and maintain independent backups. The project contributors and copyright holders are not liable for data loss or other damages.

## Prerequisites

- Git
- Docker Desktop on Windows, or Docker Engine with Compose v2 on Linux
- A Discord OAuth2 application
- Optional local Go 1.26+ and Node.js 24+ for running checks outside containers

The normal development stack is built through Docker, so Go and Node do not have to be installed directly on the host.

## Initial development setup

Clone the repository and enter its directory. Run the guided installer once so it can create `.env`, encryption keys, session keys, the engine token, PostgreSQL credentials, and the Discord client secret.

Windows:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\install.ps1
```

Linux:

```bash
chmod +x scripts/install.sh
./scripts/install.sh
```

For local development, use `http://localhost:8080` as the panel URL unless another service already owns that port. Register the callback URI printed by the installer in the Discord application.

Start or rebuild the source-based development stack:

```bash
docker compose --env-file .env -f compose.yml -f compose.dev.yml up -d --build
```

Inspect it:

```bash
docker compose --env-file .env ps
docker compose --env-file .env logs -f app worker engine
```

Stop it without removing data:

```bash
docker compose --env-file .env down
```

Do not add `--volumes` unless intentionally deleting the development database and other Compose volumes.

## Repository layout

- `cmd/app`: Discord-authenticated HTTP API and embedded React application.
- `cmd/worker`: provisioning, schedules, telemetry synchronization, recovery, backups, and webhook jobs.
- `cmd/engine`: restricted Docker and filesystem control service.
- `internal/httpapi`: browser-facing API handlers and authorization.
- `internal/store`: PostgreSQL persistence and transactional state transitions.
- `internal/engine`: Docker, console, file, backup, database, network, and telemetry operations.
- `internal/templates`: compatible template normalization and Dockside canonical validation.
- `internal/db/migrations`: ordered PostgreSQL migrations applied at startup.
- `web`: React and TypeScript application.
- `templates/sources`: release-bundled Pelican and Pterodactyl template source files.
- `scripts`: guided installation and upgrade scripts.
- `deploy`: Caddy and deployment configuration.

## Backend workflow

Format Go files:

```bash
gofmt -w ./cmd ./internal ./templates
```

Run backend tests:

```bash
go test ./cmd/... ./internal/... ./templates/...
```

The engine tests exercise privileged behavior through interfaces and fixtures. Do not make the application or worker mount the Docker socket to simplify a test.

## Frontend workflow

From `web`:

```bash
corepack enable
pnpm install
pnpm dev
```

Run static checks and a production build:

```bash
pnpm lint
pnpm build
```

The normal integrated UI is embedded into the Go application during the Docker build. The standalone Vite server is for focused frontend work.

## Database migrations

Migrations are append-only SQL files under `internal/db/migrations`.

- Never edit a migration that may already have been applied.
- Create the next numerically ordered migration.
- Make schema changes backward-readable during rolling development where practical.
- Test both a new database and an upgrade from the previous migration.
- Do not place credentials or environment-specific values in migrations.

## Template development

The running panel reads the release-bundled catalog and stored PostgreSQL versions. It does not fetch a remote catalog. Use the visual editor for normal authoring and the JSON import surface for advanced work.

See [Creating Dockside templates](TEMPLATES.md) for the complete Dockside specification and compatibility rules.

## Security boundaries

- Only `engine` may receive the Docker socket.
- The application validates session, CSRF, panel role, and server permission before calling the engine.
- Secret variables are encrypted in PostgreSQL and redacted from API responses, logs, templates, and activity records.
- REST command transports can only contact localhost inside their game container network namespace.
- File and backup paths must remain relative to the server volume.
- New Docker resources must carry the matching Dockside instance and server labels.

Read [Security](SECURITY.md) before changing authentication, secrets, uploads, archives, Docker labels, or engine endpoints.

## Release versus development

Release archives and published container tags are immutable inputs for release installation. A development checkout builds `dev` images from local source through `compose.dev.yml`.

Do not present a source checkout as a stable release. Before publishing a release:

1. Run all Go tests and frontend checks.
2. Build every Docker target.
3. Test a fresh Windows installation and a fresh Linux installation.
4. Test local, existing-reverse-proxy, and direct-HTTPS modes.
5. Test database migration from the prior release.
6. Verify the bundled template catalog.
7. Document breaking changes and backup requirements.

## Troubleshooting

- If Discord redirects fail, confirm the panel URL and callback URI exactly match the Discord application.
- If a game is unreachable, verify the host port, container port, protocol, firewall, and router forwarding.
- If a server disappears after manual Docker deletion, Dockside reconciliation intentionally removes the stale panel record after two successful Docker observations. Residual volumes and backups are not silently erased.
- If a delete operation fails, inspect the returned detail and the engine log for a helper container or volume still in use.
- On Windows, Docker runs Linux containers in a virtual machine; host telemetry represents that Docker environment rather than every Windows process.
