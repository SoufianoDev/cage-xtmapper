# Specification: Native Linux Release Packaging and Distribution

- **Project:** Cage XtMapper
- **Spec ID:** `001-native-linux-release-packaging`
- **Status:** Draft — packaging policy decisions recorded; implementation plan and task breakdown drafted separately; maintainer-owned hosting/signing decisions remain open; exact target versions and per-artifact library mappings require validation
- **Date:** 2026-10-27
- **Specification type:** Feature specification (intent and acceptance criteria; see companion `plan.md` and `tasks.md` for implementation)
- **Repository baseline reviewed:** default branch `migrate-to-c-golang-no-submodule` at source-audit commit `42271bbb533ee00fb46c9ea9146821a34cf3a4c3`; installer behavior also reviewed at commit `c86ed44ed58e49c2ac6835968c3aed25dc9fb0fa`

## Clarification log

### Q1 — Scope of the first public release (answered)

**Decision: Option C — provide downloadable artifacts for all six target families from the first public release, while clearly separating experimental artifacts from officially supported targets.** The six target families are Debian/Ubuntu, Fedora (with any additional RPM-family distribution validated separately), Arch Linux, Alpine Linux, SteamOS, and Bazzite; Debian and Ubuntu are tracked as separate distribution targets in the support matrix.

An artifact being downloadable does **not** by itself mean that its target is officially supported. The release page and support matrix must label each target/architecture as `Supported`, `Experimental`, or `Unavailable`. A target may be labeled `Supported` only after all required CI, dependency, package lifecycle, and runtime/session gates pass. Experimental artifacts must carry explicit warnings and must not be advertised as production-supported.

**Quality strategy requested:** use CI/CD and Docker/containerized test environments extensively to make package builds and validation repeatable. The workflow should test package metadata, dependency resolution, installation, upgrade, selected-version installation where supported, removal, checksums, and signatures. Container tests are an important layer, not a universal proof: Docker containers share the host kernel, and architecture/emulation limitations mean a container-only result cannot prove every target's runtime behavior. SteamOS/Bazzite image behavior, real Wayland/compositor sessions, Waydroid integration, device access, and other host-kernel/session-specific behavior require suitable VM, dedicated-runner, or physical-device tests where a container cannot validate them (see the [Docker multi-platform build documentation](https://docs.docker.com/build/building/multi-platform/)).

### Q2 — SteamOS artifact strategy (answered)

**Decision: Option C — provide both a Flatpak artifact and a portable archive for SteamOS from the first release.** Both must be downloadable by exact release tag without requiring the project's `install.sh`. They must be validated independently; shipping both artifacts does not mean both are officially supported. The release/support matrix must state the status of each path separately. Flatpak support is conditional on proving the required Wayland/compositor and Waydroid integration works in the intended Steam Deck Desktop and Gaming Mode sessions. The portable archive must have documented install, update, and removal behavior and must avoid mandatory mutation of SteamOS's base image.

This decision follows Valve's guidance favoring Flatpak to avoid application files being affected by SteamOS updates, while retaining a portable path for functionality that a Flatpak sandbox may not support. Steam Deck device/session testing remains required; Docker alone is insufficient.

### Q3 — Bazzite artifact strategy (answered)

**Decision: Option C — provide both a native RPM and a portable artifact for Bazzite from the first release.** The portable format should prefer AppImage if runtime and Wayland/compositor/Waydroid integration tests show that it works reliably; otherwise publish a `.tar.zst` portable archive with explicit install, update, and removal instructions. The RPM and portable artifact must each have their own validation status, and both must be downloadable by exact release tag without requiring `install.sh`.

The RPM acceptance path must document the consequences of package layering and reboot requirements on Bazzite and must not assume that layering is the default preferred route. The portable path must avoid mutating the base image by default. Neither artifact may be labeled `Supported` until dependency, lifecycle, and actual Bazzite runtime/session tests pass. If one path is not validated by release time, it may be marked `Experimental` rather than blocking all downloadable artifacts, but that label and the known limitations must be visible on the release page.

This decision follows Bazzite's guidance to prefer supported user-facing software delivery routes and use layering only when appropriate, while retaining an RPM for users who specifically need native package installation. See the Bazzite software management and AppImage references below.

### Q4 — Initial architecture scope (answered)

**Decision: Option C — use a per-distribution architecture matrix rather than requiring every architecture on every target.** For Debian/Ubuntu, Fedora, and Alpine, target both `amd64`/`x86_64` and `arm64`/`aarch64` where the target build, dependency metadata, installation lifecycle, and runtime tests succeed. For Arch Linux, target the architecture(s) that can be built and validated in the actual package ecosystem; `x86_64` is the initial candidate, and ARM support must be evaluated separately rather than inferred from the generic Arch package format. For SteamOS and Bazzite, `x86_64` is the initial target, with SteamOS validation on Steam Deck hardware. Additional architectures may be marked `Experimental` or `Unavailable` until evidence meets the release gate.

CI should use native architecture runners where practical. Cross-compilation, emulation, or successful artifact creation alone does not establish runtime support. Containerized package/install tests remain useful, but architecture-specific loader and session behavior must be tested in a suitable target environment. Every GitHub Release must expose the status for each distribution/version/architecture/artifact combination.

### Q5 — Repository hosting and release cadence (recorded; maintainer decision required)

**No hosting choice is made by this specification contributor.** Repository ownership, domain, storage provider, signing infrastructure, and ongoing operational costs belong to the project maintainer/owner. The recommendation below is a proposal for discussion, not an approved implementation decision.

The contributor's expected release cadence is approximately **weekly or every two weeks when a release is ready**, rather than several releases per week. This is a planning assumption, not an SLA or a promise to release on a schedule. It suggests that a distribution-specific repository approach is operationally plausible, but release frequency alone does not settle the hosting choice, signing-key ownership, support burden, or cost.

**Proposal to present to the maintainer:** keep GitHub Releases as the canonical source for version-specific manual downloads for every published artifact; separately, if the maintainer approves and has capacity to operate it, publish signed ecosystem-specific repositories for APT, RPM/DNF, Pacman, and APK to provide native package-manager updates. Design CI/CD so building and validating artifacts is separate from repository publication, and make publication provider-neutral where practical. This avoids locking the packaging build to one hosting vendor before the owner decides.

Possible hosting approaches to evaluate with the maintainer:
- GitHub Pages or GitHub-hosted static content for a modest initial repository, after checking bandwidth, storage, index/signature, and file-retention constraints.
- Static object storage behind a project-controlled domain, after reviewing ongoing cost, cache invalidation, access control, retention, and operational responsibility.
- Dedicated package-hosting services where they materially simplify repository metadata, signing, and maintenance.

Until the maintainer decides, signed native repositories remain a **target capability that is gated on approval of hosting and key ownership**. The specification still requires direct versioned downloads from GitHub Releases regardless of that decision. CI may build and test repository metadata in candidate workflows, but must not publish official repository metadata or create/rotate production signing keys without maintainer authorization.

### Q6 — Explicit downgrade/version rollback policy (answered)

**Decision: Option A — allow users to select and install an older, exact release using the native package manager where that ecosystem supports the operation, with clear warnings and documented commands.** Ordinary repository updates should continue to follow the configured repository's normal version selection and must not silently pin users to an older release. An explicitly selected older version is a deliberate user action, not the default update behavior.

**Acceptance implications:**
- Every retained release must remain addressable by exact GitHub Release tag, with immutable/version-identifiable artifacts and accurate package version metadata.
- Documentation must provide explicit older-version installation/downgrade instructions per ecosystem and state any package-manager limitations or additional flags required.
- CI must test installing a previous version over a newer version where supported, verify the final installed version, and test that the ordinary update path remains independent of the manual rollback selection.
- The specification does not assume that every package manager handles downgrades identically; behavior must be validated per target.
- The release notes must warn that rolling back the application may not roll back user data, configuration, or external Waydroid/XtMapper state, and compatibility with those data is not guaranteed unless separately tested.

### Q7 — Runtime library strategy (answered: Option C, hybrid by artifact type)

**Decision: Option C — choose system versus bundled libraries according to the artifact type and validated target environment.** This resolves the policy-level question. It does not pretend that the exact library list for each artifact is already known: that mapping must be generated from current builds and tested before release.

#### Approved policy

1. **Native packages (`.deb`, `.rpm`, `.apk`, and the Arch package):** prefer the target distribution's supported system libraries and declare runtime dependencies accurately using that ecosystem's package metadata tools. Do not guess dependency names from build-package lists or package extensions. Keep project-owned/static components embedded where the audited build supports that safely, but declare every required external runtime dependency.
2. **AppImage and portable archives:** bundle only libraries that the binary audit shows are needed and that are safe to bundle for the intended portability goal. Do not blindly bundle the entire graphics/session stack. Keep libc/loader and graphics drivers, vendor GPU stack, and other low-level graphics or host/session interfaces supplied by the host where bundling them could break ABI or hardware integration. Treat individual libraries such as Wayland, EGL, GBM, Mesa, libdrm, libinput, and udev as audit decisions, not as a single automatic include/exclude group. Document and test any justified exception.
3. **Flatpak:** use a pinned, supported Flatpak runtime and extensions for libraries that the runtime is intended to provide; declare required permissions and host interfaces explicitly. Do not bundle duplicate runtime libraries without a demonstrated need. Graphics, Wayland/compositor, device, and Waydroid-facing integration must be validated against the target SteamOS session and Flatpak sandbox.
4. **Alpine:** build a distinct Alpine-targeted artifact against musl in a compatible Alpine environment. A glibc ELF must not be relabeled or assumed compatible with musl. Its dependency metadata and runtime behavior must be validated independently for each declared architecture.
5. **SteamOS and Bazzite:** test each artifact path separately on the actual target image/session. Validate dependency resolution, Wayland/compositor startup, graphics and input access, Waydroid interaction, installation/update/removal, and image immutability constraints. Package success or container success alone is insufficient to claim runtime support.
6. **Licensing and provenance:** every bundled component must be inventoried in release metadata, and the archive must include the notices/licenses required for the actual linked or bundled components. Preserve the source commit, architecture, build environment, ELF reports, and dependency decisions for reproducibility and review.

#### Findings from the current repository source

Source-level audit performed against [`migrate-to-c-golang-no-submodule` at `42271bb`](https://github.com/SoufianoDev/cage-xtmapper/tree/42271bbb533ee00fb46c9ea9146821a34cf3a4c3). This is not yet a `readelf`/loader audit of the exact ELF; that requires a built artifact from the exact commit and architecture.

- [`build.sh`](https://github.com/SoufianoDev/cage-xtmapper/blob/42271bbb533ee00fb46c9ea9146821a34cf3a4c3/build.sh) configures Meson with `-Ddefault_library=static` and force-fallbacks for `wayland-server`, `wayland-client`, `wayland-protocols`, `xkbcommon`, and `pixman-1`. This shows an intent to statically include these selected in-tree/fallback components in the one application ELF.
- Meson's `default_library` setting controls libraries built inside the project; external dependencies may still be selected as shared system libraries. The current configuration therefore does **not** prove that the ELF is fully static or self-contained.
- Other wlroots dependencies are conditionally selected from the build host, including `libdrm`, `libdisplay-info`, `libliftoff`, `libseat`/`seatd`, `libudev`, `libinput`, EGL, GBM, GLESv2, and optional color-management support. Dependency selection can vary with target packages and Meson feature detection.
- `install_deps.sh` lists build-dependency names for Debian-family, Fedora, Arch, and Alpine systems. That is not evidence that an Ubuntu-built ELF will run on all those distributions.
- The release metadata generator ([`internal/releasebuild/build.go`](https://github.com/SoufianoDev/cage-xtmapper/blob/42271bbb533ee00fb46c9ea9146821a34cf3a4c3/internal/releasebuild/build.go), `runtimeRequirements`) inspects a built ELF to record its interpreter, direct `DT_NEEDED` SONAMEs, and highest imported `GLIBC_*` symbol version; it rejects RPATH/RUNPATH and external Cage/wlroots shared-library dependencies. These are useful inputs, but source alone cannot tell us the values for a particular artifact, and direct `DT_NEEDED` is not the whole transitive dependency closure.
- The normal build/CI baseline is Ubuntu 26.04. Alpine uses musl and requires an independently built and validated musl target.
- The GitHub Releases API exposed a draft release tagged `v20261019` with a single asset named `cage-xtmapper-v0.2.0.tar`, while the reviewed release workflow expects per-tag/per-architecture names. Its source commit and manifest have not been verified as representative of the current baseline, so it is not accepted as the current dependency profile.
- CI run [`40438105888`](https://github.com/SoufianoDev/cage-xtmapper/actions/runs/40438105888) completed successfully for reviewed commit `42271bbb533ee00fb46c9ea9146821a34cf3a4c3` on 2026-10-27, but its workflow-run artifact endpoint reports zero retained artifacts. That run does not provide a downloadable ELF, `build/go/build-info.json`, Meson introspection records, or raw ELF audit output for this review.

#### Required evidence to map individual libraries

1. Run a non-publishing build for the exact reviewed commit and each release architecture. Retain the ELF, `build/go/build-info.json`, generated archive `manifest.json`, Meson `intro-dependencies.json` and `intro-buildoptions.json`, relevant Ninja/link information, and raw audit output as CI artifacts.
2. For each ELF, record `file`, the ELF interpreter (`readelf -l`), direct `DT_NEEDED` libraries (`readelf -d`), imported symbol versions (`readelf --version-info`), and dependency-loader resolution in a clean matching environment. Inspect transitive runtime dependencies as well; do not stop at direct `DT_NEEDED` entries.
3. Build candidate artifacts in target-native or otherwise justified target environments for Debian/Ubuntu, Fedora, Arch, and Alpine. Record which Meson dependencies came from system packages and which came from fallback subprojects.
4. Generate native package dependencies with the ecosystem's packaging tools and validate clean installation and launch on each declared target release. For Debian, use the applicable shlibs/symbols and `dpkg-shlibdeps` workflow; use the corresponding native mechanisms for RPM, Pacman, and APK.
5. For AppImage and portable archives, maintain a per-library bundle/exclude rationale and run the real target graphics/session/Waydroid tests on SteamOS and Bazzite. Container checks complement but do not replace these tests.
6. Review bundled-component license notices and runtime behavior before publishing the candidate artifacts.

**Decision gate:** the hybrid policy is decided; the exact system/bundled mapping remains an implementation validation task. No artifact can be marked `Supported` until its generated dependency declarations or bundle/exclusion list match the artifact and its required clean-target/runtime tests pass.

### Remaining decisions

The hybrid runtime-library policy is now decided. Remaining work is to validate the exact support matrix and per-artifact dependency/bundling map, and to resolve maintainer-owned repository hosting/signing, package paths, migration behavior, provenance, and portable-artifact lifecycle details.

## 1. Summary

Cage XtMapper must offer users native, distribution-appropriate packages and a reliable manual-download path for every supported release. A project-owned bootstrap installer may remain as a convenience, but it must not be required to obtain, install, update, or remove the application.

Users must be able to select an exact tagged version from GitHub Releases and download the artifact for their distribution and CPU architecture. Where native package managers are supported, package metadata must accurately express runtime dependencies and package lifecycle behavior. Signed repositories should provide routine updates through the distribution's normal package manager. Compatibility must be demonstrated by tests for each declared distribution/version/architecture combination rather than inferred from the package file extension.

SteamOS and Bazzite are explicit targets from the first public release. The product goal is a verified `Supported` installation path for every named target family; `Experimental` or `Unavailable` is a transparent interim state for a specific version/architecture/artifact when a technical gate is not yet met, not a substitute for the overall compatibility goal. An unvalidated artifact may be published only as clearly labeled experimental. Neither platform may require the project's `install.sh` for manual installation. For SteamOS, both a Flatpak and portable archive are release targets, and each receives an independent support status after validation. This choice follows Valve's SteamOS Flatpak guidance while preserving a portable alternative for functionality a sandbox may not support; see the Steam Deck Flatpak FAQ in Research references.

## 2. Problem and motivation

The current release path builds a versioned `.tar` archive and a standalone installer helper for Linux `amd64` and `arm64`. The current bootstrap verifies SHA-256 checksums and installs into a project-managed layout. This is useful for newcomers, but it is not a substitute for native package ownership, dependency resolution, repositories, or a manual download that works independently of the project bootstrap.

The application links Go, Cage, and wlroots into one application ELF, while still requiring system shared libraries and external prerequisites including Waydroid and the XtMapper Android APK. A single ELF does not imply a single universally compatible Linux binary: libc/ABI, library versions, and distribution packaging policies differ.

## 3. Goals

1. Achieve a `Supported` installation path for each named target family—Debian, Ubuntu, Fedora, Arch Linux, Alpine Linux, SteamOS, and Bazzite—using a per-distribution/version/architecture/artifact matrix. Build or publish artifacts for the target families from the first public release, but never call an unvalidated combination supported.
2. Allow users to install an exact historical release without running the project's bootstrap installer.
3. Make native package managers responsible for installing, updating, dependency resolution, and removing native packages.
4. Publish signed package repositories for ecosystems where they provide the intended ongoing-update experience.
5. Preserve a simple optional bootstrap path for new users without making it the only distribution method.
6. Declare only distribution/version/architecture combinations that pass the required build, dependency, package lifecycle, and runtime checks.
7. Provide explicit support and installation guidance for SteamOS and Bazzite.
8. Migrate users of the existing project-managed installation safely, without overwriting or deleting files whose ownership cannot be established.
9. Publish integrity metadata, and establish publisher-authentication through signatures and/or verifiable release provenance. Checksums alone must not be described as publisher authentication.

## 4. Non-goals

- Installing, managing, or redistributing Waydroid or the XtMapper Android APK as part of this package feature.
- Claiming compatibility with every distribution derived from Debian, Ubuntu, Fedora, Arch, or Alpine merely because it uses the same package format.
- Guaranteeing that an old package will continue to run forever on a future rolling release after its system ABI and libraries change.
- Making one glibc-linked ELF work on Alpine/musl without a separately built and tested Alpine target.
- Removing the current bootstrap before package artifacts, migration, and fallback paths have passed their acceptance tests.
- Automatically modifying an immutable/atomic operating-system image as a mandatory installation step.

## 5. Users and primary scenarios

### Scenario A — Manual exact-version install

A user opens the GitHub Releases page, selects tag `vX.Y.Z`, downloads the artifact matching their distribution and architecture, verifies its integrity/authenticity, and installs it with the distribution-native tool or the documented SteamOS/Bazzite manual method. The project's `install.sh` is not required.

### Scenario B — Routine updates

A user configures the project's supported signed repository once. Later updates are delivered by the distribution's normal package manager, alongside other system updates. The project does not implement a competing package database for systems supported through native packages.

### Scenario C — User prefers the bootstrap

A new user runs the optional bootstrap. It detects the distribution and architecture, directs the user to a supported package/repository path where possible, or offers a clearly labeled fallback. It does not silently install a conflicting second copy if an older project-managed install already exists.

### Scenario D — Unsupported or unverified host

If the host's distribution version, libc, architecture, or session requirements are outside the tested support matrix, the tooling explains that the combination is unsupported or unverified. It must not imply success based on package format alone.

### Scenario E — SteamOS / Steam Deck

A user can download either the release-specific Flatpak or portable archive directly from GitHub Releases and follow its documented manual install/update/remove path without the project bootstrap. The paths are tested independently in Desktop Mode and the intended Steam Gaming Mode workflow; one path may be Supported while the other remains Experimental.

### Scenario F — Bazzite

A user can download the release-specific native RPM directly and install it using the supported documented path. Documentation must explain the trade-offs of layering a package onto an image-based system and must provide a tested alternative if host layering is not the recommended path.

## 6. Functional requirements

### FR-001 — Version-addressable release assets (P0)

Each published Git tag must map to immutable release assets whose application version exactly matches that tag. A user must be able to navigate to a specific GitHub Release and download that version without being redirected to `latest` and without depending on the bootstrap.

**Acceptance criteria**
- Every package's embedded/package-manager version can be traced unambiguously to the Git release tag.
- Documentation describes how a user explicitly installs a selected older release through the relevant package manager where supported; this action does not change the repository's default update policy.
- Release automation never overwrites an existing artifact for a previously published tag.
- Older supported tags retain their download assets and integrity/signature metadata.
- Assets encode enough distribution-family and architecture information in their name or release section to avoid ambiguous selection.

### FR-002 — Debian and Ubuntu packages (P0)

Publish `.deb` packages for the explicitly supported Debian and Ubuntu versions and architectures.

**Acceptance criteria**
- Package metadata lists all required runtime shared-library dependencies and any required external package dependencies.
- Dependencies are derived from and validated against the actual built artifact and target repositories, not guessed from filenames.
- Installation, upgrade, downgrade-to-an-explicitly-selected-version where the package manager permits it, and removal are tested on each declared target.
- Package files follow Debian filesystem and package-policy conventions; package-managed files are not installed into `/usr/local`.

### FR-003 — Fedora and validated RPM-family packages (P1)

Publish `.rpm` packages for Fedora versions explicitly listed in the support matrix. Extend to openSUSE, Rocky Linux, or other RPM distributions only after separate compatibility validation.

**Acceptance criteria**
- RPM metadata contains accurate version, architecture, and runtime dependency information.
- Native install, update, explicit version selection where available, removal, and clean-host dependency solving are tested for each declared target.
- The documentation does not infer cross-distribution compatibility solely from RPM format compatibility.

### FR-004 — Arch Linux native package workflow (P1)

Provide an Arch-compatible `PKGBUILD` and a maintained AUR path, and publish a directly downloadable, versioned Pacman package artifact for supported architectures when binary release assets are supported.

**Acceptance criteria**
- The `PKGBUILD` pins an immutable source/tag identity and validates downloaded inputs with checksums and signatures where available.
- `makepkg` build checks and package-content checks pass.
- The GitHub Release includes a version-specific installable package for the declared architecture, or clearly states why that release does not include a binary package.
- Documentation distinguishes AUR-helper updates from updates through a configured Pacman binary repository. It must not promise that `pacman -Syu` updates an AUR-only package by itself.
- If continuous updates through `pacman -Syu` are a product requirement, a signed binary Pacman repository is designed and tested as a separate deliverable.

### FR-005 — Alpine Linux package workflow (P1/P2 after feasibility gate)

Provide `.apk` packages only from a build target compiled and validated for Alpine's musl-based environment. Publish through a signed APK repository if Alpine is announced as officially supported.

**Acceptance criteria**
- The target is built and linked for musl in a compatible Alpine build environment, or an explicitly validated alternative is documented.
- Package metadata declares required APK dependencies and the generated package passes dependency and install checks.
- The APK and repository index/signature are verifiable through Alpine's supported trust mechanism.
- A glibc-targeted binary is never relabeled as an Alpine package without actual compatibility evidence.

### FR-006 — SteamOS Flatpak and portable artifacts (P0 for product scope)

Provide both a release-specific Flatpak artifact and a portable archive for SteamOS, directly downloadable from GitHub Releases by exact tag without requiring `install.sh`. Neither path may require enabling writable system-root mode or mutating the system image as a default installation step. The two paths must have independent support statuses and acceptance evidence.

**Acceptance criteria**
- Both artifacts are built for the declared SteamOS architecture and are included in the version-specific release assets.
- Desktop Mode and the intended Steam Gaming Mode path are tested for each artifact independently.
- Flatpak sandbox permissions and Wayland/compositor/Waydroid integration are documented and validated; do not label Flatpak `Supported` merely because it installs.
- The portable archive has documented install, update, and removal steps and avoids mandatory changes to the base image.
- Dependencies, host paths, Wayland session behavior, Waydroid integration, controller/input expectations, update behavior, and removal are documented.
- The release never claims general SteamOS support based only on a successful artifact build or extraction.

### FR-007 — Bazzite RPM and portable artifact (P0 for product scope)

Provide both a directly downloadable release-specific RPM and a portable artifact for Bazzite. Prefer AppImage for the portable artifact when runtime, graphics/session, and Waydroid integration tests prove compatibility; otherwise provide a `.tar.zst` portable archive. Neither path may require `install.sh` for manual installation.

**Acceptance criteria**
- The RPM is installable through the documented method on the tested Bazzite version and architecture.
- Documentation clearly describes `rpm-ostree` layering and reboot implications and does not recommend layering as the default without a compatibility rationale.
- AppImage is selected only after its libraries, filesystem/runtime expectations, Wayland/compositor integration, and Waydroid access are tested on Bazzite. If it fails or has unresolved limitations, ship a portable `.tar.zst` archive instead.
- The portable artifact has documented manual install, version replacement/update, and removal instructions and avoids mandatory mutation of the system image.
- The RPM and portable artifact have independent `Supported` / `Experimental` status based on their own acceptance evidence.
- Installation/removal affects only files owned by the chosen path and does not leave conflicting command paths or stale copies.

### FR-008 — Native repositories and routine updates (P0 for each ecosystem advertised as repository-supported)

Publish and document native repositories for the package ecosystems that advertise routine package-manager updates:
- APT repository for Debian/Ubuntu;
- RPM/DNF-compatible repository for supported RPM targets;
- signed Pacman repository if the project claims native `pacman -Syu` update integration;
- APK repository for Alpine.

**Acceptance criteria**
- Repository metadata and assets are signed and clients can verify them using documented trust setup.
- Install, update to a newer version, and removal/disablement of the repository are tested on clean supported systems.
- Exact-version manual artifacts remain available even when repository use is not desired.
- The project documents that installing a local package alone does not automatically configure future updates.

### FR-009 — Optional bootstrap installer (P1)

Retain the bootstrap as a convenience layer, not a prerequisite. It should use the best supported distribution-native route when possible and keep the currently supported direct archive route as a clearly labeled fallback during migration.

**Acceptance criteria**
- Users can install every supported package manually without running the bootstrap.
- Bootstrap behavior does not create a second conflicting copy when a native package already owns the command path.
- Unsupported systems receive explicit messages and do not get guessed package names or unverified automatic dependency installation.

### FR-010 — Safe migration from the current installer layout (P0 before making packages the default)

The current installer uses a managed tree under `/opt/cage-xtmapper` and a launcher under `/usr/local/bin/cage-xtmapper`. Native packages will normally use distribution-managed paths such as `/usr/bin/cage-xtmapper`; this transition must not leave an old `/usr/local/bin` link shadowing the native package.

**Acceptance criteria**
- Upgrade/migration paths from the current project-managed installation are documented and tested.
- Existing files are inventoried before mutation. The package does not delete unknown or modified user files.
- Native package install does not silently coexist with a stale command link from the old install.
- Uninstalling the native package removes package-owned files but preserves user data, Waydroid data, Android APKs, unrelated files, and unowned backups.
- A failed migration is recoverable and does not make both old and new copies appear successfully installed.

### FR-011 — Dependency and ABI compatibility matrix (P0)

Define and publish a compatibility matrix by distribution release, architecture, libc/ABI target, and artifact. Passing the matrix is required before the distribution/version is listed as supported.

**Acceptance criteria**
- Each supported matrix cell has evidence for dependency resolution, package installation/removal, and executable loading.
- Native package dependencies are generated or validated using the target ecosystem's tools and checked in a clean target environment.
- Each AppImage/portable artifact publishes or preserves an auditable bundle/exclude report tied to the actual built ELF; bundled components have required license notices.
- Alpine/musl is built and validated independently from glibc artifacts.
- SteamOS and Bazzite each have separate runtime/session evidence for each artifact path that is labeled supported.
- Unsupported architectures or unsupported OS versions fail with a clear message rather than being labeled supported.

### FR-012 — Integrity, authenticity, and provenance (P0)

Publish checksums for every asset and establish a signature/provenance mechanism appropriate to each package ecosystem and direct download.

**Acceptance criteria**
- Checksums are generated from the final assets and attached to the exact tagged release.
- Publisher-authentication is not claimed from SHA-256 alone.
- Repository metadata/package signatures are validated in integration tests.
- The signing-key distribution, rotation, revocation/recovery procedure, and CI permissions are documented before public repository rollout.

### FR-013 — Release asset index and documentation (P0)

Every release page must show an easy-to-scan list of artifacts and their supported platform/version/architecture, manual installation steps, repository option, checksums/signatures, and prerequisites.

**Acceptance criteria**
- Each artifact has one unambiguous supported target or an explicit target matrix.
- Release notes explain any packaging format that is not available for a target.
- Waydroid and the XtMapper Android APK are identified as external prerequisites.
- A user can follow the manual installation guide without being told to run `install.sh`.

### FR-014 — Automated CI/CD and containerized release validation (P0)

The release workflow must use repeatable CI/CD jobs and pinned Docker/container test environments wherever containers can reliably validate the target behavior. A release must not silently promote an untested artifact to officially supported status.

**Acceptance criteria**
- Each of the seven tracked distribution targets (Debian, Ubuntu, Fedora, Arch Linux, Alpine Linux, SteamOS, and Bazzite) has a release-candidate artifact or an explicit `Unavailable` result with a recorded technical blocker; artifacts that exist but have not passed all gates are labeled `Experimental`.
- Container images and relevant package repositories/images used for release validation are version-pinned (and digests pinned where practical); test logs record the distribution version and architecture.
- CI validates artifact identity, package metadata and paths, dependency declarations, signature/checksum verification, and package install/upgrade/removal in clean target environments where supported.
- CI tests exact-version installation and downgrade/upgrade behavior where the native package manager supports it, and verifies that package removal preserves user data and external prerequisites.
- Architecture-specific builds run on native runners where feasible; emulation/cross-build results are not treated as equivalent to native runtime evidence when target ABI/runtime behavior needs confirmation.
- Docker/container success is not accepted as the sole proof of runtime support for SteamOS, Bazzite, Wayland session integration, Waydroid, graphics/input device access, or other host/session-dependent behavior. These require a suitable VM, dedicated runner, or physical-device test and retained evidence.
- The release gate publishes a per-target validation report and blocks the `Supported` label when any required gate is missing or failing.

## 7. Proposed initial support matrix (candidate, not yet a promise)

This table defines candidate baselines for the first packaging sprint, not a promise of compatibility. Candidates are based on official release/status pages available on 2026-10-27; exact image IDs, package snapshot timestamps, and the version actually running on SteamOS/Bazzite test devices must be recorded. Each combination must still pass the acceptance gates before it is advertised as supported.

| Family | Candidate initial target(s) | Candidate architectures | Candidate artifact | Validation note |
|---|---|---|---|---|
| Debian | Debian 13.7 stable (`trixie`) | `amd64`, `arm64` where validated | `.deb` | Verify generated dependencies and lifecycle on each architecture. |
| Ubuntu | Ubuntu 24.04.5 LTS and 26.04.1 LTS | `amd64`, `arm64` where validated | `.deb` | Test candidate LTS releases and architectures separately; do not infer compatibility from host build. |
| Fedora | Fedora 44 | `x86_64`, `aarch64` where validated | `.rpm` | Validate ABI, dependency metadata, lifecycle and CI availability. |
| Arch Linux | Repository snapshot pinned from 2026-10-27 | `x86_64` initial; other architectures separately evaluated | `.pkg.tar.zst` + `PKGBUILD` | Record the exact snapshot/package set; rolling-release compatibility must be tested. |
| Alpine | Alpine 3.24.3 stable | `x86_64`, `aarch64` where validated | `.apk` | Separate musl build and runtime gate for each architecture. |
| SteamOS | Stable channel; SteamOS 3.9.5 candidate, confirm actual device build | `x86_64` initial | Flatpak + portable archive | Must be validated on device in Desktop and intended Gaming Mode. |
| Bazzite | Stable image 44.20261026 candidate; record full image identity | `x86_64` initial | `.rpm` + AppImage if validated, otherwise `.tar.zst` portable archive | Validate both paths independently; document layering/reboot behavior and test Waydroid and compositor integration. |

## 8. Release validation requirements

A candidate release is not ready until all checks below pass for the artifact/support combinations announced in that release:

1. Build and package validation in the target build environment.
2. Static inspection of package contents, ownership, file paths, licenses, architecture, and version metadata.
3. Dependency generation/reporting and installation on a clean target system.
4. Runtime loader check and shared-library resolution on the actual target release.
5. Install → update → explicit version selection/downgrade where supported → remove lifecycle tests.
6. Migration tests from the legacy `/opt/cage-xtmapper` plus `/usr/local/bin` layout.
7. Signature and checksum verification tests, including deliberate invalid-signature/checksum cases.
8. Repository install/update tests for every ecosystem whose repository is advertised.
9. For SteamOS and Bazzite, tests of their intended session and Waydroid integration, not only package installation.
10. Documentation checks so release notes do not list an artifact/target as `Supported` unless that matrix cell passed; artifacts with incomplete validation must be clearly labeled `Experimental`.
11. Docker/container smoke tests for distribution package tools and lifecycle behavior, plus non-container validation for the runtime/session requirements containers cannot prove.

CI should provide container/VM coverage for package and dependency checks. Physical or dedicated session-level testing remains a release gate for SteamOS and any other behavior that cannot be meaningfully exercised in a generic build container.

## 9. Rollout strategy (high level; detailed tasks in companion `plan.md` and `tasks.md`)

1. Define the support matrix/status labels, package-owned paths, version mapping, signing and repository policies, legacy migration contract, and CI/container/device-test split. Prepare candidate artifacts for all seven tracked distribution targets across six families for the first public release, without declaring unvalidated targets officially supported.
2. Build and test Debian/Ubuntu packages end-to-end, including APT repository publishing and manual exact-version install.
3. Build and test Fedora RPMs and RPM repository publishing.
4. Add Arch PKGBUILD/AUR workflow and decide whether a dedicated signed Pacman repository is justified for `pacman -Syu` integration.
5. Evaluate musl-specific Alpine build feasibility and proceed only when runtime dependencies can be declared and tested.
6. Test SteamOS and Bazzite distribution formats on their actual update models and session requirements.
7. Adapt the optional bootstrap to route users to supported channels while preserving a safe transition/fallback.
8. Publish the support matrix and release asset index; do not announce unvalidated targets.

## 10. Remaining decisions and validation questions

The following items are still open or gated. Candidate target baselines are proposed in Section 7 and `plan.md`; exact image digests, target-specific test evidence and maintainer decisions must be recorded before publication. Runtime compatibility checks in Sections 6–8 remain mandatory technical gates.

1. **Initial support matrix:** proposed candidates for the sprint are Debian 13.7, Ubuntu 24.04.5 LTS and 26.04.1 LTS, Fedora 44, an Arch package snapshot pinned from 2026-10-27, Alpine 3.24.3, SteamOS stable (3.9.5 candidate, confirm on device), and Bazzite stable 44.20261026. Pin exact image digests/snapshots and record actual device builds; these candidates do not imply compatibility. Keep `Supported` gated on test evidence.
2. **Repository hosting (maintainer-owned decision):** GitHub Releases must provide direct versioned downloads. The maintainer must decide whether to operate signed APT, RPM, Pacman, and APK repositories and where to host them. Expected release cadence is weekly or fortnightly when releases are ready; this is an estimate, not a schedule commitment. Keep CI build/validation separate from repository publishing until the decision is approved.
3. **Signing and provenance:** signing-key ownership, CI signing mechanism, key rotation/revocation, recovery, and whether signed tags or GitHub build provenance attestations are also required.
4. **Native package paths:** exact paths and any desktop/session integration assets for each package type. These should be proposed for maintainer review before implementation.
5. **Legacy installer migration:** whether the optional bootstrap migrates automatically or provides a guarded explicit migration command before installing a native package.
6. **Per-artifact library mapping:** the hybrid policy in Q7 is approved. Produce the exact system/bundled/excluded list from the actual ELF, Meson dependency-selection records, target-native package metadata, and runtime test results.
7. **Manual SteamOS/Bazzite lifecycle:** exact install/update/remove commands, user-local versus system-wide paths, and how portable artifacts coexist with native packages without conflicting files.

## 11. Definition of done

This specification is implemented only when:

- Every named target family—Debian, Ubuntu, Fedora, Arch Linux, Alpine Linux, SteamOS, and Bazzite—has at least one tested `Supported` installation path for its declared baseline version and architecture. A family with no supported path means the overall cross-distribution goal is not complete, even if an experimental artifact exists.
- Every advertised supported target has a version-specific, directly downloadable release artifact that does not require the project's installer script.
- Native packages have correct, tested dependency metadata and follow their ecosystem's package conventions.
- Users can opt into signed native repositories for ordinary package-manager updates where that ecosystem is advertised as repository-supported.
- SteamOS and Bazzite have explicit, tested manual installation paths and are not omitted from release documentation.
- The optional bootstrap does not conflict with package-managed installations.
- Migration, update, explicit version selection, removal, signing, and target compatibility are covered by verifiable tests.
- Unsupported combinations are clearly identified and are not advertised as compatible.

## 12. Research references

These sources inform requirements and unresolved decisions; they do not establish that Cage XtMapper is already compatible with any target.

### Project baseline

- [Project README at reviewed baseline](https://github.com/SoufianoDev/cage-xtmapper/blob/c86ed44ed58e49c2ac6835968c3aed25dc9fb0fa/README.md)
- [Installation and recovery documentation at reviewed baseline](https://github.com/SoufianoDev/cage-xtmapper/blob/c86ed44ed58e49c2ac6835968c3aed25dc9fb0fa/docs/installation.md)
- [Release workflow at reviewed baseline](https://github.com/SoufianoDev/cage-xtmapper/blob/c86ed44ed58e49c2ac6835968c3aed25dc9fb0fa/.github/workflows/release.yml)

### Spec-driven workflow

- [GitHub Spec Kit overview](https://github.github.com/spec-kit/)
- [Spec Kit quickstart and phases](https://github.github.com/spec-kit/quickstart.html)
- [Adopting Spec Kit in an existing project](https://github.github.com/spec-kit/guides/existing-projects.html)

### Native packaging and distribution

- [Debian Policy Manual — relationships and dependencies](https://www.debian.org/doc/debian-policy/ch-relationships/)
- [Debian Policy Manual — shared libraries and binary packages](https://www.debian.org/doc/debian-policy/ch-binary/)
- [Meson `library()` and `default_library`](https://mesonbuild.com/Reference-manual_functions_library.html) — `default_library` controls project-built libraries; external dependency selection is a separate decision.
- [Meson subproject/dependency fallback model](https://mesonbuild.com/Subprojects.html) — an available system dependency can be selected instead of the fallback subproject.
- [AppImage concepts and base-library exclusions](https://docs.appimage.org/introduction/concepts.html) — supports the need to audit graphics and low-level libraries before bundling them.
- [ArchWiki — creating packages](https://wiki.archlinux.org/title/Creating_packages)
- [ArchWiki — package guidelines](https://wiki.archlinux.org/title/Arch_package_guidelines)
- [Alpine APK index format](https://wiki.alpinelinux.org/wiki/Apk_spec)
- [Alpine Linux release branches](https://alpinelinux.org/releases/)
- [Ubuntu release cycle](https://ubuntu.com/about/release-cycle)
- [Debian current stable status](https://wiki.debian.org/Status/Stable)
- [Ubuntu current releases](https://wiki.ubuntu.com/Releases)
- [Fedora 44 announcement](https://fedoramagazine.org/announcing-fedora-linux-44/)
- [Alpine current downloads/releases](https://www.alpinelinux.org/downloads/)
- [SteamOS stable release notes](https://www.steamdeck.com/en/news?p=1)
- [Bazzite releases](https://github.com/ublue-os/bazzite/releases)
- [Fedora Project current release page](https://fedoraproject.org/)

### SteamOS and Bazzite

- [Bazzite software installation recommendations](https://docs.bazzite.gg/) — documents its preference order: Bazzite Portal, Flatpak for most apps, Homebrew for CLI apps, containers, AppImage, and layering only if needed.
- [Steam Deck desktop software / Flatpak FAQ](https://help.steampowered.com/en/faqs/view/671A-4453-E8D2-323C/)
- [Steam Deck Desktop Mode guidance](https://help.steampowered.com/en/faqs/view/69E3-14AF-9764-4C28)
- [Bazzite software management overview](https://docs.bazzite.gg/Installing_and_Managing_Software/)
- [Bazzite AppImage guidance](https://docs.bazzite.gg/Installing_and_Managing_Software/AppImage/)
- [Bazzite rpm-ostree package layering guidance](https://docs.bazzite.gg/Installing_and_Managing_Software/rpm-ostree/)
- [Bazzite Waydroid setup guide](https://docs.bazzite.gg/Installing_and_Managing_Software/Waydroid_Setup_Guide/)

---

**Next Spec Kit steps:** continue from the evidence-backed status in tasks.md, support-matrix.md, and library-decision-table.md. T003–T006 and T025 now have recorded implementation evidence; T001/T002 remain open for release/tag alignment and host/device target identity. Continue with package runtime/lifecycle gates, SteamOS/Bazzite validation and the release asset index. Production repository publication, signing keys and hosting remain owner-gated. Keep implementation specifics in plan.md/tasks.md; revise requirements only when an accepted decision changes.

## Research references

- GitHub Spec Kit, Agentic SDD and clarify workflow: https://github.github.com/spec-kit/reference/agentic-sdd.html
- GitHub Spec Kit quickstart: https://github.github.com/spec-kit/quickstart.html
- Docker documentation, multi-platform builds and architecture/emulation limits: https://docs.docker.com/build/building/multi-platform/
- Bazzite installation documentation (for target-specific testing requirements): https://docs.bazzite.gg/General/Installation_Guide/install-guide/

## 13. Implementation evidence snapshot — 2026-10-27

This is a status update, not a modification of the specification's acceptance criteria.

- The latest fully successful target-native audit is [run 40501883064](https://github.com/SoufianoDev/cage-xtmapper/actions/runs/40501883064), using source commit 9db78c2b1f2827bad631988edb7f6f99a1484b96. Debian 13.7, Ubuntu 24.04.5, Ubuntu 26.04.1, Fedora 44, Arch Linux on the 2026-10-26 archive snapshot and Alpine 3.24.3 all passed their target-container build/dependency/package install/remove checks.
- The matching amd64/arm64 validation run [40501883118](https://github.com/SoufianoDev/cage-xtmapper/actions/runs/40501883118) passed both jobs. Its ELF/audit artifacts are [amd64](https://github.com/SoufianoDev/cage-xtmapper/actions/runs/40501883118/artifacts/12733331913) and [arm64](https://github.com/SoufianoDev/cage-xtmapper/actions/runs/40501883118/artifacts/12733177198).
- Per-target runtime library findings and the hybrid system/bundle decisions are documented in library-decision-table.md. The exact per-distribution statuses and evidence links are in support-matrix.md, and the shared package paths/identity/ownership rules are in package-contract.md.
- All target rows remain Experimental or Unavailable; none is Supported. A successful package install/remove in a container is not proof of application startup, graphics, Wayland, input/seat access, Waydroid integration or session compatibility.
- Native .deb, .rpm, Arch and .apk candidates are CI artifacts only. The current release.yml remains the old .tar/installer-helper release path. SteamOS Flatpak and Bazzite AppImage recipes are absent. Steam Deck Desktop/Gaming Mode and Bazzite target-image/session tests remain blocked on target access. No public release, repository publication or production signing operation was performed.
