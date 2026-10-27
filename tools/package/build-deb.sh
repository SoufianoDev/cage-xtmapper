#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'USAGE'
Usage: tools/package/build-deb.sh VERSION OUTPUT.deb

Required environment:
  PACKAGE_MAINTAINER   Debian Maintainer field for the package.

The script packages the exact build/go/cage-xtmapper ELF through the project's
release manifest generator. It derives Depends using dpkg-shlibdeps from the
current target build environment. It does not publish a repository or sign
production artifacts.
USAGE
}

if [[ "${1-}" == --help || "${1-}" == -h ]]; then usage; exit 0; fi
[[ $# == 2 ]] || { usage >&2; exit 2; }

version=$1
output=$2
: "${PACKAGE_MAINTAINER:?Set PACKAGE_MAINTAINER to the approved Maintainer field}"

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
binary="$root/build/go/cage-xtmapper"
command -v dpkg-deb >/dev/null 2>&1 || { echo "dpkg-deb is required." >&2; exit 1; }
command -v dpkg-shlibdeps >/dev/null 2>&1 || { echo "dpkg-shlibdeps (dpkg-dev) is required." >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq is required." >&2; exit 1; }
[[ -x "$binary" ]] || { echo "Missing target-built ELF: $binary" >&2; exit 1; }
dpkg --validate-version "$version" >/dev/null || { echo "Invalid Debian version: $version" >&2; exit 2; }

tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
payload="$tmp/payload.tar"
payload_dir="$tmp/payload"
package_root="$tmp/root"
mkdir -p "$payload_dir" "$package_root"

(
  cd "$root"
  go run ./tools/release package --version "$version" --output "$payload"
)
tar -xf "$payload" -C "$payload_dir"

manifest="$payload_dir/manifest.json"
payload_binary="$payload_dir/cage-xtmapper"
[[ -s "$manifest" && -x "$payload_binary" ]] || {
  echo "Release payload is missing manifest.json or executable." >&2
  exit 1
}

manifest_version=$(jq -er '.version' "$manifest")
manifest_arch=$(jq -er '.target.architecture' "$manifest")
manifest_commit=$(jq -er '.main_commit' "$manifest")
manifest_elf_sha=$(jq -er '.elf_sha256' "$manifest")
[[ "$manifest_version" == "$version" ]] || {
  echo "Manifest version $manifest_version does not match requested $version." >&2
  exit 1
}
dpkg_arch=$(dpkg --print-architecture)
[[ "$manifest_arch" == "$dpkg_arch" ]] || {
  echo "ELF target architecture $manifest_arch does not match dpkg architecture $dpkg_arch." >&2
  exit 1
}
actual_elf_sha=$(sha256sum "$payload_binary" | awk '{print $1}')
[[ "$actual_elf_sha" == "$manifest_elf_sha" ]] || {
  echo "ELF checksum does not match the generated manifest." >&2
  exit 1
}

install -D -m 0755 "$payload_binary" "$package_root/usr/bin/cage-xtmapper"
doc_dir="$package_root/usr/share/doc/cage-xtmapper"
install -d -m 0755 "$doc_dir/licenses"
install -m 0644 "$manifest" "$doc_dir/manifest.json"
if [[ -f "$root/README.md" ]]; then
  install -m 0644 "$root/README.md" "$doc_dir/README.md"
fi
cp -a "$payload_dir/LICENSES/." "$doc_dir/licenses/"

# Ship all component-specific license texts in both their individual files and
# one easy-to-read Debian copyright/notice document. Do not infer a single
# license for the entire linked payload from the application's own license.
{
  printf 'Cage XtMapper — license and third-party notices\n'
  printf 'Release version: %s\nSource commit: %s\n\n' "$version" "$manifest_commit"
  for license_file in "$doc_dir"/licenses/*.txt; do
    [[ -f "$license_file" ]] || continue
    printf '\n===== %s =====\n' "$(basename "$license_file")"
    cat "$license_file"
    printf '\n'
  done
} > "$doc_dir/copyright"

mkdir -p "$package_root/DEBIAN"
cat > "$package_root/DEBIAN/preinst" <<'PREINST'
#!/bin/sh
set -eu
if [ -e /opt/cage-xtmapper ] || [ -L /opt/cage-xtmapper ] || [ -e /usr/local/bin/cage-xtmapper ] || [ -L /usr/local/bin/cage-xtmapper ]; then
  echo "Refusing to install cage-xtmapper .deb: a bootstrap-managed or local installation may shadow /usr/bin/cage-xtmapper." >&2
  echo "Remove the previous managed installation deliberately, then retry." >&2
  exit 1
fi
PREINST
chmod 0755 "$package_root/DEBIAN/preinst"

# dpkg-shlibdeps consults debian/control even when output is sent to
# stdout. Use an isolated temporary source package so this helper does not
# require Debian packaging metadata in the application repository.
mkdir -p "$tmp/debian"
cat > "$tmp/debian/control" <<CONTROL
Source: cage-xtmapper
Section: x11
Priority: optional
Maintainer: $PACKAGE_MAINTAINER
Build-Depends: dpkg-dev
Standards-Version: 4.6.2

Package: cage-xtmapper
Architecture: any
Description: Cage-based Wayland session for XtMapper
 Native Cage/wlroots executable integrated with the XtMapper Android app.
CONTROL
substvars=$(cd "$tmp" && dpkg-shlibdeps -O -e "$package_root/usr/bin/cage-xtmapper")
depends=$(printf '%s\n' "$substvars" | sed -n 's/^shlibs:Depends=//p')
[[ -n "$depends" ]] || {
  printf 'dpkg-shlibdeps did not return a Depends field:\n%s\n' "$substvars" >&2
  exit 1
}

installed_size=$(du -sk "$package_root" | awk '{print $1}')
cat > "$package_root/DEBIAN/control" <<CONTROL
Package: cage-xtmapper
Version: $version
Section: x11
Priority: optional
Architecture: $dpkg_arch
Maintainer: $PACKAGE_MAINTAINER
Depends: $depends
Installed-Size: $installed_size
Homepage: https://github.com/SoufianoDev/cage-xtmapper
Description: Cage-based Wayland session for XtMapper
 Cage XtMapper packages a Cage/wlroots-based executable with the XtMapper
 integration. Waydroid, the XtMapper APK, and optional ADB transport are
 external prerequisites and are not silently installed by this package.
X-Build-Commit: $manifest_commit
X-ELF-SHA256: $manifest_elf_sha
CONTROL

out_dir=$(dirname -- "$output")
mkdir -p "$out_dir"
dpkg-deb --build --root-owner-group "$package_root" "$output"
dpkg-deb --info "$output" > "${output}.info.txt"
dpkg-deb --contents "$output" > "${output}.contents.txt"
sha256sum "$output" > "${output}.sha256"
printf 'Built %s\n' "$output"
