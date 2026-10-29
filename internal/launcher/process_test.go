package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type deadlineOnlyContext struct {
	context.Context
	deadline time.Time
}

func (c deadlineOnlyContext) Deadline() (time.Time, bool) {
	return c.deadline, true
}

func TestCommandExitResultHonorsDeadline(t *testing.T) {
	expiredCtx, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()

	activeCtx, cancelActive := context.WithTimeout(context.Background(), time.Minute)
	defer cancelActive()

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	cases := []struct {
		name     string
		ctx      context.Context
		code     int
		wantCode int
		wantErr  error
	}{
		{name: "deadline already reported", ctx: expiredCtx, code: 0, wantCode: -1, wantErr: ErrTimedOut},
		{
			name:     "deadline elapsed before context timer publication",
			ctx:      deadlineOnlyContext{Context: context.Background(), deadline: time.Now().Add(-time.Second)},
			code:     0,
			wantCode: -1,
			wantErr:  ErrTimedOut,
		},
		{name: "context canceled", ctx: canceledCtx, code: 0, wantCode: -1, wantErr: context.Canceled},
		{name: "exit before deadline preserves status", ctx: activeCtx, code: 9, wantCode: 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotCode, err := commandExitResult(tc.ctx, tc.code)
			if gotCode != tc.wantCode {
				t.Fatalf("result code = %d, want %d (err %v)", gotCode, tc.wantCode, err)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("result error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestWaitForCommandResultHonorsExpiredDeadlineOnChildCompletion(t *testing.T) {
	// The nil Done channel forces the already-completed child branch while
	// Deadline reports an elapsed deadline not yet published by a timer.
	ctx := deadlineOnlyContext{
		Context:  context.Background(),
		deadline: time.Now().Add(-time.Second),
	}
	dead := make(chan struct{})
	close(dead)

	code, err := waitForCommandResult(ctx, dead, func() int { return 9 })
	if code != -1 || !errors.Is(err, ErrTimedOut) {
		t.Fatalf("completed child result = (%d, %v), want (-1, %v)", code, err, ErrTimedOut)
	}
}

func TestRunBoundedTimeoutKillsProcessGroupDescendants(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "descendant.pid")
	cmd := exec.Command("sh", "-c", fmt.Sprintf("sleep 30 & echo $! > %q; wait", pidFile))
	_, err := runBounded(context.Background(), cmd, 150*time.Millisecond)
	if !errors.Is(err, ErrTimedOut) {
		t.Fatalf("runBounded error = %v, want %v", err, ErrTimedOut)
	}

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("invalid descendant PID: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(err) {
			return
		}
		if err == nil {
			record := string(stat)
			end := strings.LastIndex(record, ")")
			if end >= 0 {
				fields := strings.Fields(record[end+1:])
				if len(fields) > 0 && fields[0] == "Z" {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("descendant process %d survived process-group cleanup", pid)
}

func TestStartForegroundInheritsCallerProcessGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30")
	c, err := startForeground(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer c.finish()

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if want := syscall.Getpgrp(); pgid != want {
		t.Fatalf("interactive child process group = %d, want caller group %d", pgid, want)
	}

	if err := syscall.Kill(cmd.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.dead:
	case <-time.After(3 * time.Second):
		t.Fatal("foreground child did not terminate")
	}
}

func TestContainerStartIsNonInteractiveWithoutForegroundTerminal(t *testing.T) {
	old := terminalForeground
	terminalForeground = func() bool { return false }
	t.Cleanup(func() { terminalForeground = old })

	dir := t.TempDir()
	log := filepath.Join(dir, "sudo.log")
	sudo := filepath.Join(dir, "sudo")
	waydroid := filepath.Join(dir, "waydroid")
	if err := os.WriteFile(sudo, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$SUDO_LOG\"\nshift\nexec \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(waydroid, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUDO_LOG", log)
	transport := waydroidTransport(&Config{Tools: Tools{Sudo: sudo, Waydroid: waydroid}})
	if err := transport.startContainer(context.Background()); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(args)); got != "-n "+waydroid+" container start" {
		t.Fatalf("background container command = %q", got)
	}
}

func TestContainerStartAuthenticationPTY(t *testing.T) {
	if mode := os.Getenv("XTMAPPER_SUDO_PTY_HELPER"); mode != "" {
		if !terminalForeground() {
			fmt.Fprintln(os.Stderr, "helper process is not in the terminal foreground group")
			os.Exit(10)
		}
		sudo := os.Getenv("XTMAPPER_PTY_SUDO")
		waydroid := os.Getenv("XTMAPPER_PTY_WAYDROID")
		ctx := context.Background()
		cancel := func() {}
		if mode == "cancel" {
			ctx, cancel = context.WithTimeout(ctx, 300*time.Millisecond)
		}
		err := waydroidTransport(&Config{Tools: Tools{Sudo: sudo, Waydroid: waydroid}}).startContainer(ctx)
		cancel()
		switch mode {
		case "success", "passwordless":
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		case "reject":
			if err == nil || !strings.Contains(err.Error(), "exit status 92") {
				fmt.Fprintf(os.Stderr, "wrong-password path returned %v\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		case "cancel":
			if err == nil || !strings.Contains(err.Error(), ErrTimedOut.Error()) {
				fmt.Fprintf(os.Stderr, "cancelled prompt returned %v\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		default:
			fmt.Fprintln(os.Stderr, "unknown PTY helper mode")
			os.Exit(2)
		}
	}

	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script is required for PTY authentication checks")
	}

	dir := t.TempDir()
	sudoLog := filepath.Join(dir, "sudo.log")
	waydroidLog := filepath.Join(dir, "waydroid.log")
	sudo := filepath.Join(dir, "sudo")
	waydroid := filepath.Join(dir, "waydroid")
	sudoBody := `#!/bin/sh
printf 'mode=%s argv=%s\n' "$MOCK_SUDO_MODE" "$*" >> "$SUDO_LOG"
if [ "$1" = "-n" ]; then
    shift
    exec "$@"
fi
if [ "$MOCK_SUDO_MODE" = passwordless ]; then
    exec "$@"
fi
if [ ! -t 0 ] || [ ! -t 1 ] || [ ! -t 2 ]; then
    echo 'sudo mock did not inherit a terminal' >&2
    exit 90
fi
old_stty=$(stty -g </dev/tty) || exit 93
stty -echo </dev/tty
printf 'Password: ' >/dev/tty
if [ "$MOCK_SUDO_MODE" = cancel ]; then
    exec sleep 600
fi
IFS= read -r password </dev/tty || exit 91
stty "$old_stty" </dev/tty
printf '\n' >/dev/tty
if [ "$password" != "$MOCK_PASSWORD" ]; then
    echo 'auth=rejected' >> "$SUDO_LOG"
    exit 92
fi
echo 'auth=accepted' >> "$SUDO_LOG"
exec "$@"
`
	if err := os.WriteFile(sudo, []byte(sudoBody), 0o755); err != nil {
		t.Fatal(err)
	}
	waydroidBody := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$WAYDROID_LOG\"\nexit 0\n"
	if err := os.WriteFile(waydroid, []byte(waydroidBody), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, mode, input, wantLog string
	}{
		{name: "password accepted on container command", mode: "success", input: "test-secret\n", wantLog: "auth=accepted"},
		{name: "password-free startup skips validation", mode: "passwordless", input: "", wantLog: "mode=passwordless argv="},
		{name: "password rejected", mode: "reject", input: "wrong-secret\n", wantLog: "auth=rejected"},
		{name: "prompt cancellation by deadline", mode: "cancel", input: "", wantLog: "mode=cancel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(sudoLog)
			_ = os.Remove(waydroidLog)
			command := fmt.Sprintf("%q -test.run=^TestContainerStartAuthenticationPTY$", os.Args[0])
			cmd := exec.Command("script", "-qefc", command, "/dev/null")
			cmd.Stdin = strings.NewReader(tc.input)
			cmd.Env = append(os.Environ(),
				"XTMAPPER_SUDO_PTY_HELPER="+tc.mode,
				"XTMAPPER_PTY_SUDO="+sudo,
				"XTMAPPER_PTY_WAYDROID="+waydroid,
				"MOCK_SUDO_MODE="+tc.mode,
				"MOCK_PASSWORD=test-secret",
				"SUDO_LOG="+sudoLog,
				"WAYDROID_LOG="+waydroidLog,
			)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("PTY authentication helper failed: %v\n%s", err, output)
			}
			logData, err := os.ReadFile(sudoLog)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(logData), tc.wantLog) {
				t.Fatalf("sudo mock log %q does not contain %q; output: %s", logData, tc.wantLog, output)
			}
			if tc.mode == "passwordless" {
				if strings.Contains(string(logData), "argv=-v") {
					t.Fatalf("password-free startup invoked sudo -v: %q", logData)
				}
				if !strings.Contains(string(logData), "argv="+waydroid+" container start") {
					t.Fatalf("password-free startup did not execute the Waydroid command directly: %q", logData)
				}
			}
			if tc.mode == "success" || tc.mode == "passwordless" {
				data, err := os.ReadFile(waydroidLog)
				if err != nil {
					t.Fatal(err)
				}
				if strings.TrimSpace(string(data)) != "container start" {
					t.Fatalf("waydroid was not started after authorization: %q", data)
				}
			} else if _, err := os.Stat(waydroidLog); !os.IsNotExist(err) {
				t.Fatalf("Waydroid ran despite failed/cancelled authentication: %v", err)
			}
		})
	}
}

func TestTerminalForegroundPTY(t *testing.T) {
	if mode := os.Getenv("XTMAPPER_PTY_HELPER"); mode != "" {
		if os.Getenv("XTMAPPER_PTY_BACKGROUND_CHILD") == "1" {
			if terminalForeground() {
				os.Exit(1)
			}
			return
		}
		if mode == "background" {
			child := exec.Command(os.Args[0], "-test.run=^TestTerminalForegroundPTY$")
			child.Env = append(os.Environ(), "XTMAPPER_PTY_BACKGROUND_CHILD=1")
			child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := child.Run(); err != nil {
				os.Exit(2)
			}
			return
		}
		if !terminalForeground() {
			os.Exit(1)
		}
		return
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script is required for PTY foreground checks")
	}
	for _, mode := range []string{"foreground", "background"} {
		t.Run(mode, func(t *testing.T) {
			command := fmt.Sprintf("XTMAPPER_PTY_HELPER=%s %q -test.run=^TestTerminalForegroundPTY$", mode, os.Args[0])
			if output, err := exec.Command("script", "-qefc", command, "/dev/null").CombinedOutput(); err != nil {
				t.Fatalf("PTY helper failed: %v: %s", err, output)
			}
		})
	}
}
