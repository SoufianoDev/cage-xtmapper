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

record_environment() {
  {
    echo "target_id=$TARGET_ID"
    echo "target_image=$TARGET_IMAGE"
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
      bash ca-certificates curl xz-utils file binutils git clang bison \
      meson ninja-build pkg-config golang-go libffi-dev libexpat1-dev \
      libxml2-dev libwayland-dev libseat-dev libxkbcommon-dev \
      libpixman-1-dev libudev-dev libinput-dev libdrm-dev hwdata scdoc \
      libgbm-dev libgles-dev libegl-dev wayland-protocols
    ;;
  fedora-44)
    dnf install -y \
      bash ca-certificates curl xz file binutils git clang bison meson \
      ninja-build pkgconf-pkg-config go libffi-devel expat-devel \
      libxml2-devel wayland-devel libxkbcommon-devel pixman-devel \
      libinput-devel libdrm-devel mesa-libEGL-devel mesa-libGLES-devel \
      mesa-libgbm-devel seatd-devel systemd-devel hwdata scdoc \
      wayland-protocols-devel
    ;;
  archlinux)
    pacman -Syu --noconfirm --needed \
      bash ca-certificates curl xz file binutils base-devel clang git bison \
      meson ninja pkgconf go libffi expat libxml2 wayland wayland-protocols \
      libxkbcommon pixman libinput libdrm mesa seatd systemd-libs scdoc hwdata
    ;;
  alpine-3.24)
    apk add --no-cache \
      bash ca-certificates curl xz file binutils build-base clang git bison \
      meson ninja pkgconf go libffi-dev expat-dev libxml2-dev wayland-dev \
      libxkbcommon-dev pixman-dev libinput-dev libdrm-dev mesa-dev \
      libseat-dev eudev-dev scdoc hwdata wayland-protocols
    ;;
  *)
    echo "Unknown TARGET_ID: $TARGET_ID" >&2
    exit 2
    ;;
esac

# The module requires Go 1.24+. Newer toolchains auto-select from go.mod
# where the distro package's bundled toolchain is older. Keep the observed
# toolchain in the audit report and fail clearly if Go cannot build the module.
record_environment
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

echo "Target audit completed: $TARGET_ID ($TARGET_ARCH)"
