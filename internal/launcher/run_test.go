package launcher

import (
	"context"
	"os"
	"os/exec"
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
			got := terminatePeerAfterRuntimeExitWithWait(
				func() { cancelled = true }, peer, tc.initiatingCode, "test peer",
				func(gotDead <-chan struct{}, timeout time.Duration) bool {
					waitCalled = true
					if gotDead != dead {
						t.Fatal("cleanup waited on the wrong child channel")
					}
					if timeout != runtimePeerCleanupCollectionWindow {
						t.Fatalf("collection timeout = %s, want %s", timeout, runtimePeerCleanupCollectionWindow)
					}
					return false
				},
			)
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
