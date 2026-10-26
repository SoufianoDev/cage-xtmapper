package launcher

import (
	"context"
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

func TestPrepareStartupStartsBootDeadlineAfterContainerStart(t *testing.T) {
	const bootWait = 1
	delay := 150 * time.Millisecond
	transport := transport{
		stopSession: func(context.Context) error {
			time.Sleep(delay)
			return nil
		},
		startContainer: func(context.Context) error {
			time.Sleep(delay)
			return nil
		},
	}

	started := time.Now()
	deadline, err := prepareStartup(context.Background(), transport, bootWait)
	if err != nil {
		t.Fatal(err)
	}
	if remaining := time.Until(deadline); remaining < 900*time.Millisecond {
		t.Fatalf("Android boot budget after preparation = %s; want close to %d second", remaining, bootWait)
	}
	if elapsed := time.Since(started); elapsed < 2*delay {
		t.Fatalf("preparation did not run both steps: elapsed %s", elapsed)
	}
}
