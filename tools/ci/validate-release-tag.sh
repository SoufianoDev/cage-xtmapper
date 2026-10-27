#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  echo "Usage: tools/ci/validate-release-tag.sh vYYYYMMDD|vMAJOR.MINOR.PATCH" >&2
}

[[ $# -eq 1 ]] || { usage; exit 2; }
release_tag=$1

if [[ ! "$release_tag" =~ ^v([0-9]{8}|[0-9]+[.][0-9]+[.][0-9]+)$ ]]; then
  echo "Unsupported release tag format: $release_tag" >&2
  usage
  exit 2
fi

version="${release_tag#v}"
if [[ "$version" =~ ^[0-9]{8}$ ]]; then
  year="${version:0:4}"
  month="${version:4:2}"
  day="${version:6:2}"
  calendar_date="$(date -u -d "$year-$month-$day" +%Y%m%d 2>/dev/null)" || {
    echo "Invalid calendar date in release tag: $release_tag" >&2
    exit 2
  }
  [[ "$calendar_date" == "$version" ]] || {
    echo "Invalid calendar date in release tag: $release_tag" >&2
    exit 2
  }
fi

if [[ "$version" == "0.0.0" ]]; then
  echo "v0.0.0 has no lower numeric package version for lifecycle testing." >&2
  exit 2
fi

printf '%s\n' "$version"
