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
    previous_version="0.0.$((GITHUB_RUN_ID - 1))"
    package_version="0.0.$GITHUB_RUN_ID"
    arch="$(dpkg --print-architecture)"
    previous_package="$audit/cage-xtmapper_${previous_version}_${arch}.deb"
    package="$audit/cage-xtmapper_${package_version}_${arch}.deb"
    export PACKAGE_MAINTAINER="Cage XtMapper CI <ci@example.invalid>"

    if ! bash tools/package/build-deb.sh "$previous_version" "$previous_package" > "$audit/deb-package-previous.log" 2>&1; then
      cat "$audit/deb-package-previous.log" >&2
      exit 1
    fi
    if ! bash tools/package/build-deb.sh "$package_version" "$package" > "$audit/deb-package-current.log" 2>&1; then
      cat "$audit/deb-package-current.log" >&2
      exit 1
    fi
    cat "$audit/deb-package-previous.log" "$audit/deb-package-current.log"

    assert_deb_version() {
      actual="$(dpkg-query -W -f='${Version}' cage-xtmapper)"
      if [ "$actual" != "$1" ]; then
        echo "Expected Debian package version $1, found $actual." >&2
        exit 1
      fi
      printf '%s\n' "$actual"
    }

    apt-get install -y "$previous_package" > "$audit/deb-install-previous.log" 2>&1 || {
      cat "$audit/deb-install-previous.log" >&2
      exit 1
    }
    assert_deb_version "$previous_version" > "$audit/deb-version-after-install-previous.txt"
    test -x /usr/bin/cage-xtmapper

    apt-get install -y "$package" > "$audit/deb-upgrade.log" 2>&1 || {
      cat "$audit/deb-upgrade.log" >&2
      exit 1
    }
    assert_deb_version "$package_version" > "$audit/deb-version-after-upgrade.txt"

    apt-get install -y "$package" > "$audit/deb-current-noop.log" 2>&1 || {
      cat "$audit/deb-current-noop.log" >&2
      exit 1
    }
    assert_deb_version "$package_version" > "$audit/deb-version-after-current-noop.txt"

    apt-get install --allow-downgrades -y "$previous_package" > "$audit/deb-downgrade.log" 2>&1 || {
      cat "$audit/deb-downgrade.log" >&2
      exit 1
    }
    assert_deb_version "$previous_version" > "$audit/deb-version-after-downgrade.txt"

    apt-get install -y "$package" > "$audit/deb-upgrade-after-downgrade.log" 2>&1 || {
      cat "$audit/deb-upgrade-after-downgrade.log" >&2
      exit 1
    }
    assert_deb_version "$package_version" > "$audit/deb-version-after-upgrade-after-downgrade.txt"
    dpkg --audit > "$audit/dpkg-audit-before-purge.txt"

    apt-get purge -y cage-xtmapper > "$audit/deb-purge.log" 2>&1 || {
      cat "$audit/deb-purge.log" >&2
      exit 1
    }
    test ! -e /usr/bin/cage-xtmapper
    ! dpkg-query -W cage-xtmapper > "$audit/deb-query-after-purge.txt" 2>&1
    dpkg --audit > "$audit/dpkg-audit-after-purge.txt"

    apt-get install -y "$package" > "$audit/deb-clean-reinstall.log" 2>&1 || {
      cat "$audit/deb-clean-reinstall.log" >&2
      exit 1
    }
    assert_deb_version "$package_version" > "$audit/deb-version-after-clean-reinstall.txt"
    apt-get purge -y cage-xtmapper > "$audit/deb-final-purge.log" 2>&1 || {
      cat "$audit/deb-final-purge.log" >&2
      exit 1
    }
    test ! -e /usr/bin/cage-xtmapper
    echo "Debian lifecycle passed: install previous -> upgrade -> current/no-op -> explicit downgrade -> upgrade -> purge -> clean reinstall -> purge." | tee "$audit/deb-lifecycle-result.txt"
    ;;

  fedora-44)
    previous_version="0.0.$((GITHUB_RUN_ID - 1))"
    package_version="0.0.$GITHUB_RUN_ID"
    previous_package="$audit/cage-xtmapper-$previous_version-1.rpm"
    package="$audit/cage-xtmapper-$package_version-1.rpm"

    if ! bash tools/package/build-rpm.sh "$previous_version" "$previous_package" > "$audit/rpm-package-previous.log" 2>&1; then
      cat "$audit/rpm-package-previous.log" >&2
      exit 1
    fi
    if ! bash tools/package/build-rpm.sh "$package_version" "$package" > "$audit/rpm-package-current.log" 2>&1; then
      cat "$audit/rpm-package-current.log" >&2
      exit 1
    fi
    cat "$audit/rpm-package-previous.log" "$audit/rpm-package-current.log"

    assert_rpm_version() {
      actual="$(rpm -q --qf '%{VERSION}' cage-xtmapper)"
      if [ "$actual" != "$1" ]; then
        echo "Expected RPM package version $1, found $actual." >&2
        exit 1
      fi
      printf '%s\n' "$actual"
    }

    dnf install -y "$previous_package" > "$audit/rpm-install-previous.log" 2>&1 || {
      cat "$audit/rpm-install-previous.log" >&2
      exit 1
    }
    assert_rpm_version "$previous_version" > "$audit/rpm-version-after-install-previous.txt"
    test -x /usr/bin/cage-xtmapper

    dnf install -y "$package" > "$audit/rpm-upgrade.log" 2>&1 || {
      cat "$audit/rpm-upgrade.log" >&2
      exit 1
    }
    assert_rpm_version "$package_version" > "$audit/rpm-version-after-upgrade.txt"

    rpm -Uvh --replacepkgs "$package" > "$audit/rpm-current-reinstall.log" 2>&1 || {
      cat "$audit/rpm-current-reinstall.log" >&2
      exit 1
    }
    assert_rpm_version "$package_version" > "$audit/rpm-version-after-current-reinstall.txt"

    rpm -Uvh --oldpackage "$previous_package" > "$audit/rpm-downgrade.log" 2>&1 || {
      cat "$audit/rpm-downgrade.log" >&2
      exit 1
    }
    assert_rpm_version "$previous_version" > "$audit/rpm-version-after-downgrade.txt"

    dnf install -y "$package" > "$audit/rpm-upgrade-after-downgrade.log" 2>&1 || {
      cat "$audit/rpm-upgrade-after-downgrade.log" >&2
      exit 1
    }
    assert_rpm_version "$package_version" > "$audit/rpm-version-after-upgrade-after-downgrade.txt"

    dnf remove -y cage-xtmapper > "$audit/rpm-remove.log" 2>&1 || {
      cat "$audit/rpm-remove.log" >&2
      exit 1
    }
    test ! -e /usr/bin/cage-xtmapper

    dnf install -y "$package" > "$audit/rpm-clean-reinstall.log" 2>&1 || {
      cat "$audit/rpm-clean-reinstall.log" >&2
      exit 1
    }
    assert_rpm_version "$package_version" > "$audit/rpm-version-after-clean-reinstall.txt"
    dnf remove -y cage-xtmapper > "$audit/rpm-final-remove.log" 2>&1 || {
      cat "$audit/rpm-final-remove.log" >&2
      exit 1
    }
    test ! -e /usr/bin/cage-xtmapper
    echo "RPM lifecycle passed: install previous -> upgrade -> same-version reinstall -> explicit downgrade -> upgrade -> remove -> clean reinstall -> remove." | tee "$audit/rpm-lifecycle-result.txt"
    ;;

  archlinux)
    previous_version="0.0.$((GITHUB_RUN_ID - 1))"
    package_version="0.0.$GITHUB_RUN_ID"
    machine="$(uname -m)"
    previous_package="$audit/cage-xtmapper-$previous_version-1-$machine.pkg.tar.zst"
    package="$audit/cage-xtmapper-$package_version-1-$machine.pkg.tar.zst"

    if ! bash tools/package/build-arch.sh "$previous_version" "$previous_package" > "$audit/arch-package-previous.log" 2>&1; then
      cat "$audit/arch-package-previous.log" >&2
      exit 1
    fi
    if ! bash tools/package/build-arch.sh "$package_version" "$package" > "$audit/arch-package-current.log" 2>&1; then
      cat "$audit/arch-package-current.log" >&2
      exit 1
    fi
    cat "$audit/arch-package-previous.log" "$audit/arch-package-current.log"

    assert_arch_version() {
      actual="$(pacman -Q cage-xtmapper | awk '{print $2}')"
      expected="$1-1"
      if [ "$actual" != "$expected" ]; then
        echo "Expected Arch package version $expected, found $actual." >&2
        exit 1
      fi
      printf '%s\n' "$actual"
    }

    pacman -U --noconfirm "$previous_package" > "$audit/arch-install-previous.log" 2>&1 || {
      cat "$audit/arch-install-previous.log" >&2
      exit 1
    }
    assert_arch_version "$previous_version" > "$audit/arch-version-after-install-previous.txt"
    test -x /usr/bin/cage-xtmapper

    pacman -U --noconfirm "$package" > "$audit/arch-upgrade.log" 2>&1 || {
      cat "$audit/arch-upgrade.log" >&2
      exit 1
    }
    assert_arch_version "$package_version" > "$audit/arch-version-after-upgrade.txt"

    pacman -U --noconfirm "$package" > "$audit/arch-current-reinstall.log" 2>&1 || {
      cat "$audit/arch-current-reinstall.log" >&2
      exit 1
    }
    assert_arch_version "$package_version" > "$audit/arch-version-after-current-reinstall.txt"

    pacman -U --noconfirm "$previous_package" > "$audit/arch-downgrade.log" 2>&1 || {
      cat "$audit/arch-downgrade.log" >&2
      exit 1
    }
    assert_arch_version "$previous_version" > "$audit/arch-version-after-downgrade.txt"

    pacman -U --noconfirm "$package" > "$audit/arch-upgrade-after-downgrade.log" 2>&1 || {
      cat "$audit/arch-upgrade-after-downgrade.log" >&2
      exit 1
    }
    assert_arch_version "$package_version" > "$audit/arch-version-after-upgrade-after-downgrade.txt"

    pacman -Rns --noconfirm cage-xtmapper > "$audit/arch-remove.log" 2>&1 || {
      cat "$audit/arch-remove.log" >&2
      exit 1
    }
    test ! -e /usr/bin/cage-xtmapper

    pacman -U --noconfirm "$package" > "$audit/arch-clean-reinstall.log" 2>&1 || {
      cat "$audit/arch-clean-reinstall.log" >&2
      exit 1
    }
    assert_arch_version "$package_version" > "$audit/arch-version-after-clean-reinstall.txt"
    pacman -Rns --noconfirm cage-xtmapper > "$audit/arch-final-remove.log" 2>&1 || {
      cat "$audit/arch-final-remove.log" >&2
      exit 1
    }
    test ! -e /usr/bin/cage-xtmapper
    echo "Pacman lifecycle passed: install previous -> upgrade -> same-version reinstall -> explicit downgrade -> upgrade -> remove -> clean reinstall -> remove." | tee "$audit/arch-lifecycle-result.txt"
    ;;

  alpine-3.24)
    previous_version="0.0.$((GITHUB_RUN_ID - 1))"
    package_version="0.0.$GITHUB_RUN_ID"
    previous_package="$audit/cage-xtmapper-$previous_version-r0.apk"
    package="$audit/cage-xtmapper-$package_version-r0.apk"

    if ! bash tools/package/build-apk.sh "$previous_version" "$previous_package" > "$audit/apk-package-previous.log" 2>&1; then
      cat "$audit/apk-package-previous.log" >&2
      exit 1
    fi
    if ! bash tools/package/build-apk.sh "$package_version" "$package" > "$audit/apk-package-current.log" 2>&1; then
      cat "$audit/apk-package-current.log" >&2
      exit 1
    fi
    cat "$audit/apk-package-previous.log" "$audit/apk-package-current.log"

    assert_apk_version() {
      actual="$(awk -F: '$1 == "P" { name=$2 } $1 == "V" && name == "cage-xtmapper" { print $2; exit }' /lib/apk/db/installed)"
      expected="$1-r0"
      if [ "$actual" != "$expected" ]; then
        echo "Expected APK package version $expected, found $actual." >&2
        exit 1
      fi
      printf '%s\n' "$actual"
    }

    apk add --allow-untrusted "$previous_package" > "$audit/apk-install-previous.log" 2>&1 || {
      cat "$audit/apk-install-previous.log" >&2
      exit 1
    }
    assert_apk_version "$previous_version" > "$audit/apk-version-after-install-previous.txt"
    test -x /usr/bin/cage-xtmapper

    apk add --allow-untrusted "$package" > "$audit/apk-upgrade.log" 2>&1 || {
      cat "$audit/apk-upgrade.log" >&2
      exit 1
    }
    assert_apk_version "$package_version" > "$audit/apk-version-after-upgrade.txt"

    apk add --allow-untrusted "$package" > "$audit/apk-current-noop.log" 2>&1 || {
      cat "$audit/apk-current-noop.log" >&2
      exit 1
    }
    assert_apk_version "$package_version" > "$audit/apk-version-after-current-noop.txt"

    printf '%s\n' "Skipped: Alpine documents package/version downgrade as unsupported; this test does not force downgrade by mutating APK world state. https://wiki.alpinelinux.org/wiki/Upgrading_from_older_versions" > "$audit/apk-downgrade-policy.txt"

    apk del cage-xtmapper > "$audit/apk-remove.log" 2>&1 || {
      cat "$audit/apk-remove.log" >&2
      exit 1
    }
    test ! -e /usr/bin/cage-xtmapper

    apk add --allow-untrusted "$package" > "$audit/apk-clean-reinstall.log" 2>&1 || {
      cat "$audit/apk-clean-reinstall.log" >&2
      exit 1
    }
    assert_apk_version "$package_version" > "$audit/apk-version-after-clean-reinstall.txt"
    apk del cage-xtmapper > "$audit/apk-final-remove.log" 2>&1 || {
      cat "$audit/apk-final-remove.log" >&2
      exit 1
    }
    test ! -e /usr/bin/cage-xtmapper
    echo "APK lifecycle passed: install previous -> upgrade -> current/no-op -> remove -> clean reinstall -> remove. Explicit downgrade is not claimed." | tee "$audit/apk-lifecycle-result.txt"
    ;;
esac

echo "Target audit completed: $TARGET_ID ($TARGET_ARCH)"
