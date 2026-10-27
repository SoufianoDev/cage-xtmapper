#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  echo "Usage: tools/package/build-rpm.sh VERSION OUTPUT.rpm" >&2
}

if [[ "${1-}" == --help || "${1-}" == -h ]]; then usage; exit 0; fi
[[ $# == 2 ]] || { usage; exit 2; }
version=$1
output=$2
[[ "$version" =~ ^[A-Za-z0-9][A-Za-z0-9._+]*$ ]] || {
  echo "Invalid RPM Version (hyphens are not allowed): $version" >&2
  exit 2
}

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
binary="$root/build/go/cage-xtmapper"
for command_name in rpmbuild rpm jq; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "$command_name is required to build an RPM." >&2
    exit 1
  }
done
[[ -x "$binary" ]] || { echo "Missing target-built ELF: $binary" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
payload="$tmp/cage-xtmapper-$version.tar"
payload_dir="$tmp/payload"
source_root="$tmp/source/cage-xtmapper-$version"
top="$tmp/rpmbuild"
mkdir -p "$payload_dir" "$source_root" "$top/BUILD" "$top/BUILDROOT" "$top/RPMS" "$top/SOURCES" "$top/SPECS" "$top/SRPMS"

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
[[ "$manifest_version" == "$version" ]] || {
  echo "Manifest version $manifest_version does not match requested $version." >&2
  exit 1
}
machine=$(uname -m)
case "$machine:$manifest_arch" in
  x86_64:amd64|aarch64:arm64) ;;
  *) echo "ELF architecture $manifest_arch does not match target $machine." >&2; exit 1 ;;
esac
actual_elf_sha=$(sha256sum "$payload_binary" | awk '{print $1}')
[[ "$actual_elf_sha" == "$manifest_elf_sha" ]] || {
  echo "ELF checksum does not match the generated manifest." >&2
  exit 1
}

cp -a "$payload_dir/." "$source_root/"
cp "$root/README.md" "$source_root/README.md"
tar -czf "$top/SOURCES/cage-xtmapper-$version.tar.gz" -C "$tmp/source" "cage-xtmapper-$version"

license_expr="MIT AND BSD-3-Clause"
if jq -e 'any(.licenses[]?; (.license | test("GPL-3")))' "$manifest" >/dev/null; then
  license_expr="$license_expr AND GPL-3.0-or-later"
fi

cat > "$top/SPECS/cage-xtmapper.spec" <<SPEC
Name:           cage-xtmapper
Version:        $version
Release:        1%{?dist}
Summary:        Cage-based Wayland session for XtMapper
License:        $license_expr
URL:            https://github.com/SoufianoDev/cage-xtmapper
Source0:        cage-xtmapper-%{version}.tar.gz
AutoReqProv:    yes

%description
Cage XtMapper packages a Cage/wlroots-based executable integrated with the
XtMapper Android application. Waydroid and the XtMapper APK are prerequisites
and are not installed by this package.

%prep
%autosetup -n cage-xtmapper-%{version}

%build
:

%install
install -D -m 0755 cage-xtmapper %{buildroot}%{_bindir}/cage-xtmapper
install -D -m 0644 README.md %{buildroot}%{_datadir}/doc/%{name}/README.md
install -D -m 0644 manifest.json %{buildroot}%{_datadir}/doc/%{name}/manifest.json
install -d -m 0755 %{buildroot}%{_licensedir}/%{name}
cp -a LICENSES/. %{buildroot}%{_licensedir}/%{name}/

%pre
if [ -e /opt/cage-xtmapper ] || [ -L /opt/cage-xtmapper ] || [ -e /usr/local/bin/cage-xtmapper ] || [ -L /usr/local/bin/cage-xtmapper ]; then
    echo "Refusing to install cage-xtmapper RPM: a bootstrap-managed or local installation may shadow /usr/bin/cage-xtmapper." >&2
    echo "Remove the previous managed installation deliberately, then retry." >&2
    exit 1
fi

%files
%{_bindir}/cage-xtmapper
%doc %{_datadir}/doc/%{name}/README.md
%doc %{_datadir}/doc/%{name}/manifest.json
%license %{_licensedir}/%{name}/*.txt

%changelog
* Tue Oct 27 2026 Cage XtMapper CI <ci@example.invalid> - $version-1
- Disposable native dependency audit build; not a published release.
SPEC

mkdir -p "$(dirname -- "$output")"
rpmbuild --define "_topdir $top" -bb "$top/SPECS/cage-xtmapper.spec" > "$tmp/rpmbuild.log" 2>&1 || {
  cat "$tmp/rpmbuild.log" >&2
  exit 1
}
cat "$tmp/rpmbuild.log"
rpm_file=$(find "$top/RPMS" -type f -name '*.rpm' -print -quit)
[[ -n "$rpm_file" && -s "$rpm_file" ]] || { echo "rpmbuild produced no RPM." >&2; exit 1; }
cp "$rpm_file" "$output"
rpm -qp --requires "$output" > "${output}.requires.txt"
rpm -qp --scripts "$output" > "${output}.scripts.txt"
rpm -qpl "$output" > "${output}.contents.txt"
sha256sum "$output" > "${output}.sha256"
printf 'Built %s\n' "$output"
