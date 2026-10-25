// Package launcher tests exercise the launcher orchestration against
// mock sudo/waydroid/adb/compositor executables, mirroring the legacy
// tests/test-launcher.sh suite: remote quoting, the boot-wait
// deadline, exit-status precedence, the event-stream relay, and
// signal-initiated cleanup.
package launcher_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Xtr126/cage-xtmapper/internal/launcher"
)

var sudoMock = `#!/usr/bin/env bash
printf 'sudo %s\n' "$*" >>"$MOCK_LOG/sudo.log"
if [[ ${1:-} == -n ]]; then shift; fi
exec "$@"
`

var waydroidMock = `#!/usr/bin/env bash
printf 'waydroid %s\n' "$*" >>"$MOCK_LOG/waydroid.log"
case "$1 $2" in
    'session stop') exit "${MOCK_STOP_STATUS:-0}" ;;
    'container start') exit "${MOCK_CONTAINER_STATUS:-0}" ;;
    'shell getprop')
        case $3 in
            sys.boot_completed) printf '%s\r\n' "${MOCK_BOOT_STATUS:-1}" ;;
            ro.product.cpu.abi) printf 'x86_64\r\n' ;;
        esac
        ;;
    'shell pm')
        [[ $4 == xtr.keymapper ]] && printf 'package:/data/app/xt mapper/base.apk\r\n'
        ;;
    'shell --')
        printf '%s\n' "$5" >"$MOCK_LOG/remote-command"
        cat >"$MOCK_LOG/remote-stdin"
        exit "${MOCK_REMOTE_STATUS:-0}" ;;
    *) exit 1 ;;
esac
`

var adbMock = `#!/usr/bin/env bash
printf 'adb %s\n' "$*" >>"$MOCK_LOG/adb.log"
if [[ $1 == shell && $2 == getprop ]]; then
    [[ $3 == sys.boot_completed ]] && printf '%s\r\n' "${MOCK_BOOT_STATUS:-1}" || printf 'x86_64\r\n'
elif [[ $1 == shell && $2 == pm ]]; then
    [[ $4 == xtr.keymapper ]] && printf 'package:/data/app/xt mapper/base.apk\r\n'
elif [[ $1 == shell ]]; then
    printf '%s\n' "$2" >"$MOCK_LOG/remote-command"
    cat >"$MOCK_LOG/remote-stdin"
    exit "${MOCK_REMOTE_STATUS:-0}"
else
    exit 1
fi
`

var cageMock = `#!/usr/bin/env bash
printf '%s\n' "$$" >"$MOCK_LOG/cage.pid"
printf '%s\n' "$*" >>"$MOCK_LOG/cage.log"
if [[ -n ${MOCK_PAYLOAD:-} ]]; then
    cat "$MOCK_PAYLOAD"
fi
printf 'ui stream\n'
if [[ ${MOCK_CAGE_LINGER:-0} -gt 0 ]]; then
    exec sleep "$MOCK_CAGE_LINGER"
fi
exit "${MOCK_CAGE_STATUS:-0}"
`

// testEnv builds the mock directory and the Tools configuration for
// one launcher run.
func testEnv(t *testing.T) (launcher.Tools, string) {
	t.Helper()
	dir := t.TempDir()
	// honor an override so a failing run can be inspected afterwards
	if outside := os.Getenv("TEST_MOCK_LOG"); outside != "" {
		dir = outside
	}
	logs := filepath.Join(dir, "log")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"sudo":     sudoMock,
		"waydroid": waydroidMock,
		"adb":      adbMock,
		"cage":     cageMock,
	} {
		p := filepath.Join(bin, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MOCK_LOG", logs)
	return launcher.Tools{
		SelfExe:  filepath.Join(bin, "cage"),
		Sudo:     filepath.Join(bin, "sudo"),
		Waydroid: filepath.Join(bin, "waydroid"),
		ADB:      filepath.Join(bin, "adb"),
	}, logs
}

// runLauncher captures stderr and runs one full launcher flow.
func runLauncher(t *testing.T, cfg launcher.Config) (int, string) {
	t.Helper()
	fail := filepath.Join(t.TempDir(), "stderr")
	f, err := os.Create(fail)
	if err != nil {
		t.Fatal(err)
	}
	oldStderr := os.Stderr
	os.Stderr = f
	defer func() {
		os.Stderr = oldStderr
		f.Close()
	}()
	start := time.Now()
	code := launcher.Run(cfg)
	t.Logf("Run took %s", time.Since(start))
	f.Sync()
	out, err := os.ReadFile(fail)
	if err != nil {
		t.Fatal(err)
	}
	return code, string(out)
}

// waitRemote polls until the remote command was launched (file
// exists) or the deadline passes.
func waitRemote(t *testing.T, logs string) string {
	t.Helper()
	cmdFile := filepath.Join(logs, "remote-command")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(cmdFile); err == nil {
			return string(data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("remote service was not launched in time")
	return ""
}

func TestApplyEnvironmentParity(t *testing.T) {
	t.Setenv("DISPLAY", ":0")
	t.Setenv("GAMESCOPE_WAYLAND_DISPLAY", "gs.sock")
	launcher.ApplyEnvironment(&launcher.Config{TitleBar: true, WindowWidth: "800"})
	if got := os.Getenv("WAYLAND_DISPLAY"); got != "gs.sock" {
		t.Fatalf("WAYLAND_DISPLAY = %q, want gs.sock", got)
	}
	if os.Getenv("DISPLAY") != "" {
		t.Fatal("DISPLAY was not unset for the gamescope session")
	}
	if got := os.Getenv("WLR_NO_DECORATION"); got != "0" {
		t.Fatalf("WLR_NO_DECORATION = %q, want 0", got)
	}
	if got := os.Getenv("XTMAPPER_WIDTH"); got != "800" {
		t.Fatalf("XTMAPPER_WIDTH = %q, want 800", got)
	}
	launcher.ApplyEnvironment(&launcher.Config{})
	if got := os.Getenv("WLR_NO_DECORATION"); got != "1" {
		t.Fatalf("default WLR_NO_DECORATION = %q, want 1", got)
	}
}

func TestAdbRemoteQuoting(t *testing.T) {
	tools, logs := testEnv(t)
	t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "1")
	cfg := launcher.Config{UseADB: true, Tools: tools}
	cfg.XtmapperArgs = []string{"--wayland-client", "arg with spaces", "quote'and;touch /tmp/xt-injected"}
	code, stderr := runLauncher(t, cfg)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	remote := waitRemote(t, logs)
	if !strings.Contains(remote, `'arg with spaces'`) {
		t.Fatalf("remote argument with spaces was split: %s", remote)
	}
	if !strings.Contains(remote, `quote'\''and;touch /tmp/xt-injected`) {
		t.Fatalf("remote shell metacharacters were not quoted: %s", remote)
	}
	if _, err := os.Stat("/tmp/xt-injected"); err == nil {
		os.Remove("/tmp/xt-injected")
		t.Fatal("remote argument caused local shell injection")
	}
	if !strings.Contains(remote, `-Djava.library.path='/data/app/xt mapper/lib/x86_64'`) {
		t.Fatalf("native lib path with a space was not quoted: %s", remote)
	}
	if strings.Contains(remote, "BOOTCLASSPATH") {
		t.Fatalf("the adb path must not carry the Waydroid BOOTCLASSPATH chain: %s", remote)
	}
	data, err := os.ReadFile(filepath.Join(logs, "remote-stdin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ui stream\n" {
		t.Fatalf("launcher output was not streamed to remote stdin: %q", data)
	}
}

func TestWaydroidRemoteQuoting(t *testing.T) {
	tools, logs := testEnv(t)
	t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "1")
	cfg := launcher.Config{Tools: tools}
	cfg.XtmapperArgs = append(cfg.XtmapperArgs, "waydroid arg")
	code, stderr := runLauncher(t, cfg)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	remote := waitRemote(t, logs)
	if !strings.Contains(remote, `'waydroid arg'`) {
		t.Fatalf("Waydroid remote argument was not quoted: %s", remote)
	}
	if idx := strings.Index(remote, "BOOTCLASSPATH=$BOOTCLASSPATH"); idx < 0 {
		t.Fatalf("Waydroid remote BOOTCLASSPATH is missing: %s", remote)
	}
	data, err := os.ReadFile(filepath.Join(logs, "remote-stdin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ui stream\n" {
		t.Fatalf("Waydroid input stream was not preserved: %q", data)
	}
	// `waydroid session stop` never runs under sudo.
	waydroidLog, err := os.ReadFile(filepath.Join(logs, "waydroid.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(waydroidLog), "session stop") {
		t.Fatalf("waydroid session stop was not invoked: %s", waydroidLog)
	}
	sudoLog, _ := os.ReadFile(filepath.Join(logs, "sudo.log"))
	if strings.Contains(string(sudoLog), "session stop") {
		t.Fatalf("waydroid session stop ran under sudo: %s", sudoLog)
	}
}

func TestBootTimeout(t *testing.T) {
	tools, logs := testEnv(t)
	t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "2")
	t.Setenv("MOCK_BOOT_STATUS", "10")
	code, stderr := runLauncher(t, launcher.Config{UseADB: true, Tools: tools})
	if code != 1 {
		t.Fatalf("boot timeout exit %d, want 1 (stderr %s)", code, stderr)
	}
	if !strings.Contains(stderr, "did not finish booting within 2 seconds") {
		t.Fatalf("boot timeout error was not reported: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(logs, "remote-command")); err == nil {
		t.Fatal("remote service launched before Android boot completed")
	}
}

func TestCageFailureSurfaces(t *testing.T) {
	tools, logs := testEnv(t)
	t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "1")
	t.Setenv("MOCK_CAGE_STATUS", "9")
	code, stderr := runLauncher(t, launcher.Config{UseADB: true, Tools: tools})
	if code != 9 {
		t.Fatalf("compositor failure surfaced as %d, want 9 (stderr %s)", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(logs, "remote-command")); err == nil {
		t.Fatal("remote service launched after the compositor failed")
	}
}

func TestMissingToolReports127(t *testing.T) {
	tools, _ := testEnv(t)
	t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "1")
	t.Setenv("PATH", t.TempDir()) // no sudo, waydroid or adb
	cfg := launcher.Config{Tools: tools}
	cfg.Tools.Sudo = ""
	cfg.Tools.Waydroid = ""
	code, stderr := runLauncher(t, cfg)
	if code != 127 {
		t.Fatalf("missing sudo exit %d, want 127 (stderr %s)", code, stderr)
	}
	if !strings.Contains(stderr, "Error: sudo was not found in PATH") {
		t.Fatalf("missing sudo error not reported: %s", stderr)
	}
}

func TestStopSessionFailure(t *testing.T) {
	tools, _ := testEnv(t)
	t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "1")
	t.Setenv("MOCK_STOP_STATUS", "3")
	code, stderr := runLauncher(t, launcher.Config{Tools: tools})
	if code != 1 {
		t.Fatalf("session stop failure exit %d, want 1 (stderr %s)", code, stderr)
	}
	if !strings.Contains(stderr, "could not stop the Waydroid session") {
		t.Fatalf("session stop failure not reported: %s", stderr)
	}
}

func TestContainerStartFailure(t *testing.T) {
	tools, _ := testEnv(t)
	t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "1")
	t.Setenv("MOCK_CONTAINER_STATUS", "7")
	code, stderr := runLauncher(t, launcher.Config{Tools: tools})
	if code != 1 {
		t.Fatalf("container start failure exit %d, want 1 (stderr %s)", code, stderr)
	}
	if !strings.Contains(stderr, "could not start the Waydroid container") {
		t.Fatalf("container start failure not reported: %s", stderr)
	}
}

// TestRemoteFailureSurfaces covers the pipefail contract: nonzero
// remote status wins over a successful compositor.
func TestRemoteFailureSurfaces(t *testing.T) {
	tools, _ := testEnv(t)
	t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "1")
	t.Setenv("MOCK_REMOTE_STATUS", "5")
	code, stderr := runLauncher(t, launcher.Config{UseADB: true, Tools: tools})
	if code != 5 {
		t.Fatalf("remote failure surfaced as %d, want 5 (stderr %s)", code, stderr)
	}
}

// TestEventStreamBinaryPreserved relays a multi-megabyte payload (larger than typical Linux pipe capacity,
// containing NUL bytes) through the
// compositor -> remote stdin relay while the remote side starts
// reading late: byte-identical, no deadlock, no launcher buffering.
func TestEventStreamBinaryPreserved(t *testing.T) {
	tools, logs := testEnv(t)
	t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "1")

	payloadDir := t.TempDir()
	payloadFile := filepath.Join(payloadDir, "payload")
	payload := make([]byte, 0, 2<<20)
	for i := 0; i < cap(payload); i++ {
		payload = append(payload, byte(i*7+i/13))
	}
	if err := os.WriteFile(payloadFile, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOCK_PAYLOAD", payloadFile)

	// make the adb sleep before consuming stdin: the compositor
	// fills the 64KiB kernel pipe buffer and must block until the
	// reader appears
	adbBody := strings.Replace(adbMock, `cat >"$MOCK_LOG/remote-stdin"`, `sleep 0.5; cat >"$MOCK_LOG/remote-stdin"`, 1)
	if err := os.WriteFile(tools.ADB, []byte(adbBody), 0o755); err != nil {
		t.Fatal(err)
	}

	code, stderr := runLauncher(t, launcher.Config{UseADB: true, Tools: tools})
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	relayed, err := os.ReadFile(filepath.Join(logs, "remote-stdin"))
	if err != nil {
		t.Fatalf("remote did not receive the stream: %v", err)
	}
	// trailing "ui stream\n" comes after the binary payload
	if len(relayed) != len(payload)+len("ui stream\n") {
		t.Fatalf("relayed %d bytes, want the payload plus the final record", len(relayed))
	}
	if string(relayed[:len(payload)]) != string(payload) {
		t.Fatal("event stream bytes were corrupted in transit")
	}
	if string(relayed[len(payload):]) != "ui stream\n" {
		t.Fatal("final event record was altered")
	}
}

// TestSignalTerminatedRun covers signal-owned cleanup: a SIGINT /
// SIGTERM arriving during the boot wait terminates the owned process
// groups and reports 130 / 143.
func TestSignalTerminatedRun(t *testing.T) {
	for _, tc := range []struct {
		name string
		sig  syscall.Signal
		want int
	}{
		{"sigint", syscall.SIGINT, 130},
		{"sigterm", syscall.SIGTERM, 143},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools, logs := testEnv(t)
			t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "30")
			t.Setenv("MOCK_BOOT_STATUS", "10")  // setup stays in the boot loop
			t.Setenv("MOCK_CAGE_LINGER", "600") // compositor stays alive

			cfg := launcher.Config{Tools: tools}
			result := make(chan int, 1)
			go func() { result <- launcher.Run(cfg) }()

			// signal.Notify is installed moments after the
			// compositor appears; wait for the log then allow the
			// install window to elapse
			cageLog := filepath.Join(logs, "cage.log")
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(cageLog); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("compositor was not started in time")
				}
				time.Sleep(20 * time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond)
			if err := syscall.Kill(syscall.Getpid(), tc.sig); err != nil {
				t.Fatal(err)
			}
			select {
			case code := <-result:
				if code != tc.want {
					t.Fatalf("signal-terminated run reported %d, want %d", code, tc.want)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("signal-terminated run did not return")
			}
		})
	}
}
