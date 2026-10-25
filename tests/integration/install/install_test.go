// Installer tests use actual files, locks and SIGKILL; /bin/true is inert fixture data.
package install_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestLifecycleAndStableLock(t *testing.T) {
	h := newHarness(t, false)
	if status := h.status(); status.State != "not installed" {
		t.Fatal(status)
	}
	h.run("update", h.v1, 1)
	h.run("install", h.v1, 0)
	lock := stat(t, filepath.Join(h.root, ".install.lock"))
	if h.active() != "releases/"+h.id1 {
		t.Fatal("wrong active release")
	}
	launcher, err := os.Readlink(filepath.Join(h.bin, "cage-xtmapper"))
	must(t, err)
	if launcher != filepath.Join(h.root, "current/cage-xtmapper") {
		t.Fatal(launcher)
	}
	for _, name := range []string{"state.json", ".install.lock"} {
		path := filepath.Join(h.root, name)
		before, data := stat(t, path), readFile(t, path)
		if status := h.status(); status.State != "installed" {
			t.Fatal(status)
		}
		after := stat(t, path)
		if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || !bytes.Equal(data, readFile(t, path)) {
			t.Fatalf("status modified %s", path)
		}
	}
	stateBeforeRepeat := readFile(t, filepath.Join(h.root, "state.json"))
	h.run("install", h.v1, 0)
	h.run("update", h.v1, 0)
	if !bytes.Equal(stateBeforeRepeat, readFile(t, filepath.Join(h.root, "state.json"))) {
		t.Fatal("repeating the active release changed installation state")
	}
	h.onlyRelease(h.id1)
	h.run("update", h.v2, 0)
	h.onlyRelease(h.id2)
	h.run("uninstall", "", 0)
	assertAbsent(t, filepath.Join(h.bin, "cage-xtmapper"))
	entries, err := os.ReadDir(filepath.Join(h.root, "releases"))
	must(t, err)
	if len(entries) != 0 || h.status().State != "uninstalled" {
		t.Fatal("uninstall left a release")
	}
	h.run("uninstall", "", 1)
	h.run("install", h.v1, 0)
	if !os.SameFile(lock, stat(t, filepath.Join(h.root, ".install.lock"))) {
		t.Fatal("stable lock replaced")
	}
	h.onlyRelease(h.id1)
}

func TestBootstrapRecovery(t *testing.T) {
	h := newHarness(t, false)
	h.crash("install", h.v1, "bootstrap")
	assertAbsent(t, h.root)
	if status := h.status(); len(status.BootstrapCandidates) != 1 {
		t.Fatal(status)
	}
	h.run("recover", "", 0)
	if status := h.status(); status.State != "uninstalled" {
		t.Fatal(status)
	}
	h.run("recover", "", 0)
	h.run("install", h.v1, 0)
	h.onlyRelease(h.id1)
}

func TestInterruptedFreshInstall(t *testing.T) {
	for _, checkpoint := range []string{"before-commit", "after-commit"} {
		t.Run(checkpoint, func(t *testing.T) {
			h := newHarness(t, false)
			h.crash("install", h.v1, checkpoint)
			if checkpoint == "before-commit" {
				assertAbsent(t, filepath.Join(h.root, "current"))
				assertAbsent(t, filepath.Join(h.bin, "cage-xtmapper"))
			}
			h.run("recover", "", 0)
			h.run("recover", "", 0)
			if checkpoint == "before-commit" {
				h.run("install", h.v1, 0)
			}
			h.onlyRelease(h.id1)
		})
	}
}

func TestInterruptedUpdate(t *testing.T) {
	for _, checkpoint := range []string{"stage-created", "staging", "file-recorded", "staged-file", "links-created", "before-commit", "after-commit", "cleanup", "cleanup-file"} {
		t.Run(checkpoint, func(t *testing.T) {
			h := newHarness(t, false)
			h.run("install", h.v1, 0)
			old := stat(t, filepath.Join(h.root, "current/cage-xtmapper"))
			h.crash("update", h.v2, checkpoint)
			committed := checkpoint == "after-commit" || checkpoint == "cleanup" || checkpoint == "cleanup-file"
			want := h.id1
			if committed {
				want = h.id2
			}
			if h.active() != "releases/"+want {
				t.Fatal("wrong active release after crash")
			}
			if !committed && !os.SameFile(old, stat(t, filepath.Join(h.root, "current/cage-xtmapper"))) {
				t.Fatal("precommit changed old executable")
			}
			status := h.status()
			if status.Transaction == nil || status.Transaction.Committed != committed {
				t.Fatal(status)
			}
			h.run("update", h.v2, 4)
			h.run("recover", "", 0)
			h.onlyRelease(want)
			h.run("recover", "", 0)
		})
	}
}

func TestCleanupFailureRetainsNewReleaseAndJournal(t *testing.T) {
	h := newHarness(t, false)
	h.run("install", h.v1, 0)
	h.crash("update", h.v2, "after-commit")
	path := filepath.Join(h.root, "releases", h.id1, "LICENSES/fixture.txt")
	original := readFile(t, path)
	changed := append(bytes.Clone(original), []byte("User edit\n")...)
	writeFile(t, path, changed, 0644)
	h.run("recover", "", 4)
	if h.active() != "releases/"+h.id2 || !bytes.Equal(readFile(t, path), changed) {
		t.Fatal("failed cleanup lost active release or user content")
	}
	stat(t, filepath.Join(h.root, "transaction.json"))
	writeFile(t, path, original, 0644)
	h.run("recover", "", 0)
	h.onlyRelease(h.id2)
}

func TestUninstallRecovery(t *testing.T) {
	for _, checkpoint := range []string{"before-commit", "after-commit", "cleanup-file"} {
		t.Run(checkpoint, func(t *testing.T) {
			h := newHarness(t, false)
			h.run("install", h.v1, 0)
			h.crash("uninstall", "", checkpoint)
			h.run("recover", "", 0)
			if checkpoint == "before-commit" {
				h.onlyRelease(h.id1)
				h.run("uninstall", "", 0)
			}
			assertAbsent(t, filepath.Join(h.bin, "cage-xtmapper"))
			if h.status().State != "uninstalled" {
				t.Fatal("uninstall recovery left active release")
			}
			h.run("recover", "", 0)
		})
	}
}

func TestAtomicActivation(t *testing.T) {
	h := newHarness(t, false)
	h.run("install", h.v1, 0)
	p := h.pause("update", h.v2, "before-commit")
	if h.active() != "releases/"+h.id1 {
		t.Fatal("release changed before commit")
	}
	must(t, p.cmd.Process.Signal(syscall.SIGCONT))
	for {
		current := h.active()
		if current != "releases/"+h.id1 && current != "releases/"+h.id2 {
			t.Fatal("non-atomic current:", current)
		}
		select {
		case <-p.done:
			h.wait(p, 0)
			h.onlyRelease(h.id2)
			return
		default:
		}
	}
}

func TestLegacyRefusal(t *testing.T) {
	h := newHarness(t, false)
	for _, name := range []string{"cage_xtmapper", "cage_xtmapper.sh"} {
		path := filepath.Join(h.bin, name)
		writeFile(t, path, []byte("unowned user file\n"), 0644)
		_, stderr := h.run("install", h.v1, 1)
		if !strings.Contains(stderr, "legacy conflict") || string(readFile(t, path)) != "unowned user file\n" {
			t.Fatal(stderr)
		}
		assertAbsent(t, h.root)
		must(t, os.Remove(path))
	}
	h.run("install", h.v1, 0)
	path := filepath.Join(h.bin, "cage_xtmapper.sh")
	writeFile(t, path, []byte("added after install\n"), 0644)
	h.run("update", h.v2, 1)
	if h.active() != "releases/"+h.id1 {
		t.Fatal("legacy conflict activated update")
	}
	h.run("uninstall", "", 0)
	if string(readFile(t, path)) != "added after install\n" {
		t.Fatal("uninstall removed legacy data")
	}
}

func TestKnownLegacyShellAndBinaryMigrateAutomatically(t *testing.T) {
	h := newHarness(t, false)
	scriptPath := filepath.Join(h.bin, "cage_xtmapper.sh")
	binaryPath := filepath.Join(h.bin, "cage_xtmapper")
	legacy := []byte("#!/usr/bin/env bash\nBOOT_WAIT_SECONDS=120\n# cage_xtmapper legacy launcher\n# xtr.keymapper.server.RemoteServiceShell\n")
	writeFile(t, scriptPath, legacy, 0755)
	testExecutable, err := os.Executable()
	must(t, err)
	binary, err := os.ReadFile(testExecutable)
	must(t, err)
	writeFile(t, binaryPath, binary, 0755)

	stdout, _ := h.run("install", h.v1, 0)
	assertAbsent(t, scriptPath)
	assertAbsent(t, binaryPath)
	if !strings.Contains(stdout, "Migrated legacy launcher to backup:") {
		t.Fatal(stdout)
	}
	entries, err := os.ReadDir(h.bin)
	must(t, err)
	foundScript, foundBinary := false, false
	for _, entry := range entries {
		switch {
		case strings.HasPrefix(entry.Name(), "cage_xtmapper.sh.legacy-backup-"):
			foundScript = true
			if !bytes.Equal(readFile(t, filepath.Join(h.bin, entry.Name())), legacy) {
				t.Fatal("legacy shell backup content changed")
			}
		case strings.HasPrefix(entry.Name(), "cage_xtmapper.legacy-backup-"):
			foundBinary = true
			if !bytes.Equal(readFile(t, filepath.Join(h.bin, entry.Name())), binary) {
				t.Fatal("legacy binary backup content changed")
			}
		}
	}
	if !foundScript || !foundBinary {
		t.Fatalf("legacy pair was not preserved: script=%t binary=%t", foundScript, foundBinary)
	}
	h.onlyRelease(h.id1)
}

func TestInstallAutomaticallyUpdatesNewerRelease(t *testing.T) {
	h := newHarness(t, false)
	h.run("install", h.v1, 0)
	h.run("install", h.v2, 0)
	if h.active() != "releases/"+h.id2 {
		t.Fatal("install did not activate the newer release")
	}
	h.onlyRelease(h.id2)
}

func TestUninstallPreservesUnrelatedData(t *testing.T) {
	h := newHarness(t, false)
	h.run("install", h.v1, 0)
	paths := []string{"var/lib/waydroid/data", "home/user/.local/share/applications/steam.desktop", "home/user/apk"}
	for _, relative := range paths {
		path := filepath.Join(h.prefix, relative)
		must(t, os.MkdirAll(filepath.Dir(path), 0755))
		writeFile(t, path, []byte("user data\n"), 0644)
	}
	note := filepath.Join(h.root, "user-note")
	writeFile(t, note, []byte("not installer data\n"), 0644)
	link, outside := filepath.Join(h.root, "user-link"), filepath.Join(h.bin, "user-tool")
	must(t, os.Symlink(filepath.Join(h.prefix, "var/lib/waydroid"), link))
	must(t, os.Symlink(note, outside))
	stdout, _ := h.run("uninstall", "", 0)
	if !strings.Contains(stdout, "Preserved unowned path") || string(readFile(t, note)) != "not installer data\n" {
		t.Fatal(stdout)
	}
	for _, path := range []string{link, outside} {
		_, err := os.Readlink(path)
		must(t, err)
	}
	for _, relative := range paths {
		if string(readFile(t, filepath.Join(h.prefix, relative))) != "user data\n" {
			t.Fatal("removed unrelated data:", relative)
		}
	}
}

func TestUnknownReleaseDataIsNotDeleted(t *testing.T) {
	h := newHarness(t, false)
	h.run("install", h.v1, 0)
	path := filepath.Join(h.root, "current/user-note")
	writeFile(t, path, []byte("unowned\n"), 0644)
	h.run("uninstall", "", 1)
	if string(readFile(t, path)) != "unowned\n" || h.active() != "releases/"+h.id1 {
		t.Fatal("deleted unowned release data")
	}
	assertAbsent(t, filepath.Join(h.root, "transaction.json"))
}

func TestReplacedLauncherAndLockAreNotAdopted(t *testing.T) {
	h := newHarness(t, false)
	h.run("install", h.v1, 0)
	launcher := filepath.Join(h.bin, "cage-xtmapper")
	target, err := os.Readlink(launcher)
	must(t, err)
	replacement := filepath.Join(h.bin, "replacement")
	must(t, os.Symlink(target, replacement))
	must(t, os.Rename(replacement, launcher))
	h.run("uninstall", "", 1)
	_, err = os.Readlink(launcher)
	must(t, err)
	lock := filepath.Join(h.root, ".install.lock")
	replacement = filepath.Join(h.root, "replacement-lock")
	writeFile(t, replacement, readFile(t, lock), 0644)
	must(t, os.Rename(replacement, lock))
	h.run("update", h.v2, 1)
	if h.active() != "releases/"+h.id1 {
		t.Fatal("adopted substituted lock")
	}
}

func TestSymlinkComponentsAndUnknownRoots(t *testing.T) {
	h := newHarness(t, false)
	outside := filepath.Join(h.workspace, "outside")
	must(t, os.Mkdir(outside, 0755))
	keep := filepath.Join(outside, "keep")
	writeFile(t, keep, []byte("unchanged\n"), 0644)
	must(t, os.Symlink(outside, h.root))
	h.run("install", h.v1, 1)
	if string(readFile(t, keep)) != "unchanged\n" {
		t.Fatal("followed root symlink")
	}
	must(t, os.Remove(h.root))
	launcher := filepath.Join(h.bin, "cage-xtmapper")
	must(t, os.Symlink(keep, launcher))
	h.run("install", h.v1, 1)
	_, err := os.Readlink(launcher)
	must(t, err)
	must(t, os.Remove(launcher))
	must(t, os.Mkdir(h.root, 0755))
	path := filepath.Join(h.root, "state.json")
	writeFile(t, path, []byte("{}\n"), 0644)
	h.run("install", h.v1, 1)
	if string(readFile(t, path)) != "{}\n" {
		t.Fatal("overwrote unknown metadata")
	}
}

func TestWritableTestRootRefused(t *testing.T) {
	h := newHarness(t, false)
	must(t, os.Chmod(h.prefix, 0777))
	h.run("install", h.v1, 1)
	assertAbsent(t, h.root)
}

func TestKernelLockBlocksMutations(t *testing.T) {
	h := newHarness(t, false)
	h.run("install", h.v1, 0)
	lock, err := os.Open(filepath.Join(h.root, ".install.lock"))
	must(t, err)
	defer lock.Close()
	must(t, syscall.Flock(int(lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB))
	for _, operation := range []string{"update", "uninstall", "recover"} {
		archive := ""
		if operation == "update" {
			archive = h.v2
		}
		_, stderr := h.run(operation, archive, 3)
		if !strings.Contains(stderr, "busy") {
			t.Fatal(stderr)
		}
	}
	h.onlyRelease(h.id1)
}

func TestSpecialFilesFailWithoutBlocking(t *testing.T) {
	h := newHarness(t, false)
	fifo := filepath.Join(h.workspace, "fifo")
	must(t, syscall.Mkfifo(fifo, 0600))
	_, stderr := h.run("install", fifo, 1)
	if !strings.Contains(stderr, "regular file") {
		t.Fatal(stderr)
	}
	assertAbsent(t, h.root)
	h.run("install", h.v1, 0)
	state := filepath.Join(h.root, "state.json")
	must(t, os.Rename(state, filepath.Join(h.root, "saved-state.json")))
	must(t, syscall.Mkfifo(state, 0644))
	h.run("uninstall", "", 1)
	if h.active() != "releases/"+h.id1 {
		t.Fatal("special metadata changed active release")
	}
}

func TestPrecommitConflictsPreserveOldRelease(t *testing.T) {
	h := newHarness(t, false)
	h.run("install", h.v1, 0)
	lock := stat(t, filepath.Join(h.root, ".install.lock"))
	destination := filepath.Join(h.root, "releases", h.id2)
	must(t, os.Mkdir(destination, 0755))
	note := filepath.Join(destination, "user-data")
	writeFile(t, note, []byte("unowned\n"), 0644)
	h.run("update", h.v2, 1)
	if string(readFile(t, note)) != "unowned\n" || h.active() != "releases/"+h.id1 {
		t.Fatal("overwrote unknown destination")
	}
	must(t, os.Remove(note))
	must(t, os.Remove(destination))
	p := h.pause("update", h.v2, "before-commit")
	legacy := filepath.Join(h.bin, "cage_xtmapper.sh")
	must(t, os.Symlink(filepath.Join(h.workspace, "unrelated-target"), legacy))
	must(t, p.cmd.Process.Signal(syscall.SIGCONT))
	h.wait(p, 4)
	if h.active() != "releases/"+h.id1 {
		t.Fatal("precommit failure changed active release")
	}
	_, err := os.Readlink(legacy)
	must(t, err)
	h.run("recover", "", 0)
	h.onlyRelease(h.id1)
	if !os.SameFile(lock, stat(t, filepath.Join(h.root, ".install.lock"))) {
		t.Fatal("lock replaced")
	}
}

func TestRecoveryPreservesSymlinkSubstitution(t *testing.T) {
	h := newHarness(t, false)
	h.run("install", h.v1, 0)
	h.crash("update", h.v2, "after-commit")
	original := filepath.Join(h.root, "releases", h.id1, "LICENSES")
	moved := filepath.Join(h.workspace, "moved-licenses")
	must(t, os.Rename(original, moved))
	must(t, os.Symlink(moved, original))
	h.run("recover", "", 4)
	_, err := os.Readlink(original)
	must(t, err)
	stat(t, filepath.Join(moved, "fixture.txt"))
	if h.active() != "releases/"+h.id2 {
		t.Fatal("postcommit failure reverted active release")
	}
	must(t, os.Remove(original))
	must(t, os.Rename(moved, original))
	h.run("recover", "", 0)
	h.onlyRelease(h.id2)
}
