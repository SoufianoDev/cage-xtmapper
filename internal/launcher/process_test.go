package launcher

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
