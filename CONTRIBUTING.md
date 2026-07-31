# Contributing to Dockside

Thank you for helping build Dockside. The project is in early development and
is not recommended for production use. Test changes against disposable game
servers and keep independent backups of all data.

## Before opening an issue

- Search existing issues for the same behavior.
- Use the matching structured issue form.
- Remove Discord credentials, webhook URLs, tokens, passwords, public IP
  addresses, player data, and private server files from screenshots and logs.
- Use the private security-reporting process for vulnerabilities.

Support requests and configuration questions should include the host operating
system, Docker and Compose versions, Dockside version or commit, and the
relevant template source. Do not include secrets.

## Development setup

Follow [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for the complete local setup,
repository layout, and test commands.

Create a focused branch from the current development branch:

```text
feature/short-description
fix/short-description
docs/short-description
```

Keep each pull request focused on one problem. Explain the user-visible result,
security implications, migrations, and how the change was tested.

## Required checks

Run these checks before requesting review:

```powershell
go test ./cmd/... ./internal/... ./templates/...
cd web
pnpm install --frozen-lockfile
pnpm lint
pnpm build
```

Changes involving Docker or installation should also validate the Compose
configuration and the relevant install smoke tests.

## Code and security expectations

- Preserve the app/worker/engine trust boundaries documented in
  [docs/SECURITY.md](docs/SECURITY.md).
- Never give the public app or game containers direct Docker socket access.
- Validate all file paths, port allocations, template inputs, and external
  command transports at a trust boundary.
- Include tests for security-sensitive or lifecycle behavior.
- Do not commit `.env`, `secrets/`, runtime data, backups, database dumps,
  credentials, private keys, Discord webhook URLs, or personal information.
- Do not copy implementation code from other game panels. Compatibility code
  must be independently implemented from documented JSON behavior.

## Database migrations

Migrations are append-only after they are merged. Create a new numbered
migration instead of editing one that may already exist in another
installation. Document rollback or recovery considerations in the pull request.

## Templates

See [docs/TEMPLATES.md](docs/TEMPLATES.md) before changing the canonical
template format or compatibility normalizer. Template issues should identify
the source format and template name without including server credentials.

## Releases

Dockside uses Semantic Versioning. Version-impacting changes should include an
entry under `Unreleased` in [CHANGELOG.md](CHANGELOG.md). The complete policy is
in [docs/VERSIONING.md](docs/VERSIONING.md).

By submitting a contribution, you agree that it may be distributed under the
repository's [Apache License 2.0](LICENSE).
