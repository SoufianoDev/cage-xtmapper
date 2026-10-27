#!/bin/sh
set -eu

: "${TARGET_ID:?TARGET_ID is required}"
: "${TARGET_IMAGE:?TARGET_IMAGE is required}"
: "${GITHUB_RUN_ID:=local}"
: "${GITHUB_SHA:=unknown}"
: "${TARGET_ARCH:=amd64}"

if [ -n "${PACKAGE_VERSION:-}" ]; then
  case "$PACKAGE_VERSION" in
    *[!0-9.]*|.*|*.)
      echo "PACKAGE_VERSION must contain only numeric dot-separated components: $PACKAGE_VERSION" >&2
      exit 2
      ;;
  esac
  case "$PACKAGE_VERSION" in
    *..*)
      echo "PACKAGE_VERSION must not contain empty components: $PACKAGE_VERSION" >&2
      exit 2
      ;;
  esac
fi

root=$(pwd -P)
audit="$root/build/target-audit/$TARGET_ID"
mkdir -p "$audit"

# CI artifacts are public build evidence. Never put the APK private signing
# key inside the directory uploaded as an artifact.
if [ "$TARGET_ID" = "alpine-3.24" ] && [ -n "${APK_CI_KEY_DIR:-}" ]; then
  case "$APK_CI_KEY_DIR" in
    /*) ;;
    *)
      echo "APK_CI_KEY_DIR must be an absolute path outside the uploaded audit directory." >&2
      exit 2
      ;;
  esac
  case "$APK_CI_KEY_DIR" in
    "$audit"|"$audit"/*|*"/../"*|*"/./"*)
      echo "Refusing to place the APK signing key inside or ambiguously near the uploaded audit directory." >&2
      exit 2
      ;;
  esac
fi
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
    echo "requested_package_version=${PACKAGE_VERSION:-ci-default}"
    uname -a
    cat /etc/os-release
    if [ -r /etc/debian_version ]; then
      printf "debian_version_file="
      tr -d '\n' < /etc/debian_version
      printf '\n'
    fi
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

version="${PACKAGE_VERSION:-audit-$GITHUB_RUN_ID-$TARGET_ID}"
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

run_logged() {
  log_name="$1"; shift
  log_file="$audit/$log_name.log"
  status_file="$audit/$log_name.status"
  if "$@" > "$log_file" 2>&1; then
    status=0
  else
    status=$?
  fi
  printf 'exit_status=%s\n' "$status" > "$status_file"
  if [ "$status" -ne 0 ]; then
    cat "$log_file" >&2
    exit "$status"
  fi
}

assert_cli_smoke() {
  suffix="$1"
  run_logged "$suffix" /usr/bin/cage-xtmapper --help
  if ! grep -Fq 'Usage: cage-xtmapper [options]' "$audit/$suffix.log"; then
    echo "Installed cage-xtmapper did not emit the expected CLI usage text ($suffix)." >&2
    cat "$audit/$suffix.log" >&2
    exit 1
  fi
  printf 'result=passed\n' > "$audit/$suffix.result"
}

assert_installed_version() {
  requested="$1"
  suffix="$2"
  case "$TARGET_ID" in
    debian-13|ubuntu-24.04|ubuntu-26.04)
      actual="$(dpkg-query -W -f='${Version}' cage-xtmapper)"
      expected="$requested"
      ;;
    fedora-44)
      actual="$(rpm -q --qf '%{VERSION}' cage-xtmapper)"
      expected="$requested"
      ;;
    archlinux)
      actual="$(pacman -Q cage-xtmapper | awk '{print $2}')"
      expected="$requested-1"
      ;;
    alpine-3.24)
      actual="$(awk -F: '$1 == "P" { name=$2 } $1 == "V" && name == "cage-xtmapper" { print $2; exit }' /lib/apk/db/installed)"
      expected="$requested-r0"
      ;;
  esac
  if [ "$actual" != "$expected" ]; then
    echo "Expected installed package version $expected, found $actual on $TARGET_ID." >&2
    exit 1
  fi
  printf '%s\n' "$actual" > "$audit/package-version-$suffix.txt"
}

assert_removed() {
  suffix="$1"
  case "$TARGET_ID" in
    debian-13|ubuntu-24.04|ubuntu-26.04)
      if dpkg-query -W cage-xtmapper >/dev/null 2>&1; then
        echo "Debian package metadata remains after removal ($suffix)." >&2
        exit 1
      fi
      ;;
    fedora-44)
      if rpm -q cage-xtmapper >/dev/null 2>&1; then
        echo "RPM package metadata remains after removal ($suffix)." >&2
        exit 1
      fi
      ;;
    archlinux)
      if pacman -Q cage-xtmapper >/dev/null 2>&1; then
        echo "Pacman package metadata remains after removal ($suffix)." >&2
        exit 1
      fi
      ;;
    alpine-3.24)
      if apk info --exists cage-xtmapper >/dev/null 2>&1; then
        echo "APK package metadata remains after removal ($suffix)." >&2
        exit 1
      fi
      ;;
  esac

  for path in /usr/bin/cage-xtmapper /usr/share/doc/cage-xtmapper /usr/share/licenses/cage-xtmapper; do
    if [ -e "$path" ] || [ -L "$path" ]; then
      echo "Package-owned path remains after removal ($suffix): $path" >&2
      exit 1
    fi
  done
  {
    echo "package_installed=false"
    echo "absent=/usr/bin/cage-xtmapper"
    echo "absent=/usr/share/doc/cage-xtmapper"
    echo "absent=/usr/share/licenses/cage-xtmapper"
  } > "$audit/package-removal-$suffix.txt"
}

previous_version="0.0.$((GITHUB_RUN_ID - 1))"
package_version="${PACKAGE_VERSION:-0.0.$GITHUB_RUN_ID}"

case "$TARGET_ID" in
  debian-13|ubuntu-24.04|ubuntu-26.04)
    arch="$(dpkg --print-architecture)"
    previous_package="$audit/cage-xtmapper_${previous_version}_${arch}.deb"
    package="$audit/cage-xtmapper_${package_version}_${arch}.deb"
    export PACKAGE_MAINTAINER="Cage XtMapper CI <ci@example.invalid>"
    run_logged deb-package-previous bash tools/package/build-deb.sh "$previous_version" "$previous_package"
    run_logged deb-package-current bash tools/package/build-deb.sh "$package_version" "$package"
    run_logged deb-install-previous apt-get install -y "$previous_package"
    assert_installed_version "$previous_version" deb-install-previous
    test -x /usr/bin/cage-xtmapper
    assert_cli_smoke deb-cli-smoke
    run_logged deb-upgrade apt-get install -y "$package"
    assert_installed_version "$package_version" deb-upgrade
    run_logged deb-current-noop apt-get install -y "$package"
    assert_installed_version "$package_version" deb-current-noop
    run_logged deb-downgrade apt-get install --allow-downgrades -y "$previous_package"
    assert_installed_version "$previous_version" deb-downgrade
    run_logged deb-upgrade-after-downgrade apt-get install -y "$package"
    assert_installed_version "$package_version" deb-upgrade-after-downgrade
    dpkg --audit > "$audit/dpkg-audit-before-purge.txt"
    run_logged deb-purge apt-get purge -y cage-xtmapper
    assert_removed deb-purge
    test ! -e /usr/bin/cage-xtmapper
    if dpkg-query -W cage-xtmapper > "$audit/deb-query-after-purge.txt" 2>&1; then
      echo "Debian package metadata remains after purge." >&2
      exit 1
    fi
    dpkg --audit > "$audit/dpkg-audit-after-purge.txt"
    run_logged deb-clean-reinstall apt-get install -y "$package"
    assert_installed_version "$package_version" deb-clean-reinstall
    run_logged deb-final-purge apt-get purge -y cage-xtmapper
    assert_removed deb-final-purge
    test ! -e /usr/bin/cage-xtmapper
    echo "Debian lifecycle passed: install, upgrade, current/no-op, explicit downgrade, upgrade, purge, clean reinstall, purge." | tee "$audit/deb-lifecycle-result.txt"
    ;;

  fedora-44)
    previous_package="$audit/cage-xtmapper-$previous_version-1.rpm"
    package="$audit/cage-xtmapper-$package_version-1.rpm"
    run_logged rpm-package-previous bash tools/package/build-rpm.sh "$previous_version" "$previous_package"
    run_logged rpm-package-current bash tools/package/build-rpm.sh "$package_version" "$package"
    run_logged rpm-install-previous dnf install -y "$previous_package"
    assert_installed_version "$previous_version" rpm-install-previous
    test -x /usr/bin/cage-xtmapper
    assert_cli_smoke rpm-cli-smoke
    run_logged rpm-upgrade dnf install -y "$package"
    assert_installed_version "$package_version" rpm-upgrade
    run_logged rpm-current-reinstall rpm -Uvh --replacepkgs "$package"
    assert_installed_version "$package_version" rpm-current-reinstall
    run_logged rpm-downgrade rpm -Uvh --oldpackage "$previous_package"
    assert_installed_version "$previous_version" rpm-downgrade
    run_logged rpm-upgrade-after-downgrade dnf install -y "$package"
    assert_installed_version "$package_version" rpm-upgrade-after-downgrade
    run_logged rpm-remove dnf remove -y cage-xtmapper
    assert_removed rpm-remove
    test ! -e /usr/bin/cage-xtmapper
    run_logged rpm-clean-reinstall dnf install -y "$package"
    assert_installed_version "$package_version" rpm-clean-reinstall
    run_logged rpm-final-remove dnf remove -y cage-xtmapper
    assert_removed rpm-final-remove
    test ! -e /usr/bin/cage-xtmapper
    echo "RPM lifecycle passed: install, upgrade, same-version reinstall, explicit downgrade, upgrade, remove, clean reinstall, remove." | tee "$audit/rpm-lifecycle-result.txt"
    ;;

  archlinux)
    machine="$(uname -m)"
    previous_package="$audit/cage-xtmapper-$previous_version-1-$machine.pkg.tar.zst"
    package="$audit/cage-xtmapper-$package_version-1-$machine.pkg.tar.zst"
    run_logged arch-package-previous bash tools/package/build-arch.sh "$previous_version" "$previous_package"
    run_logged arch-package-current bash tools/package/build-arch.sh "$package_version" "$package"
    run_logged arch-install-previous pacman -U --noconfirm "$previous_package"
    assert_installed_version "$previous_version" arch-install-previous
    test -x /usr/bin/cage-xtmapper
    assert_cli_smoke arch-cli-smoke
    run_logged arch-upgrade pacman -U --noconfirm "$package"
    assert_installed_version "$package_version" arch-upgrade
    run_logged arch-current-reinstall pacman -U --noconfirm "$package"
    assert_installed_version "$package_version" arch-current-reinstall
    run_logged arch-downgrade pacman -U --noconfirm "$previous_package"
    assert_installed_version "$previous_version" arch-downgrade
    run_logged arch-upgrade-after-downgrade pacman -U --noconfirm "$package"
    assert_installed_version "$package_version" arch-upgrade-after-downgrade
    run_logged arch-remove pacman -Rns --noconfirm cage-xtmapper
    assert_removed arch-remove
    test ! -e /usr/bin/cage-xtmapper
    run_logged arch-clean-reinstall pacman -U --noconfirm "$package"
    assert_installed_version "$package_version" arch-clean-reinstall
    run_logged arch-final-remove pacman -Rns --noconfirm cage-xtmapper
    assert_removed arch-final-remove
    test ! -e /usr/bin/cage-xtmapper
    echo "Pacman lifecycle passed: install, upgrade, same-version reinstall, explicit downgrade, upgrade, remove, clean reinstall, remove." | tee "$audit/arch-lifecycle-result.txt"
    ;;

  alpine-3.24)
    previous_package="$audit/cage-xtmapper-$previous_version-r0.apk"
    package="$audit/cage-xtmapper-$package_version-r0.apk"
    run_logged apk-package-previous bash tools/package/build-apk.sh "$previous_version" "$previous_package"
    run_logged apk-package-current bash tools/package/build-apk.sh "$package_version" "$package"
    run_logged apk-install-previous apk add --allow-untrusted "$previous_package"
    assert_installed_version "$previous_version" apk-install-previous
    test -x /usr/bin/cage-xtmapper
    assert_cli_smoke apk-cli-smoke
    run_logged apk-upgrade apk add --allow-untrusted "$package"
    assert_installed_version "$package_version" apk-upgrade
    run_logged apk-current-noop apk add --allow-untrusted "$package"
    assert_installed_version "$package_version" apk-current-noop
    printf '%s\n' "Skipped: Alpine documents package/version downgrade as unsupported; no APK world-state mutation attempted. https://wiki.alpinelinux.org/wiki/Upgrading_from_older_versions" > "$audit/apk-downgrade-policy.txt"
    run_logged apk-remove apk del cage-xtmapper
    assert_removed apk-remove
    test ! -e /usr/bin/cage-xtmapper
    run_logged apk-clean-reinstall apk add --allow-untrusted "$package"
    assert_installed_version "$package_version" apk-clean-reinstall
    run_logged apk-final-remove apk del cage-xtmapper
    assert_removed apk-final-remove
    test ! -e /usr/bin/cage-xtmapper
    echo "APK lifecycle passed: install, upgrade, current/no-op, remove, clean reinstall, remove. Explicit downgrade is not claimed." | tee "$audit/apk-lifecycle-result.txt"
    ;;
esac

echo "Target audit completed: $TARGET_ID ($TARGET_ARCH)"
