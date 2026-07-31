#!/usr/bin/env bash
set -Eeuo pipefail

source_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf "$test_root"' EXIT

docker() {
  return 0
}
openssl() {
  printf 'ZG9ja3NpZGUtZ3VpZGVkLWluc3RhbGxlci10ZXN0LXRva2Vu'
}
export -f docker openssl

prepare_case() {
  local name="$1" release_version="${2:-}"
  mkdir -p "$test_root/$name/scripts"
  cp "$source_root/scripts/install.sh" "$test_root/$name/scripts/install.sh"
  if [[ -n "$release_version" ]]; then
    printf '%s\n' "$release_version" > "$test_root/$name/.dockside-release"
  fi
}

run_local_case() {
  prepare_case local
  (
    cd "$test_root/local"
    printf '1\n18088\nhttp://localhost:18088\n123456789012345678\n\n\n\n\n' |
      DOCKSIDE_INSTALL_DISCORD_CLIENT_SECRET='local-test-secret' bash scripts/install.sh >/dev/null
    grep -Fx 'COMPOSE_FILE=compose.yml' .env
    grep -Fx 'DOCKSIDE_PUBLIC_URL=http://localhost:18088' .env
    grep -Fx 'DOCKSIDE_BIND_ADDRESS=127.0.0.1' .env
    grep -Fx 'DOCKSIDE_SECURE_COOKIES=false' .env
    [[ "$(stat -c '%a' secrets/discord_client_secret)" == "600" ]]
  )
}

run_proxy_case() {
  prepare_case proxy
  (
    cd "$test_root/proxy"
    printf '2\n18089\nhttps://panel.example.test\n123456789012345678\n\n\n\n\n' |
      DOCKSIDE_INSTALL_DISCORD_CLIENT_SECRET='proxy-test-secret' bash scripts/install.sh >/dev/null
    grep -Fx 'COMPOSE_FILE=compose.yml' .env
    grep -Fx 'DOCKSIDE_PUBLIC_URL=https://panel.example.test' .env
    grep -Fx 'DOCKSIDE_BIND_ADDRESS=127.0.0.1' .env
    grep -F 'server_name panel.example.test;' deploy/generated/nginx-dockside.conf
    grep -F 'proxy_pass http://127.0.0.1:18089;' deploy/generated/nginx-dockside.conf
  )
}

run_public_case() {
  prepare_case public
  (
    cd "$test_root/public"
    printf '3\nhttps://panel.example.test\nops@example.test\n123456789012345678\n\n\n\n\n' |
      DOCKSIDE_INSTALL_DISCORD_CLIENT_SECRET='public-test-secret' bash scripts/install.sh >/dev/null
    grep -Fx 'COMPOSE_FILE=compose.yml:compose.public.yml' .env
    grep -Fx 'DOCKSIDE_PUBLIC_URL=https://panel.example.test' .env
    grep -Fx 'DOCKSIDE_BIND_ADDRESS=0.0.0.0' .env
    grep -Fx 'DOCKSIDE_HTTP_PORT=80' .env
    grep -Fx 'DOCKSIDE_HTTPS_PORT=443' .env
  )
}

run_release_case() {
  prepare_case release 0.1.0-alpha.1
  (
    cd "$test_root/release"
    printf '1\n18090\nhttp://localhost:18090\n123456789012345678\n\n\n\n' |
      DOCKSIDE_INSTALL_DISCORD_CLIENT_SECRET='release-test-secret' bash scripts/install.sh >/dev/null
    grep -Fx 'DOCKSIDE_VERSION=0.1.0-alpha.1' .env
  )
}

run_local_case
run_proxy_case
run_public_case
run_release_case
printf 'Linux guided installer smoke tests passed.\n'
