# Changelog

All notable user-visible changes to Dockside are recorded here.

The project uses Semantic Versioning. While the project is in `0.y.z` early
development, minor releases may contain breaking changes when they are clearly
documented.

## Unreleased

## [0.1.0-alpha.1] - 2026-07-31

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

[Unreleased]: https://github.com/Dockside-GG/game-panel/compare/v0.1.0-alpha.1...HEAD
[0.1.0-alpha.1]: https://github.com/Dockside-GG/game-panel/releases/tag/v0.1.0-alpha.1
