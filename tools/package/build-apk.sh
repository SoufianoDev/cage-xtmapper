#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  echo "Usage: tools/package/build-apk.sh VERSION OUTPUT.apk" >&2
}

if [[ "${1-}" == --help || "${1-}" == -h ]]; then usage; exit 0; fi
[[ $# == 2 ]] || { usage; exit 2; }
version=$1
output=$2
[[ "$version" =~ ^[0-9][A-Za-z0-9._+]*$ ]] || {
  echo "Invalid Alpine pkgver: $version" >&2
  exit 2
}

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
binary="$root/build/go/cage-xtmapper"
for command_name in abuild apk jq openssl runuser adduser addgroup chown; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "$command_name is required to build an APK." >&2
    exit 1
  }
done
[[ -x "$binary" ]] || { echo "Missing target-built ELF: $binary" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
payload="$tmp/payload.tar"
payload_dir="$tmp/payload"
source_root="$tmp/source/cage-xtmapper-$version"
work="$tmp/apk-work"
mkdir -p "$payload_dir" "$source_root" "$work"

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
manifest_elf_sha=$(jq -er '.elf_sha256' "$manifest")
[[ "$manifest_version" == "$version" ]] || { echo "Manifest version mismatch." >&2; exit 1; }
machine=$(uname -m)
case "$machine:$manifest_arch" in
  x86_64:amd64|aarch64:arm64) ;;
  *) echo "ELF architecture $manifest_arch does not match target $machine." >&2; exit 1 ;;
esac
[[ "$(sha256sum "$payload_binary" | awk '{print $1}')" == "$manifest_elf_sha" ]] || {
  echo "ELF checksum does not match the generated manifest." >&2
  exit 1
}

cp -a "$payload_dir/." "$source_root/"
cp "$root/README.md" "$source_root/README.md"
tar -czf "$work/cage-xtmapper-$version.tar.gz" -C "$tmp/source" "cage-xtmapper-$version"
source_sha=$(sha256sum "$work/cage-xtmapper-$version.tar.gz" | awk '{print $1}')

preinstall="$work/cage-xtmapper.pre-install"
cat > "$preinstall" <<'PREINSTALL'
#!/bin/sh
set -eu
if [ -e /opt/cage-xtmapper ] || [ -L /opt/cage-xtmapper ] || [ -e /usr/local/bin/cage-xtmapper ] || [ -L /usr/local/bin/cage-xtmapper ]; then
  echo "Refusing to install cage-xtmapper APK: a bootstrap-managed or local installation may shadow /usr/bin/cage-xtmapper." >&2
  echo "Remove the previous managed installation deliberately, then retry." >&2
  exit 1
fi
PREINSTALL
chmod 0644 "$preinstall"
install_sha=$(sha256sum "$preinstall" | awk '{print $1}')
arch=$(apk --print-arch)
licenses="MIT BSD-3-Clause"
if jq -e 'any(.licenses[]?; (.license | test("GPL-3")))' "$manifest" >/dev/null; then
  licenses="$licenses GPL-3.0-or-later"
fi

cat > "$work/APKBUILD.template" <<'APKBUILD'
pkgname=cage-xtmapper
pkgver=@PKGVER@
pkgrel=0
pkgdesc='Cage-based Wayland session for XtMapper'
url='https://github.com/SoufianoDev/cage-xtmapper'
arch='@ARCH@'
license='@LICENSES@'
install="$pkgname.pre-install"
source="$pkgname-$pkgver.tar.gz $pkgname.pre-install"
sha256sums='@SOURCE_SHA@ @INSTALL_SHA@'
options='!check'

build() {
  :
}

package() {
  cd "$srcdir/$pkgname-$pkgver"
  install -Dm755 cage-xtmapper "$pkgdir/usr/bin/cage-xtmapper"
  install -Dm644 README.md "$pkgdir/usr/share/doc/cage-xtmapper/README.md"
  install -Dm644 manifest.json "$pkgdir/usr/share/doc/cage-xtmapper/manifest.json"
  install -d -m755 "$pkgdir/usr/share/licenses/cage-xtmapper"
  cp -a LICENSES/. "$pkgdir/usr/share/licenses/cage-xtmapper/"
}
APKBUILD

sed \
  -e "s/@PKGVER@/$version/g" \
  -e "s/@ARCH@/$arch/g" \
  -e "s/@LICENSES@/$licenses/g" \
  -e "s/@SOURCE_SHA@/$source_sha/g" \
  -e "s/@INSTALL_SHA@/$install_sha/g" \
  "$work/APKBUILD.template" > "$work/APKBUILD"
cp "$preinstall" "$work/cage-xtmapper.pre-install"
chmod +x "$work/cage-xtmapper.pre-install"

# Alpine's package builder must run as an unprivileged user. This signing key
# is generated only for this isolated CI build and is never a release key.
if ! getent group abuild >/dev/null 2>&1; then addgroup -S abuild; fi
if ! id cagepackager >/dev/null 2>&1; then adduser -D -G abuild -h "$tmp/home" cagepackager; fi
home=$(getent passwd cagepackager | cut -d: -f6)
mkdir -p "$home/.abuild"
openssl genrsa -out "$home/.abuild/cage-xtmapper-ci.rsa" 2048 >/dev/null 2>&1
openssl rsa -in "$home/.abuild/cage-xtmapper-ci.rsa" -pubout -out "$home/.abuild/cage-xtmapper-ci.rsa.pub" >/dev/null 2>&1
chown -R cagepackager:abuild "$home" "$work"
chmod 0600 "$home/.abuild/cage-xtmapper-ci.rsa"
export PACKAGER="Cage XtMapper CI <ci@example.invalid>"
export PACKAGER_PRIVKEY="$home/.abuild/cage-xtmapper-ci.rsa"
export PKGDEST="$tmp/packages"
mkdir -p "$PKGDEST"
chown -R cagepackager:abuild "$PKGDEST"

if ! (cd "$work" && runuser -u cagepackager -- env HOME="$home" PACKAGER="$PACKAGER" PACKAGER_PRIVKEY="$PACKAGER_PRIVKEY" PKGDEST="$PKGDEST" abuild -F) > "$tmp/abuild.log" 2>&1; then
  cat "$tmp/abuild.log" >&2
  exit 1
fi
cat "$tmp/abuild.log"
apk_file=$(find "$PKGDEST" -type f -name 'cage-xtmapper-*.apk' -print -quit)
[[ -n "$apk_file" && -s "$apk_file" ]] || { echo "abuild produced no APK." >&2; exit 1; }
mkdir -p "$(dirname -- "$output")"
cp "$apk_file" "$output"
apk info --who-owns "$output" > "${output}.owner.txt" 2>&1 || true
tar -xOf "$output" .PKGINFO > "${output}.info.txt" 2>/dev/null || true
sha256sum "$output" > "${output}.sha256"
printf 'Built %s\n' "$output"
