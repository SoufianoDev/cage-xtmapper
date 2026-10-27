#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
validator="$root/tools/ci/validate-release-tag.sh"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

assert_accept() {
  local tag=$1 expected=$2 actual
  actual="$(bash "$validator" "$tag")"
  if [[ "$actual" != "$expected" ]]; then
    echo "$tag returned $actual; expected $expected" >&2
    exit 1
  fi
}

assert_reject() {
  local tag=$1
  if bash "$validator" "$tag" >"$tmp" 2>&1; then
    echo "Expected release tag to be rejected: $tag" >&2
    exit 1
  fi
}

assert_accept v20261019 20261019
assert_accept v20261027 20261027
assert_accept v1.2.3 1.2.3
assert_accept v0.0.1 0.0.1

assert_reject ""
assert_reject 20261019
assert_reject v1
assert_reject v1.2
assert_reject vfoo
assert_reject v20261345
assert_reject v00000000
assert_reject v0.0.0

echo "Release tag validation fixtures passed."
