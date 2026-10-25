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

Use a release archive in the [managed release format](docs/installation.md#release-format), together with a matching source checkout. The source-only installer requires Bash, Go 1.24 or newer, and Linux. Installation is system-wide.

From the source checkout, install your downloaded or locally built archive:

```bash
sudo ./install.sh install --archive /path/to/cage-xtmapper-VERSION.tar
./install.sh status
```

The installer stores releases under `/opt/cage-xtmapper` and creates `/usr/local/bin/cage-xtmapper`. It checks the archive without running the downloaded application as root. Obtain archives from a trusted source; SHA-256 checksums verify integrity, not publisher authenticity.

To replace an existing managed installation, close Cage XtMapper and run:

```bash
sudo ./install.sh update --archive /path/to/cage-xtmapper-NEW-VERSION.tar
```

An active application makes conflicting installation operations fail with a busy error. Successful updates remove the obsolete managed release. If an operation is interrupted, inspect and recover it:

```bash
./install.sh status
sudo ./install.sh recover
```

Existing `cage_xtmapper` or `cage_xtmapper.sh` files cause a migration conflict because the old manual installation procedure did not record ownership. Inspect those files and move them explicitly after establishing what owns them, then retry. Old archives containing `usr/local/bin/` use the older branch's installer instructions.

See [Installation And Recovery](docs/installation.md) for the commit point, ownership rules, error codes, test paths, and uninstall behavior.

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

Press **F10** to switch mouse handling between XtMapper and Waydroid. The Cage fork's [`togglekey.h`](forks/cage/togglekey.h) defines this key.

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

`XTMAPPER_BOOT_WAIT_SECONDS` accepts positive decimal seconds, up to `2^31`; an unset or empty value means 120. The window options set `XTMAPPER_WIDTH`, `XTMAPPER_HEIGHT`, and `XTMAPPER_REFRESH`. Window decorations default to disabled through `WLR_NO_DECORATION=1`.

## License

The project uses the [MIT License](LICENSE). Bundled native code and the Go runtime retain their own notices in each archive's `LICENSES/` directory. The manifest lists the licenses for the components actually linked, including any selected system static libraries. See [CONTRIBUTORS.md](CONTRIBUTORS.md) for credits and provenance.
