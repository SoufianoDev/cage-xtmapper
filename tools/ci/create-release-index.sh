#!/usr/bin/env bash
set -Eeuo pipefail

dist_dir="${1:-dist}"
: "${RELEASE_TAG:?RELEASE_TAG is required}"
: "${PACKAGE_VERSION:?PACKAGE_VERSION is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"
[[ -d "$dist_dir" ]] || { echo "Distribution directory does not exist: $dist_dir" >&2; exit 2; }

shopt -s nullglob
expected_assets=(
  "cage-xtmapper-${RELEASE_TAG}-amd64.tar"
  "cage-xtmapper-${RELEASE_TAG}-arm64.tar"
  "cage-xtmapper-installer-linux-amd64"
  "cage-xtmapper-installer-linux-arm64"
  "cage-xtmapper-${RELEASE_TAG}-debian-13-amd64.deb"
  "cage-xtmapper-${RELEASE_TAG}-ubuntu-24.04-amd64.deb"
  "cage-xtmapper-${RELEASE_TAG}-ubuntu-26.04-amd64.deb"
  "cage-xtmapper-${RELEASE_TAG}-fedora-44-x86_64.rpm"
  "cage-xtmapper-${RELEASE_TAG}-archlinux-x86_64.pkg.tar.zst"
  "cage-xtmapper-${RELEASE_TAG}-alpine-3.24-x86_64.apk"
)

for asset in "${expected_assets[@]}"; do
  if [[ ! -s "$dist_dir/$asset" ]]; then
    echo "Expected release asset is missing: $asset" >&2
    exit 1
  fi
  if [[ ! -s "$dist_dir/$asset.sha256" ]]; then
    echo "Expected checksum sidecar is missing: $asset.sha256" >&2
    exit 1
  fi
done

tar_assets=("$dist_dir/cage-xtmapper-$RELEASE_TAG-"*.tar)
deb_assets=("$dist_dir/cage-xtmapper-$RELEASE_TAG-"*.deb)
rpm_assets=("$dist_dir/cage-xtmapper-$RELEASE_TAG-"*.rpm)
arch_assets=("$dist_dir/cage-xtmapper-$RELEASE_TAG-"*.pkg.tar.zst)
apk_assets=("$dist_dir/cage-xtmapper-$RELEASE_TAG-"*.apk)
amd64_helper=("$dist_dir/cage-xtmapper-installer-linux-amd64")
arm64_helper=("$dist_dir/cage-xtmapper-installer-linux-arm64")
if [[ "${#tar_assets[@]}" -ne 2 || "${#deb_assets[@]}" -ne 3 ||
      "${#rpm_assets[@]}" -ne 1 || "${#arch_assets[@]}" -ne 1 ||
      "${#apk_assets[@]}" -ne 1 || "${#amd64_helper[@]}" -ne 1 ||
      "${#arm64_helper[@]}" -ne 1 ]]; then
  echo "Unexpected release asset counts for tag $RELEASE_TAG." >&2
  printf 'tar=%s deb=%s rpm=%s arch=%s apk=%s helpers=%s/%s\n' \
    "${#tar_assets[@]}" "${#deb_assets[@]}" "${#rpm_assets[@]}" \
    "${#arch_assets[@]}" "${#apk_assets[@]}" \
    "${#amd64_helper[@]}" "${#arm64_helper[@]}" >&2
  exit 1
fi

for checksum in "$dist_dir"/*.sha256; do
  (cd "$dist_dir" && sha256sum --check "$(basename "$checksum")")
done

index_asset() {
  local file="$1"
  local target="$2"
  local architecture="$3"
  local format="$4"
  local status="$5"
  local notes="$6"
  local base digest url
  base="$(basename "$file")"
  digest="$(sha256sum "$file" | awk '{print $1}')"
  url="https://github.com/$GITHUB_REPOSITORY/releases/download/$RELEASE_TAG/$base"
  printf '| [%s](%s) ([SHA-256](%s.sha256)) | %s | %s | %s | %s | %s | %s |\n' \
    "$base" "$url" "$url" "$target" "$architecture" "$format" "$status" "$digest" "$notes"
}

{
  printf '# Release asset index — %s\n\n' "$RELEASE_TAG"
  printf -- '- Source commit: %s\n- Native package version: %s (release tag prefix v removed)\n' "$GITHUB_SHA" "$PACKAGE_VERSION"
  printf -- '- No native package in this draft is marked Supported. CI does not prove startup in a real Wayland/graphics session or SteamOS/Bazzite host integration.\n'
  printf -- '- SHA-256 sidecars detect corruption but do not authenticate the publisher.\n'
  printf -- '- Alpine APK uses a disposable CI signing key, not an approved production key. Do not treat its signature as publisher authentication; manual installation requires a deliberate trust bypass unless the maintainer approves and deploys a production signing process.\n\n'
  printf '| Asset | Target/build baseline | Architecture | Format | Status | SHA-256 | Notes |\n'
  printf '|---|---|---|---|---|---|---|\n'

  for file in "${tar_assets[@]}"; do
    case "$(basename "$file")" in
      *-amd64.tar) arch="amd64" ;;
      *-arm64.tar) arch="arm64" ;;
      *) echo "Unexpected archive name: $file" >&2; exit 1 ;;
    esac
    index_asset "$file" "Ubuntu 26.04.1 build baseline; generic release archive" "$arch" ".tar" "Experimental" "Host libraries and real-session compatibility must be checked on the target system."
  done

  for file in "${deb_assets[@]}"; do
    base="$(basename "$file")"
    case "$base" in
      *-debian-13-amd64.deb) target="Debian 13.7 (Trixie)";;
      *-ubuntu-24.04-amd64.deb) target="Ubuntu 24.04.5 LTS";;
      *-ubuntu-26.04-amd64.deb) target="Ubuntu 26.04.1 LTS";;
      *) echo "Unexpected Debian-family asset name: $base" >&2; exit 1;;
    esac
    index_asset "$file" "$target" "amd64" ".deb" "Experimental" "Container package/dependency/lifecycle checks pass; real Wayland and Waydroid session launch is not yet proven."
  done

  for file in "${rpm_assets[@]}"; do
    index_asset "$file" "Fedora 44" "x86_64" ".rpm" "Experimental" "RPM dependency/lifecycle and rpmlint pass in a container; the RPM %pre guard blocks legacy paths; Bazzite host testing is separate and has not been done."
  done
  for file in "${arch_assets[@]}"; do
    index_asset "$file" "Arch Linux; repositories pinned to snapshot 2026-10-26" "x86_64" ".pkg.tar.zst" "Experimental" "Package-manager lifecycle passed; legacy-path scriptlet only warns, so inspect/remove the old /opt and /usr/local/bin install before relying on /usr/bin; rolling-release runtime compatibility remains unproven."
  done
  for file in "${apk_assets[@]}"; do
    index_asset "$file" "Alpine Linux 3.24.3" "x86_64" ".apk" "Experimental" "Musl-native candidate; legacy-path scriptlet only warns, so inspect/remove the old /opt and /usr/local/bin install before relying on /usr/bin; production signature/trust is not configured and no real Wayland session test has passed."
  done

  for file in "${amd64_helper[0]}" "${arm64_helper[0]}"; do
    base="$(basename "$file")"
    case "$base" in
      *amd64) arch="amd64";;
      *arm64) arch="arm64";;
      *) echo "Unexpected installer-helper filename: $base" >&2; exit 1;;
    esac
    index_asset "$file" "Installer utility; not a distribution package" "$arch" "Go helper executable" "Component only" "Used by the bootstrap with its matching versioned .tar archive; not a standalone application runtime."
  done
} > "$dist_dir/native-package-index.md"
(cd "$dist_dir" && sha256sum native-package-index.md > native-package-index.md.sha256)
