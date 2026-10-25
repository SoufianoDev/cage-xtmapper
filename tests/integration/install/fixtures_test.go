package install_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Xtr126/cage-xtmapper/internal/release"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	must(t, err)
	return data
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	must(t, os.WriteFile(path, data, mode))
	must(t, os.Chmod(path, mode))
}

func jsonBytes(t *testing.T, value any) []byte {
	t.Helper()
	data, err := release.JSON(value)
	must(t, err)
	return data
}

func fixture(t *testing.T, binary []byte, version string) (map[string][]byte, string) {
	t.Helper()
	arch, err := release.ELFArch(binary)
	must(t, err)
	dirty := true
	id := version + "-" + arch + "-" + release.Digest(binary)[:16]
	manifest := release.Manifest{
		SchemaVersion: release.ManifestSchemaVersion, Application: release.App, Version: version, ReleaseID: id,
		Target: map[string]string{"os": "linux", "architecture": arch}, MainCommit: strings.Repeat("1", 40),
		MainDirty: &dirty, SourceSHA256: strings.Repeat("4", 64), ELFSHA256: release.Digest(binary),
		NativeCommits:       map[string]string{"cage": strings.Repeat("2", 40), "wlroots": strings.Repeat("3", 40)},
		NativeSourceDigests: map[string]string{"cage": strings.Repeat("6", 64), "wlroots": strings.Repeat("7", 64)},
		NativeOrigins: map[string]release.NativeOrigin{
			"cage":    {URL: "https://example.invalid/cage.git", Revision: strings.Repeat("8", 40)},
			"wlroots": {URL: "https://example.invalid/wlroots.git", Revision: strings.Repeat("9", 40)},
		},
		Toolchains:          map[string]string{"go": "fixture", "meson": "fixture", "c": "fixture"},
		Build:               release.Build{CGOEnabled: true, MesonOptions: map[string]any{"fixture": true}, GoFlags: []string{}, GoBuildInfo: "fixture", NativeInputsSHA256: strings.Repeat("5", 64)},
		Dependencies:        []release.Dependency{{Name: "fixture", Version: "1", Selection: "test"}},
		RuntimeRequirements: release.RuntimeRequirements{SharedLibraries: []string{}, Commands: []string{}, Android: "fixture", BuildHost: "fixture"},
		Licenses:            []release.License{{Component: "fixture", License: "test notice", Path: "LICENSES/fixture.txt"}},
	}
	files := map[string][]byte{release.App: binary, "manifest.json": jsonBytes(t, manifest), "LICENSES/fixture.txt": []byte("Test fixture; not a distributable release.\n")}
	files["SHA256SUMS"] = release.Checksums(files)
	return files, id
}

func archiveBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	must(t, release.WriteArchive(&buffer, files))
	return buffer.Bytes()
}

type harness struct {
	t                                  *testing.T
	repo, workspace, prefix, root, bin string
	v1, v2, id1, id2                   string
	binary                             []byte
}

func newHarness(t *testing.T, native bool) *harness {
	t.Helper()
	repo, err := filepath.Abs("../../..")
	must(t, err)
	binary := "/bin/true"
	if native {
		if os.Geteuid() == 0 {
			t.Skip("real compositor tests require an unprivileged test user")
		}
		binary = filepath.Join(repo, "build/go", release.App)
		if _, err := os.Stat(binary); os.IsNotExist(err) {
			t.Skip("run ./build.sh before real runtime/install integration tests")
		}
	}
	home, err := os.UserHomeDir()
	must(t, err)
	workspace, err := os.MkdirTemp(home, ".cage-install-test-*")
	must(t, err)
	t.Cleanup(func() { must(t, os.RemoveAll(workspace)) })
	h := &harness{t: t, repo: repo, workspace: workspace, prefix: filepath.Join(workspace, "prefix"), binary: readFile(t, binary)}
	must(t, os.Mkdir(h.prefix, 0700))
	for _, name := range []string{"opt", "usr", "usr/local", "usr/local/bin"} {
		path := filepath.Join(h.prefix, name)
		must(t, os.Mkdir(path, 0755))
		must(t, os.Chmod(path, 0755))
	}
	h.root, h.bin = filepath.Join(h.prefix, "opt", release.App), filepath.Join(h.prefix, "usr/local/bin")
	h.v1, h.id1 = h.archive("v1")
	h.v2, h.id2 = h.archive("v2")
	return h
}

func (h *harness) archive(version string) (string, string) {
	h.t.Helper()
	files, id := fixture(h.t, h.binary, version)
	path := filepath.Join(h.workspace, version+".tar")
	writeFile(h.t, path, archiveBytes(h.t, files), 0644)
	return path, id
}

func (h *harness) args(operation, archive string) []string {
	args := []string{operation, "--test-prefix", h.prefix}
	if archive != "" {
		args = append(args, "--archive", archive)
	}
	return args
}

func cleanEnv() []string {
	var env []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "CAGE_XTMAPPER_TEST_PAUSE=") && !strings.HasPrefix(value, "CAGE_XTMAPPER_LOCK_FD=") {
			env = append(env, value)
		}
	}
	return env
}

func (h *harness) run(operation, archive string, code int) (string, string) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(h.repo, "install.sh"), h.args(operation, archive)...)
	cmd.Env = cleanEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != code {
		h.t.Fatalf("installer %s: want exit %d, got %v\n%s%s", operation, code, err, &stdout, &stderr)
	}
	return stdout.String(), stderr.String()
}

func (h *harness) active() string {
	h.t.Helper()
	target, err := os.Readlink(filepath.Join(h.root, "current"))
	must(h.t, err)
	return target
}

func (h *harness) onlyRelease(id string) {
	h.t.Helper()
	entries, err := os.ReadDir(filepath.Join(h.root, "releases"))
	must(h.t, err)
	if len(entries) != 1 || entries[0].Name() != id {
		h.t.Fatalf("expected only release %s, got %v", id, entries)
	}
	assertAbsent(h.t, filepath.Join(h.root, "transaction.json"))
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent; got %v", path, err)
	}
}

func stat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	must(t, err)
	return info
}

type installStatus struct {
	State               string                    `json:"state"`
	ActiveRelease       string                    `json:"active_release"`
	BootstrapCandidates []string                  `json:"bootstrap_candidates"`
	Transaction         *struct{ Committed bool } `json:"transaction"`
}

func (h *harness) status() installStatus {
	h.t.Helper()
	stdout, _ := h.run("status", "", 0)
	var status installStatus
	must(h.t, json.Unmarshal([]byte(stdout), &status))
	return status
}

type process struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	log  string
}

func (h *harness) start(args, env []string) *process {
	h.t.Helper()
	log, err := os.CreateTemp(h.workspace, "process-*")
	must(h.t, err)
	h.t.Cleanup(func() { log.Close() })
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout, cmd.Stderr, cmd.Env = log, log, env
	must(h.t, cmd.Start())
	p := &process{cmd: cmd, done: make(chan struct{}), log: log.Name()}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	h.t.Cleanup(func() {
		select {
		case <-p.done:
			return
		default:
			cmd.Process.Kill()
		}
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			h.t.Errorf("process %d did not exit after SIGKILL", cmd.Process.Pid)
		}
	})
	return p
}

func (h *harness) pause(operation, archive, checkpoint string) *process {
	h.t.Helper()
	ready := filepath.Join(h.prefix, "checkpoint")
	if err := os.Remove(ready); err != nil && !os.IsNotExist(err) {
		h.t.Fatal(err)
	}
	args := append([]string{filepath.Join(h.repo, "install.sh")}, h.args(operation, archive)...)
	p := h.start(args, append(cleanEnv(), "CAGE_XTMAPPER_TEST_PAUSE="+checkpoint))
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(ready)
		status, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", p.cmd.Process.Pid))
		if string(data) == checkpoint && bytes.Contains(status, []byte("State:\tT")) {
			return p
		}
		select {
		case <-p.done:
			h.t.Fatalf("installer exited before %s: %v\n%s", checkpoint, p.err, readFile(h.t, p.log))
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("installer did not pause at %s\n%s", checkpoint, readFile(h.t, p.log))
	return nil
}

func (h *harness) wait(p *process, code int) {
	h.t.Helper()
	select {
	case <-p.done:
		if p.cmd.ProcessState.ExitCode() != code {
			h.t.Fatalf("expected exit %d, got %v\n%s", code, p.err, readFile(h.t, p.log))
		}
	case <-time.After(15 * time.Second):
		h.t.Fatalf("process did not exit\n%s", readFile(h.t, p.log))
	}
}

func (h *harness) crash(operation, archive, checkpoint string) {
	h.t.Helper()
	p := h.pause(operation, archive, checkpoint)
	must(h.t, p.cmd.Process.Kill())
	h.wait(p, -1)
	if p.cmd.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		h.t.Fatal("installer did not exit from SIGKILL")
	}
}

func (h *harness) waitPID(path string, p *process) int {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(path)
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			h.t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
			return pid
		}
		select {
		case <-p.done:
			h.t.Fatalf("application exited early: %v\n%s", p.err, readFile(h.t, p.log))
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("application did not create %s\n%s", path, readFile(h.t, p.log))
	return 0
}
