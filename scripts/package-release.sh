#!/usr/bin/env bash
set -Eeuo pipefail

version="${1:-}"
output_directory="${2:-artifacts}"
semver_pattern='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'
[[ "$version" =~ $semver_pattern ]] || {
  printf 'Version must be Semantic Versioning without a v prefix.\n' >&2
  exit 1
}

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "$output_directory" = /* ]]; then
  output_root="$output_directory"
else
  output_root="$project_root/$output_directory"
fi
package_name="dockside-game-panel-$version"
mkdir -p "$output_root"
staging_parent="$(mktemp -d "$output_root/.package.XXXXXX")"
trap 'rm -rf "$staging_parent"' EXIT
staging_root="$staging_parent/$package_name"
mkdir -p "$staging_root"

package_version="$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$project_root/web/package.json" | head -n1)"
[[ "$package_version" == "$version" ]] || {
  printf 'web/package.json is version %s; expected %s.\n' "$package_version" "$version" >&2
  exit 1
}
grep -Fq "## [$version]" "$project_root/CHANGELOG.md" || {
  printf 'CHANGELOG.md does not contain a [%s] release heading.\n' "$version" >&2
  exit 1
}

for file in .env.example CHANGELOG.md compose.yml compose.public.yml CONTRIBUTING.md LICENSE NOTICE README.md; do
  cp "$project_root/$file" "$staging_root/"
done
for directory in deploy docs scripts; do
  cp -R "$project_root/$directory" "$staging_root/"
done
printf '%s\n' "$version" > "$staging_root/.dockside-release"

zip_path="$output_root/$package_name.zip"
tar_path="$output_root/$package_name.tar.gz"
rm -f "$zip_path" "$tar_path"
command -v zip >/dev/null 2>&1 || { printf 'zip is required.\n' >&2; exit 1; }
(cd "$staging_parent" && zip -qr "$zip_path" "$package_name")
tar -czf "$tar_path" -C "$staging_parent" "$package_name"
(
  cd "$output_root"
  sha256sum "$(basename "$zip_path")" "$(basename "$tar_path")" > SHA256SUMS
)
printf 'Release bundle created in %s\n' "$output_root"
