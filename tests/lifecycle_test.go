package launcher_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Xtr126/cage-xtmapper/internal/launcher"
)

func waitPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process did not become ready: %s", path)
	return 0
}

func assertStopped(t *testing.T, pid int) {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if os.IsNotExist(err) {
		return
	}
	// Orphaned grandchildren may await reaping by init after they have been killed.
	if err == nil && strings.HasPrefix(string(data)[strings.LastIndex(string(data), ")")+1:], " Z ") {
		return
	}
	t.Fatalf("process %d survived launcher cleanup: %s (%v)", pid, data, err)
}

func TestStartupSignalsCleanChildren(t *testing.T) {
	for _, phase := range []string{"stop", "container", "probe"} {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
			t.Run(fmt.Sprintf("%s/%s", phase, sig), func(t *testing.T) {
				tools, logs := testEnv(t)
				t.Setenv("MOCK_PHASE", phase)
				t.Setenv("MOCK_CAGE_LINGER", "600")
				body := `#!/bin/sh
case "$1 $2" in
    'session stop') [ "$MOCK_PHASE" = stop ] || exit 0 ;;
    'container start') [ "$MOCK_PHASE" = container ] || exit 0 ;;
    'shell getprop') ;;
    *) exit 1 ;;
esac
printf '%s\n' "$$" > "$MOCK_LOG/blocked.pid"
exec sleep 600
`
				if err := os.WriteFile(tools.Waydroid, []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
				done := make(chan int, 1)
				go func() { done <- launcher.Run(launcher.Config{Tools: tools}) }()
				pid := waitPID(t, filepath.Join(logs, "blocked.pid"))
				if err := syscall.Kill(syscall.Getpid(), sig); err != nil {
					t.Fatal(err)
				}
				select {
				case code := <-done:
					if code != 128+int(sig) {
						t.Fatalf("exit %d, want %d", code, 128+int(sig))
					}
				case <-time.After(9 * time.Second):
					t.Fatal("signal did not stop startup within the cleanup budget")
				}
				assertStopped(t, pid)
				if phase == "probe" {
					assertStopped(t, waitPID(t, filepath.Join(logs, "cage.pid")))
				}
			})
		}
	}
}

func TestSetupFailureCleansCompositor(t *testing.T) {
	tools, logs := testEnv(t)
	t.Setenv("MOCK_BOOT_STATUS", "0")
	t.Setenv("MOCK_CAGE_LINGER", "600")
	t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "1")
	code, stderr := runLauncher(t, launcher.Config{UseADB: true, Tools: tools})
	if code != 1 || !strings.Contains(stderr, "did not finish booting") {
		t.Fatalf("exit %d, stderr %s", code, stderr)
	}
	assertStopped(t, waitPID(t, filepath.Join(logs, "cage.pid")))
}

func TestCompositorFailureCancelsProbe(t *testing.T) {
	tools, logs := testEnv(t)
	for path, body := range map[string]string{
		tools.SelfExe: `#!/bin/sh
while [ ! -s "$MOCK_LOG/probe.pid" ]; do sleep 0.01; done
exit 9
`,
		tools.ADB: `#!/bin/sh
printf '%s\n' "$$" > "$MOCK_LOG/probe.pid"
exec sleep 600
`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	code, stderr := runLauncher(t, launcher.Config{UseADB: true, Tools: tools})
	if code != 9 || time.Since(start) > 3*time.Second {
		t.Fatalf("compositor failure took %s, exit %d, stderr %s", time.Since(start), code, stderr)
	}
	assertStopped(t, waitPID(t, filepath.Join(logs, "probe.pid")))
}

func TestPipefailRightmostFailure(t *testing.T) {
	tools, _ := testEnv(t)
	t.Setenv("MOCK_REMOTE_STATUS", "5")
	body := `#!/bin/sh
printf 'event\n'
while [ ! -s "$MOCK_LOG/remote-command" ]; do sleep 0.01; done
exit 9
`
	if err := os.WriteFile(tools.SelfExe, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	code, stderr := runLauncher(t, launcher.Config{UseADB: true, Tools: tools})
	if code != 5 {
		t.Fatalf("both sides failed: exit %d, want rightmost status 5; stderr %s", code, stderr)
	}
}

func TestCleanupKillsRemainingDescendants(t *testing.T) {
	tools, logs := testEnv(t)
	t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "1")
	t.Setenv("MOCK_BOOT_STATUS", "0")
	body := `#!/bin/sh
sh -c 'trap "" TERM; printf "%s\n" "$$" > "$MOCK_LOG/descendant.pid"; exec sleep 600' &
exit 0
`
	if err := os.WriteFile(tools.SelfExe, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	code, stderr := runLauncher(t, launcher.Config{UseADB: true, Tools: tools})
	if code != 1 || time.Since(start) > 9*time.Second {
		t.Fatalf("cleanup took %s, exit %d, stderr %s", time.Since(start), code, stderr)
	}
	assertStopped(t, waitPID(t, filepath.Join(logs, "descendant.pid")))
}

func TestFailedBootProbeOutputIsDiscarded(t *testing.T) {
	tools, logs := testEnv(t)
	t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "1")
	if err := os.WriteFile(tools.ADB, []byte("#!/bin/sh\nprintf '1\\r\\n'\nexit 4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, stderr := runLauncher(t, launcher.Config{UseADB: true, Tools: tools})
	if code != 1 || !strings.Contains(stderr, "did not finish booting") {
		t.Fatalf("failed boot probe was accepted: exit %d, stderr %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(logs, "remote-command")); !os.IsNotExist(err) {
		t.Fatal("remote service started after a failed boot probe")
	}
}

// TestRuntimePeerExitCancelsOtherSide prevents either pipeline child from
// keeping the launcher alive after its peer has failed.
func TestRuntimePeerExitCancelsOtherSide(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       string
		compositor string
		wantCode   int
	}{
		{
			name: "compositor exits first",
			mode: "compositor",
			compositor: `#!/bin/sh
printf '%s\n' "$$" > "$MOCK_LOG/cage.pid"
while [ ! -s "$MOCK_LOG/remote-command" ]; do sleep 0.01; done
exit 9
`,
			wantCode: 9,
		},
		{
			name: "remote exits first",
			mode: "remote",
			compositor: `#!/bin/sh
printf '%s\n' "$$" > "$MOCK_LOG/cage.pid"
while [ ! -s "$MOCK_LOG/remote-command" ]; do sleep 0.01; done
exec sleep 600
`,
			wantCode: 7,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools, logs := testEnv(t)
			if err := os.WriteFile(tools.SelfExe, []byte(tc.compositor), 0o755); err != nil {
				t.Fatal(err)
			}
			waydroid := `#!/bin/sh
case "$1 $2" in
    'session stop'|'container start') exit 0 ;;
    'shell getprop')
        [ "$3" = sys.boot_completed ] && printf '1\n' || printf 'x86_64\n'
        ;;
    'shell pm') printf 'package:/data/app/xtmapper/base.apk\n' ;;
    'shell --')
        printf '%s\n' "$5" > "$MOCK_LOG/remote-command"
        printf '%s\n' "$$" > "$MOCK_LOG/remote.pid"
        if [ "$MOCK_PEER_MODE" = remote ]; then exit 7; fi
        exec sleep 600
        ;;
    *) exit 1 ;;
esac
`
			if err := os.WriteFile(tools.Waydroid, []byte(waydroid), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("MOCK_PEER_MODE", tc.mode)
			start := time.Now()
			code, _ := runLauncher(t, launcher.Config{Tools: tools})
			if code != tc.wantCode {
				t.Fatalf("exit %d, want %d", code, tc.wantCode)
			}
			if elapsed := time.Since(start); elapsed > 4*time.Second {
				t.Fatalf("peer exit took too long: %s", elapsed)
			}
			assertStopped(t, waitPID(t, filepath.Join(logs, "cage.pid")))
			assertStopped(t, waitPID(t, filepath.Join(logs, "remote.pid")))
		})
	}
}

// TestTermResistantPeerCannotStallExit prevents a remote peer that ignores
// SIGTERM from extending the bounded first-exit cleanup indefinitely: the
// launcher must still return the initiating compositor status after the
// escalation deadline.
func TestTermResistantPeerCannotStallExit(t *testing.T) {
	tools, logs := testEnv(t)
	compositor := `#!/bin/sh
printf '%s\n' "$$" > "$MOCK_LOG/cage.pid"
while [ ! -s "$MOCK_LOG/remote-command" ]; do sleep 0.01; done
exit 9
`
	if err := os.WriteFile(tools.SelfExe, []byte(compositor), 0o755); err != nil {
		t.Fatal(err)
	}
	waydroid := `#!/bin/sh
case "$1 $2" in
    'session stop'|'container start') exit 0 ;;
    'shell getprop')
        [ "$3" = sys.boot_completed ] && printf '1\n' || printf 'x86_64\n'
        ;;
    'shell pm') printf 'package:/data/app/xtmapper/base.apk\n' ;;
    'shell --')
        printf '%s\n' "$5" > "$MOCK_LOG/remote-command"
        printf '%s\n' "$$" > "$MOCK_LOG/remote.pid"
        trap '' TERM
        sleep 600 &
        wait "$!"
        exit 0
        ;;
    *) exit 1 ;;
esac
`
	if err := os.WriteFile(tools.Waydroid, []byte(waydroid), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	code, _ := runLauncher(t, launcher.Config{Tools: tools})
	if code != 9 {
		t.Fatalf("exit %d, want the initiating compositor status 9", code)
	}
	// The TERM-resistant group must survive past the TERM grace into the
	// SIGKILL escalation instead of ending the wait early or never.
	if elapsed := time.Since(start); elapsed < 4*time.Second || elapsed > 15*time.Second {
		t.Fatalf("resistant peer cleanup took %s, want the bounded escalation window", elapsed)
	}
	assertStopped(t, waitPID(t, filepath.Join(logs, "cage.pid")))
	assertStopped(t, waitPID(t, filepath.Join(logs, "remote.pid")))
}

// TestFailureSurvivesLateSignalDuringCleanup keeps the initiating failure
// status when SIGTERM arrives while cleanup of the failed run is still in
// progress: the late signal must not replace the recorded cause with 143.
func TestFailureSurvivesLateSignalDuringCleanup(t *testing.T) {
	tools, logs := testEnv(t)
	t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "10")
	// The compositor exits 9 once the probe appears. One group member
	// ignores TERM (SIG_IGN traps are inherited; captured ones are not)
	// to keep the group alive through the TERM grace, and a second
	// member records the delivered TERM. Cleanup only runs after
	// supervise recorded the failure, so the marker is the
	// deterministic signal that the shutdown cause is already fixed.
	compositor := `#!/bin/sh
printf '%s\n' "$$" > "$MOCK_LOG/cage.pid"
sh -c 'trap "" TERM; sleep 600 & wait "$!"' &
sh -c 'trap "touch \"\$MOCK_LOG/comp-term\"" TERM; sleep 600' &
while [ ! -s "$MOCK_LOG/probe.pid" ]; do sleep 0.01; done
exit 9
`
	if err := os.WriteFile(tools.SelfExe, []byte(compositor), 0o755); err != nil {
		t.Fatal(err)
	}
	adb := `#!/bin/sh
if [ "$2" = getprop ]; then
    printf '%s\n' "$$" > "$MOCK_LOG/probe.pid"
    exec sleep 600
fi
exit 1
`
	if err := os.WriteFile(tools.ADB, []byte(adb), 0o755); err != nil {
		t.Fatal(err)
	}
	result := make(chan int, 1)
	go func() { result <- launcher.Run(launcher.Config{UseADB: true, Tools: tools}) }()

	termMarker := filepath.Join(logs, "comp-term")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(termMarker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("compositor cleanup did not start in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-result:
		if code != 9 {
			t.Fatalf("late cleanup signal changed the exit to %d, want the initiating failure 9", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return within the cleanup budget")
	}
	assertStopped(t, waitPID(t, filepath.Join(logs, "cage.pid")))
}

// TestHangingBootProbeRespectsDeadline bounds a startup probe that never
// completes: the boot deadline must include probe time and the hung probe
// must be cleaned up without reaching the remote launch.
func TestHangingBootProbeRespectsDeadline(t *testing.T) {
	tools, logs := testEnv(t)
	t.Setenv("XTMAPPER_BOOT_WAIT_SECONDS", "2")
	adb := `#!/bin/sh
if [ "$2" = getprop ]; then
    printf '%s\n' "$$" > "$MOCK_LOG/probe.pid"
    exec sleep 600
fi
exit 1
`
	if err := os.WriteFile(tools.ADB, []byte(adb), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	code, stderr := runLauncher(t, launcher.Config{UseADB: true, Tools: tools})
	if code != 1 || !strings.Contains(stderr, "did not finish booting within 2 seconds") {
		t.Fatalf("hanging probe exit %d, stderr %s", code, stderr)
	}
	if elapsed := time.Since(start); elapsed > 6*time.Second {
		t.Fatalf("hung probe was not bounded by the boot deadline: %s", elapsed)
	}
	assertStopped(t, waitPID(t, filepath.Join(logs, "probe.pid")))
	if _, err := os.Stat(filepath.Join(logs, "remote-command")); !os.IsNotExist(err) {
		t.Fatal("remote service launched after a hung boot probe")
	}
}
