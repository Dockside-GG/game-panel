# Changelog

All notable user-visible changes to Dockside are recorded here.

The project uses Semantic Versioning. While the project is in `0.y.z` early
development, minor releases may contain breaking changes when they are clearly
documented.

## Unreleased

### Added

- Discord-first authentication, single-use invitations, MFA policy, and
  installation-wide and per-server authorization.
- Docker-native panel, worker, engine, PostgreSQL, and reverse-proxy services.
- Game-server provisioning, lifecycle controls, console, files, backups,
  schedules, databases, networking, telemetry, activity, startup, and settings.
- Locally bundled Pelican- and Pterodactyl-compatible template library.
- Dockside visual template authoring and Dockside template extensions.
- Guided Windows and Linux installation and upgrade scripts.

### Security

- Docker socket access is isolated to the engine service.
- Runtime secrets are file-backed and local configuration is ignored by Git.

[Unreleased]: https://github.com/Dockside-GG/game-panel/compare/main...HEAD
