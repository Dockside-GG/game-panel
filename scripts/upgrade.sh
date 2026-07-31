#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_root"
[[ -f .env ]] || { printf 'No .env file found; run scripts/install.sh first.\n' >&2; exit 1; }

version="${1:-}"
if [[ -n "$version" && ! "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]]; then
  printf 'The panel image version contains unsupported characters.\n' >&2
  exit 1
fi
backup_dir="data/upgrades"
mkdir -p "$backup_dir"
backup_file="$backup_dir/dockside-$(date -u +%Y%m%d-%H%M%S).sql"

printf 'Creating pre-upgrade database backup: %s\n' "$backup_file"
docker compose --env-file .env exec -T postgres \
  pg_dump -U dockside -d dockside --clean --if-exists > "$backup_file"
chmod 600 "$backup_file"

if [[ -n "$version" ]]; then
  if grep -q '^DOCKSIDE_VERSION=' .env; then
    sed -i.bak "s/^DOCKSIDE_VERSION=.*/DOCKSIDE_VERSION=$version/" .env
    rm -f .env.bak
  else
    printf '\nDOCKSIDE_VERSION=%s\n' "$version" >> .env
  fi
fi

effective_version="${version:-$(sed -n 's/^DOCKSIDE_VERSION=//p' .env | tail -n1)}"
if [[ "$effective_version" == "dev" ]]; then
  docker compose --env-file .env up -d --build
else
  docker compose --env-file .env pull
  docker compose --env-file .env up -d --no-build
fi

ready="false"
for _ in $(seq 1 60); do
  if docker compose --env-file .env exec -T app \
    /dockside healthcheck http://127.0.0.1:8080/health/ready >/dev/null 2>&1; then
    ready="true"
    break
  fi
  sleep 2
done
if [[ "$ready" != "true" ]]; then
  docker compose --env-file .env ps
  printf 'Upgrade health check failed. Database backup: %s\n' "$backup_file" >&2
  exit 1
fi
printf '\033[32mDockside upgrade completed.\033[0m Database backup: %s\n' "$backup_file"
