#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_root"

release_version=""
if [[ -f .dockside-release ]]; then
  release_version="$(tr -d '\r\n' < .dockside-release)"
  if [[ ! "$release_version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]]; then
    printf 'The release bundle contains invalid version metadata.\n' >&2
    exit 1
  fi
fi

die() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

read_default() {
  local prompt="$1" default="$2" value
  read -r -p "$prompt [$default]: " value
  printf '%s' "${value:-$default}"
}

random_token() {
  local bytes="$1"
  openssl rand -base64 "$bytes" | tr '+/' '-_' | tr -d '=\r\n'
}

write_secret() {
  local path="$1" value="$2"
  printf '%s' "$value" > "$path"
  chmod 600 "$path"
}

printf '\n\033[36mDockside.GG Game Panel guided installer\033[0m\n'
printf 'This installer creates local secrets and starts the Docker Compose stack.\n\n'

[[ ! -e .env ]] || die ".env already exists. Use scripts/upgrade.sh; this installer will not overwrite secrets."
command -v docker >/dev/null 2>&1 || die "Docker Engine is not installed."
command -v openssl >/dev/null 2>&1 || die "OpenSSL is required to generate secrets."
docker version >/dev/null || die "Docker Engine is not running or this user cannot access it."
docker compose version >/dev/null || die "Docker Compose v2 is required."

printf 'How will the panel be reached?\n'
printf '  1) Local computer only (HTTP on localhost)\n'
printf '  2) Behind an existing Nginx/Caddy/Apache site (recommended for shared web hosts)\n'
printf '  3) Directly from the internet with Dockside Caddy managing HTTPS\n'
read -r -p 'Select [2]: ' mode_choice
mode_choice="${mode_choice:-2}"
case "$mode_choice" in
  1) mode="local" ;;
  2) mode="proxy" ;;
  3) mode="public" ;;
  *) die "Invalid access mode." ;;
esac

bind_address="127.0.0.1"
http_port="8080"
https_port="8443"
caddy_file="./deploy/caddy/Caddyfile"
secure_cookies="true"
acme_email=""

case "$mode" in
  local)
    http_port="$(read_default "Local panel port" "8080")"
    public_url="$(read_default "Exact local panel URL" "http://localhost:$http_port")"
    secure_cookies="false"
    ;;
  proxy)
    http_port="$(read_default "Loopback upstream port for your existing reverse proxy" "8080")"
    read -r -p 'Exact external panel URL (example: https://panel.example.com): ' public_url
    ;;
  public)
    bind_address="0.0.0.0"
    http_port="80"
    https_port="443"
    caddy_file="./deploy/caddy/Caddyfile.public"
    read -r -p 'Exact public panel URL (example: https://panel.example.com): ' public_url
    read -r -p "Email for Let's Encrypt/ACME notices: " acme_email
    [[ "$acme_email" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]] || die "Direct public TLS mode requires a valid ACME contact email."
    ;;
esac
public_url="${public_url%/}"
[[ "$public_url" =~ ^https?://[^/]+$ ]] || die "The URL must contain only scheme, hostname, and optional port."
if [[ "$mode" == "local" ]]; then
  [[ "$public_url" =~ ^http://(localhost|127\.0\.0\.1|\[::1\])(:[0-9]+)?$ ]] || die "Local mode requires a loopback HTTP URL."
  if [[ "$http_port" == "80" ]]; then
    [[ "$public_url" == "http://localhost" || "$public_url" == "http://127.0.0.1" || "$public_url" == "http://[::1]" ||
       "$public_url" == "http://localhost:80" || "$public_url" == "http://127.0.0.1:80" || "$public_url" == "http://[::1]:80" ]] ||
      die "The local panel URL port must match the selected listener port."
  else
    [[ "$public_url" == "http://localhost:$http_port" || "$public_url" == "http://127.0.0.1:$http_port" || "$public_url" == "http://[::1]:$http_port" ]] ||
      die "The local panel URL port must match the selected listener port."
  fi
else
  [[ "$public_url" == https://* ]] || die "External access requires HTTPS."
fi
hostname="$(printf '%s' "$public_url" | sed -E 's#^https?://\[?([^]/:]+)\]?(:[0-9]+)?$#\1#')"
[[ -n "$hostname" ]] || die "Could not determine the panel hostname."
if [[ "$mode" == "public" && ("$hostname" == "localhost" || "$hostname" =~ ^[0-9.]+$ || "$public_url" == *"://["*) ]]; then
  die "Direct public TLS mode requires a DNS hostname."
fi
redirect_uri="$public_url/api/v1/auth/discord/callback"
if [[ "$mode" == "public" ]]; then
  compose_files="compose.yml:compose.public.yml"
else
  compose_files="compose.yml"
fi

printf '\n\033[36mDiscord application setup\033[0m\n'
printf '  1. Open https://discord.com/developers/applications and create/select an application.\n'
printf '  2. In OAuth2, add this exact Redirect URI:\n'
printf '     \033[33m%s\033[0m\n' "$redirect_uri"
printf '  3. A bot is not required. Dockside requests only the OAuth2 identify scope.\n'
printf '  4. Copy the Application ID and Client Secret below.\n'
read -r -p 'Discord Application (Client) ID: ' discord_client_id
[[ "$discord_client_id" =~ ^[0-9]{15,25}$ ]] || die "The Discord Client ID must be numeric."
if [[ -n "${DOCKSIDE_INSTALL_DISCORD_CLIENT_SECRET:-}" ]]; then
  discord_client_secret="$DOCKSIDE_INSTALL_DISCORD_CLIENT_SECRET"
else
  read -r -s -p 'Discord Client Secret: ' discord_client_secret
  printf '\n'
fi
[[ -n "$discord_client_secret" ]] || die "The Discord Client Secret cannot be empty."

printf '\nWhich Discord users must have MFA enabled?\n'
printf '  1) Owners and administrators\n'
printf '  2) Administrators and operators\n'
printf '  3) Everyone\n'
printf '  4) Do not require Discord MFA\n'
read -r -p 'Select [1]: ' mfa_choice
case "${mfa_choice:-1}" in
  1) mfa_policy="administrators" ;;
  2) mfa_policy="operators" ;;
  3) mfa_policy="everyone" ;;
  4) mfa_policy="off" ;;
  *) die "Invalid MFA policy." ;;
esac

game_port_start="$(read_default "First game-server host port" "20000")"
game_port_end="$(read_default "Last game-server host port" "29999")"
[[ "$game_port_start" =~ ^[0-9]+$ && "$game_port_end" =~ ^[0-9]+$ ]] || die "Game ports must be numeric."
(( game_port_start >= 1024 && game_port_end <= 65535 && game_port_start <= game_port_end )) || die "Invalid game port range."
if [[ -n "$release_version" ]]; then
  version="$release_version"
  printf 'Using release bundle version %s.\n' "$version"
else
  version="$(read_default "Panel image version (use dev to build this checkout)" "dev")"
fi
[[ "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || die "The panel image version contains unsupported characters."

mkdir -p secrets deploy/generated data/servers data/backups
chmod 700 secrets data data/servers data/backups
postgres_password="$(random_token 36)"
encryption_key="$(random_token 32)"
session_key="$(random_token 32)"
engine_token="$(random_token 48)"
bootstrap_token="$(random_token 32)"
instance_id="$(cat /proc/sys/kernel/random/uuid 2>/dev/null || uuidgen)"

write_secret secrets/postgres_password "$postgres_password"
write_secret secrets/database_url "postgres://dockside:$postgres_password@postgres:5432/dockside?sslmode=disable"
write_secret secrets/encryption_key "$encryption_key"
write_secret secrets/session_key "$session_key"
write_secret secrets/discord_client_secret "$discord_client_secret"
write_secret secrets/bootstrap_token "$bootstrap_token"
write_secret secrets/engine_token "$engine_token"

docker_gid="$(stat -c '%g' /var/run/docker.sock 2>/dev/null || printf '0')"
cat > .env <<EOF
COMPOSE_PROJECT_NAME=dockside
COMPOSE_FILE=$compose_files
DOCKSIDE_VERSION=$version
DOCKSIDE_INSTANCE_ID=$instance_id
DOCKSIDE_PUBLIC_URL=$public_url
DOCKSIDE_HOSTNAME=$hostname
DOCKSIDE_ACME_EMAIL=$acme_email
DOCKSIDE_CADDYFILE=$caddy_file
DOCKSIDE_BIND_ADDRESS=$bind_address
DOCKSIDE_HTTP_PORT=$http_port
DOCKSIDE_HTTPS_PORT=$https_port
DOCKSIDE_POSTGRES_DB=dockside
DOCKSIDE_POSTGRES_USER=dockside
DOCKSIDE_DISCORD_CLIENT_ID=$discord_client_id
DOCKSIDE_MFA_POLICY=$mfa_policy
DOCKSIDE_GAME_PORT_START=$game_port_start
DOCKSIDE_GAME_PORT_END=$game_port_end
DOCKSIDE_SERVER_UID=1000
DOCKSIDE_SERVER_GID=1000
DOCKSIDE_DOCKER_GID=$docker_gid
DOCKSIDE_HOST_DATA_ROOT=./data/servers
DOCKSIDE_HOST_BACKUP_ROOT=./data/backups
DOCKSIDE_LOG_LEVEL=info
DOCKSIDE_SECURE_COOKIES=$secure_cookies
EOF
chmod 600 .env

if [[ "$mode" == "proxy" ]]; then
  cat > deploy/generated/nginx-dockside.conf <<EOF
map \$http_upgrade \$dockside_connection_upgrade {
    default upgrade;
    "" close;
}

server {
    listen 443 ssl http2;
    server_name $hostname;

    # Keep your existing certificate directives for this hostname here.
    client_max_body_size 2g;

    location / {
        proxy_pass http://127.0.0.1:$http_port;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$dockside_connection_upgrade;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
EOF
fi

printf '\nValidating generated Compose configuration...\n'
docker compose --env-file .env config --quiet
printf 'Starting Dockside containers. Initial image downloads can take several minutes...\n'
if [[ "$version" == "dev" ]]; then
  docker compose --env-file .env up -d --build
else
  docker compose --env-file .env pull
  docker compose --env-file .env up -d --no-build
fi

ready="false"
for _ in $(seq 1 60); do
  if docker compose --env-file .env exec -T app /dockside healthcheck http://127.0.0.1:8080/health/ready >/dev/null 2>&1; then
    ready="true"
    break
  fi
  sleep 2
done
if [[ "$ready" != "true" ]]; then
  docker compose --env-file .env ps
  die "The panel did not become ready. Run: docker compose logs app worker engine postgres"
fi

printf '\n\033[32mDockside installation is ready.\033[0m\n'
printf 'Panel URL: %s\n' "$public_url"
printf 'Discord Redirect URI: %s\n' "$redirect_uri"
printf '\033[33mOne-time owner bootstrap token: %s\033[0m\n' "$bootstrap_token"
printf 'Open the panel, choose the owner-claim flow, and enter that token.\n'
if [[ "$mode" == "proxy" ]]; then
  printf 'Generated Nginx vhost: deploy/generated/nginx-dockside.conf\n'
  printf 'Dockside is bound only to 127.0.0.1:%s, so other sites remain unaffected.\n' "$http_port"
fi
if [[ "$mode" == "public" ]]; then
  printf 'Confirm DNS for %s points here and inbound TCP 80/443 are allowed.\n' "$hostname"
fi
printf 'Allow game ports %s-%s only as required by provisioned templates.\n' "$game_port_start" "$game_port_end"
