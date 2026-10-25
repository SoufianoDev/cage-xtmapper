package launcher

import (
	"context"
	"os/exec"
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
