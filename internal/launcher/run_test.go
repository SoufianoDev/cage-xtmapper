package launcher

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestShutdownCausePreservesInitiatingKind(t *testing.T) {
	t.Run("signal cannot replace an earlier pipeline exit", func(t *testing.T) {
		cause := &shutdownCause{ready: make(chan struct{})}
		cause.mark(9, shutdownPipeline)
		cause.mark(143, shutdownSignal)
		if code, ok := cause.result(); !ok || code != 9 {
			t.Fatalf("shutdown result = (%d, %v), want (9, true)", code, ok)
		}
	})

	t.Run("pipeline result can account for both independently exited sides", func(t *testing.T) {
		cause := &shutdownCause{ready: make(chan struct{})}
		cause.mark(9, shutdownPipeline)
		cause.mark(141, shutdownPipeline)
		cause.mark(143, shutdownSignal)
		if code, ok := cause.result(); !ok || code != 141 {
			t.Fatalf("shutdown result = (%d, %v), want (141, true)", code, ok)
		}
	})

	t.Run("signal initiates shutdown when no earlier cause exists", func(t *testing.T) {
		cause := &shutdownCause{ready: make(chan struct{})}
		cause.mark(130, shutdownSignal)
		cause.mark(9, shutdownPipeline)
		if code, ok := cause.result(); !ok || code != 130 {
			t.Fatalf("shutdown result = (%d, %v), want (130, true)", code, ok)
		}
	})
}

func TestFailureResultRecordsCauseBeforeReturning(t *testing.T) {
	cause := &shutdownCause{ready: make(chan struct{})}
	ctx := context.WithValue(context.Background(), shutdownCauseKey{}, cause)

	if got := failureResult(ctx, 127); got != 127 {
		t.Fatalf("failureResult returned %d, want 127", got)
	}
	cause.mark(143, shutdownSignal)
	if code, ok := cause.result(); !ok || code != 127 {
		t.Fatalf("shutdown result after a late signal = (%d, %v), want (127, true)", code, ok)
	}
}

// TestWaitForChildExitIsBounded covers an already-exited child and a pending child that reaches the timeout.
func TestWaitForChildExitIsBounded(t *testing.T) {
	t.Run("already exited", func(t *testing.T) {
		dead := make(chan struct{})
		close(dead)
		if !waitForChildExit(dead, time.Hour) {
			t.Fatal("an already-closed child channel was not collected")
		}
	})

	t.Run("pending child times out", func(t *testing.T) {
		start := time.Now()
		if waitForChildExit(make(chan struct{}), 20*time.Millisecond) {
			t.Fatal("pending child was reported as exited")
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("bounded child wait took %s; want it to return near its 20ms limit", elapsed)
		}
	})
}

// TestTerminatePeerAfterRuntimeExitUsesBoundedCollection checks cleanup bounds, warning output, and status preservation.
func TestTerminatePeerAfterRuntimeExitUsesBoundedCollection(t *testing.T) {
	for _, tc := range []struct {
		name           string
		initiatingCode int
		wantCode       int
	}{
		{name: "preserve initiating failure", initiatingCode: 9, wantCode: 9},
		{name: "incomplete collection cannot report success", initiatingCode: 0, wantCode: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dead := make(chan struct{})
			peer := &child{cmd: &exec.Cmd{Process: &os.Process{Pid: 123}}, dead: dead}
			cancelled := false
			waitCalled := false
			wait := func(gotDead <-chan struct{}, timeout time.Duration) bool {
				waitCalled = true
				if gotDead != dead {
					t.Fatal("cleanup waited on the wrong child channel")
				}
				if timeout != runtimePeerCleanupCollectionWindow {
					t.Fatalf("collection timeout = %s, want %s", timeout, runtimePeerCleanupCollectionWindow)
				}
				return false
			}
			cleanup := func() int {
				return terminatePeerAfterRuntimeExitWithWait(
					func() { cancelled = true }, peer, tc.initiatingCode, "test peer", wait,
				)
			}
			var got int
			if tc.initiatingCode != 0 {
				// os.Stderr is process-global: keep this test non-parallel and
				// restore it even if an assertion inside cleanup terminates the test.
				stderrFile, err := os.CreateTemp(t.TempDir(), "stderr-")
				if err != nil {
					t.Fatal(err)
				}
				defer stderrFile.Close()
				oldStderr := os.Stderr
				os.Stderr = stderrFile
				got = func() int {
					defer func() { os.Stderr = oldStderr }()
					return cleanup()
				}()
				if err := stderrFile.Sync(); err != nil {
					t.Fatalf("sync captured stderr: %v", err)
				}
				if _, err := stderrFile.Seek(0, io.SeekStart); err != nil {
					t.Fatalf("rewind captured stderr: %v", err)
				}
				captured, err := io.ReadAll(stderrFile)
				if err != nil {
					t.Fatalf("read captured stderr: %v", err)
				}
				const wantStderr = "Error: timed out waiting for test peer process 123 after cleanup; exit status collection is incomplete\n"
				if string(captured) != wantStderr {
					t.Fatalf("stderr = %q, want exact warning %q", string(captured), wantStderr)
				}
			} else {
				got = cleanup()
			}
			if !cancelled {
				t.Fatal("cleanup did not cancel the peer context")
			}
			if !waitCalled {
				t.Fatal("cleanup did not perform bounded exit collection")
			}
			if got != tc.wantCode {
				t.Fatalf("cleanup result = %d, want %d", got, tc.wantCode)
			}
		})
	}
}

// TestABIDiscoveryTimeoutTerminatesProbe verifies that a stalled ABI probe is stopped before remote launch.
func TestABIDiscoveryTimeoutTerminatesProbe(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "abi-probe.pid")
	abiTimeout := 200 * time.Millisecond
	remoteCalled := false
	tr := transport{
		probeCmd: func(verb string, args ...string) *exec.Cmd {
			if verb == "getprop" && len(args) == 1 && args[0] == "sys.boot_completed" {
				return exec.Command("sh", "-c", "printf '1\\n'")
			}
			if verb == "getprop" && len(args) == 1 && args[0] == "ro.product.cpu.abi" {
				return exec.Command("sh", "-c", fmt.Sprintf("printf '%%s\\n' \"$$\" > %q; exec sleep 30", pidFile))
			}
			return exec.Command("sh", "-c", "exit 1")
		},
		remoteShellCmd: func(string) *exec.Cmd {
			remoteCalled = true
			return exec.Command("sh", "-c", "exit 0")
		},
	}

	_, err := setupWithProbeTimeouts(context.Background(), tr, true, nil, 1, abiTimeout, 200*time.Millisecond)
	wantError := fmt.Sprintf("timed out after %s while determining the Android CPU ABI", abiTimeout)
	if err == nil || err.Error() != wantError {
		t.Fatalf("startup error = %v, want %q", err, wantError)
	}
	if remoteCalled {
		t.Fatal("remote service launched after ABI discovery timed out")
	}

	pid := readTimedOutProbePID(t, pidFile)
	assertTimedOutProbeStopped(t, pid)
}

// readTimedOutProbePID waits for a timed-out probe fixture to publish its process ID.
func readTimedOutProbePID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr != nil {
				t.Fatalf("invalid ABI probe PID %q: %v", strings.TrimSpace(string(data)), parseErr)
			}
			return pid
		}
		if !os.IsNotExist(err) {
			t.Fatalf("read ABI probe PID: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ABI probe did not write its PID file %s", path)
	return 0
}

// assertTimedOutProbeStopped checks that a timed-out probe is gone or has been reaped.
func assertTimedOutProbeStopped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(err) {
			return
		}
		if err == nil {
			record := string(data)
			if end := strings.LastIndex(record, ")"); end >= 0 {
				fields := strings.Fields(record[end+1:])
				if len(fields) > 0 && fields[0] == "Z" {
					return
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ABI probe process %d survived its command timeout", pid)
}

// TestAPKDiscoveryUsesFreshTimeoutPerProbe verifies that a timed-out debug lookup does not consume the release lookup's budget.
func TestAPKDiscoveryUsesFreshTimeoutPerProbe(t *testing.T) {
	apkTimeout := 2 * time.Second
	debugProbeMarker := filepath.Join(t.TempDir(), "debug-probe-completed")
	var probes []string
	var remoteCommand string
	tr := transport{
		probeCmd: func(verb string, args ...string) *exec.Cmd {
			if verb == "getprop" && len(args) == 1 && args[0] == "sys.boot_completed" {
				return exec.Command("sh", "-c", "printf '1\\n'")
			}
			if verb == "getprop" && len(args) == 1 && args[0] == "ro.product.cpu.abi" {
				return exec.Command("sh", "-c", "printf 'x86_64\\n'")
			}
			if verb == "pm" && len(args) == 2 && args[0] == "path" {
				pkg := args[1]
				probes = append(probes, pkg)
				if pkg == "xtr.keymapper.debug" {
					// If the timeout is not applied, this command completes and
					// leaves a marker before returning a debug APK path.
					script := fmt.Sprintf("sleep 5; printf done > %q; printf 'package:/data/app/debug/base.apk\\n'", debugProbeMarker)
					return exec.Command("sh", "-c", script)
				}
				if pkg == "xtr.keymapper" {
					return exec.Command("sh", "-c", "printf 'package:/data/app/release/base.apk\\n'")
				}
			}
			return exec.Command("sh", "-c", "exit 1")
		},
		remoteShellCmd: func(command string) *exec.Cmd {
			remoteCommand = command
			return exec.Command("sh", "-c", "exit 0")
		},
	}

	if _, err := setupWithProbeTimeouts(context.Background(), tr, true, nil, 1, time.Second, apkTimeout); err != nil {
		t.Fatalf("APK discovery failed after the debug probe timed out: %v", err)
	}
	if got, want := strings.Join(probes, "\n"), "xtr.keymapper.debug\nxtr.keymapper"; got != want {
		t.Fatalf("APK probe order = %q, want %q", got, want)
	}
	if _, err := os.Stat(debugProbeMarker); !os.IsNotExist(err) {
		t.Fatalf("debug probe completed instead of timing out; marker stat error = %v", err)
	}
	if !strings.Contains(remoteCommand, "/data/app/release/base.apk") {
		t.Fatalf("remote command did not use the release APK found after the debug timeout: %q", remoteCommand)
	}
}

// TestPrepareStartupRunsWaydroidLifecycleInOrder verifies that session stop precedes container start.
func TestPrepareStartupRunsWaydroidLifecycleInOrder(t *testing.T) {
	var calls []string
	transport := transport{
		stopSession: func(context.Context) error {
			calls = append(calls, "stop")
			return nil
		},
		startContainer: func(context.Context) error {
			calls = append(calls, "container start")
			return nil
		},
	}

	if err := prepareStartup(context.Background(), transport); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "stop" || calls[1] != "container start" {
		t.Fatalf("Waydroid startup calls = %v, want [stop container start]", calls)
	}
}
