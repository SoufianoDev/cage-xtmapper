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
