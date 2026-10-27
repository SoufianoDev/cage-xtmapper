#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
workflow="$root/.github/workflows/release.yml"

require_text() {
  local pattern=$1
  if ! grep -Fq -- "$pattern" "$workflow"; then
    echo "Release workflow is missing required contract: $pattern" >&2
    exit 1
  fi
}

require_text "workflow_dispatch:"
require_text "release_tag:"
require_text "name: Upload dry-run release bundle"
require_text "if: github.event_name == 'workflow_dispatch'"
require_text "if: github.event_name == 'push'"
require_text "overwrite_files: false"
require_text "draft: true"
require_text "fail_on_unmatched_files: true"
require_text 'package_version: ${{ steps.validate.outputs.package_version }}'
require_text 'release_tag: ${{ steps.validate.outputs.release_tag }}'
require_text "target: debian-13"
require_text "target: ubuntu-24.04"
require_text "target: ubuntu-26.04"
require_text "target: fedora-44"
require_text "target: archlinux"
require_text "target: alpine-3.24"

push_only_steps="$(grep -Fc "if: github.event_name == 'push'" "$workflow")"
if [[ "$push_only_steps" -lt 2 ]]; then
  echo "Release inspection and release attachment must both be gated to tag pushes." >&2
  exit 1
fi

echo "Release workflow dry-run and draft-publication contract passed."
