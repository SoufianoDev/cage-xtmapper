// Package launcher implements the cage-xtmapper CLI and runtime
// orchestration: Waydroid/ADB discovery, the Android boot wait, the
// XtMapper launch pipeline, signal handling, cleanup and exit status.
package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Tools names the external executables the launcher may invoke; tests
// override the paths with mocks.
type Tools struct {
	SelfExe  string // this ELF, for the embedded compositor dispatch
	Sudo     string
	Waydroid string
	ADB      string
}

// Config is the parsed CLI configuration.
type Config struct {
	WindowWidth     string // XTMAPPER_WIDTH, "" for default
	WindowHeight    string // XTMAPPER_HEIGHT, "" for default
	RefreshMHz      string // XTMAPPER_REFRESH, "" for default
	TitleBar        bool   // --window-show-title-bar (WLR_NO_DECORATION=0)
	PrintOutputInfo bool   // --print-output-info (stops parsing, as before)
	UseADB          bool   // --adb
	XtmapperArgs    []string

	Tools       Tools
	RuntimeLock *os.File
}

const bootClasspath = "/apex/com.android.art/javalib/core-oj.jar:" +
	"/apex/com.android.art/javalib/core-libart.jar:" +
	"/apex/com.android.art/javalib/okhttp.jar:" +
	"/apex/com.android.art/javalib/bouncycastle.jar:" +
	"/apex/com.android.art/javalib/apache-xml.jar:" +
	"/system/framework/framework.jar:/system/framework/framework-graphics.jar:" +
	"/system/framework/ext.jar:/system/framework/telephony-common.jar:" +
	"/system/framework/voip-common.jar:/system/framework/ims-common.jar:" +
	"/apex/com.android.i18n/javalib/core-icu4j.jar:" +
	"/apex/com.android.adservices/javalib/framework-adservices.jar:" +
	"/apex/com.android.adservices/javalib/framework-sdksandbox.jar:" +
	"/apex/com.android.btservices/javalib/framework-bluetooth.jar:" +
	"/apex/com.android.conscrypt/javalib/conscrypt.jar:" +
	"/apex/com.android.ipsec/javalib/android.net.ipsec.ike.jar:" +
	"/apex/com.android.media/javalib/updatable-media.jar:" +
	"/apex/com.android.mediaprovider/javalib/framework-mediaprovider.jar:" +
	"/apex/com.android.ondevicepersonalization/javalib/framework-ondevicepersonalization.jar:" +
	"/apex/com.android.os.statsd/javalib/framework-statsd.jar:" +
	"/apex/com.android.permission/javalib/framework-permission.jar:" +
	"/apex/com.android.permission/javalib/framework-permission-s.jar:" +
	"/apex/com.android.scheduling/javalib/framework-scheduling.jar:" +
	"/apex/com.android.sdkext/javalib/framework-sdkextensions.jar:" +
	"/apex/com.android.tethering/javalib/framework-connectivity.jar:" +
	"/apex/com.android.tethering/javalib/framework-connectivity-t.jar:" +
	"/apex/com.android.uwb/javalib/framework-uwb.jar:" +
	"/apex/com.android.wifi/javalib/framework-wifi.jar"

// BootWaitSeconds is the Android boot wait deadline in seconds
// (XTMAPPER_BOOT_WAIT_SECONDS, default 120).
func BootWaitSeconds() int64 {
	if n, ok := parseBootWait(os.Getenv("XTMAPPER_BOOT_WAIT_SECONDS")); ok {
		return n
	}
	return -1
}

// parseOptionInteger mirrors the shell CLI validator: digits only,
// value > 0, leading zeros allowed ("010" == 10). The shell accepts
// any positive value up to the 64-bit arithmetic maximum, so there is
// no upper bound here either.
func parseOptionInteger(v string) (int64, bool) {
	if v == "" {
		return -1, false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return -1, false
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return -1, false
	}
	return n, true
}

// parseBootWait mirrors XTMAPPER_BOOT_WAIT_SECONDS: ^[1-9][0-9]*$.
func parseBootWait(v string) (int64, bool) {
	if v == "" {
		return 120, true
	}
	if v[0] < '1' || v[0] > '9' {
		return -1, false
	}
	for i := 1; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return -1, false
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	// shortcut: boot waits cap at 2^31 seconds; widen with overflow checks if ever needed.
	if err != nil || n > 1<<31 {
		return -1, false
	}
	return n, true
}

// CliError is a CLI validation failure. ReportUsage tells the main
// entry whether the usage text accompanies the error line (the shell
// printed usage only for unknown options).
type CliError struct {
	msg       string
	showUsage bool
}

func (e *CliError) Error() string { return e.msg }

// ReportUsage reports whether the usage text should be printed.
func (e *CliError) ReportUsage() bool { return e.showUsage }

func cliErr(msg string, showUsage bool) error {
	return &CliError{msg: msg, showUsage: showUsage}
}

const usageText = `Usage: cage-xtmapper [options]

Options:
  --window-width PIXELS       Set output width (positive integer)
  --window-height PIXELS      Set output height (positive integer)
  --refresh-mhz MHZ           Set output refresh rate (positive integer)
  --print-output-info         Print output information and exit
  --window-show-title-bar      Show the window title bar
  --adb                        Use adb instead of Waydroid's sudo shell
  --xtmapper-args ARG...       Pass remaining arguments to XtMapper
  -h, --help                   Show this help
`

// Usage prints the CLI help text.
func Usage() {
	fmt.Print(usageText)
}

// UsageToStderr prints the CLI help text next to an error message.
func UsageToStderr() {
	fmt.Fprint(os.Stderr, usageText)
}

// RequireTool resolves a tool path, honoring an explicit override.
func findTool(override, name string) (string, bool) {
	if override != "" {
		return override, true
	}
	p, err := exec.LookPath(name)
	return p, err == nil
}

// Parse validates the arguments and returns the configuration. It
// never launches anything. helpRequested distinguishes -h/--help from
// validation errors (exit 0 vs exit 2).
func Parse(argv []string, tools Tools) (cfg Config, help bool, err error) {
	cfg = Config{Tools: tools}
	cfg.XtmapperArgs = []string{"--wayland-client"}
	i := 0
	for ; i < len(argv); i++ {
		arg := argv[i]
		switch arg {
		case "--window-width", "--window-height", "--refresh-mhz":
			name := arg
			i++
			if i == len(argv) || strings.HasPrefix(argv[i], "--") {
				return cfg, false, cliErr(name+" requires a value", false)
			}
			if _, ok := parseOptionInteger(argv[i]); !ok {
				return cfg, false, cliErr(name+" must be a positive integer", false)
			}
			switch name {
			case "--window-width":
				cfg.WindowWidth = argv[i]
			case "--window-height":
				cfg.WindowHeight = argv[i]
			case "--refresh-mhz":
				cfg.RefreshMHz = argv[i]
			}
		case "--window-show-title-bar":
			cfg.TitleBar = true
		case "--adb":
			cfg.UseADB = true
		case "--print-output-info":
			// The shell ran this option at its position and exited
			// without looking at later arguments. -h works the same
			// way: both stop parsing immediately.
			cfg.PrintOutputInfo = true
			return cfg, false, nil
		case "--xtmapper-args":
			i++
			cfg.XtmapperArgs = append(cfg.XtmapperArgs, argv[i:]...)
			i = len(argv)
		case "-h", "--help":
			return cfg, true, nil
		default:
			return cfg, false, cliErr("invalid argument: "+arg, true)
		}
	}
	return cfg, false, nil
}
