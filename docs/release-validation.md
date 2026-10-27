# Release Candidate Validation

## Purpose

The release workflow builds the existing amd64/arm64 .tar archives and installer helpers, plus experimental x86_64 native packages for Debian 13, Ubuntu 24.04 and 26.04, Fedora 44, Arch Linux, and Alpine 3.24. Native candidates are not yet Supported install paths; package CI does not prove startup in a real Wayland/graphics session or Waydroid input integration.

## Safe dry run

After the updated workflow file is available to GitHub Actions on the repository's default branch, open **Actions → Package release → Run workflow**, select the branch to validate, and provide a tag in one of these forms:

- vYYYYMMDD using a real calendar date, such as v20261027
- vMAJOR.MINOR.PATCH, such as v1.2.3

The manual run builds the requested version against the selected branch's source commit, tests native package metadata and lifecycle in the pinned target containers, verifies package checksums, prepares the asset index, and uploads a release-dry-run artifact. The manual path is always non-publishing: it skips the GitHub Release preflight and attachment steps and does not alter a release or publish a package repository.

Review the downloaded bundle for the expected names and sidecars:

- Versioned .tar and .sha256 files for amd64 and arm64.
- The standalone installer helper and .sha256 sidecar for each architecture.
- .deb candidates for Debian 13, Ubuntu 24.04, and Ubuntu 26.04.
- Fedora .rpm, Arch .pkg.tar.zst, and Alpine .apk candidates.
- native-package-index.md, its checksum, plus per-target version, manifest, provenance, and lifecycle evidence.

Alpine's package signature uses a disposable CI-only key. It does not authenticate the project as publisher; production signing and repository trust configuration remain maintainer-gated.

## Tag-triggered draft release

A push of an accepted version tag triggers the normal publication path. It builds the same target packages, checksums, and index, then attaches them to a new or clean draft release. The workflow deliberately refuses to modify published releases and refuses to append to an existing draft that already has assets; it never overwrites existing assets. Use a new, unused tag for a clean candidate rather than repurposing a draft containing older assets.

The release stays as a draft for maintainer review. This workflow does not publish APT, DNF/RPM, Pacman, or APK repositories. SHA-256 sidecars detect corruption but do not authenticate the publisher.

## Evidence required before support claims

Do not change a support-matrix cell to Supported until the exact distribution/version/architecture/format has package metadata, dependency, lifecycle, executable startup, real session, and Waydroid/platform integration evidence. Steam Deck Desktop/Gaming Mode and Bazzite must be tested independently; a Fedora RPM result is not proof for Bazzite.
