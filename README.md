# Cage XtMapper

Cage XtMapper runs Waydroid in a Cage window and forwards keyboard, mouse, and touchpad input to [XtMapper](https://github.com/Xtr126/XtMapper) on Android. You can set the window size and refresh rate, show a title bar, and switch mouse input between XtMapper and Waydroid.

This checkout builds Cage 0.3.0 and wlroots 0.20.2 into one Linux executable, `cage-xtmapper`, with a Go launcher.

## Before You Start

- Set up Waydroid and install the XtMapper Android APK.
- Use a Wayland desktop, Steam gaming session, or a supported DRM session from a TTY.
- Choose a build compatible with your system. The CI and release build baseline is Ubuntu 26.04 on amd64. Each release manifest records its architecture, shared-library requirements, and build host.
- Run the application as your regular user. The default transport invokes `sudo` for Waydroid container and Android shell operations.

The `v0.2.0` and `v0.1.5` branches retain their own build and installation procedures. Follow the [v0.2.0 instructions](https://github.com/Xtr126/cage-xtmapper/blob/v0.2.0/README.md) or [v0.1.5 instructions](https://github.com/Xtr126/cage-xtmapper/blob/v0.1.5/README.md) for those versions.

## Install Or Update

Run the installer without an operation argument. It checks the current installation and selects the necessary action automatically: install if absent, update if an older release is active, report that the system is already current when the release matches, and recover a recorded interrupted transaction before proceeding when its ownership records are valid.

```bash
curl -fsSL https://raw.githubusercontent.com/SoufianoDev/cage-xtmapper/migrate-to-c-golang-no-submodule/install.sh | sudo bash
```

To pin a published release, pass only the version option (the operation is still selected automatically):

```bash
curl -fsSL https://raw.githubusercontent.com/SoufianoDev/cage-xtmapper/migrate-to-c-golang-no-submodule/install.sh | sudo bash -s -- --version vX.Y.Z
```

The bootstrap downloads the prebuilt helper and release archive for Linux amd64 or arm64, then verifies both SHA-256 checksums before installation. The automated installer still uses the project's versioned `.tar` archive. The tag-triggered release workflow also builds experimental native `.deb`, `.rpm`, Arch and Alpine candidates for draft-release review, with a checksummed asset index. These packages are not official `Supported` install paths: CI tests package-manager lifecycle and a `--help` smoke test, but not a real Wayland/graphics session or Waydroid runtime. Alpine APKs use a disposable CI signing key, not a production publisher key. The release workflow has not yet been exercised by a version-tag push. Runtime shared-library packages can currently be installed automatically through APT on Debian/Ubuntu; other distributions receive a clear missing-library error rather than guessed package names. Waydroid and the XtMapper Android APK remain prerequisites.

The installer stores managed releases under `/opt/cage-xtmapper` and creates `/usr/local/bin/cage-xtmapper`. Updates switch releases atomically and remove only files recorded as installer-owned. A recognized old `cage_xtmapper.sh` launcher and its paired valid ELF are moved to uniquely named backups only after activation. Unknown files are preserved and may block installation until inspected. SHA-256 checksums detect corruption but do not authenticate the publisher.

The same no-argument command can be run again safely. When the installed release exactly matches the newest selected archive, the installer reports `already up to date` instead of staging it again. Recorded incomplete transactions are recovered automatically when their journal and object identities are sufficient; if ownership cannot be verified, the installer stops and preserves conflicting data. Explicit `status`, `recover`, and `uninstall` operations remain available for diagnostics and maintenance.

Developers can test automatic install, update, same-version no-op, status, and uninstall without publishing a GitHub Release:

```bash
bash tests/test-installer-local.sh
```

See [Installation And Recovery](docs/installation.md) for the ownership guarantees, legacy migration, recovery behavior, and local test options.

## Run

```bash
cage-xtmapper
```

Set a window size and a 60 Hz refresh rate:

```bash
cage-xtmapper --window-width 1280 --window-height 720 --refresh-mhz 60000
```

| Option | Behavior |
|---|---|
| `--window-width PIXELS` | Set a positive output width. |
| `--window-height PIXELS` | Set a positive output height. |
| `--refresh-mhz MHZ` | Set the refresh rate in millihertz; `60000` means 60 Hz. |
| `--window-show-title-bar` | Show window decorations. |
| `--print-output-info` | Print the parent Wayland output's modes to stderr and exit. |
| `--adb` | Use ADB for Android shell commands; Waydroid still launches the UI. |
| `--xtmapper-args ARG...` | Append all remaining arguments to XtMapper's default `--wayland-client` argument. |
| `-h`, `--help` | Show help. |

Press **F10** to switch mouse handling between XtMapper and Waydroid. The Cage fork's [`togglekey.h`](native-src/cage/togglekey.h) defines this key.

## Common Setup Issues

### Steam Gaming Mode

In desktop mode, add your terminal application to Steam. Open it from the gaming session and run `cage-xtmapper`. The launcher uses `GAMESCOPE_WAYLAND_DISPLAY` when the session provides it.

### Invisible Cursor

Enable Waydroid's subsurface cursor:

```bash
waydroid prop set persist.waydroid.cursor_on_subsurface true
```

### Mouse Movement Conflicts

Waydroid's fake-touch setting can interfere with mouse movement. Clear it if enabled:

```bash
waydroid prop set persist.waydroid.fake_touch ""
```

### Boot Timeout

The launcher waits up to 120 seconds for Android by default. To allow longer:

```bash
XTMAPPER_BOOT_WAIT_SECONDS=180 cage-xtmapper
```

More XtMapper guides cover [refresh rate](https://xtr126.github.io/XtMapper-docs/waydroid/2-fps/), [touchpad input](https://xtr126.github.io/XtMapper-docs/waydroid/3-touchpad/), [window customization](https://xtr126.github.io/XtMapper-docs/waydroid/4-window-customization/), [fullscreen](https://xtr126.github.io/XtMapper-docs/waydroid/5-fullscreen/), and [logcat output](https://xtr126.github.io/XtMapper-docs/waydroid/6-disable-logging/).

## For Developers

[BUILDING.md](BUILDING.md) covers dependencies, the pinned native sources, debug builds, local packaging, and build artifacts. [CONTRIBUTING.md](CONTRIBUTING.md) lists the required checks and fork workflow.

The Go launcher re-executes `/proc/self/exe` for its private compositor role. Cage and wlroots link into that same ELF. A kernel pipe carries events directly from compositor stdout to the Android transport; diagnostics use stderr.

The checked-in native sources under `native-src/` are authoritative. `build/source/` is generated staging that `build.sh` reconstructs on each run; do not edit it as a source of record. Change `native-src/` and rebuild instead.

`XTMAPPER_BOOT_WAIT_SECONDS` accepts positive decimal seconds, up to `2^31`; an unset or empty value means 120. The window options set `XTMAPPER_WIDTH`, `XTMAPPER_HEIGHT`, and `XTMAPPER_REFRESH`. Window decorations default to disabled through `WLR_NO_DECORATION=1`.

## License

The project uses the [MIT License](LICENSE). Bundled native code and the Go runtime retain their own notices in each archive's `LICENSES/` directory. The manifest lists the licenses for the components actually linked, including any selected system static libraries. See [CONTRIBUTORS.md](CONTRIBUTORS.md) for credits and provenance.
