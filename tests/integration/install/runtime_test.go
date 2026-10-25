package install_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Xtr126/cage-xtmapper/internal/release"
)

func (h *harness) runtimeEnv() []string {
	h.t.Helper()
	directory := filepath.Join(h.workspace, "runtime")
	must(h.t, os.Mkdir(directory, 0700))
	return append(cleanEnv(), "XDG_RUNTIME_DIR="+directory, "WLR_BACKENDS=headless", "WLR_RENDERER=pixman")
}

func runCommand(t *testing.T, directory, name string, args ...string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = directory, cleanEnv()
	output, err := cmd.CombinedOutput()
	if cmd.ProcessState == nil {
		t.Fatalf("command did not start: %v", err)
	}
	return string(output), cmd.ProcessState.ExitCode()
}

func TestRuntimeRejectsReplacedOrSpecialMetadata(t *testing.T) {
	h := newHarness(t, true)
	h.run("install", h.v1, 0)
	state := filepath.Join(h.root, "state.json")
	original := readFile(t, state)
	must(t, os.Rename(state, filepath.Join(h.root, "saved-state.json")))
	must(t, syscall.Mkfifo(state, 0644))
	if output, code := runCommand(t, h.repo, filepath.Join(h.bin, release.App), "--help"); code != 1 {
		t.Fatalf("expected unsafe state refusal: %d %s", code, output)
	}
	must(t, os.Remove(state))
	writeFile(t, state, original, 0644)
	if output, code := runCommand(t, h.repo, filepath.Join(h.bin, release.App), "--help"); code != 1 {
		t.Fatalf("expected stale state inode refusal: %d %s", code, output)
	}
}

func TestRuntimeRefusesAdminContentionAndStaleInode(t *testing.T) {
	h := newHarness(t, true)
	h.run("install", h.v1, 0)
	old, err := os.Open(filepath.Join(h.bin, release.App))
	must(t, err)
	defer old.Close()
	p := h.pause("update", h.v2, "before-commit")
	output, code := runCommand(t, h.repo, filepath.Join(h.bin, release.App), "--help")
	if code != 1 || !strings.Contains(output, "busy") {
		t.Fatalf("runtime/admin contention: %d %s", code, output)
	}
	must(t, p.cmd.Process.Signal(syscall.SIGCONT))
	h.wait(p, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", "--help")
	cmd.ExtraFiles, cmd.Env = []*os.File{old}, cleanEnv()
	data, _ := cmd.CombinedOutput()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 || !bytes.Contains(data, []byte("active release changed")) {
		t.Fatalf("stale executable was not refused: %s", data)
	}
	h.onlyRelease(h.id2)
}

func TestGeneratedArchiveInstallation(t *testing.T) {
	h := newHarness(t, true)
	archive := filepath.Join(h.workspace, "packaged.tar")
	output, code := runCommand(t, h.repo, "go", "run", "./tools/release", "package", "--version", "v0.3.0-test", "--output", archive)
	if code != 0 {
		t.Fatalf("package failed: %s", output)
	}
	manifest, files, err := release.ReadArchive(archive, "")
	must(t, err)
	if manifest.ELFSHA256 != release.Digest(h.binary) {
		t.Fatal("package has wrong executable")
	}
	var info struct {
		NativeCommits map[string]string `json:"native_commits"`
	}
	must(t, json.Unmarshal(readFile(t, filepath.Join(h.repo, "build/go/build-info.json")), &info))
	if manifest.NativeCommits["cage"] != info.NativeCommits["cage"] || manifest.NativeCommits["wlroots"] != info.NativeCommits["wlroots"] {
		t.Fatal("package provenance changed")
	}
	before := stat(t, archive)
	if _, code := runCommand(t, h.repo, "go", "run", "./tools/release", "package", "--version", "v0.3.0-test", "--output", archive); code == 0 {
		t.Fatal("packager replaced an existing archive")
	}
	if !os.SameFile(before, stat(t, archive)) {
		t.Fatal("packager changed existing archive inode")
	}
	h.run("install", archive, 0)
	h.onlyRelease(manifest.ReleaseID)
	for name, data := range files {
		if !bytes.Equal(readFile(t, filepath.Join(h.root, "current", name)), data) {
			t.Fatalf("installed bytes differ: %s", name)
		}
	}
	output, code = runCommand(t, h.repo, filepath.Join(h.bin, release.App), "--help")
	if code != 0 || !strings.Contains(output, "Usage:") {
		t.Fatalf("installed runtime: %d %s", code, output)
	}
	h.run("uninstall", "", 0)
}

func TestNativeRuntimeBlocksAdminWithoutLeakingLock(t *testing.T) {
	h := newHarness(t, true)
	h.run("install", h.v1, 0)
	pidfile := filepath.Join(h.workspace, "client.pid")
	p := h.start([]string{filepath.Join(h.bin, release.App), "--internal-cage", "--", "sh", "-c", `echo $$ > "$PIDFILE"; exec sleep 600`}, append(h.runtimeEnv(), "PIDFILE="+pidfile))
	client := h.waitPID(pidfile, p)
	descriptors, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", client))
	must(t, err)
	for _, descriptor := range descriptors {
		target, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", client, descriptor.Name()))
		if os.IsNotExist(err) {
			continue
		}
		must(t, err)
		if target == filepath.Join(h.root, ".install.lock") {
			t.Fatal("native client inherited installation lock")
		}
	}
	h.run("update", h.v2, 3)
	h.run("uninstall", "", 3)
	must(t, p.cmd.Process.Signal(syscall.SIGTERM))
	h.wait(p, 143)
	h.run("uninstall", "", 0)
}

func TestParentDeathKeepsNativeLock(t *testing.T) {
	h := newHarness(t, true)
	h.run("install", h.v1, 0)
	mocks := filepath.Join(h.workspace, "mocks")
	must(t, os.Mkdir(mocks, 0755))
	for name, text := range map[string]string{
		"waydroid": "#!/bin/bash\nprintf '%s\\n' \"$PPID\" > \"$TEST_STATE/native.pid\"\nprintf '%s\\n' \"$$\" > \"$TEST_STATE/client.pid\"\nexec sleep 600\n",
		"adb":      "#!/bin/sh\necho 0\n",
	} {
		writeFile(t, filepath.Join(mocks, name), []byte(text), 0755)
	}
	env := append(h.runtimeEnv(), "PATH="+mocks+":/usr/bin:/bin", "TEST_STATE="+h.workspace, "XTMAPPER_BOOT_WAIT_SECONDS=120")
	p := h.start([]string{filepath.Join(h.bin, release.App), "--adb"}, env)
	native := h.waitPID(filepath.Join(h.workspace, "native.pid"), p)
	h.waitPID(filepath.Join(h.workspace, "client.pid"), p)
	target, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/3", native))
	must(t, err)
	if target != filepath.Join(h.root, ".install.lock") {
		t.Fatal("native did not inherit shared lock descriptor")
	}
	must(t, p.cmd.Process.Kill())
	h.wait(p, -1)
	must(t, syscall.Kill(native, 0))
	h.run("update", h.v2, 3)
	h.run("uninstall", "", 3)
	must(t, syscall.Kill(native, syscall.SIGTERM))
	lock, err := os.Open(filepath.Join(h.root, ".install.lock"))
	must(t, err)
	deadline := time.Now().Add(10 * time.Second)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK || !time.Now().Before(deadline) {
			lock.Close()
			t.Fatal("native lock was not released:", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	must(t, lock.Close())
	h.run("uninstall", "", 0)
}
