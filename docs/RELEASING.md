# Releasing Dockside

Dockside uses Semantic Versioning and publishes prerelease builds while the
project remains experimental. The first public release is
`v0.1.0-alpha.2`; its container and bundle version is `0.1.0-alpha.2` without
the Git tag's `v` prefix.

> [!CAUTION]
> A release is not a production recommendation. Alpha releases require
> disposable test hosts, independent backups, and explicit known limitations.

## Release boundary

A release is one reviewed commit on `main`, one matching annotated tag, three
container images built from that tag, versioned installation archives,
checksums, release notes, and an updated changelog.

The first published alpha freezes `internal/db/migrations/0001_initial.sql` as
the released base schema. Later database changes must use new, forward-only
migrations. Never rewrite a migration that has shipped in a release.

## Prepare the candidate

1. Work from a clean release branch based on the intended `main` commit.
2. Set `web/package.json` to the version without the `v` prefix.
3. Move user-visible changes from `Unreleased` into a dated version section in
   `CHANGELOG.md` and recreate an empty `Unreleased` section.
4. Confirm the README, installation guide, upgrade notes, compatibility
   statements, and known limitations match the candidate.
   Start from the versioned draft under `docs/releases/`.
5. Confirm no `.env`, secret, token, webhook URL, private backup, runtime data,
   personal screenshot information, or generated diagnostic archive is tracked.

## Required local verification

Run from the repository root:

```powershell
go fmt ./cmd/... ./internal/... ./templates/...
go test ./cmd/... ./internal/... ./templates/...
corepack pnpm --dir web install --frozen-lockfile
corepack pnpm --dir web lint
corepack pnpm --dir web build
powershell -ExecutionPolicy Bypass -File .\scripts\tests\install-smoke.ps1
docker compose --env-file .env config --quiet
docker compose --env-file .env -f compose.yml -f compose.dev.yml config --quiet
docker compose --env-file .env -f compose.yml -f compose.public.yml config --quiet
```

Run the Linux installer smoke test on Linux:

```bash
bash scripts/tests/install-smoke.sh
```

Build every production target using the candidate metadata:

```powershell
$Version = "0.1.0-alpha.2"
$Revision = (git rev-parse HEAD).Trim()
$BuiltAt = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$Arguments = @(
    "--build-arg", "DOCKSIDE_VERSION=$Version",
    "--build-arg", "DOCKSIDE_REVISION=$Revision",
    "--build-arg", "DOCKSIDE_BUILT_AT=$BuiltAt"
)
docker build @Arguments --target app -t "ghcr.io/dockside-gg/game-panel:$Version" .
docker build @Arguments --target worker -t "ghcr.io/dockside-gg/game-panel-worker:$Version" .
docker build @Arguments --target engine -t "ghcr.io/dockside-gg/game-panel-engine:$Version" .
```

Do not use `latest` for an alpha image.

## Acceptance matrix

The candidate is blocked until all of the following pass:

- Fresh Windows Docker Desktop and Linux Docker Engine installations.
- Localhost, existing reverse proxy, and direct HTTPS access modes.
- Discord OAuth callback, bootstrap claim, invitations, MFA, and role checks.
- Bundled Pelican/Pterodactyl compatibility loading while offline.
- Dockside-native catalog synchronization and catalog-unavailable behavior.
- At least one Steam-based and one non-Steam template, without game-specific
  logic in the panel core.
- Port allocation, internal-only ports, published ports, and collision errors.
- Start, stop, restart, kill, crash recovery, and console-triggered restart.
- stdin, RCON, and REST command transports.
- File upload, folder upload/download, editing, and rename operations.
- Backup creation, retention, download, Discord delivery, restore, and failure
  recovery.
- Schedules, template updates, manual Docker deletion reconciliation, and
  owner/operator/viewer authorization.
- Diagnostics and logs remain useful without exposing secrets.
- Version display, stable/prerelease checks, exact asset selection, checksum
  rejection, the full pre-update snapshot, successful update, and automatic
  rollback from a deliberately unhealthy candidate.

## Create installation artifacts

PowerShell:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\package-release.ps1 -Version 0.1.0-alpha.2
```

Linux:

```bash
chmod +x scripts/package-release.sh
./scripts/package-release.sh 0.1.0-alpha.2
```

The packaging scripts create versioned ZIP and TAR archives plus
`SHA256SUMS`. Each archive includes `.dockside-release`; the installer and
upgrader use it to select the exact immutable container tag instead of `dev`.

Install once from the generated archive in a new directory. Testing only a Git
checkout does not validate the published installation path.

## GitHub release automation contract

The tag workflow maintained in GitHub must:

1. Run only for `v*` tags and validate strict Semantic Versioning.
2. Verify the tag commit is contained in `main` and has passed required checks.
3. Build the app, worker, and engine images from that exact commit.
4. Pass version, revision, and UTC build time into the Docker build arguments.
5. Publish exact version and commit-SHA image tags to GHCR.
6. Generate an SPDX SBOM, SHA-256 checksums, and provenance attestations for
   release archives and image digests.
7. Attach the versioned ZIP, TAR, checksum, and SBOM assets to a draft GitHub
   release.
8. Mark `alpha`, `beta`, and `rc` versions as prereleases and never move
   `latest` to them.

The workflow should use the repository `GITHUB_TOKEN` with only the permissions
needed for contents, packages, identity tokens, and attestations. Pin actions to
reviewed versions or commit digests.

Publish only after the exact-version app, worker, and engine images are
publicly readable. Releases missing the versioned ZIP or SHA256SUMS are
intentionally invisible to the in-panel updater.

## Publish and verify

1. Merge the reviewed release candidate to `main`.
2. Create and push the annotated tag, for example
   `git tag -a v0.1.0-alpha.2 -m "Dockside v0.1.0-alpha.2"`.
3. Allow GitHub to build the images and draft release from that tag.
4. Compare asset checksums, inspect the SBOM, and verify provenance.
5. Install from the attached archive and confirm the Diagnostics page reports
   the expected version and revision.
6. Publish the GitHub release as a prerelease with installation steps, known
   limitations, backup warnings, and issue-reporting links.
7. Confirm all three GHCR packages are publicly readable without credentials.
8. From the prior supported release, use Panel Settings to perform the update,
   verify the retained snapshot manifest, and confirm stopped servers stay
   stopped while previously running servers recover.

If any artifact differs from the tagged source, discard the draft and create a
new candidate. Do not rebuild different bits under an existing immutable tag.
