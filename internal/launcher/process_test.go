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
		case "success":
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
	sudoCached := filepath.Join(dir, "sudo.cached")
	waydroidLog := filepath.Join(dir, "waydroid.log")
	sudo := filepath.Join(dir, "sudo")
	waydroid := filepath.Join(dir, "waydroid")
	sudoBody := `#!/bin/sh
printf 'mode=%s argv=%s\n' "$MOCK_SUDO_MODE" "$*" >> "$SUDO_LOG"
if [ "$1" = "-v" ]; then
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
    : > "$SUDO_CACHED"
    exit 0
fi
if [ "$1" = "-n" ]; then
    if [ ! -f "$SUDO_CACHED" ]; then
        echo 'sudo mock has no cached credentials' >&2
        exit 92
    fi
    shift
    exec "$@"
fi
echo 'unexpected sudo arguments' >&2
exit 94
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
		{name: "password accepted", mode: "success", input: "test-secret\n", wantLog: "auth=accepted"},
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
				"SUDO_CACHED="+sudoCached,
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
			if tc.mode == "success" {
				data, err := os.ReadFile(waydroidLog)
				if err != nil {
					t.Fatal(err)
				}
				if strings.TrimSpace(string(data)) != "container start" {
					t.Fatalf("waydroid was not started after authentication: %q", data)
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
