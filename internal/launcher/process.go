package launcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var terminalForeground = func() bool {
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	defer tty.Close()
	var foreground int32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, tty.Fd(), syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&foreground)))
	return errno == 0 && int(foreground) == syscall.Getpgrp()
}

var ErrTimedOut = errors.New("timed out")
var ErrForegroundInterrupted = errors.New("foreground command interrupted")

const (
	cleanupTermGrace = 5 * time.Second
	cleanupKillGrace = 2 * time.Second
)

func errChildExit(msg string, code int) error {
	return fmt.Errorf("%s (exit status %d)", msg, code)
}

type child struct {
	cmd          *exec.Cmd
	dead         chan struct{}
	code         int // Published before dead closes; every observer can read it.
	cleanupOnce  sync.Once
	stopCleanup  func() bool
	processGroup bool
}

func startGroup(ctx context.Context, cmd *exec.Cmd) (*child, error) {
	return startChild(ctx, cmd, true)
}

// startForeground runs a short interactive command in the caller's process
// group. This keeps it in the terminal's foreground group for sudo prompts.
func startForeground(ctx context.Context, cmd *exec.Cmd) (*child, error) {
	return startChild(ctx, cmd, false)
}

func startChild(ctx context.Context, cmd *exec.Cmd, processGroup bool) (*child, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if processGroup {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.WaitDelay = cleanupKillGrace
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", cmd.Path, err)
	}
	c := &child{cmd: cmd, dead: make(chan struct{}), processGroup: processGroup}
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
	pid := c.cmd.Process.Pid
	if c.processGroup {
		pid = -pid
	}
	return syscall.Kill(pid, sig)
}

func (c *child) cleanupComplete() bool {
	if !c.exited() {
		return false
	}
	return !c.processGroup || errors.Is(c.kill(0), syscall.ESRCH)
}

// finish also joins cleanup already started by cancellation.
func (c *child) finish() {
	c.stopCleanup()
	c.cleanup()
}

func (c *child) cleanup() {
	c.cleanupOnce.Do(func() {
		if c.cleanupComplete() {
			return
		}
		_ = c.kill(syscall.SIGTERM)
		deadline := time.Now().Add(cleanupTermGrace)
		for time.Now().Before(deadline) {
			// A reaped leader can still leave descendants in its process group.
			if c.cleanupComplete() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		_ = c.kill(syscall.SIGKILL)
		// Wait for the whole group to disappear, not just the leader: the
		// leader may be reaped long before its descendants die, and callers
		// observe the group immediately after cleanup returns.
		deadline = time.Now().Add(cleanupKillGrace)
		for !c.cleanupComplete() {
			if time.Now().After(deadline) {
				fmt.Fprintf(os.Stderr, "Error: cleanup did not complete for process %d\n", c.cmd.Process.Pid)
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
}

func runBounded(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (int, error) {
	return runWithTimeout(ctx, cmd, timeout, true)
}

// runBoundedForeground keeps interactive authentication commands in the
// caller's process group so terminal job control does not stop password reads.
func runBoundedForeground(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (int, error) {
	return runWithTimeout(ctx, cmd, timeout, false)
}

// runWithTimeout runs a command under a deadline and returns its exit status or cancellation error.
func runWithTimeout(ctx context.Context, cmd *exec.Cmd, timeout time.Duration, processGroup bool) (int, error) {
	if timeout <= 0 {
		return -1, ErrTimedOut
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := startChild(ctx, cmd, processGroup)
	if err != nil {
		return -1, err
	}
	defer c.finish()
	return waitForCommandResult(ctx, c.dead, func() int { return c.code })
}

// waitForCommandResult resolves child completion against the command context.
func waitForCommandResult(ctx context.Context, done <-chan struct{}, exitCode func() int) (int, error) {
	select {
	case <-done:
		return commandExitResult(ctx, exitCode())
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return -1, ErrTimedOut
		}
		return -1, ctx.Err()
	}
}

// commandExitResult preserves cancellation and timeouts when process exit and
// context completion become observable at nearly the same time.
func commandExitResult(ctx context.Context, code int) (int, error) {
	ctxErr := ctx.Err()
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return -1, ErrTimedOut
	}
	// The deadline can already have elapsed before the context timer goroutine
	// publishes DeadlineExceeded. Do not treat that narrow race as success.
	if ctxErr == nil {
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return -1, ErrTimedOut
		}
	}
	if ctxErr != nil {
		return -1, ctxErr
	}
	return code, nil
}

func runInherit(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (int, error) {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return runBounded(ctx, cmd, timeout)
}

func runInheritForeground(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (int, error) {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return runBoundedForeground(ctx, cmd, timeout)
}

func setTerminalForegroundGroup(tty *os.File, pgid int) error {
	// Restoring the launcher's group happens while its process group is in the
	// background, so temporarily ignore SIGTTOU around the terminal ioctl.
	if !terminalForeground() {
		signal.Ignore(syscall.SIGTTOU)
		defer signal.Reset(syscall.SIGTTOU)
	}
	foreground := int32(pgid)
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, tty.Fd(), syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&foreground)))
	if errno != 0 {
		return errno
	}
	return nil
}

// runInheritTerminalForeground gives an isolated child process group terminal
// foreground ownership while it runs. This permits sudo to prompt on the
// Waydroid command itself without exposing the launcher's process group to cleanup.
func runInheritTerminalForeground(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (int, error) {
	if timeout <= 0 {
		return -1, ErrTimedOut
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return -1, fmt.Errorf("could not open the controlling terminal: %w", err)
	}
	defer tty.Close()

	launcherGroup := syscall.Getpgrp()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c, err := startGroup(ctx, cmd)
	if err != nil {
		return -1, err
	}
	defer func() {
		c.finish()
		if err := setTerminalForegroundGroup(tty, launcherGroup); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not restore terminal foreground process group: %v\n", err)
		}
	}()

	if c.exited() {
		return c.code, nil
	}
	if err := setTerminalForegroundGroup(tty, cmd.Process.Pid); err != nil {
		if c.exited() {
			return c.code, nil
		}
		return -1, fmt.Errorf("could not assign the terminal to the Waydroid command: %w", err)
	}
	// A background child may have stopped with SIGTTIN before the terminal
	// handoff completed. Resume the isolated group after it becomes foreground.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGCONT)

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
