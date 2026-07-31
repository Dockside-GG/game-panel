# Security model

## Trust boundaries

- Only `engine` mounts `/var/run/docker.sock`.
- App/worker communicate with the engine over an internal Docker network using a high-entropy bearer token.
- PostgreSQL and engine ports are not published in production Compose.
- Each game server gets an isolated network and explicit published ports.
- Database services have no host port and use scoped roles.

Docker socket access is host-root-equivalent. Compromising the engine container remains high impact; keep the host, Docker, images, and Dockside updated.

## Authentication and sessions

- Discord OAuth2 authorization-code flow and single-use stored OAuth state.
- Only `identify`; no bot token.
- Session token and CSRF token are independently random and stored as hashes.
- HttpOnly, SameSite cookies; Secure outside loopback HTTP.
- Exact Origin validation on state-changing API calls.
- Absolute and idle session expiration.
- Optional Discord MFA policy.

## Authorization

- First owner claim is protected by a one-time bootstrap token.
- Invite links are random, expiring, stored as hashes, and consumed once.
- Claimed invite users remain pending until approved.
- Owner/administrator are installation-wide.
- Operator/viewer server visibility and actions require explicit role bindings.
- Role downgrades prune stronger bindings; suspension revokes sessions.

## Container controls

- Managed resources require instance/server labels before inspection, mutation, or deletion.
- Runtime containers drop all Linux capabilities and enable `no-new-privileges`.
- File helper containers have no network and use the minimum temporary identity/capabilities.
- File paths are normalized and realpath-checked inside the mounted volume; absolute traversal and escaping symlinks are rejected.
- Container replacement is allowed only while stopped and preserves the labeled data volume.
- Full deletion resolves the exact UUID-derived resource names and backup directory.

Catalog compatibility does not make third-party game images or installation scripts trusted. Template definitions are snapshotted and validated for structure, but their installer commands and referenced images originate from their respective authors. Review local templates, pin images by digest for high-assurance deployments, and avoid granting game containers access to host paths or the Docker socket.

## Secrets

Never commit `.env`, `secrets`, data, backups, database dumps, Discord webhook URLs, or generated credentials. The default `.gitignore` excludes these paths.

Back up the encryption key separately. Rotating it requires a migration that re-encrypts existing values; replacing only the file makes encrypted values unreadable.

## Reporting

Until a public disclosure address is published, report vulnerabilities privately to the repository owner and do not include production secrets or exploit data in a public issue.
