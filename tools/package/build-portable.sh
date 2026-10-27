#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'USAGE'
Usage: tools/package/build-portable.sh VERSION OUTPUT.tar.zst

Creates a compressed, versioned portable release payload for manual extraction.
The archive does not bundle host graphics/session libraries; the manifest lists
the direct ELF runtime requirements, and compatibility must be validated on the
intended host.
USAGE
}

if [[ "${1-}" == --help || "${1-}" == -h ]]; then usage; exit 0; fi
[[ $# == 2 ]] || { usage >&2; exit 2; }
version=$1
output=$2
[[ "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || {
  echo "Invalid release version: $version" >&2
  exit 2
}

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
command -v zstd >/dev/null 2>&1 || { echo "zstd is required." >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
payload="$tmp/cage-xtmapper-$version.tar"
verified="$tmp/verified.tar"

(
  cd "$root"
  go run ./tools/release package --version "$version" --output "$payload"
)

mkdir -p "$(dirname -- "$output")"
zstd -q -T2 -10 "$payload" -o "$output"
zstd -t "$output" >/dev/null
zstd -dc "$output" > "$verified"
(
  cd "$root"
  go run ./tools/release verify "$verified"
)
tar -tf "$verified" > "${output}.contents.txt"
sha256sum "$output" > "${output}.sha256"
printf 'Built and verified %s\n' "$output"
