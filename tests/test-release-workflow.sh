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
require_text "name: Upload prepared release bundle"
require_text "name: Download prepared release bundle"
require_text "bash tools/ci/create-release-index.sh dist"
require_text "prepare-release-bundle:"
require_text "publish:"
require_text "contents: read"
require_text "contents: write"
require_text "release-bundle-"
require_text "'release-dry-run'"
require_text "github.event_name == 'workflow_dispatch' && 'release-dry-run'"
require_text "if: github.event_name == 'push'"
require_text "overwrite_files: false"
require_text "draft: true"
require_text "fail_on_unmatched_files: true"
require_text "package_version: \${{ steps.validate.outputs.package_version }}"
require_text "release_tag: \${{ steps.validate.outputs.release_tag }}"
require_text "target: debian-13"
require_text "target: ubuntu-24.04"
require_text "target: ubuntu-26.04"
require_text "target: fedora-44"
require_text "target: archlinux"
require_text "target: alpine-3.24"

push_only_steps="$(grep -Fc "if: github.event_name == 'push'" "$workflow")"
if [[ "$push_only_steps" -lt 3 ]]; then
  echo "The publish job, release inspection, and release attachment must all be gated to tag pushes." >&2
  exit 1
fi

index_script="$root/tools/ci/create-release-index.sh"
if [[ ! -x "$index_script" ]]; then
  echo "Release index builder must exist and be executable." >&2
  exit 1
fi

if grep -A 8 '^  prepare-release-bundle:' "$workflow" | grep -Fq 'contents: write'; then
  echo "Bundle preparation must not have release write permission." >&2
  exit 1
fi

echo "Release workflow dry-run and draft-publication contract passed."
