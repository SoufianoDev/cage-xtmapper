package native_test

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestSingleELFNativeLink(t *testing.T) {
	f, err := elf.Open(binPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	libraries, err := f.ImportedLibraries()
	if err != nil {
		t.Fatal(err)
	}
	for _, library := range libraries {
		if strings.Contains(library, "cage") || strings.Contains(library, "wlroots") {
			t.Errorf("project code depends on an external shared library: %s", library)
		}
	}
	for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH} {
		paths, err := f.DynString(tag)
		if err != nil || len(paths) != 0 {
			t.Errorf("unexpected %s: %v (%v)", tag, paths, err)
		}
	}
	symbols, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"cage_xtmapper_main": true, "wlr_backend_autocreate": true, "wl_display_create": true}
	for _, symbol := range symbols {
		if symbol.Section != elf.SHN_UNDEF {
			delete(want, symbol.Name)
		}
	}
	for symbol := range want {
		t.Errorf("native symbol was not linked into the ELF: %s", symbol)
	}
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

func TestPrintOutputInfoHeadless(t *testing.T) {
	hostRun := t.TempDir()
	hostRuntime := filepath.Join(hostRun, "run")
	if err := os.Mkdir(hostRuntime, 0o700); err != nil {
		t.Fatal(err)
	}
	host := exec.Command(binPath, "--internal-cage", "--", "sleep", "2000")
	host.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	host.WaitDelay = 2 * time.Second
	host.Env = append(os.Environ(),
		"XDG_RUNTIME_DIR="+hostRuntime,
		"WLR_BACKENDS=headless",
		"WLR_RENDERER=pixman",
		"XTMAPPER_WIDTH=800",
		"XTMAPPER_HEIGHT=600",
	)
	host.Stderr = os.Stderr
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = syscall.Kill(-host.Process.Pid, syscall.SIGKILL)
		_, _ = host.Process.Wait()
	}()

	var socket string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		matches, err := filepath.Glob(filepath.Join(hostRuntime, "wayland-*"))
		if err == nil && len(matches) > 0 {
			socket = matches[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if socket == "" {
		t.Fatal("host compositor did not expose a wayland socket")
	}

	runtimeDir := t.TempDir()
	nestedRuntime := filepath.Join(runtimeDir, "run")
	if err := os.Mkdir(nestedRuntime, 0o700); err != nil {
		t.Fatal(err)
	}
	nested := exec.Command(binPath, "--print-output-info")
	nested.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	nested.WaitDelay = 2 * time.Second
	nested.Env = append(os.Environ(),
		"XDG_RUNTIME_DIR="+nestedRuntime,
		"WAYLAND_DISPLAY="+socket,
		"WLR_BACKENDS=wayland",
		"WLR_RENDERER=pixman",
	)
	var out, errb bytes.Buffer
	nested.Stdout = &out
	nested.Stderr = &errb
	done := make(chan error, 1)
	go func() { done <- nested.Run() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected failure: %v (stderr %q)", err, errb.String())
		}
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-nested.Process.Pid, syscall.SIGKILL)
		t.Fatalf("print-output-info hung (stderr %q)", errb.String())
	}
	if !bytes.Contains(errb.Bytes(), []byte("Output mode: 800x600")) {
		t.Fatalf("output info missing on stderr: %q", errb.String())
	}
}

// TestInternalHelpFlushesStdout checks that C stdio output survives Go's
// os.Exit path when native help is redirected through a pipe.
func TestInternalHelpFlushesStdout(t *testing.T) {
	cmd := exec.Command(binPath, "--internal-cage", "-h")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 {
		t.Fatalf("expected native help exit status 1, got %v (stderr %q)", err, errb.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("Usage: cage-xtmapper")) {
		t.Fatalf("native C stdout was not fully flushed: %q (stderr %q)", out.String(), errb.String())
	}
}

// TestInternalClientArguments preserves empty and trailing arguments across
// the Go-to-C argv bridge and posix_spawnp boundary.
func TestInternalClientArguments(t *testing.T) {
	cmd := exec.Command(binPath, "--internal-cage", "--", "sh", "-c",
		`test "$#" -eq 2 && test -z "$1" && test "$2" = sentinel`, "_", "", "sentinel")
	cmd.Env = headlessEnv(t)
	if err := cmd.Run(); err != nil {
		t.Fatalf("native client argument vector changed: %v", err)
	}
}
