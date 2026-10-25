#!/usr/bin/env bash
set -Eeuo pipefail

repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$repo"

skip_build=false
if [[ "${1-}" == "--skip-build" ]]; then
    skip_build=true
    shift
fi
(($# == 0)) || { echo 'Usage: tests/test-installer-local.sh [--skip-build]' >&2; exit 2; }

if [[ "$skip_build" == false ]]; then
    ./build.sh
elif [[ ! -x build/go/cage-xtmapper || ! -f build/go/build-info.json ]]; then
    echo 'Local native build is missing; run ./build.sh or omit --skip-build.' >&2
    exit 1
fi

temporary="$(mktemp -d)"
trap 'rm -rf -- "$temporary"' EXIT
prefix="$temporary/prefix"
mkdir -p "$prefix/opt" "$prefix/usr/local/bin"
chmod 0700 "$prefix"

CGO_ENABLED=0 go build -trimpath -o "$temporary/install" ./tools/install
go run ./tools/release package --version local-test-1 --output "$temporary/release-1.tar"
go run ./tools/release package --version local-test-2 --output "$temporary/release-2.tar"

export XTMAPPER_INSTALLER_BIN="$temporary/install"
./install.sh install --test-prefix "$prefix" --archive "$temporary/release-1.tar"
./install.sh status --test-prefix "$prefix" > "$temporary/status-install.json"
grep -q '"state":"installed"' "$temporary/status-install.json"

./install.sh install --test-prefix "$prefix" --archive "$temporary/release-2.tar"
./install.sh status --test-prefix "$prefix" > "$temporary/status-update.json"
grep -q '"state":"installed"' "$temporary/status-update.json"

./install.sh update --test-prefix "$prefix" --archive "$temporary/release-2.tar"
./install.sh uninstall --test-prefix "$prefix"
./install.sh status --test-prefix "$prefix" > "$temporary/status-uninstall.json"
grep -q '"state":"uninstalled"' "$temporary/status-uninstall.json"

echo 'Local installer lifecycle tests passed without publishing a GitHub Release.'
