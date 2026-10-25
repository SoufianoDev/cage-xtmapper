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

Wayland 1.24, libxkbcommon 1.8, and Pixman 0.46 use bundled sources. Other dependencies use the system or pinned fallbacks according to Meson. The build records the selected versions and configuration. Bundled libdrm and libxkbcommon retain compiler warnings without promoting them to errors.

## Build

Clone the branch you intend to work on, including its submodules:

```bash
git clone --recurse-submodules -b <branch> https://github.com/Xtr126/cage-xtmapper
cd cage-xtmapper
./build.sh
```

For an existing checkout:

```bash
git submodule update --init --recursive
./build.sh
```

The default is a release build. Build output stays under ignored `build/`:

| Path | Contents |
|---|---|
| `build/source/` | Source assembled from the committed native submodule trees. |
| `build/meson/` | Meson state, native objects, and archives. |
| `build/native/pkgconfig/cage-xtmapper-native.pc` | Generated cgo link contract derived from Ninja's dependency graph. |
| `build/go/cage-xtmapper` | Final application ELF. |
| `build/go/provenance.txt` | Build mode, native commits, input fingerprint, Go version. |
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

Use `update` for an existing managed installation. The packager refuses stale source/ELF digests or an existing output archive. Rebuild after changing runtime sources, source tools, or build scripts. Its manifest records a dirty checkout when applicable; a commit ID alone does not describe uncommitted changes.

The archive contains only the ELF, manifest, checksums, and license notices. The source-only installer compiles `tools/install` using the matching checkout's Go module; keep `install.sh`, `go.mod`, `tools/`, and `internal/` together. [Installation And Recovery](docs/installation.md) defines its commands, ownership model, and transaction guarantees.

License collection uses pinned native source notices and the installed Go toolchain notice. If the native link selects LittleCMS's static GPL plugins, packaging also needs their installed component notices and complete GPL-3 text. Unknown static libraries without a license rule stop metadata generation rather than producing an incomplete archive.

## Pinned Source Inputs

Cage and wlroots are submodules pinned by this repository's gitlinks. Their development branches are recorded in `.gitmodules`: Cage `codex/xtmapper-v0.3.0` and wlroots `codex/xtmapper-v0.20.2`.

The build requires clean, committed fork trees matching the parent index. It assembles those commits under `build/source/` and keeps Meson state under `build/meson/`. Dependency archives and Meson wraps remain maintained inputs; do not regenerate exported source patches.

Verify the dependency archives with:

```bash
sha256sum --check SHA256SUMS
```

When intentionally replacing an archive, update its digest and review its version/source URL in the wrap file. [CONTRIBUTING.md](CONTRIBUTING.md) covers fork changes and verification.

## CI And Release Packaging

CI verifies archive checksums, shell syntax/style, shell fixtures, the real native/Go build, and Go formatting/vet/race tests, including filesystem/runtime-lock integration. Tests use isolated prefixes and headless compositors. Waydroid/ADB transport tests use mocks.

The tag workflow uses the same Ubuntu 26.04 baseline, runs the runtime/installer checks, and builds an allowlisted `cage-xtmapper-<tag>.tar` for the matching draft release. No reproducible-build or wider distribution/architecture compatibility guarantee is implied by the recorded provenance.
