// Package installlock protects a managed release for the process's lifetime.
package installlock

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const inheritedFD = "CAGE_XTMAPPER_LOCK_FD"

// Acquire locates the installation from the running inode, never through current.
func Acquire(internal bool) (*os.File, error) {
	inherited := os.Getenv(inheritedFD)
	os.Unsetenv(inheritedFD)
	executable, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return nil, err
	}
	executable = strings.TrimSuffix(executable, " (deleted)")
	releaseDir := filepath.Dir(executable)
	releases := filepath.Dir(releaseDir)
	if filepath.Base(releases) != "releases" || filepath.Base(executable) != "cage-xtmapper" {
		if internal && inherited != "" {
			return nil, fmt.Errorf("inherited installation lock without a managed release")
		}
		return nil, nil // Source-tree executables have no managed installation.
	}
	root := filepath.Dir(releases)
	lockPath := filepath.Join(root, ".install.lock")
	lockInfo, err := os.Lstat(lockPath)
	if err != nil {
		return nil, err
	}
	owner := lockInfo.Sys().(*syscall.Stat_t).Uid
	if lockInfo.Mode() != 0644 || lockInfo.Sys().(*syscall.Stat_t).Nlink != 1 || (owner != 0 && owner != uint32(os.Geteuid())) {
		return nil, fmt.Errorf("unsafe installation lock: %s", lockPath)
	}
	for path := root; path != "/"; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		uid := info.Sys().(*syscall.Stat_t).Uid
		// Test prefixes can live beneath a root-owned sticky temporary directory.
		sticky := owner != 0 && path != root && uid == 0 && info.Mode()&os.ModeSticky != 0
		if !info.IsDir() || (uid != 0 && uid != owner) || (info.Mode().Perm()&0022 != 0 && !sticky) {
			return nil, fmt.Errorf("unsafe installation directory: %s", path)
		}
	}
	var lock *os.File
	if internal && inherited != "" {
		if inherited != "3" {
			return nil, fmt.Errorf("invalid inherited installation lock descriptor")
		}
		syscall.CloseOnExec(3) // The native client and other tools must not inherit this lock.
		lock = os.NewFile(3, lockPath)
	} else {
		fd, err := syscall.Open(lockPath, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		lock = os.NewFile(uintptr(fd), lockPath)
	}
	if err := validate(lock, lockInfo, root, releaseDir); err != nil {
		lock.Close()
		return nil, err
	}
	return lock, nil
}

func validate(lock *os.File, expected os.FileInfo, root, releaseDir string) error {
	info, err := lock.Stat()
	if err != nil || !os.SameFile(info, expected) {
		return fmt.Errorf("installation lock identity changed")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("installation is busy: %w", err)
	}
	fd, err := syscall.Open(filepath.Join(root, "state.json"), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	stateFile := os.NewFile(uintptr(fd), "state.json")
	defer stateFile.Close()
	stateInfo, err := stateFile.Stat()
	if err != nil {
		return err
	}
	lockStat := info.Sys().(*syscall.Stat_t)
	stateStat := stateInfo.Sys().(*syscall.Stat_t)
	if stateInfo.Mode() != 0644 || stateStat.Nlink != 1 || stateStat.Uid != lockStat.Uid || stateInfo.Size() > 1<<20 {
		return fmt.Errorf("unsafe installation state")
	}
	type identity struct {
		Dev uint64 `json:"dev"`
		Ino uint64 `json:"ino"`
	}
	var state struct {
		SchemaVersion int      `json:"schema_version"`
		Application   string   `json:"application"`
		Root          string   `json:"root"`
		Self          identity `json:"self"`
		LockIdentity  identity `json:"lock_identity"`
	}
	if err := json.NewDecoder(stateFile).Decode(&state); err != nil {
		return err
	}
	if state.SchemaVersion != 1 || state.Application != "cage-xtmapper" || state.Root != root || state.Self != (identity{uint64(stateStat.Dev), stateStat.Ino}) || state.LockIdentity != (identity{uint64(lockStat.Dev), lockStat.Ino}) {
		return fmt.Errorf("installation ownership record does not match the lock")
	}
	// Only now may current be resolved. A pre-lock update makes this launch stale.
	target, err := os.Readlink(filepath.Join(root, "current"))
	if err != nil || target != "releases/"+filepath.Base(releaseDir) {
		return fmt.Errorf("active release changed during launch; retry cage-xtmapper")
	}
	for _, path := range []string{filepath.Dir(releaseDir), releaseDir} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode().Perm()&0022 != 0 || info.Sys().(*syscall.Stat_t).Uid != lockStat.Uid {
			return fmt.Errorf("unsafe release directory: %s", path)
		}
	}
	active, err := os.Stat(filepath.Join(root, "current", "cage-xtmapper"))
	if err != nil {
		return err
	}
	running, err := os.Stat("/proc/self/exe")
	if err != nil || !os.SameFile(active, running) {
		return fmt.Errorf("active executable changed during launch; retry cage-xtmapper")
	}
	return nil
}

// Pass keeps the same flock open description alive across exec and parent death.
func Pass(cmd *exec.Cmd, lock *os.File) {
	if lock != nil {
		cmd.ExtraFiles = []*os.File{lock}
		cmd.Env = append(os.Environ(), inheritedFD+"=3")
	}
}
