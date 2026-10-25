package launcher

import "testing"

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
