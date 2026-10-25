package launcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

var ErrTimedOut = errors.New("timed out")

const (
	cleanupTermGrace = 5 * time.Second
	cleanupKillGrace = 2 * time.Second
)

func errChildExit(msg string, code int) error {
	return fmt.Errorf("%s (exit status %d)", msg, code)
}

type child struct {
	cmd         *exec.Cmd
	dead        chan struct{}
	code        int // Published before dead closes; every observer can read it.
	cleanupOnce sync.Once
	stopCleanup func() bool
}

func startGroup(ctx context.Context, cmd *exec.Cmd) (*child, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = cleanupKillGrace
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", cmd.Path, err)
	}
	c := &child{cmd: cmd, dead: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		c.code = exitStatusOf(cmd)
		if err != nil && c.code == 0 {
			c.code = 1
		}
		close(c.dead)
	}()
	c.stopCleanup = context.AfterFunc(ctx, c.cleanup)
	return c, nil
}

func (c *child) exited() bool {
	select {
	case <-c.dead:
		return true
	default:
		return false
	}
}

func exitStatusOf(cmd *exec.Cmd) int {
	if cmd.ProcessState == nil {
		return 1
	}
	if code := cmd.ProcessState.ExitCode(); code >= 0 {
		return code
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return 1
}

func (c *child) kill(sig syscall.Signal) error {
	return syscall.Kill(-c.cmd.Process.Pid, sig)
}

// finish also joins cleanup already started by cancellation.
func (c *child) finish() {
	c.stopCleanup()
	c.cleanup()
}

func (c *child) cleanup() {
	c.cleanupOnce.Do(func() {
		if c.exited() && errors.Is(c.kill(0), syscall.ESRCH) {
			return
		}
		_ = c.kill(syscall.SIGTERM)
		deadline := time.Now().Add(cleanupTermGrace)
		for time.Now().Before(deadline) {
			// A reaped leader can still leave descendants in its process group.
			if c.exited() && errors.Is(c.kill(0), syscall.ESRCH) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		_ = c.kill(syscall.SIGKILL)
		// Wait for the whole group to disappear, not just the leader: the
		// leader may be reaped long before its descendants die, and callers
		// observe the group immediately after cleanup returns.
		deadline = time.Now().Add(cleanupKillGrace)
		for !(c.exited() && errors.Is(c.kill(0), syscall.ESRCH)) {
			if time.Now().After(deadline) {
				fmt.Fprintf(os.Stderr, "Error: cleanup did not complete for process group %d\n", c.cmd.Process.Pid)
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
}

func runBounded(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (int, error) {
	if timeout <= 0 {
		return -1, ErrTimedOut
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := startGroup(ctx, cmd)
	if err != nil {
		return -1, err
	}
	defer c.finish()
	select {
	case <-c.dead:
		return c.code, nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return -1, ErrTimedOut
		}
		return -1, ctx.Err()
	}
}

func runInherit(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (int, error) {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return runBounded(ctx, cmd, timeout)
}

func runCapture(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (string, error) {
	var data bytes.Buffer
	cmd.Stdout = &data
	code, err := runBounded(ctx, cmd, timeout)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", errChildExit("Android probe failed", code)
	}
	return data.String(), nil
}
