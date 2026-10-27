#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
index_script="$root/tools/ci/create-release-index.sh"
tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT
dist="$tmp/dist"
mkdir -p "$dist"

release_tag=v20261027
package_version=20261027
export RELEASE_TAG="$release_tag"
export PACKAGE_VERSION="$package_version"
export GITHUB_REPOSITORY="SoufianoDev/cage-xtmapper"
export GITHUB_SHA="0123456789abcdef0123456789abcdef01234567"

assets=(
  "cage-xtmapper-$release_tag-amd64.tar"
  "cage-xtmapper-$release_tag-arm64.tar"
  "cage-xtmapper-installer-linux-amd64"
  "cage-xtmapper-installer-linux-arm64"
  "cage-xtmapper-$release_tag-debian-13-amd64.deb"
  "cage-xtmapper-$release_tag-ubuntu-24.04-amd64.deb"
  "cage-xtmapper-$release_tag-ubuntu-26.04-amd64.deb"
  "cage-xtmapper-$release_tag-fedora-44-x86_64.rpm"
  "cage-xtmapper-$release_tag-archlinux-x86_64.pkg.tar.zst"
  "cage-xtmapper-$release_tag-alpine-3.24-x86_64.apk"
)

for asset in "${assets[@]}"; do
  printf 'release asset fixture: %s\n' "$asset" > "$dist/$asset"
  (cd "$dist" && sha256sum "$asset" > "$asset.sha256")
done

bash "$index_script" "$dist"
(
  cd "$dist"
  sha256sum --check native-package-index.md.sha256
)
index="$dist/native-package-index.md"
grep -Fq "Release asset index — $release_tag" "$index"
grep -Fq "Source commit: $GITHUB_SHA" "$index"
grep -Fq "Native package version: $package_version" "$index"
grep -Fq "disposable CI signing key" "$index"
rows="$(grep -Fc '| [' "$index")"
if [[ "$rows" -ne "${#assets[@]}" ]]; then
  echo "Release index contains $rows asset rows, expected ${#assets[@]}." >&2
  cat "$index" >&2
  exit 1
fi
for asset in "${assets[@]}"; do
  if ! grep -Fq "$asset" "$index"; then
    echo "Release index is missing asset $asset." >&2
    exit 1
  fi
done

# A missing required asset must fail instead of producing a partial index.
rm -f "$dist/cage-xtmapper-$release_tag-alpine-3.24-x86_64.apk"
if bash "$index_script" "$dist" >"$tmp/missing-asset.log" 2>&1; then
  echo "Index generation succeeded despite a missing required package." >&2
  exit 1
fi
grep -Fq 'Expected release asset is missing' "$tmp/missing-asset.log"

echo "Release index fixture checks passed."
