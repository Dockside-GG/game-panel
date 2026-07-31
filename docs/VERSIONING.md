# Versioning and releases

Dockside uses Semantic Versioning in the form `MAJOR.MINOR.PATCH`.

Examples:

- `0.1.0-alpha.1` — the first public alpha release.
- `0.2.0` — an early-development feature release that may contain documented
  breaking changes.
- `0.2.1` — a compatible bug or security fix for `0.2.x`.
- `1.0.0` — the first stable public contract.
- `1.1.0` — backward-compatible features after `1.0.0`.
- `2.0.0` — breaking changes after the stable contract.

Git tags and GitHub releases use a `v` prefix, such as `v0.2.1`. The version
itself does not include the prefix.

## Early-development policy

Versions below `1.0.0` are under active development and are not recommended for
production use. Before `1.0.0`:

- `MINOR` may include breaking configuration, API, template, migration, or
  storage changes.
- `PATCH` is reserved for compatible bug, documentation, dependency, and
  security fixes.
- Breaking changes must be called out prominently in the changelog and release
  notes with migration or recovery instructions.

Pre-release builds use identifiers such as `0.1.0-alpha.1`, `0.2.0-beta.1`, or
`0.2.0-rc.1`. Development container images may use `dev` or a commit identifier
and are not releases.

## Stable policy

Starting at `1.0.0`:

- `MAJOR` increments for incompatible public API, configuration, template, or
  operational changes.
- `MINOR` increments for backward-compatible features.
- `PATCH` increments for backward-compatible fixes.

Database migrations remain forward-only unless release notes explicitly
provide a tested downgrade path.

## Release source of truth

A release consists of:

1. A reviewed commit on `main`.
2. A matching annotated Git tag such as `v0.1.0-alpha.1`.
3. A GitHub release with upgrade notes and known limitations.
4. Release artifacts or container images built from that exact tag.
5. An updated `CHANGELOG.md`.

The release version must be consistent in release metadata, container image
tags, and the web package where applicable. `DOCKSIDE_VERSION` identifies the
runtime build and defaults to `dev` for source builds.

The first published alpha establishes the initial database upgrade boundary.
After that release, the shipped base migration is immutable and schema changes
are added as new forward-only migrations.

The `develop` branch may be used as an integration branch. Release work is
promoted to `main`; urgent fixes branch from the supported release line and are
merged back into active development.

## Deciding the next version

- User-visible compatible fix only: increment `PATCH`.
- New backward-compatible capability: increment `MINOR`.
- Breaking change before `1.0.0`: increment `MINOR` and document it.
- Breaking change at or after `1.0.0`: increment `MAJOR`.
- Documentation or repository-only maintenance with no release artifact may
  remain under `Unreleased` until the next relevant release.
