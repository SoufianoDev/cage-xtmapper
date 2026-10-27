#!/bin/sh
set -eu

: "${TARGET_ID:?TARGET_ID is required}"
: "${TARGET_IMAGE:?TARGET_IMAGE is required}"
: "${GITHUB_RUN_ID:=local}"
: "${GITHUB_SHA:=unknown}"
: "${TARGET_ARCH:=amd64}"

root=$(pwd -P)
audit="$root/build/target-audit/$TARGET_ID"
mkdir -p "$audit"
# Container root creates some build outputs with mode 0600. Always make the
# evidence readable by actions/upload-artifact, including on failure.
trap 'chmod -R a+rX "$audit" 2>/dev/null || true' EXIT

record_environment() {
  {
    echo "target_id=$TARGET_ID"
    echo "target_image=$TARGET_IMAGE"
    echo "target_image_digest=${TARGET_IMAGE_DIGEST:-unknown}"
    echo "target_repo_snapshot=${TARGET_REPO_SNAPSHOT:-not-pinned}"
    echo "target_arch=$TARGET_ARCH"
    echo "source_commit=$(git rev-parse HEAD 2>/dev/null || echo unknown)"
    echo "workflow_sha=$GITHUB_SHA"
    echo "workflow_run_id=$GITHUB_RUN_ID"
    uname -a
    cat /etc/os-release
    echo "--- Go ---"
    go version
    echo "--- Clang ---"
    clang --version
    echo "--- Meson ---"
    meson --version
    echo "--- Ninja ---"
    ninja --version
  } > "$audit/environment.txt"
}

case "$TARGET_ID" in
  debian-13|ubuntu-24.04|ubuntu-26.04)
    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y --no-install-recommends \
      bash ca-certificates curl xz-utils file binutils git clang bison jq dpkg-dev zstd \
      meson ninja-build pkg-config golang-go libffi-dev libexpat1-dev \
      libxml2-dev libwayland-dev libseat-dev libxkbcommon-dev \
      libpixman-1-dev libudev-dev libinput-dev libdrm-dev \
      libdisplay-info-dev libliftoff-dev hwdata scdoc libgbm-dev \
      libgles-dev libegl-dev wayland-protocols
    ;;
  fedora-44)
    dnf install -y \
      bash ca-certificates curl xz file binutils git clang bison meson \
      ninja-build pkgconf-pkg-config go libffi-devel expat-devel \
      libxml2-devel wayland-devel libxkbcommon-devel pixman-devel \
      libinput-devel libdrm-devel libdisplay-info-devel libliftoff-devel \
      mesa-libEGL-devel mesa-libGLES-devel mesa-libgbm-devel libseat-devel \
      systemd-devel hwdata scdoc wayland-protocols-devel rpm-build jq zstd
    ;;
  archlinux)
    if [ "${TARGET_REPO_SNAPSHOT:-}" != "2026-10-26" ]; then
      echo "TARGET_REPO_SNAPSHOT must equal 2026-10-26 for this pinned audit." >&2
      exit 2
    fi
    cat > /etc/pacman.d/mirrorlist <<'ARCH_SNAPSHOT'
Server = https://archive.archlinux.org/repos/2026/10/26/$repo/os/$arch
ARCH_SNAPSHOT
    pacman -Syyu --noconfirm --needed \
      bash ca-certificates curl xz file binutils base-devel clang git bison \
      meson ninja pkgconf go libffi expat libxml2 wayland wayland-protocols \
      libxkbcommon pixman libinput libdrm mesa seatd systemd-libs scdoc hwdata jq zstd
    ;;
  alpine-3.24)
    apk add --no-cache \
      bash ca-certificates curl xz file binutils build-base clang git bison \
      meson ninja pkgconf go libffi-dev expat-dev libxml2-dev wayland-dev \
      libxkbcommon-dev pixman-dev libinput-dev libdrm-dev mesa-dev \
      libseat-dev eudev-dev scdoc hwdata wayland-protocols jq alpine-sdk openssl util-linux zstd
    ;;
  *)
    echo "Unknown TARGET_ID: $TARGET_ID" >&2
    exit 2
    ;;
esac

# git is installed by the target-specific package list above. The checkout is
# bind-mounted from the hosted runner and may have a different UID; allow only
# this source tree so Go can retain VCS metadata during automatic build stamping.
git config --global --add safe.directory "$root"
git -C "$root" rev-parse --is-inside-work-tree >/dev/null

# The module requires Go 1.24+. Newer toolchains auto-select from go.mod
# where the distro package's bundled toolchain is older. Keep the observed
# toolchain in the audit report and fail clearly if Go cannot build the module.
record_environment

case "$TARGET_ID" in
  debian-13|ubuntu-24.04|ubuntu-26.04)
    dpkg-query -W -f='${binary:Package}\t${Version}\t${Architecture}\n' | sort > "$audit/installed-packages.tsv"
    ;;
  fedora-44)
    rpm -qa --qf '%{NAME}\t%{VERSION}-%{RELEASE}\t%{ARCH}\n' | sort > "$audit/installed-packages.tsv"
    ;;
  archlinux)
    pacman -Q | sort > "$audit/installed-packages.tsv"
    ;;
  alpine-3.24)
    apk info -vv | sort > "$audit/installed-packages.tsv"
    ;;
esac

go env GOVERSION > "$audit/go-toolchain.txt"

if ! ./build.sh > "$audit/build.log" 2>&1; then
  cat "$audit/build.log" >&2
  exit 1
fi
cat "$audit/build.log"

binary="build/go/cage-xtmapper"
meson_info="build/meson/meson-info"
test -x "$binary"

file "$binary" > "$audit/elf-file.txt"
readelf --wide --program-headers "$binary" > "$audit/elf-program-headers.txt"
readelf --wide --dynamic "$binary" > "$audit/elf-dynamic.txt"
if grep -Eq '\\((RPATH|RUNPATH)\\)' "$audit/elf-dynamic.txt"; then
  cat "$audit/elf-dynamic.txt" >&2
  echo "ELF contains an unexpected RPATH/RUNPATH; refusing audit." >&2
  exit 1
fi
readelf --wide --version-info "$binary" > "$audit/elf-version-info.txt"

# Safe here because this ELF was compiled from the checked-out source within
# this same container. Package metadata must still be generated per target.
if ! ldd "$binary" > "$audit/loader-resolution.txt" 2>&1; then
  cat "$audit/loader-resolution.txt" >&2
  echo "Target loader resolution failed." >&2
  exit 1
fi
if grep -q 'not found' "$audit/loader-resolution.txt"; then
  cat "$audit/loader-resolution.txt" >&2
  echo "Target ELF has unresolved runtime dependencies." >&2
  exit 1
fi

mkdir -p "$audit/meson"
# Preserve dependency/link provenance even if a later packaging or license
# check fails; this makes target-specific failures diagnosable from the artifact.
cp build/native/pkgconfig/cage-xtmapper-native.pc "$audit/cage-xtmapper-native.pc"
for name in intro-dependencies.json intro-buildoptions.json intro-projectinfo.json intro-compilers.json; do
  test -f "$meson_info/$name"
  cp "$meson_info/$name" "$audit/meson/$name"
done
meson introspect build/meson --dependencies > "$audit/meson/dependencies-introspect.json"
meson introspect build/meson --buildoptions > "$audit/meson/buildoptions-introspect.json"
ninja -C build/meson -t commands cage_xtmapper > "$audit/meson/ninja-commands.txt"
cp build/go/build-info.json "$audit/build-info.json"
cp build/go/provenance.txt "$audit/provenance.txt"
cp build/native/pkgconfig/cage-xtmapper-native.pc "$audit/cage-xtmapper-native.pc"

version="audit-$GITHUB_RUN_ID-$TARGET_ID"
archive="$audit/cage-xtmapper-audit.tar"
go run ./tools/release package --version "$version" --output "$archive"
tar -xOf "$archive" manifest.json > "$audit/manifest.json"
sha256sum "$binary" "$audit/manifest.json" "$archive" > "$audit/SHA256SUMS"

# Create the same verified payload in the portable SteamOS/Bazzite transport
# format. This tests archive integrity only, not runtime compatibility on-device.
portable="$audit/cage-xtmapper-$version-linux-$(uname -m).tar.zst"
if ! bash tools/package/build-portable.sh "$version" "$portable" > "$audit/portable-package.log" 2>&1; then
  cat "$audit/portable-package.log" >&2
  exit 1
fi
cat "$audit/portable-package.log"
zstd -t "$portable" > "$audit/portable-zstd-test.txt"

case "$TARGET_ID" in
  debian-13|ubuntu-24.04|ubuntu-26.04)
    # Keep the diagnostic package version within the release manifest's
    # conservative [A-Za-z0-9._-] version grammar (tilde is valid Debian
    # syntax but intentionally not accepted by the project's archive format).
    package_version="0.0.0-ci$GITHUB_RUN_ID"
    package="$audit/cage-xtmapper_${package_version}_$(dpkg --print-architecture).deb"
    export PACKAGE_MAINTAINER="Cage XtMapper CI <ci@example.invalid>"
    if ! bash tools/package/build-deb.sh "$package_version" "$package" > "$audit/deb-package.log" 2>&1; then
      cat "$audit/deb-package.log" >&2
      exit 1
    fi
    cat "$audit/deb-package.log"
    # This disposable diagnostic package has a reserved example.invalid contact
    # and is never attached to a release. It exists to test native metadata and
    # dependency resolution only; release packages require an approved identity.
    if ! apt-get install -y "$package" > "$audit/deb-install.log" 2>&1; then
      cat "$audit/deb-install.log" >&2
      exit 1
    fi
    dpkg-query -W -f='${Status}\n' cage-xtmapper | grep -Fx 'install ok installed'
    test -x /usr/bin/cage-xtmapper
    dpkg --audit > "$audit/dpkg-audit-after-install.txt"
    if ! apt-get remove -y cage-xtmapper > "$audit/deb-remove.log" 2>&1; then
      cat "$audit/deb-remove.log" >&2
      exit 1
    fi
    test ! -e /usr/bin/cage-xtmapper
    dpkg --audit > "$audit/dpkg-audit-after-remove.txt"
    echo "Debian package install/remove smoke test passed." | tee "$audit/deb-lifecycle-result.txt"
    ;;
  fedora-44)
    package_version="0.0.0"
    package="$audit/cage-xtmapper-$package_version-1.rpm"
    if ! bash tools/package/build-rpm.sh "$package_version" "$package" > "$audit/rpm-package.log" 2>&1; then
      cat "$audit/rpm-package.log" >&2
      exit 1
    fi
    cat "$audit/rpm-package.log"
    if ! dnf install -y "$package" > "$audit/rpm-install.log" 2>&1; then
      cat "$audit/rpm-install.log" >&2
      exit 1
    fi
    rpm -q cage-xtmapper > "$audit/rpm-query.txt"
    test -x /usr/bin/cage-xtmapper
    if ! dnf remove -y cage-xtmapper > "$audit/rpm-remove.log" 2>&1; then
      cat "$audit/rpm-remove.log" >&2
      exit 1
    fi
    test ! -e /usr/bin/cage-xtmapper
    echo "RPM install/remove smoke test passed." | tee "$audit/rpm-lifecycle-result.txt"
    ;;
  archlinux)
    package_version="0.0.0"
    package="$audit/cage-xtmapper-$package_version-1-$(uname -m).pkg.tar.zst"
    if ! bash tools/package/build-arch.sh "$package_version" "$package" > "$audit/arch-package.log" 2>&1; then
      cat "$audit/arch-package.log" >&2
      exit 1
    fi
    cat "$audit/arch-package.log"
    pacman -U --noconfirm "$package" > "$audit/arch-install.log" 2>&1 || {
      cat "$audit/arch-install.log" >&2
      exit 1
    }
    pacman -Q cage-xtmapper > "$audit/arch-query.txt"
    test -x /usr/bin/cage-xtmapper
    pacman -Rns --noconfirm cage-xtmapper > "$audit/arch-remove.log" 2>&1 || {
      cat "$audit/arch-remove.log" >&2
      exit 1
    }
    test ! -e /usr/bin/cage-xtmapper
    echo "Pacman install/remove smoke test passed." | tee "$audit/arch-lifecycle-result.txt"
    ;;
  alpine-3.24)
    package_version="0.0.0"
    package="$audit/cage-xtmapper-$package_version-r0.apk"
    if ! bash tools/package/build-apk.sh "$package_version" "$package" > "$audit/apk-package.log" 2>&1; then
      cat "$audit/apk-package.log" >&2
      exit 1
    fi
    cat "$audit/apk-package.log"
    apk add --allow-untrusted "$package" > "$audit/apk-install.log" 2>&1 || {
      cat "$audit/apk-install.log" >&2
      exit 1
    }
    apk info -e cage-xtmapper > "$audit/apk-query.txt"
    test -x /usr/bin/cage-xtmapper
    apk del cage-xtmapper > "$audit/apk-remove.log" 2>&1 || {
      cat "$audit/apk-remove.log" >&2
      exit 1
    }
    test ! -e /usr/bin/cage-xtmapper
    echo "APK install/remove smoke test passed." | tee "$audit/apk-lifecycle-result.txt"
    ;;
esac

echo "Target audit completed: $TARGET_ID ($TARGET_ARCH)"
