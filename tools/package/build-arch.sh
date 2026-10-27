#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  echo "Usage: tools/package/build-arch.sh VERSION OUTPUT.pkg.tar.zst" >&2
}

if [[ "${1-}" == --help || "${1-}" == -h ]]; then usage; exit 0; fi
[[ $# == 2 ]] || { usage; exit 2; }
version=$1
output=$2
[[ "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._+]*$ ]] || {
  echo "Invalid Arch pkgver: $version" >&2
  exit 2
}

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
binary="$root/build/go/cage-xtmapper"
for command_name in makepkg pacman jq ldd runuser useradd chown; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "$command_name is required to build a Pacman package." >&2
    exit 1
  }
done
[[ -x "$binary" ]] || { echo "Missing target-built ELF: $binary" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
payload="$tmp/payload.tar"
payload_dir="$tmp/payload"
source_root="$tmp/source/cage-xtmapper-$version"
work="$tmp/arch-work"
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

# Derive package names from the target package database for every direct
# DT_NEEDED entry. Fail rather than guess when a SONAME cannot be mapped.
declare -a package_deps=()
while IFS= read -r soname; do
  resolved=$(awk -v so="$soname" '
    $1 == so && $2 == "=>" && $3 ~ /^\// { print $3; exit }
    $1 ~ /^\// && $2 == "=>" && index($1, so) > 0 && $3 ~ /^\// { print $3; exit }
  ' < <(ldd "$binary"))
  [[ -n "$resolved" ]] || { echo "Cannot resolve $soname in ldd output." >&2; exit 1; }
  owner=$(pacman -Qqo "$resolved" 2>/dev/null || true)
  if [[ -z "$owner" ]]; then owner=$(pacman -Qqo "$(readlink -f "$resolved")" 2>/dev/null || true); fi
  [[ -n "$owner" ]] || { echo "Cannot map $soname ($resolved) to an installed Arch package." >&2; exit 1; }
  package_deps+=("$owner")
done < <(jq -er '.runtime_requirements.shared_libraries[]' "$manifest")

mapfile -t package_deps < <(printf '%s\n' "${package_deps[@]}" | sort -u)
depends_expr=""
for dep in "${package_deps[@]}"; do depends_expr+="'$dep' "; done

declare -a license_tags=('MIT' 'BSD-3-Clause')
if jq -e 'any(.licenses[]?; (.license | test("GPL-3")))' "$manifest" >/dev/null; then
  license_tags+=('GPL-3.0-or-later')
fi
license_expr=""
for item in "${license_tags[@]}"; do license_expr+="'$item' "; done

preinstall="$work/cage-xtmapper.install"
cat > "$preinstall" <<'INSTALL'
pre_install() {
  if [[ -e /opt/cage-xtmapper || -L /opt/cage-xtmapper || -e /usr/local/bin/cage-xtmapper || -L /usr/local/bin/cage-xtmapper ]]; then
    echo "Refusing to install cage-xtmapper package: a bootstrap-managed or local installation may shadow /usr/bin/cage-xtmapper." >&2
    echo "Remove the previous managed installation deliberately, then retry." >&2
    return 1
  fi
}
INSTALL
install_sha=$(sha256sum "$preinstall" | awk '{print $1}')
arch=$(case "$machine" in x86_64) echo x86_64;; aarch64) echo aarch64;; *) echo unsupported;; esac)
[[ "$arch" != unsupported ]] || { echo "Unsupported Arch architecture: $machine" >&2; exit 1; }

cat > "$work/PKGBUILD.template" <<'PKGBUILD'
pkgname=cage-xtmapper
pkgver=@PKGVER@
pkgrel=1
pkgdesc='Cage-based Wayland session for XtMapper'
arch=('@ARCH@')
url='https://github.com/SoufianoDev/cage-xtmapper'
license=(@LICENSES@)
depends=(@DEPENDS@)
install='cage-xtmapper.install'
source=("cage-xtmapper-${pkgver}.tar.gz" 'cage-xtmapper.install')
sha256sums=('@SOURCE_SHA@' '@INSTALL_SHA@')
options=(!strip)

package() {
  cd "$srcdir/cage-xtmapper-$pkgver"
  install -Dm755 cage-xtmapper "$pkgdir/usr/bin/cage-xtmapper"
  install -Dm644 README.md "$pkgdir/usr/share/doc/cage-xtmapper/README.md"
  install -Dm644 manifest.json "$pkgdir/usr/share/doc/cage-xtmapper/manifest.json"
  install -d -m755 "$pkgdir/usr/share/licenses/cage-xtmapper"
  cp -a LICENSES/. "$pkgdir/usr/share/licenses/cage-xtmapper/"
}
PKGBUILD

sed \
  -e "s/@PKGVER@/$version/g" \
  -e "s/@ARCH@/$arch/g" \
  -e "s/@LICENSES@/$license_expr/g" \
  -e "s/@DEPENDS@/$depends_expr/g" \
  -e "s/@SOURCE_SHA@/$source_sha/g" \
  -e "s/@INSTALL_SHA@/$install_sha/g" \
  "$work/PKGBUILD.template" > "$work/PKGBUILD"

if ! id cagepackager >/dev/null 2>&1; then
  useradd --create-home --shell /bin/bash cagepackager
fi
# mktemp creates a private 0700 parent. Permit traversal to the explicitly
# named, user-owned work tree so makepkg can create its configured BUILDDIR.
chmod 0711 "$tmp"
chown -R cagepackager:cagepackager "$work"
if ! (cd "$work" && runuser -u cagepackager -- makepkg --noconfirm) > "$tmp/makepkg.log" 2>&1; then
  cat "$tmp/makepkg.log" >&2
  exit 1
fi
cat "$tmp/makepkg.log"
pkg_file=$(find "$work" -maxdepth 1 -type f -name 'cage-xtmapper-*.pkg.tar.zst' -print -quit)
[[ -n "$pkg_file" && -s "$pkg_file" ]] || { echo "makepkg produced no package." >&2; exit 1; }
mkdir -p "$(dirname -- "$output")"
cp "$pkg_file" "$output"
tar -xOf "$output" .PKGINFO | sed -n 's/^depend = //p' > "${output}.depends.txt"
pacman -Qip "$output" > "${output}.info.txt"
pacman -Qlp "$output" > "${output}.contents.txt"
sha256sum "$output" > "${output}.sha256"
printf 'Built %s\n' "$output"
