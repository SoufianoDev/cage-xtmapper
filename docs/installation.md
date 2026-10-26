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

There is one application ELF. The installer does not create a runtime shell wrapper or a second Cage executable. `install.sh` and `tools/` remain in the source checkout.

Use Linux with Go 1.24+, Bash, `/proc`, local-filesystem `flock`, directory `fsync`, `renameat2(RENAME_NOREPLACE)`, and `O_TMPFILE` support. `install.sh` compiles the source-only Go tool into a private temporary directory, then executes its open inode after removing the temporary files. Go is not required to run the installed application. Unsupported filesystem operations fail explicitly. Production installation roots, their parents, and the binary directory must be root-owned, non-symlink directories without group/other write permission. `/opt` and `/usr/local/bin` must already exist.

## Commands

Run from the matching source checkout:

```bash
sudo ./install.sh install --archive /absolute/path/release.tar
sudo ./install.sh update --archive /absolute/path/new-release.tar
./install.sh status
sudo ./install.sh recover
sudo ./install.sh uninstall
```

Relative archive paths are relative to the calling directory. `--archive` is required for `install` and `update`, and rejected for the other commands. There is no persistent rollback command.

| Command | Required State | Result |
|---|---|---|
| `install` | No active managed release; no conflicting files | Stage and activate a release. A prior uninstalled ownership record can be reused. |
| `update` | Existing, intact managed installation | Activate a new release and remove the obsolete managed release. |
| `status` | Any readable state | Report active release and any incomplete transaction without modifying installation state. Unknown/corrupt installations produce an error. |
| `recover` | Managed state or a complete recorded bootstrap | Inspect the real `current` symlink and journal; roll back precommit staging or finish postcommit cleanup. Repeating successful recovery is harmless. |
| `uninstall` | Existing, intact managed installation | Remove the recorded launcher link and managed release data. |

System mutations require root. A runtime or another administrative process holding a conflicting lock causes an immediate busy error. A pending transaction blocks install, update, and uninstall until recovery completes.

| Exit Status | Meaning |
|---|---|
| `0` | Operation completed, or status reported successfully. |
| `1` | Operation failed; no retained transaction was identified. |
| `2` | Invalid command-line syntax. |
| `3` | Busy lock; active installation files were not changed. |
| `4` | An incomplete transaction remains. Inspect status and recover; the active release depends on whether the commit point was crossed. |

Errors go to stderr. Status uses JSON on stdout; mutating commands report the active release, removals, and intentionally preserved paths. The installer never executes the release ELF to validate it.

Repeating a command in a state it does not apply to is a deliberate refusal, not a no-op: `install` with an active managed release, `update` whose archive has the release id that is already active, and `uninstall` without an installed release all fail with exit status 1 and change nothing. A refused operation stages no release, replaces no link, state record, ownership inventory, or lock, and writes no transaction journal. Identical archives are never re-staged over an existing release id; to redeploy the same version, uninstall first or publish it under a new release id. Refusals keep conflicting or modified files protected; inspect them with `status` and resolve them explicitly.

## Ownership And Legacy Files

`state.json` identifies the installation, expected paths and owner, directory identities, stable lock inode, active release's file inventory, and launcher/current symlink identities and targets. Inventories include device/inode, owner, mode, and file digest. Each atomic metadata file records its own inode identity. The journal records the objects created or reserved by its transaction.

An arbitrary matching filename, symlink target, or checksum is insufficient to adopt a file. Existing unknown roots and launcher paths cause refusal. Root components, symlink components, hard links, unexpected file types, changed permissions, file capabilities, and changed owned content fail validation.

The old documented procedure manually copied `/usr/local/bin/cage_xtmapper` and `/usr/local/bin/cage_xtmapper.sh`. It left no reliable installation receipt. Therefore either legacy path, including an unrelated symlink at that name, blocks install/update before activation. Inspect and move a conflicting file explicitly after establishing its ownership. The installer does not delete or adopt it.

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
