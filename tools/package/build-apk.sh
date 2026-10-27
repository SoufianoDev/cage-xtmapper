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
  echo "WARNING: a bootstrap-managed or local installation may shadow /usr/bin/cage-xtmapper." >&2
  echo "Inspect and remove the previous installation deliberately before relying on the package-managed executable." >&2
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
sha256sums='@SOURCE_SHA@  cage-xtmapper-@PKGVER@.tar.gz
@INSTALL_SHA@  cage-xtmapper.pre-install'
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
chmod 0644 "$work/cage-xtmapper.pre-install"

# Alpine package builds run unprivileged and use a disposable CI-only key.
# The CI lifecycle test calls this script more than once in the same container;
# keep one key across those calls so previously built test packages remain trusted.
if ! getent group abuild >/dev/null 2>&1; then addgroup -S abuild; fi
if ! id cagepackager >/dev/null 2>&1; then adduser -D -G abuild -h "$tmp/home" cagepackager; fi
chmod 0711 "$tmp"
home=$(getent passwd cagepackager | cut -d: -f6)
mkdir -p "$home/.abuild"
key_dir="${APK_CI_KEY_DIR:-$tmp/ci-key}"
mkdir -p "$key_dir"
private_key="$key_dir/cage-xtmapper-ci.rsa"
public_key="$key_dir/cage-xtmapper-ci.rsa.pub"
if [[ -s "$private_key" && -s "$public_key" ]]; then
  :
elif [[ ! -e "$private_key" && ! -e "$public_key" ]]; then
  openssl genrsa -out "$private_key" 2048 >/dev/null 2>&1
  openssl rsa -in "$private_key" -pubout -out "$public_key" >/dev/null 2>&1
else
  echo "Incomplete temporary APK signing key pair in $key_dir." >&2
  exit 1
fi
chown -R cagepackager:abuild "$home" "$work" "$key_dir"
chmod 0600 "$private_key"
# Trust only the disposable key used by this isolated build/test container.
install -Dm644 "$public_key" /etc/apk/keys/cage-xtmapper-ci.rsa.pub
export PACKAGER="Cage XtMapper CI <ci@example.invalid>"
export PACKAGER_PRIVKEY="$private_key"
export REPODEST="$tmp/packages"
mkdir -p "$REPODEST"
chown -R cagepackager:abuild "$REPODEST"
# abuild normally reads PACKAGER and PACKAGER_PRIVKEY from /etc/abuild.conf.
# This is a disposable container; append an ephemeral CI-only config instead
# of reusing or modifying any real publisher key/configuration.
cat >> /etc/abuild.conf <<ABUILD_CONFIG
PACKAGER="$PACKAGER"
PACKAGER_PRIVKEY="$PACKAGER_PRIVKEY"
REPODEST="$REPODEST"
ABUILD_CONFIG
chmod 0644 /etc/abuild.conf

if ! (cd "$work" && runuser -u cagepackager -- env HOME="$home" PACKAGER="$PACKAGER" PACKAGER_PRIVKEY="$PACKAGER_PRIVKEY" REPODEST="$REPODEST" abuild -F) > "$tmp/abuild.log" 2>&1; then
  cat "$tmp/abuild.log" >&2
  exit 1
fi
cat "$tmp/abuild.log"
apk_file=$(find "$REPODEST" -type f -name 'cage-xtmapper-*.apk' -print -quit)
[[ -n "$apk_file" && -s "$apk_file" ]] || { echo "abuild produced no APK." >&2; exit 1; }
mkdir -p "$(dirname -- "$output")"
cp "$apk_file" "$output"
apk info --who-owns "$output" > "${output}.owner.txt" 2>&1 || true
tar -xOf "$output" .PKGINFO > "${output}.info.txt" 2>/dev/null || true
sha256sum "$output" > "${output}.sha256"
printf 'Built %s\n' "$output"
