package launcher

import (
	"context"
	"testing"
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
