package launcher

import (
	"fmt"
	"strings"
)

// shellQuote wraps a value in single quotes, escaping embedded
// quotes, exactly like the shell launcher did.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// PackageProbe returns one APK-lookup command pair made of distinct
// building blocks. The first is the debug build (preferred), the
// second the release build.
var PackageProbeOrder = []string{"xtr.keymapper.debug", "xtr.keymapper"}

// parsePmPath extracts the first `package:<path>` line from a
// `pm path` probe, CR-stripped.
func parsePmPath(out string) string {
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if p, ok := strings.CutPrefix(line, "package:"); ok {
			return p
		}
	}
	return ""
}

// deriveNativeLibPath mirrors APK_DIR/lib/$ABI with
// APK_DIR=${APK_PATH%/*}: the text before the last slash (the whole
// value when it contains no slash).
func deriveNativeLibPath(apkPath, abi string) string {
	dir := apkPath
	if i := strings.LastIndex(apkPath, "/"); i >= 0 {
		dir = apkPath[:i]
	}
	return dir + "/lib/" + abi
}

// buildRemoteCommand assembles the Android remote-shell command that
// starts Xtmapper's RemoteServiceShell with the APP arguments. The
// adb path invokes the shell directly; the Waydroid path chains env
// BOOTCLASSPATH=$BOOTCLASSPATH:<list> first.
func buildRemoteCommand(useADB bool, nativeLibPath, apkPath string, args []string) string {
	return buildRemoteCommandWithExecutable(useADB, nativeLibPath, apkPath, args, "/system/bin/app_process")
}

// buildRemoteCommandWithExecutable shares the production quoting logic while
// allowing a local fixture executable to exercise the remote shell round trip.
func buildRemoteCommandWithExecutable(useADB bool, nativeLibPath, apkPath string, args []string, executable string) string {
	var b strings.Builder
	if useADB {
		b.WriteString("exec ")
		b.WriteString(shellQuote(executable))
	} else {
		b.WriteString("exec env BOOTCLASSPATH=$BOOTCLASSPATH:")
		b.WriteString(shellQuote(bootClasspath))
		b.WriteByte(' ')
		b.WriteString(shellQuote(executable))
	}
	fmt.Fprintf(&b, " -Djava.library.path=%s -Djava.class.path=%s / xtr.keymapper.server.RemoteServiceShell",
		shellQuote(nativeLibPath), shellQuote(apkPath))
	for _, arg := range args {
		b.WriteByte(' ')
		b.WriteString(shellQuote(arg))
	}
	return b.String()
}
