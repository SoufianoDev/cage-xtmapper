# native-src — repository-owned Cage and wlroots sources

This directory holds the complete native source trees that `build.sh` stages
into `build/source/` for the single-ELF build. They are ordinary files tracked
by this repository; no submodule initialization or network fetch is required to
build. There is no nested Git metadata in these trees.

## Provenance

| Tree | Upstream project | Origin repository | Origin revision (at import) | Import commit |
|------|------------------|-------------------|-----------------------------|---------------|
| `cage/` | [cage-kiosk/cage](https://github.com/cage-kiosk/cage) 0.3.0, MIT | `https://github.com/Xtr126/cage.git`, branch `codex/xtmapper-v0.3.0` | `c317ff85f05bd3f671bbb18b6b985b0d43b62cfe` (remote branch head at import) | `3195865db1548e8a8101de82284f03697e835f14` |
| `wlroots-xtmapper/` | [wlroots](https://gitlab.freedesktop.org/wlroots/wlroots) 0.20.2, MIT | `https://github.com/Xtr126/wlroots-xtmapper.git`, branch `codex/xtmapper-v0.20.2` | `cd59bb46568e491b58574c5bc6fb24b97a32ae8f` (remote branch head at import) | `cd59bb46568e491b58574c5bc6fb24b97a32ae8f` |

Machine-readable record: [`ORIGINS`](./ORIGINS). The fields are
`key import-commit origin-url origin-revision`. `ORIGINS` is a maintained build
input: it is covered by the build-input fingerprint, and
`internal/releasebuild` copies it into release manifests
(`native_commits`, `native_origins`). Update it only when a reviewed import or
upstream merge changes the lineage; never invent revisions.

Notes on the lineage:

- The cage import commit `3195865` is five commits ahead of the origin branch
  head (`c317ff8`) and existed only in the old local submodule checkout; the
  five commits are part of this repository's history now (their content is
  imported here). The origin revision column records what the remote branch
  advertised at import time, not the imported tree itself.
- The wlroots import commit equals its origin revision (the branch was in sync
  at import time).

## Project-specific modifications

The imported trees are not pristine upstream releases; they carry the XtMapper
integration developed on the `codex/xtmapper-*` fork branches:

- `cage/`: the XtMapper integration (`xtmapper.h`, compositor/seat/output/view
  changes, the `cage_xtmapper` Meson target) and the single-ELF adaptations
  (library entry point `cage_xtmapper_main` extraction, building the entry
  without the standalone `main` symbol, posix_spawnp for the primary client,
  terminating the primary client on embedded shutdown).
- `wlroots-xtmapper/`: XtMapper output and pointer behavior, custom refresh
  rate handling, and related fixes on the `codex/xtmapper-v0.20.2` branch.

One additional project change is applied in this directory (see below): cage's
Meson version string only embeds Git information when the source tree is its
own Git checkout, so the imported tree reports the plain project version
instead of the parent repository's branch and commit.

## Build integration

- `build.sh` stages `cage/` into `build/source/cage-0.3.0/` and
  `wlroots-xtmapper/` into `build/source/cage-0.3.0/subprojects/wlroots/`,
  then adds `meson_wrapfiles/` wraps and `wlroots_deps/` archives (maintained,
  checksummed inputs at the repository root).
- The content fingerprint that invalidates the staged source and gates release
  packaging covers every file here (paths, contents, executable bits, symlink
  targets), plus `ORIGINS`, the wraps, the dependency archives, `build.sh`, and
  the toolchain identity. See `internal/releasebuild` (`NativeInputDigest`).

## Incorporating future upstream changes

1. Fetch the upstream project and the origin fork branch into a scratch clone;
   never fetch during the build.
2. Review and merge/cherry-pick upstream changes into a copy of the relevant
   tree here, resolving conflicts against the XtMapper integration.
3. Update `ORIGINS` (import commit and origin revision) to reflect the reviewed
   import, and update this document if the integration points changed.
4. Run the full build and test suite; the fingerprint change forces a fresh
   native rebuild automatically.

## License

Both trees are MIT licensed (`cage/LICENSE`, `wlroots-xtmapper/LICENSE`).
Release packaging collects these notices plus bundled-dependency notices from
the staged source tree into `LICENSES/`; do not remove or rewrite license and
copyright headers when editing these trees.
