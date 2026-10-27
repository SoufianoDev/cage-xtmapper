# Installation And Recovery

## Layout

The installer supports system-wide installation. Its production paths are fixed:

```text
/opt/cage-xtmapper/
  releases/<release-id>/
    cage-xtmapper
    manifest.json
    SHA256SUMS
    LICENSES/
  current -> releases/<release-id>
  state.json
  transaction.json       # only while a transaction is incomplete
  .install.lock

/usr/local/bin/cage-xtmapper -> /opt/cage-xtmapper/current/cage-xtmapper
```

There is one installed application ELF; the installer helper is a separate, temporary executable and is not retained in the installation. The bootstrap `install.sh` downloads a prebuilt helper from a published release, so end users do not need Go or a source checkout.

Use supported 64-bit Linux (amd64 or arm64), Bash, curl, `sha256sum`, `/proc`, local-filesystem `flock`, directory `fsync`, `renameat2(RENAME_NOREPLACE)`, and `O_TMPFILE` support. The helper validates the release archive before staging it. It checks the declared ELF interpreter, glibc baseline, and shared libraries; missing library packages are installed through APT on Debian/Ubuntu. Other distributions receive an explicit missing-library error rather than guessed package names. Waydroid and the XtMapper Android APK remain prerequisites and are not installed by this script. Production installation roots, their parents, and the binary directory must be root-owned, non-symlink directories without group/other write permission. `/opt` and `/usr/local/bin` must already exist.

## Commands

Run the bootstrap without an operation argument to let it inspect the system and choose the right action. It installs when no managed release exists, updates an older active release, reports `already up to date` when the selected latest archive is identical, and attempts recovery of a recorded incomplete transaction before continuing.

```bash
curl -fsSL https://raw.githubusercontent.com/SoufianoDev/cage-xtmapper/migrate-to-c-golang-no-submodule/install.sh | sudo bash
```

You may pin a published release without selecting an explicit operation:

```bash
curl -fsSL https://raw.githubusercontent.com/SoufianoDev/cage-xtmapper/migrate-to-c-golang-no-submodule/install.sh | sudo bash -s -- --version vX.Y.Z
```

The bootstrap downloads the prebuilt helper and matching archive, verifies both SHA-256 checksums, and delegates the filesystem transaction to the Go installer. The automated installer still uses the versioned `.tar` format for Linux amd64 and arm64; it does not call APT, DNF, Pacman or APK to install the main application. The tag-triggered release workflow additionally builds experimental `.deb`, `.rpm`, Arch/Pacman and Alpine/APK candidates and is configured to attach them to a draft release with a checksummed asset index. This native release path has passed branch CI package lifecycle checks, but the release workflow itself has not yet been exercised by a version-tag push. These candidates are not yet `Supported` runtime paths, because a container `--help` smoke test is not a real Wayland/graphics-session or Waydroid integration test. Existing non-empty draft releases are deliberately left unchanged; the workflow refuses to append assets to them or overwrite already-published releases. Alpine APK files are signed with a disposable CI key, not a publisher-authenticated production key.

## Experimental native package candidates

The tag-triggered release workflow is configured to attach native package candidates to a clean draft release. These files are not available as public release downloads until a maintainer publishes that draft, and the tag-triggered release workflow has not yet been run end-to-end. Treat all native formats as Experimental: CI has tested package metadata, package-manager lifecycle, and the installed CLI help output in containers, but not startup in a real Wayland/graphics session or Waydroid input integration.

When a published release index lists the matching package for your exact distribution version and architecture, download that asset and its adjacent .sha256 file. In the same directory, verify the file before installing. Example filenames below use sample versions; use the exact asset names listed in the release index.

### Debian 13 and Ubuntu 24.04 / 26.04 (.deb)

For Ubuntu, choose the asset matching your LTS release; for Debian, choose the Debian 13 asset.

```bash
pkg='./cage-xtmapper-v1.2.3-ubuntu-24.04-amd64.deb'
sha256sum -c "$pkg.sha256"
sudo apt install "$pkg"

older_pkg='./cage-xtmapper-v1.2.2-ubuntu-24.04-amd64.deb'
sha256sum -c "$older_pkg.sha256"
sudo apt install --allow-downgrades "$older_pkg"

sudo apt purge cage-xtmapper
```

For Debian 13, substitute the asset names containing `debian-13`; for Ubuntu 26.04, substitute `ubuntu-26.04`. Installing a newer local .deb through APT upgrades the installed package and resolves its declared dependencies. APT repositories are not configured by this project.

### Fedora 44 (.rpm)

```bash
pkg='./cage-xtmapper-v1.2.3-fedora-44-x86_64.rpm'
sha256sum -c "$pkg.sha256"
sudo dnf install "$pkg"

older_pkg='./cage-xtmapper-v1.2.2-fedora-44-x86_64.rpm'
sha256sum -c "$older_pkg.sha256"
sudo rpm -Uvh --oldpackage "$older_pkg"

sudo dnf remove cage-xtmapper
```

Fedora's RPM candidate has passed rpmlint using its canonical NEVRA filename and the DNF/RPM lifecycle audit in CI. This is not evidence of Bazzite compatibility; Bazzite requires separate host validation.

### Arch Linux (.pkg.tar.zst)

```bash
pkg='./cage-xtmapper-v1.2.3-archlinux-x86_64.pkg.tar.zst'
sha256sum -c "$pkg.sha256"
sudo pacman -U "$pkg"

older_pkg='./cage-xtmapper-v1.2.2-archlinux-x86_64.pkg.tar.zst'
sha256sum -c "$older_pkg.sha256"
sudo pacman -U "$older_pkg"

sudo pacman -Rns cage-xtmapper
```

These are direct package-file operations. The project does not operate a Pacman binary repository, and it does not publish or manage an AUR entry for you. Updating requires downloading the new versioned asset (or rebuilding a reviewed PKGBUILD); do not expect `pacman -Syu` to update this package automatically.

### Alpine Linux 3.24 (.apk)

```bash
pkg='./cage-xtmapper-v1.2.3-alpine-3.24-x86_64.apk'
sha256sum -c "$pkg.sha256"
sudo apk add --allow-untrusted "$pkg"

sudo apk del cage-xtmapper
```

The CI-only APK signature is made with a disposable key that is not a production publisher key, so `--allow-untrusted` is an explicit trust bypass. Verify the SHA-256 sidecar before installation; the checksum detects corruption but does not authenticate who published the file. Explicit downgrade is not claimed or documented for APK in this workflow.

### Legacy installer conflict and common limitations

Native packages install the executable under /usr/bin/cage-xtmapper, while the bootstrap installer owns /opt/cage-xtmapper and /usr/local/bin/cage-xtmapper. The .deb preinst and RPM %pre scripts stop installation when those legacy paths exist. Arch and Alpine package scriptlets are warning-only in practice: they report that the existing /usr/local/bin launcher may shadow /usr/bin, but they do not guarantee refusal and their package managers may continue installing even if a scriptlet fails. CI explicitly tests these two behaviors and confirms the old sentinel files remain unchanged; it removes the test package after the warning-only case. Do not treat these scriptlets as a migration or hard safety barrier. The bootstrap uninstaller deliberately retains ownership state and lock metadata, so running uninstall may not remove the conflict; inspect leftovers rather than deleting them blindly or removing a lock file while a process may hold it.

These packages do not install Waydroid, the XtMapper APK, or a compositor/graphics stack. They do not configure native package repositories. Production signing, a published trust root, and support status beyond Experimental remain maintainer-gated.

At present, the installer can install recognized missing shared-library packages through APT on Debian/Ubuntu. Other distributions receive explicit errors for missing libraries; package names are not guessed. Waydroid and the XtMapper Android APK remain prerequisites. System mutations require root, normally supplied by `sudo`. The bootstrap's default auto mode is the normal user-facing entry point; explicit operations are retained for diagnostics and maintenance.

| Explicit Operation | Result |
|---|---|
| `status` | Report the active release and any recorded incomplete transaction without modifying state. |
| `recover` | Attempt recovery directly. Normal auto mode attempts this when it detects a recorded incomplete transaction. |
| `uninstall` | Remove the recorded launcher link and managed release data while preserving unowned files and the stable ownership record. |
| `install` / `update` | Force the corresponding operation choice; the default auto mode is preferred for routine use. |

Auto recovery is conservative. If the journal is valid, the installer determines whether the commit point was crossed and completes rollback or cleanup accordingly, then continues toward the selected release. If required ownership metadata is missing or an object has been modified, it reports the conflict and preserves it rather than guessing. A busy runtime lock is not forcibly broken; the installer reports that the application is still running or another administrative operation is active.

The status command can be run without root. The installer does not execute the release ELF to validate it. SHA-256 checksums detect corruption but do not authenticate the publisher; this remains a documented limitation until release-signing or provenance verification is added.


## Ownership And Legacy Files

`state.json` identifies the installation, expected paths and owner, directory identities, stable lock inode, active release's file inventory, and launcher/current symlink identities and targets. Inventories include device/inode, owner, mode, and file digest. Each atomic metadata file records its own inode identity. The journal records the objects created or reserved by its transaction.

An arbitrary matching filename, symlink target, or checksum is insufficient to adopt a file. Existing unknown roots and launcher paths cause refusal. Root components, symlink components, hard links, unexpected file types, changed permissions, file capabilities, and changed owned content fail validation.

The old documented procedure manually copied `/usr/local/bin/cage_xtmapper` and `/usr/local/bin/cage_xtmapper.sh` without a reliable installation receipt. A regular shell file is recognized only when its shebang and XtMapper-specific content markers match the old launcher. After the new release is active, the script is moved to `cage_xtmapper.sh.legacy-backup-<id>` in the same directory; if it is paired with an executable ELF at `cage_xtmapper`, that binary is moved to `cage_xtmapper.legacy-backup-<id>` using the same random suffix. Both contents are preserved and neither backup is executed or deleted by migration. A changed or unknown script, any symlink at either legacy path, or an unpaired/non-ELF `cage_xtmapper` blocks installation until inspected. The installer does not guess ownership or remove unknown files.

Uninstall verifies the recorded launcher symlink's inode and target before removing it. It removes only the recorded release files, then directories that are still owned and empty. Unknown or modified content inside a managed release causes safe refusal. Unrelated root entries, other links and their targets, Waydroid data/APKs, shared libraries/tools, Steam entries, and desktop/user configuration are preserved.

Uninstall retains `.install.lock`, `state.json` as an uninstalled ownership record, and the empty managed directory structure. This preserves the stable synchronization object and makes a later install distinguishable from an unknown installation. Never delete or replace `.install.lock` while a process may hold it.

## Transaction And Commit Point

1. Validate all archive members and content in memory before staging or activation. Obtain the exclusive, nonblocking installation lock.
2. Record the transaction, including unique staging/link reservations and the old release's ownership inventory. A fresh installation publishes a fully recorded bootstrap directory with a no-replace rename.
3. Stage on the managed root's filesystem. Write regular files through `O_TMPFILE`, flush them, and record their inode/digest before linking them into the staging tree. An interrupted write has no named partial file to adopt or delete.
4. Verify the staged tree and move it into `releases/<release-id>`. Persist the temporary `current` symlink's identity. The old release remains active throughout staging.
5. **The install/update commit point is the atomic rename of the temporary symlink onto `current`.** Flush the root directory immediately afterward. Recovery treats whichever recorded symlink actually survived as authoritative, including interruption between rename and directory flush.
6. After commit, finish the launcher link and state record, then remove the obsolete owned release. Preflight its inventory before deletion and check each object again before removal. Missing objects are tolerated only during recovery cleanup.
7. Remove and flush `transaction.json` only after cleanup succeeds. Exactly one managed release remains after a successful install/update.

Before the commit point, `recover` keeps the old release active and removes only recorded replacement/staging objects. After it, `recover` keeps the new release active and retries cleanup. It does not switch back after obsolete release files have begun disappearing. Modified or unowned files remain intact, and recovery retains the journal instead of declaring success.

For uninstall, the commit point is removal of the recorded `current` symlink, followed by directory flush. Before that point recovery preserves the installation; afterward it finishes removal of the owned launcher and release.

A completed bootstrap record can be recovered if interruption happened before its directory became `/opt/cage-xtmapper`. If a crash or outside edit leaves data without sufficient ownership records, recovery reports the conflict and preserves it for inspection. It never guesses ownership to force cleanup. Unknown temporary metadata files are also preserved.

## Runtime Lock Lifetime

The running ELF uses `/proc/self/exe` to identify its already-mapped inode's release directory. It opens the stable `.install.lock` and acquires a shared lock **before reading or resolving `current`**. It then verifies that `current` still names that running inode. An update that wins the startup race causes a stale launch to fail with a retry message.

The Go launcher holds the shared lock for its lifetime. Its `/proc/self/exe --internal-cage` child inherits the same open file description as descriptor 3, so there is no protection gap across exec or unexpected parent death. The internal role restores close-on-exec before native Cage starts; Waydroid and other clients/tools do not inherit the lock. Source-tree executions outside the managed release layout have no installation lock.

Integration tests inspect the native/client descriptors, kill the Go parent, and check that update/uninstall remain busy until the native process exits. This uses actual kernel locks and a real headless compositor.

## Release Format

Archives are uncompressed USTAR `.tar` files containing only these entries and regular license-notice files:

```text
cage-xtmapper
manifest.json
SHA256SUMS
LICENSES/
```

The application has mode 0755; metadata and license files have mode 0644; `LICENSES` has mode 0755. Archive ownership is root:root, but extraction assigns the installation's owner. Links, devices, FIFOs, sparse files, extended archive metadata, duplicate entries, unsafe paths, and extra files/directories are rejected. License files use flat names below `LICENSES/`.

The manifest includes application/version/release identity, Linux target architecture, main and native commits, dirty-source/digest information, Go/Meson/compiler versions, build options, dependency selection, dynamic runtime requirements, ELF digest, and component license paths. `SHA256SUMS` covers the ELF, manifest, and all license files; it does not hash itself. The manifest does not hash itself either.

The current validator supports little-endian 64-bit amd64 and arm64 ELF headers and verifies the archive's target against the installation host. This is a format/architecture check, not evidence that both architectures or every distribution were tested. Bundles are limited to 512 MiB and 256 members. Check the manifest's runtime libraries and compatibility requirements for the target system.

Checksums do not authenticate the publisher. Build provenance does not assert bit-for-bit reproducibility. The packager validates its output and refuses to replace an existing archive.

## Isolated Test Prefixes

`--test-prefix` relocates both paths beneath one existing 0700 directory owned by the caller. It is for filesystem/process tests, not a supported per-user production installation. It does not change the production defaults or allow unsafe ownership/symlink components.

Create the test system directories with explicit permissions under a trusted home directory:

```bash
prefix=$(mktemp -d "$HOME/cage-install-test.XXXXXX")
mkdir -m755 "$prefix/opt" "$prefix/usr"
mkdir -m755 "$prefix/usr/local" "$prefix/usr/local/bin"
./install.sh install --test-prefix "$prefix" --archive /path/to/release.tar
./install.sh status --test-prefix "$prefix"
./install.sh uninstall --test-prefix "$prefix"
```

This manages only `$prefix/opt/cage-xtmapper` and `$prefix/usr/local/bin/cage-xtmapper`. The Go integration suite creates and cleans its own private prefixes. Its named pause hooks are enabled only with a test prefix and write a checkpoint file there, so SIGKILL can exercise staging, activation, and partial cleanup without modifying the real system installation.


## Developer Tests Without a GitHub Release

Run the complete local installer lifecycle test:

```bash
bash tests/test-installer-local.sh
```

The script builds the application and helper locally, packages two temporary release archives, and exercises install, automatic update through the normal `install` command, repeated-install no-op behavior, status, and uninstall under an isolated private test prefix. It does not publish a GitHub Release or modify the real `/opt/cage-xtmapper` or `/usr/local/bin/cage-xtmapper` paths. CI can use `bash tests/test-installer-local.sh --skip-build` after the normal native build has completed.

For testing the bootstrap shell script, set `XTMAPPER_INSTALLER_BIN` to a local helper path and pass `--archive FILE --test-prefix DIR`. This bypass is intended only for local development. Normal installations download and checksum-verify the helper and archive from a published release.
