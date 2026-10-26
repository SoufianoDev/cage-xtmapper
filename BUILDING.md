# Building Cage XtMapper

This checkout builds the Cage 0.3.0 / wlroots 0.20.2 line as one Go/cgo ELF, `cage-xtmapper`. Ubuntu 26.04 on amd64 is the CI and release build baseline. The older branches retain their separate workflows.

## Host Dependencies

The build needs Go 1.24 or newer, Clang and binutils, Meson, Ninja, pkg-config, Bison, and the native development packages selected by `install_deps.sh`. Project-owned tooling and tests use Go and shell; Cage and wlroots use C compiled with Clang.

The helper supports Debian/Ubuntu, Fedora, Arch, and Alpine:

```bash
./install_deps.sh --dry-run
sudo ./install_deps.sh
```

The helper prints its package list in dry-run mode. `build.sh` never invokes the package installer automatically. Package names vary by distribution; the helper is the maintained list.

Wayland 1.24, libxkbcommon 1.8, and Pixman 0.46 use bundled sources. Other dependencies use the system or pinned fallbacks according to Meson. The build records the selected versions and configuration. Bundled Wayland, libdrm and libxkbcommon retain compiler warnings without promoting them to errors.

## Build

Clone the branch you intend to work on:

```bash
git clone -b <branch> https://github.com/Xtr126/cage-xtmapper
cd cage-xtmapper
./build.sh
```

For an existing checkout, just run `./build.sh`. The native Cage and wlroots
sources are committed under `native-src/`; no submodule initialization or
network fetch is needed.

The default is a release build. Build output stays under ignored `build/`:

| Path | Contents |
|---|---|
| `build/source/` | Source assembled from the committed `native-src/` trees. |
| `build/meson/` | Meson state, native objects, and archives. |
| `build/native/pkgconfig/cage-xtmapper-native.pc` | Generated cgo link contract derived from Ninja's dependency graph. |
| `build/go/cage-xtmapper` | Final application ELF. |
| `build/go/provenance.txt` | Build mode, input fingerprint, Go version. |
| `build/go/build-info.json` | Build-time source/ELF digests, commits, toolchains, Meson options, dependencies, runtime requirements, license notices. |
| `build/installed/usr/local/bin/cage-xtmapper` | One-file build staging area; this is not the managed system installation. |

Go's build cache does not track external native archive changes, so `build.sh` uses `CC=clang CGO_ENABLED=1 go build -a`. The native input fingerprint includes Clang's version so a compiler change rebuilds Meson's configuration and native objects. Native build or Go link failure preserves the previous successfully staged application.

Run an uninstalled build as a regular user:

```bash
./build/go/cage-xtmapper --help
./build/go/cage-xtmapper
```

## Debug Builds

Use debug mode when investigating build or runtime errors:

```bash
./build.sh --debug
./build/go/cage-xtmapper
```

This builds Cage and wlroots in debug mode and enables Cage's verbose logging. `./build.sh --debug --dry-run` assembles sources and configures Meson without compiling or staging the application. Running `./build.sh` again switches back to release mode. Both flags may appear in either order.

## Package And Install Locally

After building, create a local archive with an explicit version and unused output filename:

```bash
go run ./tools/release package --version v0.3.0-local --output build/releases/cage-xtmapper-v0.3.0-local.tar
go run ./tools/release verify build/releases/cage-xtmapper-v0.3.0-local.tar
sudo ./install.sh install --archive build/releases/cage-xtmapper-v0.3.0-local.tar
```

Use `update` for an existing managed installation. The packager refuses stale source/ELF digests, a stale native-input fingerprint, or an existing output archive. Rebuild after changing runtime sources, source tools, native sources, or build scripts. Its manifest records a dirty checkout when applicable; a commit ID alone does not describe uncommitted changes.

The archive contains only the ELF, manifest, checksums, and license notices. The source-only installer compiles `tools/install` using the matching checkout's Go module; keep `install.sh`, `go.mod`, `tools/`, and `internal/` together. [Installation And Recovery](docs/installation.md) defines its commands, ownership model, and transaction guarantees.

License collection uses pinned native source notices and the installed Go toolchain notice. If the produced ELF actually contains LittleCMS static GPL plugin code, packaging includes the matching plugin header as its component notice and the complete GPL-3 text. Both plugin components take their classification from the embedded upstream notice, which licenses them as GPL-3-or-later (Debian labels the same files GPL-3-only inside its package; the embedded upstream text is what the archive ships). The release manifest records them as separate components rather than assuming that both use the same license variant. Unknown static libraries without a license rule stop metadata generation rather than producing an incomplete archive.

Whether those plugins are linked depends on the build host's lcms2 development files: wlroots links lcms2 for color management, and Debian's pkg-config file puts `liblcms2_fast_float.a` and `liblcms2_threaded.a` on every lcms2 link line. Listing is not linking: the metadata records a plugin as a component only when its registration symbol appears in the produced ELF, because the GNU linker otherwise drops the unreferenced archive members. If a future build host does link a plugin, make available the corresponding plugin source with the release archive, as GPL-3 requires — for these Debian-built archives, that is the matching `lcms2` source distribution for the linked version. Review the actual linked component set and notices recorded in the release manifest before distributing an archive; these plugin licenses differ from the MIT notices covering the project's own sources.

## Pinned Source Inputs

Cage and wlroots live as ordinary committed files under `native-src/cage` and
`native-src/wlroots-xtmapper`. `native-src/ORIGINS` records each tree's
upstream URL, revision, and imported commit for release provenance, and
`native-src/README.md` documents how the trees were imported.

`build.sh` fingerprints every native build input (both trees, `ORIGINS`,
Meson wraps, dependency archives, `build.sh`, the Clang version, and the build
type) before staging, and the packager recomputes that fingerprint so a changed
input can never produce a stale package. Dependency archives and Meson wraps
remain maintained inputs; do not regenerate exported source patches.

Verify the dependency archives with:

```bash
sha256sum --check SHA256SUMS
```

When intentionally replacing an archive, update its digest and review its version/source URL in the wrap file. [CONTRIBUTING.md](CONTRIBUTING.md) covers fork changes and verification.

## CI And Release Packaging

CI verifies archive checksums, shell syntax/style, shell fixtures, the real native/Go build, and Go formatting/vet/race tests, including filesystem/runtime-lock integration. Tests use isolated prefixes and headless compositors. Waydroid/ADB transport tests use mocks.

The tag workflow uses the same Ubuntu 26.04 baseline, runs the runtime/installer checks, and builds an allowlisted `cage-xtmapper-<tag>.tar` for the matching draft release. No reproducible-build or wider distribution/architecture compatibility guarantee is implied by the recorded provenance.
