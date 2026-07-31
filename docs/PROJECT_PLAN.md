# Product and engineering plan

Dockside Game Panel is an early-development, Discord-first control plane for
communities that host game servers and game nights. The implementation favors a
small, auditable local architecture: the panel is installed on the same Docker
host that runs its game workloads, while each installation remains independent.

This document records the current product contract and the gates required before
the project can be recommended for production.

## Product contract

- Discord OAuth2 authentication with single-use invite links, owner approval,
  optional Discord MFA enforcement, panel roles, and per-server permissions.
- A responsive dashboard with host meters, system-container health and
  explanations, game-server resource meters, recent restore state, and audit
  history.
- Server lifecycle controls for start, graceful stop, restart, and immediate
  kill, with durable transitional states, Docker restart protection, and
  persistent desired-state recovery after clean or unexpected exits.
- Live installation and runtime output, plus template-defined stdin, RCON, HTTP
  REST, or disabled command transports.
- File and folder browsing, drag-and-drop upload, rename, text editing, and
  downloads.
- Safe, checksummed backups with selectable paths, retention, download,
  transactional restore orchestration, pre-restore rollback protection, and
  optional Discord webhook delivery.
- Cron schedules with presets and task chains for backups, power actions,
  commands, delays, and notifications.
- Startup variables, custom environment variables, resource limits that are
  unlimited by default, conflict-free network allocations, webhooks, activity
  history, and private per-server PostgreSQL databases.
- Bundled offline Pelican/Pterodactyl compatibility snapshots plus a separately
  synchronized, Dockside-only public template catalog.
- Full-page template preview and authoring, visual and raw JSON editing,
  import/export, immutable versions, server-to-template creation, and guarded
  server template updates with a mandatory backup.
- Owner-managed panel settings and privileged diagnostics for panel, worker,
  engine, Docker-control, and system-container failures.

## Architecture invariants

1. The app and worker never receive the Docker socket. Only the authenticated,
   label-verifying engine service may control Docker.
2. The core is game- and distribution-platform-neutral. It does not assume
   Steam or any other storefront and contains no game-name provisioning rules.
   Templates own images, install scripts, startup behavior, ports, protocols,
   environment variables, command transports, backup defaults, and optional
   resource defaults.
3. Game stdout and stderr are preserved as raw console output. The panel does
   not classify game-specific text such as warnings from a particular SDK.
   Structured diagnostics are reserved for failures in Dockside, its worker,
   its engine, or Docker control operations.
4. Every game server uses labeled, installation-scoped Docker resources, an
   isolated network, and its own named data volume.
5. Secrets remain encrypted at rest and are never exported into templates,
   diagnostics, audit payloads, or browser responses after storage.
6. State-changing work is durable and auditable. Provisioning, power changes,
   backups, restores, deliveries, schedules, recovery, and template updates use
   database-backed operations or outbox work.
7. Until the first release, schema changes update the single base migration and
   disposable development infrastructure is recreated. Compatibility
   migrations and legacy fallbacks begin only after a released upgrade
   boundary exists.
8. A running request is durable. Only an explicit panel Stop or Kill action may
   leave a game server offline; clean exits, crashes, and template-declared
   console shutdown commands remain supervised and restart automatically.

## Template catalog boundary

Release archives embed offline Pelican- and Pterodactyl-compatible snapshots.
They do not need a live upstream website during panel installation or runtime.

The configured remote repository is exclusively for original Dockside
templates. Catalog synchronization validates every definition before applying
the update atomically. A remote outage never removes bundled, local, or
previously synchronized definitions and does not prevent the panel from
starting.

The ignored `template-repository-scaffold/` directory is the copy-ready starting
point for the public repository. It includes a schema, validator, deterministic
catalog builder, example template, contribution/security documents, issue
forms, and CI.

## Release gates

Before the first production recommendation:

1. Complete repeated clean-install tests on current Linux Docker Engine and
   Windows Docker Desktop releases.
2. Exercise multiple representative non-Steam and Steam-based templates without
   adding game-specific rules to the core.
3. Test concurrent multi-server port allocation, crash recovery, and host
   resource pressure.
4. Perform destructive backup/restore fault injection and verify rollback from
   interrupted helpers, corrupt archives, and low disk space.
5. Test Discord webhook delivery across documented attachment limits and rate
   limiting, including operator-visible retry behavior.
6. Complete threat modeling and an independent security review of OAuth,
   authorization, archive handling, Docker boundaries, and outbound requests.
7. Establish the first supported schema-upgrade contract, signed release
   artifacts, rollback instructions, and a compatibility matrix.
8. Run accessibility, keyboard, responsive-layout, browser, and usability
   testing across every owner and delegated-user workflow.
9. Publish a release candidate, resolve all release-blocking issues, and
   document known limitations.

Until those gates are satisfied, treat Dockside as disposable development and
testing software and maintain independent backups.
