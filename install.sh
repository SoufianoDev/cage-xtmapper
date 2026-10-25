#!/usr/bin/env bash
set -Eeuo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
temporary="$(mktemp -d)"
trap 'rm -rf -- "$temporary"' EXIT
(cd -- "$script_dir" && CGO_ENABLED=0 go build -o "$temporary/install" ./tools/install)
# Exec the unlinked inode to preserve exit codes/signals without leaving a tool behind.
exec {installer}< "$temporary/install"
rm -rf -- "$temporary"
trap - EXIT
exec "/proc/self/fd/$installer" "$@"
