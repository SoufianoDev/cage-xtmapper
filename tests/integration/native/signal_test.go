// Package native_test exercises the embedded compositor dispatch mode
// of the single-ELF application: same-ELF native execution, primary
// client status propagation, client-stdstress separation, and signal
// ownership through the control channel.
package native_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

var binPath string
var tmpRoot string

func TestMain(m *testing.M) {
	repo, _ := filepath.Abs(filepath.Join("..", "..", ".."))
	tmpRoot, _ = os.MkdirTemp("", "cage-xtmapper-native-test")
	binPath = filepath.Join(tmpRoot, "cage-xtmapper")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/cage-xtmapper")
	build.Dir = repo
	build.Env = append(os.Environ(),
		"PKG_CONFIG_PATH="+filepath.Join(repo, "build", "native", "pkgconfig"),
	)
	out, err := build.CombinedOutput()
	if err != nil {
		os.RemoveAll(tmpRoot)
		os.Stderr.Write(out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(tmpRoot)
	os.Exit(code)
}

// headlessEnv returns the environment every test needs: a runtime dir
// for the compositor socket and the pure-software, display-less
// backends.
func headlessEnv(t *testing.T) []string {
	t.Helper()
	runtimeDir := filepath.Join(t.TempDir(), "run")
	if err := os.Mkdir(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return append(os.Environ(),
		"XDG_RUNTIME_DIR="+runtimeDir,
		"WLR_BACKENDS=headless",
		"WLR_RENDERER=pixman",
	)
}

func TestInternalClientStatusPropagation(t *testing.T) {
	cmd := exec.Command(binPath, "--internal-cage", "--", "sh", "-c", "exit 42")
	cmd.Env = headlessEnv(t)
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exit error, got %v", err)
	}
	if exit.ExitCode() != 42 {
		t.Fatalf("client status not propagated: got %d", exit.ExitCode())
	}
}

func TestInternalClientStdoutSeparated(t *testing.T) {
	cmd := exec.Command(binPath, "--internal-cage", "--", "sh", "-c", "printf 'PRE\\000POST\\n'")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	cmd.Env = headlessEnv(t)
	if err := cmd.Run(); err != nil {
		t.Fatalf("unexpected failure: %v (stderr %q)", err, errb.String())
	}
	if out.Len() != 0 {
		t.Fatalf("client output leaked into the compositor stdout: %q", out.String())
	}
	if errb.Len() == 0 {
		t.Fatal("client diagnostics disappeared instead of going to stderr")
	}
}

func TestInternalSignalShutdown(t *testing.T) {
	cases := []struct {
		name   string
		signal syscall.Signal
		status int
	}{
		{"sigint", syscall.SIGINT, 130},
		{"sigterm", syscall.SIGTERM, 143},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binPath, "--internal-cage", "--", "sleep", "1000")
			cmd.Env = headlessEnv(t)
			var errb bytes.Buffer
			cmd.Stderr = &errb
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Wait() //nolint:errcheck // status asserted below
			time.Sleep(500 * time.Millisecond)
			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != tc.status {
					t.Fatalf("expected %d, got %v (stderr %q)", tc.status, err, errb.String())
				}
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatalf("signal-initiated shutdown hung (stderr %q)", errb.String())
			}
		})
	}
}

func TestInternalEarlySignalShutdown(t *testing.T) {
	// A request arriving before the event loop starts must not be lost.
	cmd := exec.Command(binPath, "--internal-cage", "--", "sleep", "1000")
	cmd.Env = headlessEnv(t)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Wait() //nolint:errcheck // status asserted below
	time.Sleep(50 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 143 {
			t.Fatalf("expected 143 on pre-loop SIGTERM, got %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("early termination request was lost")
	}
}
