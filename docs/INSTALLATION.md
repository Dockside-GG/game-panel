# Installation

> [!CAUTION]
> Dockside is early development software and is not recommended for production use. Use disposable test hosts and independent backups. The project contributors and copyright holders are not liable for data loss, downtime, security incidents, or other damages.

## Choose a release or development installation

- **Release installation:** download and extract a published release archive, then run the guided installer from that directory. Release installations use the version recorded in the archive.
- **Development/contributor installation:** clone the source repository, run the guided installer to generate secrets, then start `compose.yml` with `compose.dev.yml` and `--build`.

Do not use a moving source checkout as though it were a stable release. Development migrations and template normalization may change while the project is being tested.

Dockside supports Windows through Docker Desktop using Linux containers and Linux through Docker Engine. The same Compose services and game-server images run on both.

## Before starting

You need:

- A 64-bit host with hardware virtualization enabled.
- Docker Desktop on Windows, or Docker Engine plus Docker Compose v2 on Linux.
- At least 4 GB RAM for the panel and a small game server; practical communities should start with 8 GB or more.
- Free disk space for images, game files, and backups.
- A Discord application Client ID and Client Secret.

Choose one access mode:

| Mode | Panel listener | Public URL | Use when |
|---|---|---|---|
| Local | `127.0.0.1:8080` by default | `http://localhost:8080` | Only the host computer needs access |
| Existing proxy | `127.0.0.1:8080` by default | `https://panel.example.com` | Nginx/Caddy/Apache already serves this machine |
| Direct HTTPS | Host TCP 80 and 443 | `https://panel.example.com` | Dockside Caddy should obtain and renew TLS |

The panel does not support URL path prefixes such as `example.com/panel`. Give it a dedicated hostname or local port.

Only direct-HTTPS mode publishes host ports 80 and 443. Local and existing-proxy installations publish only the selected loopback HTTP port, so Dockside does not reserve an unrelated HTTPS port on a shared host.

## Configure Discord first

In the [Discord Developer Portal](https://discord.com/developers/applications):

1. Create or select an application.
2. Copy its Application ID.
3. On OAuth2, add this exact redirect:

   ```text
   https://panel.example.com/api/v1/auth/discord/callback
   ```

   Replace the origin with the exact URL you will enter in the installer. Local installations use, for example, `http://localhost:8080/api/v1/auth/discord/callback`.

4. Reset/copy the Client Secret if needed.

Dockside requests only `identify`. Do not add a bot, bot token, guild-members intent, or DM permission.

## Windows

1. Install Docker Desktop and select WSL 2/Linux containers.
2. Confirm Docker Desktop is running:

   ```powershell
   docker version
   docker compose version
   ```

3. Open PowerShell in the Dockside checkout and run:

   ```powershell
   powershell -ExecutionPolicy Bypass -File .\scripts\install.ps1
   ```

4. Follow the prompts. The Discord Client Secret prompt is masked.
5. For an existing reverse proxy, install only the generated `deploy\generated\nginx-dockside.conf` vhost after adding the certificate directives for that hostname.
6. Open the printed panel URL and claim the owner account with the printed one-time bootstrap token.

The installer restricts the `secrets` directory ACL to the current user, local System, and Administrators.

## Linux

Install Docker Engine and the Compose plugin from Docker’s official packages for your distribution. Add the installation user to the `docker` group only if you accept that Docker access is effectively root-equivalent.

Then:

```bash
chmod +x scripts/install.sh scripts/upgrade.sh
./scripts/install.sh
```

The installer creates files with `umask 077`, sets secret files to mode `0600`, detects the Docker socket group, validates Compose, starts the services, and waits for application readiness.

For direct HTTPS:

- Create DNS A/AAAA records for the chosen hostname.
- Allow inbound TCP 80 and 443.
- Ensure no other process already owns those ports.

For an existing proxy:

- Dockside binds only to the chosen `127.0.0.1` port.
- Other sites and their listeners are unchanged.
- Follow [Reverse proxy configuration](REVERSE_PROXY.md).

## Firewall and game ports

The guided installer asks for a game allocation range, default `20000-29999`. Do not blindly expose the whole range unless needed. Open the TCP and/or UDP ports actually assigned to servers.

The metadata PostgreSQL database, engine API, per-server database services, and control networks are not published to the host.

## First sign-in

1. Open the panel URL.
2. Choose the owner claim flow and paste the bootstrap token.
3. Complete Discord OAuth2.
4. Confirm the dashboard loads.
5. Create a disposable server from a compatible template and verify its port/firewall.
6. In Users & Access, confirm the desired Discord MFA policy.

After the owner is claimed, the bootstrap token is invalid in the database even if its local secret file remains. Keep the entire `secrets` directory protected.

## Generated files

- `.env` — non-secret installation settings and exact public URL.
- `COMPOSE_FILE` in `.env` selects the public port-443 override only for direct-HTTPS installations.
- `secrets/*` — database, encryption, session, Discord, bootstrap, and engine secrets.
- `data/servers` — engine host data root.
- `data/backups` — local backup archives.
- `deploy/generated/nginx-dockside.conf` — generated only for existing-proxy mode.

Back up `.env`, `secrets`, the Compose PostgreSQL volume, and `data/backups`. Without `encryption_key`, encrypted webhook, variable, and database credentials cannot be recovered.
