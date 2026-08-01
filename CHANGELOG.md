# Changelog

All notable user-visible changes to Dockside are recorded here.

The project uses Semantic Versioning. While the project is in `0.y.z` early
development, minor releases may contain breaking changes when they are clearly
documented.

## Unreleased

## [0.1.0-alpha.3] - 2026-07-31

### Added

- Existing schedules can now be edited through the same visual cron builder
  used to create them.
- Scheduled backup names now include the run date, time, and schedule timezone
  so every generated archive is easy to identify.

### Changed

- The schedule list now uses the full server page width and opens create/edit
  forms in a focused modal.
- Panel version information loads locally and immediately. GitHub release
  checks are manual, idle polling has been removed, and active updates remain
  live-polled until completion.
- Panel update controls now appear below Discord authentication settings and
  clearly identify development builds that must be updated from source.
- Server cards have a larger desktop minimum width and less compressed live
  metrics while retaining responsive mobile behavior.

### Fixed

- The Console command schedule task option is no longer clipped.
- Disabled schedules no longer retain a misleading next-run timestamp.
- Development builds no longer contact GitHub or retry remote update checks
  before reporting their local `dev` version.
- Scheduled backup retention remains attached to every distinct run; expired
  unlocked archives continue to be removed automatically by the worker.

## [0.1.0-alpha.2] - 2026-07-31

### Added

- Discord-first authentication, single-use invitations, MFA policy, and
  installation-wide and per-server authorization.
- Docker-native panel, worker, engine, PostgreSQL, and reverse-proxy services.
- Game-server provisioning, lifecycle controls, console, files, backups,
  schedules, databases, networking, telemetry, activity, startup, and settings.
- Locally bundled Pelican- and Pterodactyl-compatible template library.
- Dockside visual template authoring and Dockside template extensions.
- Guided Windows and Linux installation and upgrade scripts.
- Owner-only version display and verified GitHub release updates with a
  rotating panel/PostgreSQL/container/game-data recovery snapshot, health-gated
  rollout, and automatic rollback.

### Changed

- Running game servers now use Docker restart protection plus persistent
  desired-state reconciliation. Console shutdown commands restart the game,
  while explicit panel Stop and Kill actions keep it offline.
- Automatic recovery remains active with capped retry backoff instead of
  silently changing a repeatedly failing server to a stopped request.

### Security

- Docker socket access is isolated to the engine service.
- Runtime secrets are file-backed and local configuration is ignored by Git.

[Unreleased]: https://github.com/Dockside-GG/game-panel/compare/v0.1.0-alpha.3...HEAD
[0.1.0-alpha.3]: https://github.com/Dockside-GG/game-panel/releases/tag/v0.1.0-alpha.3
[0.1.0-alpha.2]: https://github.com/Dockside-GG/game-panel/releases/tag/v0.1.0-alpha.2
