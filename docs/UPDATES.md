# Panel updates and recovery snapshots

Dockside release installations can check for and apply published updates from
**Panel settings → Panel version & updates**. Only the installation owner can
see or start this operation. Development and test Compose projects are
deliberately excluded; contributors update those installations from Git.

> [!CAUTION]
> An update causes downtime and may require substantial temporary disk space.
> Maintain an independent, tested backup outside this host. The updater's local
> recovery snapshot is a rollback aid, not a disaster-recovery replacement.

## Trusted release contract

The panel queries only the public Dockside-GG/game-panel GitHub releases API.
Draft releases, invalid Semantic Versions, and releases without both required
assets are ignored. Before the panel stops any workload, the isolated update
helper:

1. downloads the versioned ZIP and SHA256SUMS over HTTPS;
2. restricts redirects to GitHub release-asset hosts;
3. verifies the ZIP's SHA-256 digest;
4. verifies the archive root and embedded release version; and
5. accepts only the documented release-managed paths from the archive.

The updater writes the exact release version to .env and pulls immutable app,
worker, and engine image tags. It never applies a latest tag or a URL supplied
directly by the browser.

## Update sequence

1. The release is downloaded, verified, and staged while the panel stays live.
2. The worker, running game servers, and app are stopped to quiesce writes.
3. A new private recovery snapshot is created under
   data/backups/panel-updates/.partial-(operation ID).
4. Only after every snapshot artifact and checksum succeeds is it promoted to
   data/backups/panel-updates/pre-update.
5. An older completed snapshot is removed only after the new snapshot is
   promoted. Failed partial snapshots never replace the last valid snapshot.
6. Release-managed files are applied and exact container tags are pulled.
7. PostgreSQL, the engine, app, and gateway must become healthy before the new
   worker starts. Servers observed as stopped before the update remain stopped.
8. The retained status and recovery path are shown in Panel Settings after the
   app reconnects.

Do not stop Docker, restart the host, edit .env, or remove containers while an
update is running.

## Recovery snapshot contents

The private pre-update directory is created with restrictive permissions and
contains:

- **panel-config.tar.gz** — installed panel files, .env, secrets, Compose
  definitions, proxy configuration, scripts, and documentation;
- **postgres.sql** — a consistent plain-SQL dump created after writers stop;
- **game-servers.tar.gz** — every managed game-server named volume, keyed by
  server UUID;
- **system-volumes.tar.gz** — non-database system volumes such as Caddy
  certificate and configuration state;
- **container-images.tar.gz** — a Docker image archive for panel and managed
  game-server images;
- **containers.json** — inspected configuration for managed system and game
  containers;
- **manifest.json** — versions, timestamps, notes, and SHA-256 digests for
  every snapshot artifact.

The most recent helper log remains beside the snapshot as update-helper.log.
It can contain host paths and operational details, so treat it as private.

Existing game backups under data/backups/(server UUID) remain in place and are
not copied recursively into the update snapshot.

The image archive and container manifest are used instead of docker commit.
Runtime commits do not capture named volumes and can preserve an unsafe,
inconsistent process state.

## Automatic rollback

If the verified release cannot be applied or its core services fail health
checks, the helper attempts to:

1. stop any new app and worker processes;
2. reload the saved container images;
3. restore the prior installed files and .env;
4. restore the PostgreSQL dump;
5. recreate the prior Compose services; and
6. resume only workloads that were running before the update.

Panel Settings records whether rollback completed. If both update and rollback
fail, do not retry or delete the snapshot. Collect the update-helper logs and
use the snapshot with an administrator who understands Docker volumes and
PostgreSQL recovery.

## Release channel

Stable mode ignores prereleases. During the alpha period, owners can enable
**Include alpha, beta, and release-candidate updates**. Selecting that option
does not downgrade the panel and does not install an older prerelease.

## Publisher requirements

An update is not discoverable until all three exact-version GHCR images are
public and the GitHub release contains:

- the versioned Dockside ZIP;
- the versioned TAR.GZ for manual installation;
- SHA256SUMS; and
- release notes describing migrations, downtime, known limitations, and any
  manual recovery considerations.

Publish the release only after installing it from the final archive and testing
an update from the previous supported version.
