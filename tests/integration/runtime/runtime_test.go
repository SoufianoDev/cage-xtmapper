// Package runtime_test exercises the cage-xtmapper CLI contract at
// process level: help output, argument validation, exit statuses and
// the stderr/stdout split, mirroring the legacy tests/test-launcher.sh
// suite (the runtime orchestration cases live next to the launcher
// package).
package runtime_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var binPath string

func TestMain(m *testing.M) {
	repo, _ := filepath.Abs(filepath.Join("..", "..", ".."))
	tmp, _ := os.MkdirTemp("", "cage-xtmapper-runtime-test")
	binPath = filepath.Join(tmp, "cage-xtmapper")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/cage-xtmapper")
	build.Dir = repo
	build.Env = append(os.Environ(),
		"PKG_CONFIG_PATH="+filepath.Join(repo, "build", "native", "pkgconfig"),
	)
	if out, err := build.CombinedOutput(); err != nil {
		os.WriteFile(filepath.Join(os.TempDir(), "runtime-test-build.log"), out, 0o644)
		os.RemoveAll(tmp)
		os.Stderr.Write(out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

func run(t *testing.T, args []string, env ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	// Isolate the flow: the last PATH wins in the child, so the
	// empty PATH here guarantees no real system tools are touched.
	env = append(env, "PATH=")
	cmd.Env = append(os.Environ(), env...)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}
	return code, out.String(), errb.String()
}

func TestHelpBeforeAnyLaunch(t *testing.T) {
	code, out, _ := run(t, []string{"-h"})
	if code != 0 {
		t.Fatalf("--help exit %d, want 0", code)
	}
	for _, want := range []string{"--window-width", "--window-height", "--refresh-mhz", "--print-output-info", "--adb", "--xtmapper-args", "-h"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help omitted %s: %s", want, out)
		}
	}
	code, _, _ = run(t, []string{"--help"})
	if code != 0 {
		t.Fatalf("--help long form exit %d, want 0", code)
	}
}

func TestOptionValidation(t *testing.T) {
	for _, option := range []string{"--window-width", "--window-height", "--refresh-mhz"} {
		for _, bad := range []string{"nope", "0", "--late"} {
			code, _, stderr := run(t, []string{option, bad})
			if bad == "--late" {
				// a following --* word is treated as the missing value
				code, _, stderr = run(t, []string{option})
			}
			if code != 2 {
				t.Fatalf("%s %q exit %d, want 2", option, bad, code)
			}
			if strings.Contains(stderr, "Usage:") {
				t.Fatalf("%s %q printed usage; the shell only printed the error line", option, bad)
			}
		}
		// a missing value entirely
		code, _, stderr := run(t, []string{option})
		if code != 2 {
			t.Fatalf("%s with no value exit %d, want 2 (stderr %q)", option, code, stderr)
		}
	}
}

func TestUnknownOption(t *testing.T) {
	code, _, stderr := run(t, []string{"--not-an-option"})
	if code != 2 {
		t.Fatalf("unknown option exit %d, want 2", code)
	}
	if !strings.Contains(stderr, "invalid argument: --not-an-option") {
		t.Fatalf("unknown option error missing: %s", stderr)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Fatalf("unknown option did not print usage to stderr: %s", stderr)
	}
}

// TestOversizedNumerics follows the shell arithmetic validator: any
// positive value up to the 64-bit maximum is accepted and passes on
// to the env handling, then the flow stops with the missing-tool
// status on an empty PATH (not the validation status 2).
func TestOversizedNumerics(t *testing.T) {
	code, _, stderr := run(t, []string{"--window-width", "9223372036854775807"})
	if code != 127 {
		t.Fatalf("width %s exit %d, want 127 (stderr %q)", "9223372036854775807", code, stderr)
	}
	if !strings.Contains(stderr, "sudo was not found in PATH") {
		t.Fatalf("flow stopped before the tool check: %s", stderr)
	}
	// beyond the 64-bit arithmetic maximum the shell rejects the
	// value: exit 2
	code, _, stderr = run(t, []string{"--window-width", "99999999999999999999"})
	if code != 2 {
		t.Fatalf("20-digit width exit %d, want 2", code)
	}
	if strings.Contains(stderr, "Usage:") {
		t.Fatalf("value error printed usage: %s", stderr)
	}
}

func TestBootWaitEnvValidation(t *testing.T) {
	for _, bad := range []string{"0", "-1", "abc", "1.5"} {
		code, _, stderr := run(t, []string{"--adb"}, "XTMAPPER_BOOT_WAIT_SECONDS="+bad)
		if code != 2 {
			t.Fatalf("BOOT_WAIT_SECONDS=%q exit %d, want 2", bad, code)
		}
		if !strings.Contains(stderr, "XTMAPPER_BOOT_WAIT_SECONDS must be a positive integer") {
			t.Fatalf("BOOT_WAIT_SECONDS=%q did not report the error: %s", bad, stderr)
		}
	}
	// An empty value behaves as unset: the shell's ${VAR:-120}
	// default applies and the flow reaches the tool checks.
	code, _, stderr := run(t, []string{"--adb"}, "XTMAPPER_BOOT_WAIT_SECONDS=")
	if code != 127 {
		t.Fatalf("empty BOOT_WAIT_SECONDS exit %d, want 127 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, "adb was not found in PATH") {
		t.Fatalf("empty BOOT_WAIT_SECONDS did not apply the default: %s", stderr)
	}
}

func TestDefaultBootWaitIsValid(t *testing.T) {
	// Without XTMAPPER_BOOT_WAIT_SECONDS the flow must reach the
	// tool checks (missing adb on a clear PATH).
	code, _, stderr := run(t, []string{"--adb"}, "PATH=")
	if code != 127 {
		t.Fatalf("default boot wait flow exit %d, want 127 (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, "adb was not found in PATH") {
		t.Fatalf("missing adb error not reported: %s", stderr)
	}
}

func TestSelfExecSurvivesPathChange(t *testing.T) {
	image, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"unlink", "replace"} {
		t.Run(change, func(t *testing.T) {
			dir := t.TempDir()
			self := filepath.Join(dir, "cage-xtmapper")
			for name, body := range map[string][]byte{
				"cage-xtmapper": image,
				"sudo":          []byte("#!/bin/sh\nexec \"$@\"\n"),
				"waydroid": []byte(`#!/bin/sh
case "$1 ${2-}" in
    'session stop')
        : > "$MOCK_ROOT/ready"
        for i in $(seq 1 500); do
            [ ! -e "$MOCK_ROOT/continue" ] || exit 0
            sleep 0.01
        done
        exit 1 ;;
    'container start'|'show-full-ui ') exit 0 ;;
    'shell getprop')
        if [ "$3" = sys.boot_completed ]; then echo 1; else echo x86_64; fi ;;
    'shell pm') echo package:/data/app/xtmapper/base.apk ;;
    'shell --') cat > "$MOCK_ROOT/events" ;;
    *) exit 1 ;;
esac
`),
			} {
				if err := os.WriteFile(filepath.Join(dir, name), body, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			runtimeDir := filepath.Join(dir, "runtime")
			if err := os.Mkdir(runtimeDir, 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, self)
			cmd.Env = append(os.Environ(),
				"PATH="+dir+":/usr/bin:/bin", "MOCK_ROOT="+dir,
				"XDG_RUNTIME_DIR="+runtimeDir, "WLR_BACKENDS=headless", "WLR_RENDERER=pixman",
			)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			cmd.WaitDelay = 2 * time.Second
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
					break
				}
				if time.Now().After(deadline) {
					cancel()
					<-done
					t.Fatalf("startup did not reach the pause: %s", stderr.String())
				}
				time.Sleep(10 * time.Millisecond)
			}
			if change == "unlink" {
				err = os.Remove(self)
			} else {
				replacement := filepath.Join(dir, "replacement")
				if err = os.WriteFile(replacement, []byte("#!/bin/sh\necho wrong-executable >&2\nexit 91\n"), 0o755); err == nil {
					err = os.Rename(replacement, self)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "continue"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatalf("self exec after %s failed: %v; %s", change, err, stderr.String())
			}
			if _, err := os.Stat(filepath.Join(dir, "events")); err != nil {
				t.Fatalf("remote service never started after %s: %v", change, err)
			}
		})
	}
}
