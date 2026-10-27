# Implementation Plan: Native Linux Release Packaging and Distribution

- **Project:** Cage XtMapper
- **Spec:** [`spec.md`](./spec.md)
- **Status:** In progress; evidence snapshot updated 2026-10-27
- **Planning baseline:** `migrate-to-c-golang-no-submodule` at `42271bbb533ee00fb46c9ea9146821a34cf3a4c3`
- **Installer baseline:** `c86ed44ed58e49c2ac6835968c3aed25dc9fb0fa`
- **Planning date:** 2026-10-27

## 1. Objective

Deliver a repeatable release pipeline and a version-specific installation path for every distribution family named in the specification: Debian, Ubuntu, Fedora, Arch Linux, Alpine Linux, SteamOS, and Bazzite. The product goal is a tested `Supported` route for each family. An artifact may be produced earlier as `Experimental`, but build success or package installation alone must never be presented as proof of runtime compatibility.

The 48-hour window is a focused implementation sprint, not permission to skip target testing or claim unverified support. SteamOS/Gaming Mode and Bazzite session tests depend on access to suitable target systems. If that evidence is unavailable in the window, record the block and keep that matrix cell `Experimental` until tested.

## 2. Existing implementation facts

- `build.sh` produces one Linux ELF from Go/cgo and the checked-in Cage/wlroots trees. It sets Meson `default_library=static` and requests fallbacks for Wayland server/client/protocols, xkbcommon, and pixman; this does not prove that all third-party dependencies are static or that the resulting ELF works across distributions.
- `internal/releasebuild/build.go` already inspects ELF metadata while generating release provenance: interpreter, direct shared-library SONAMEs, imported GLIBC symbol versions, and rejection of RPATH/RUNPATH or external Cage/wlroots shared-library dependencies.
- `install_deps.sh` contains build dependencies for Debian-compatible systems, Fedora, Arch, and Alpine. Those lists are not runtime compatibility declarations.
- On the active branch, .github/workflows/ci.yml validates amd64 and arm64 on Ubuntu 26.04 and retains ELF/audit artifacts. The successful validation run for source commit 9db78c2b1f2827bad631988edb7f6f99a1484b96 is [run 40501883118](https://github.com/SoufianoDev/cage-xtmapper/actions/runs/40501883118); both architecture jobs passed.
- .github/workflows/release.yml remains the legacy release path: it builds amd64 and arm64 archives on Ubuntu runners, publishes the existing versioned .tar archive plus installer helpers, and creates a draft GitHub Release. The new target audit workflow creates CI-only .deb, .rpm, Arch .pkg.tar.zst, Alpine .apk, and portable .tar.zst candidates, but does not attach those candidates to a release. Flatpak and AppImage recipes are absent.
- The current target audit retains per-target ELF and dependency evidence. [Run 40501883064](https://github.com/SoufianoDev/cage-xtmapper/actions/runs/40501883064) passed all six container target jobs on source commit 9db78c2b1f2827bad631988edb7f6f99a1484b96. The generated evidence includes file, readelf -l/-d/--version-info, guarded loader resolution/transitive closure, Meson introspection, Ninja link commands, manifests/provenance, native package metadata, installed package inventories, lifecycle logs, and checksums. Historical run 40438105888 remains a valid record of the earlier zero-artifact state, but it is no longer the current evidence state.
- The current bootstrap layout uses `/opt/cage-xtmapper` and `/usr/local/bin/cage-xtmapper`; package migration must avoid silently claiming or deleting files owned by that installer.

## 3. Accepted packaging policy

Use the hybrid model (spec Q7):

1. **Native packages** (`.deb`, `.rpm`, Arch package, `.apk`) use target-appropriate system libraries and accurate package-manager dependency metadata generated/validated with the target ecosystem's tools.
2. **Portable archives / AppImage** bundle only libraries demonstrated to be safe and necessary to bundle. Keep libc, graphics drivers/loader, low-level system components, and other host-coupled libraries out of the bundle unless a documented compatibility audit supports a deliberate exception.
3. **Alpine** gets a distinct musl-targeted build and package path; never relabel the glibc ELF as Alpine-compatible without evidence.
4. **SteamOS and Bazzite** get independent package/session validation. Flatpak and portable archive on SteamOS are separate support cells. Bazzite RPM and its portable artifact are separate support cells; AppImage is selected only if real tests pass, otherwise use `.tar.zst`.
5. **Repository hosting and production signing** are not implemented or published until the project maintainer approves ownership of hosting, keys, operating costs, and recovery/rotation procedures. Direct, exact-version downloads from GitHub Releases remain the initial artifact path.

## 4. Target matrix for the sprint

Proposed first-sprint baselines below use the current stable/recommended release as of 2026-10-27 where an official release page identifies one. Treat them as candidate test targets, not a claim of compatibility. In Phase A, pin image digests / repository snapshots where possible and record the exact SteamOS build reported by the actual device; if an image has moved, record the tested version rather than relying on a mutable `latest` label. Do not quietly mix an unpinned rolling container, a live image, and a stable release in the same result.

| Target | Initial architecture scope | Candidate artifact(s) | Required validation before `Supported` |
|---|---|---|---|
| Debian | 13.7 stable (`trixie`) | amd64; arm64 where native build/runtime tests pass | `.deb`, existing portable `.tar` | Native dependency generation, clean install/launch, update, explicit downgrade where supported, removal |
| Ubuntu | 24.04.5 LTS and 26.04.1 LTS | amd64; arm64 where native build/runtime tests pass | `.deb`, existing portable `.tar` | Validate each LTS baseline independently; do not infer one LTS from the other |
| Fedora | 44 | amd64; arm64 where native build/runtime tests pass | `.rpm`, existing portable `.tar` | RPM dependency solving, clean install/launch, update, version selection, removal |
| Arch Linux | Snapshot pinned from 2026-10-27 repositories | x86_64 initial; other arches only after validation | `PKGBUILD`/Pacman package; AUR recipe path documented | `makepkg` checks, package content/metadata, install/upgrade/remove on the exact snapshot |
| Alpine Linux | 3.24.3 stable | amd64; arm64 where a native target build can be validated | APKBUILD and `.apk` from a musl build | musl interpreter/ABI, `apk` dependency resolution and install/launch/removal |
| SteamOS | Stable channel; SteamOS 3.9.5 candidate (confirm actual build on device) | x86_64 | Flatpak and portable archive, each versioned | Separate Desktop Mode and intended Gaming Mode / Wayland / Waydroid tests for each path |
| Bazzite | Stable image 44.20261026 candidate; record full image identity | x86_64 | RPM plus AppImage if validated; otherwise `.tar.zst` | RPM/layering implications and separate portable-path Wayland/graphics/Waydroid tests |

The candidate versions above come from the current release/status pages: [Debian stable](https://www.debian.org/releases/), [Ubuntu releases](https://wiki.ubuntu.com/Releases), [Fedora 44 announcement](https://fedoramagazine.org/announcing-fedora-linux-44/), [Alpine downloads/releases](https://www.alpinelinux.org/downloads/), [SteamOS 3.9 release notes](https://www.steamdeck.com/en/news?p=1), and [Bazzite releases](https://github.com/ublue-os/bazzite/releases). Pin exact image digests or snapshots and record the installed SteamOS/Bazzite versions before tests; version recency does not establish application compatibility. Architecture scope remains a per-matrix decision, not an implication of the package format. The actual image digests, observed distribution versions and evidence links are tracked in support-matrix.md; these are the versions actually tested, not compatibility promises.

## 5. Proposed workflow architecture

### 5.1 Build/audit before packaging

Create a non-publishing build job for each available native architecture. Preserve the ELF, generated `build/go/build-info.json`, archive `manifest.json`, provenance, Meson dependency and build-option introspection JSON, relevant link command/contract, and raw ELF audit reports. Collect at minimum:

- `file` output;
- `readelf -l` (including `PT_INTERP`);
- `readelf -d` (`DT_NEEDED`, RPATH/RUNPATH);
- `readelf --version-info` (GLIBC and other imported symbol versions);
- dependency-loader resolution and transitive runtime closure in a clean matching target environment;
- Meson dependency selections/fallbacks and native link inputs;
- checksum and source commit identity for each saved artifact.

The existing manifest is useful provenance, but direct `DT_NEEDED` names alone do not describe the full transitive dependency closure or map a SONAME to every distro's package name.

### 5.2 Shared release payload

Keep one authoritative build output and provenance identity per target build. Packaging adapters should consume that exact artifact and generate ecosystem-native metadata; they must not silently rebuild from different source revisions or package an ELF from another target's build directory. Any target-specific rebuild (especially Alpine/musl) is its own signed-off target artifact with its own audit and manifest.

### 5.3 Target-native packaging jobs

Use native target package tools in pinned containers or target runners wherever practical. Containerized tests are suitable for metadata, package-content, dependency-solver, installation, and removal checks. Container success is not a substitute for real device/session tests that depend on the host compositor, graphics stack, kernel, or Waydroid integration.

### 5.4 Release publication

Release jobs should produce candidate artifacts and checksums, verify names/version/architecture, attach artifacts to the matching Git tag, and leave the release as a draft for review. Do not publish repository metadata or use production signing keys until owner approval is recorded. SHA-256 verifies integrity only; it is not publisher authentication.

### 5.5 Support status

For each distribution/version/architecture/artifact cell, store test evidence and assign exactly one of:

- `Supported`: required build, package metadata, dependency, lifecycle, runtime and platform-specific tests pass.
- `Experimental`: a usable candidate exists but at least one support gate is unproven or known to be limited; limitations are visible to users.
- `Unavailable`: no viable artifact/path is available for that cell.

The overall distribution goal is not complete until every named distribution family has at least one `Supported` path for its declared baseline. Reporting another cell as `Experimental` is acceptable only as an honest interim status, not as completion of the goal.

## 6. Execution order and 48-hour timebox

### Phase A — Establish a truthful baseline (hours 0–4)

- Freeze the reviewed source commit for the sprint and verify that CI and release workflows use the same intended branch/ref.
- Select and record one target baseline version/image per distribution family, and architecture candidates per spec Q4.
- Add non-publishing diagnostic outputs to CI and save the ELF, manifest/build-info, Meson introspection, link contract, and ELF audit outputs.
- Run the audit for current supported build architectures and issue a dependency report; separate proven facts from expected dependencies.

**Exit gate:** every inspected artifact has an identified source commit, architecture, ELF interpreter, direct dependencies, symbol-version report, and recorded limitations.

### Phase B — Native package prototypes (hours 4–18)

- Implement Debian/Ubuntu `.deb` packaging and native dependency resolution first, while preserving versioned portable assets.
- Implement Fedora RPM packaging and validate dependencies using target-native tooling.
- Implement Arch `PKGBUILD`/package build and check package contents, version identity, and dependency metadata.
- Implement Alpine's musl build path and `.apk` packaging independently; do not mark it supported if the build or runtime proof fails.
- Save all package outputs as CI artifacts on pull requests/branches before attaching them to a release.

**Exit gate:** every native package prototype passes format/metadata checks and clean install/removal tests in its target package environment; runtime gaps are documented.

### Phase C — Portable and immutable-system paths (hours 18–32)

- Build a Flatpak candidate plus a portable archive for SteamOS; validate packaging and documented install/update/remove paths.
- Build the Bazzite RPM and test an AppImage candidate only if its host-coupled dependencies can be handled safely; fall back to `.tar.zst` otherwise.
- Verify Wayland/compositor environment propagation, graphics access, Waydroid transport, filesystem paths, permissions, logs, and graceful failure cases.
- Run actual Steam Deck Desktop/Gaming Mode and Bazzite tests if devices/sessions are available; store precise evidence and test versions.

**Exit gate:** each platform path has an independent support decision based on the tests actually completed; lack of physical/session access is recorded as a blocker, not a passing test.

### Phase D — Lifecycle, migration, release automation (hours 32–44)

- Test install, update, explicit previous-version install/downgrade where supported, no-op/current version, removal, and clean reinstall.
- Test coexistence and migration from `/opt/cage-xtmapper` and `/usr/local/bin/cage-xtmapper`; avoid conflicting package-owned paths and preserve unknown files.
- Expand CI to build candidate artifacts for the target matrix, run package checks, save evidence, compute checksums, and validate release asset names/version/architecture.
- Add an artifact index/support matrix that reflects only test-backed statuses.

**Exit gate:** candidate release has reproducible names and version mapping, lifecycle evidence, and an accurate status matrix; no workflow overwrites assets for an existing tag.

### Phase E — Release-candidate decision (hours 44–48)

- Run the full CI suite and all available target checks on the exact release candidate commit.
- Review licenses/notices for bundled native components and each packaging format.
- Ensure `Supported` has no missing mandatory test evidence; demote unproven cells to `Experimental` and enumerate the remaining blockers.
- Keep release as draft pending maintainer review and approval for signing/repository hosting.

**Exit gate:** a reviewable, checksummed, versioned draft release and a truthful matrix exist. Publishing signed repositories and declaring platform support beyond tested scope remain gated on owner decisions and test evidence.

## 7. Cross-cutting engineering rules

- Keep package-owned application files under distro-compliant paths (normally `/usr`, not `/usr/local`) and keep native package state separate from the existing bootstrap-managed `/opt` tree.
- Never install package-owned files and bootstrap-owned files into the same path without an explicit, tested ownership-transfer strategy.
- Do not install runtime library packages from guessed SONAME-to-package mappings. Generate metadata with native tools and verify on clean target versions.
- Treat Waydroid, the XtMapper APK, permissions, session variables, and privilege requirements as explicit external prerequisites; packaging must not silently install or change them.
- Never execute `ldd` on an untrusted binary; the release build is project-produced, but prefer `readelf` for inspection and use loader checks only in controlled target environments.
- Use immutable version/tag and source digests. Attach checksums. Do not claim checksums authenticate the publisher.
- Keep production signing keys, repository hosting, costs, and rotation/recovery under maintainer ownership.

## 8. Main risks and mitigations

| Risk | Mitigation |
|---|---|
| Current baseline is glibc-oriented and native deps vary by build host | Audit the exact ELF; build per ecosystem; give Alpine a separate musl target |
| Dynamic dependencies vary with Meson auto-detection | Record Meson dependency selections and compare package metadata against the actual target build |
| Graphics/Wayland libraries are unsafe to bundle indiscriminately | Maintain a per-library bundle/exclude rationale; test on actual hosts |
| SteamOS/Bazzite runtime paths cannot be proven in generic CI | Reserve real device/session test time; do not mark `Supported` without evidence |
| Install paths collide with existing bootstrap | Separate filesystem ownership and test migration explicitly |
| 48 hours insufficient for all hardware-dependent gates | Build candidates early, parallelize target package jobs, publish statuses honestly, keep all missing proof as explicit blockers |
| Repository hosting/key policy needs maintainer approval | Build/validate locally and keep publication gated; do not create production keys or incur costs |

### Execution evidence snapshot — 2026-10-27

The latest fully successful target package audit is [run 40510507392](https://github.com/SoufianoDev/cage-xtmapper/actions/runs/40510507392), source commit e5a294463b814b92bac5f7e7d12f683e154a1108. All six target jobs passed: Debian 13.7, Ubuntu 24.04.5, Ubuntu 26.04.1, Fedora 44, Arch using the available 2026-10-26 repository snapshot, and Alpine 3.24.3. The matching [multi-architecture validation run 40510508129](https://github.com/SoufianoDev/cage-xtmapper/actions/runs/40510508129) passed amd64 and arm64. Artifact IDs and target baselines are recorded in support-matrix.md; per-target ELF and library decisions are in library-decision-table.md.

The target audit now checks native package metadata/dependencies, previous-version installation, upgrades, no-op or reinstall behavior, explicit downgrade where tested, removal, clean reinstall, post-removal package-owned path absence, and execution of the installed CLI `--help` smoke test. This is not a real graphical-session test. The RPM lifecycle initially exposed a leftover documentation directory; the RPM spec now explicitly owns its documentation/license directories, and the full target audit passed afterward. An ARM64 lifecycle-test race was also corrected by waiting for compositor readiness before sending the test signal; the subsequent amd64/arm64 validation passed.

A tag-triggered release workflow is now configured to build the six native candidate packages from digest-pinned target images, run the target lifecycle checks using the tag-derived package version, attach package candidates/checksums and a generated status index to a clean draft release, and refuse to edit published releases or non-empty drafts. The release workflow has not yet been executed on a version-tag push, so asset upload and the index remain unverified end-to-end. The existing bootstrap installer still uses the versioned .tar and helper; native package candidates do not automatically install the application through APT/DNF/Pacman/APK.

No target is marked Supported. The remaining blockers include real Wayland/graphics-session and Waydroid integration tests, package installation migration/coexistence with the legacy /opt and /usr/local/bin installer, RPM lint, Steam Deck Desktop/Gaming Mode testing, Bazzite-host validation, SteamOS Flatpak/portable paths, native arm64 package variants, and an approved publisher trust/signing policy. Production signing and repository hosting remain maintainer-gated.
