package native_test

import (
	"bytes"
	"debug/elf"
	"errors"
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

// elfSymbolNames reads every name from both symbol tables of an ELF file.
func elfSymbolNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	file, err := elf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	seen := map[string]bool{}
	for _, table := range []func() ([]elf.Symbol, error){file.Symbols, file.DynamicSymbols} {
		symbols, err := table()
		if errors.Is(err, elf.ErrNoSymbols) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, symbol := range symbols {
			seen[symbol.Name] = true
		}
	}
	if len(seen) == 0 {
		t.Fatalf("no symbol tables in %s", path)
	}
	return seen
}

// Debian's lcms2 package lists liblcms2_fast_float.a and liblcms2_threaded.a
// (GPL-3-or-later plugin code) on every lcms2 link line, but the GNU linker
// pulls an archive member only to satisfy a reference and no project code
// registers the plugins. Pin the invariant that no plugin code reaches the
// launcher ELF: recording those components without the code sends reviewers
// hunting for GPL obligations that the binary does not carry, and shipping
// the code without them would violate GPL-3 section 6.
func TestLinkedELFContainsNoLittleCMSPluginCode(t *testing.T) {
	names := elfSymbolNames(t, binPath)
	for _, entry := range []string{"cmsFastFloatExtensions", "cmsThreadedExtensions"} {
		if names[entry] {
			t.Errorf("linked ELF contains LittleCMS GPL plugin code: %s", entry)
		}
	}
}

// TestLittleCMSPluginCodeDetectionPositive compiles a fixture holding a live
// reference to the plugin entry and pins the positive side of the same scan,
// so a future build that actually links plugin code stays visible.
func TestLittleCMSPluginCodeDetectionPositive(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skipf("no C compiler on this host: %v", err)
	}
	libdirOut, err := exec.Command("pkg-config", "--variable", "libdir", "lcms2").Output()
	if err != nil {
		t.Skipf("no lcms2 pkg-config on this host: %v", err)
	}
	libdir := strings.TrimSpace(string(libdirOut))
	archive := filepath.Join(libdir, "liblcms2_fast_float.a")
	if _, err := os.Stat(archive); err != nil {
		t.Skipf("no lcms2 fast-float plugin archive on this host: %v", err)
	}
	// A global function keeps the reference live: the linker must pull the
	// archive member that defines the plugin registration symbol.
	body := "extern void *cmsFastFloatExtensions(void);\n" +
		"void *keep_lcms_plugin_reference(void) { return (void *)cmsFastFloatExtensions; }\n" +
		"int main(void) { return 0; }\n"
	source := filepath.Join(t.TempDir(), "fixture.c")
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "fixture")
	// The plugin member references helpers in the lcms2 core, so the fixture
	// links the core library after the archive (Ubuntu links with
	// --as-needed, which would otherwise drop the core before it resolves
	// anything).
	libsOut, err := exec.Command("pkg-config", "--libs", "lcms2").Output()
	if err != nil {
		t.Skipf("no lcms2 pkg-config libraries on this host: %v", err)
	}
	build := exec.Command("cc", source, "-o", binary, archive)
	for _, lib := range strings.Fields(string(libsOut)) {
		if !strings.HasPrefix(lib, "-llcms2_") {
			build.Args = append(build.Args, lib)
		}
	}
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("could not build plugin fixture: %v", err)
	}
	names := elfSymbolNames(t, binary)
	if !names["cmsFastFloatExtensions"] {
		t.Fatal("plugin registration symbol invisible in a linking fixture")
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
	// The first and last usage lines must both arrive: buffered output that
	// is only partially flushed loses the tail through the pipe.
	if !bytes.Contains(out.Bytes(), []byte("Usage: cage-xtmapper")) ||
		!bytes.Contains(out.Bytes(), []byte("Use -- when you want to pass arguments to APPLICATION")) {
		t.Fatalf("native C stdout was not fully flushed: %q (stderr %q)", out.String(), errb.String())
	}
}

// TestInternalClientArguments preserves argument vectors across the
// Go-to-C argv bridge and the posix_spawnp boundary: argc, empty
// arguments, arguments with separators, and long arguments must all be
// reconstructed exactly, which requires the terminating NULL the kernel
// copies argv from.
func TestInternalClientArguments(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		check string
	}{
		{
			name:  "empty and trailing arguments",
			args:  []string{"_", "", "sentinel"},
			check: `test "$#" -eq 2 && test -z "$1" && test "$2" = sentinel`,
		},
		{
			name:  "single argument",
			args:  []string{"_", "only"},
			check: `test "$#" -eq 1 && test "$1" = only`,
		},
		{
			name:  "arguments with separators",
			args:  []string{"_", "a b", "c\td", "e\nf"},
			check: `test "$#" -eq 3 && test "$1" = "a b" && test "$2" = "$(printf 'c\td')" && test "$3" = "$(printf 'e\nf')"`,
		},
		{
			name:  "long argument",
			args:  []string{"_", strings.Repeat("x", 65536) + "end"},
			check: `test "$#" -eq 1 && test "${#1}" -eq 65539 && case "$1" in x*end) true ;; *) false ;; esac`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binPath, append([]string{"--internal-cage", "--", "sh", "-c", tc.check}, tc.args...)...)
			var errb bytes.Buffer
			cmd.Stderr = &errb
			cmd.Env = headlessEnv(t)
			if err := cmd.Run(); err != nil {
				t.Fatalf("native client argument vector changed: %v (stderr %q)", err, errb.String())
			}
		})
	}
}

// TestInternalClientSpawnFailureTearsDownCleanly pins the startup-failure path
// that used to SIGABRT: a failed primary-client spawn reaches the shared
// teardown, where still-registered listeners made wl_display_destroy trip
// wlroots assertions. Every teardown listener must be removed on all paths.
func TestInternalClientSpawnFailureTearsDownCleanly(t *testing.T) {
	cmd := exec.Command(binPath, "--internal-cage", "-s", "-m", "last", "--", "/does/not/exist")
	var errb bytes.Buffer
	cmd.Stderr = &errb
	cmd.Env = headlessEnv(t)
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exit error, got %v (stderr %q)", err, errb.String())
	}
	if exit.ExitCode() != 1 {
		t.Fatalf("expected exit status 1, got %d (stderr %q)", exit.ExitCode(), errb.String())
	}
	if bytes.Contains(errb.Bytes(), []byte("SIGABRT")) ||
		bytes.Contains(errb.Bytes(), []byte("wl_list_empty")) {
		t.Fatalf("compositor aborted during teardown: %q", errb.String())
	}
	if !bytes.Contains(errb.Bytes(), []byte("Unable to spawn client")) {
		t.Fatalf("spawn-failure diagnostics missing from stderr: %q", errb.String())
	}
}

// TestInternalNoClientStarts covers the zero-argument spawn boundary
// (optind == argc): the compositor starts without a primary client and
// still shuts down through the embedded control path.
func TestInternalNoClientStarts(t *testing.T) {
	cmd := exec.Command(binPath, "--internal-cage")
	cmd.Env = headlessEnv(t)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Wait() //nolint:errcheck // status asserted below
	time.Sleep(500 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("compositor without a client did not accept termination: %v (stderr %q)", err, errb.String())
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 143 {
			t.Fatalf("expected 143 without a primary client, got %v (stderr %q)", err, errb.String())
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("clientless compositor shutdown hung (stderr %q)", errb.String())
	}
}
