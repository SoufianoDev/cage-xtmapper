package installer

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

type identity struct {
	Dev  uint64 `json:"dev"`
	Ino  uint64 `json:"ino"`
	UID  uint32 `json:"uid"`
	GID  uint32 `json:"gid"`
	Mode uint32 `json:"mode"`
}

type record struct {
	identity
	SHA256 string `json:"sha256,omitempty"`
	Target string `json:"target,omitempty"`
}

func identify(info os.FileInfo) identity {
	st := info.Sys().(*syscall.Stat_t)
	return identity{uint64(st.Dev), st.Ino, st.Uid, st.Gid, st.Mode}
}

func randomID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func validID(id string) bool {
	data, err := hex.DecodeString(id)
	return err == nil && len(data) == 16 && hex.EncodeToString(data) == id
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return !os.IsNotExist(err)
}

func openFile(path string, flags int, mode uint32) (*os.File, error) {
	fd, err := syscall.Open(path, flags|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, mode)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

func syncDir(path string) error {
	file, err := openFile(path, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func temporaryFile(directory string) (*os.File, error) {
	// O_TMPFILE leaves no partial named file after SIGKILL or power loss.
	return openFile(directory, syscall.O_RDWR|syscall.O_DIRECTORY|0x400000, 0600)
}

func renameNew(source, target string) error {
	var number uintptr
	switch runtime.GOARCH {
	case "amd64":
		number = 316
	case "arm64":
		number = 276
	default:
		return fmt.Errorf("renameat2 is unsupported on %s", runtime.GOARCH)
	}
	from, err := syscall.BytePtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.BytePtrFromString(target)
	if err != nil {
		return err
	}
	// RENAME_NOREPLACE also protects unknown empty directories from replacement.
	_, _, errno := syscall.Syscall6(number, ^uintptr(99), uintptr(unsafe.Pointer(from)), ^uintptr(99), uintptr(unsafe.Pointer(to)), 1, 0)
	if errno != 0 {
		return &os.LinkError{Op: "renameat2", Old: source, New: target, Err: errno}
	}
	return nil
}

func publish(file *os.File, path string) error {
	from, err := syscall.BytePtrFromString(fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
	if err != nil {
		return err
	}
	to, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(syscall.SYS_LINKAT, ^uintptr(99), uintptr(unsafe.Pointer(from)), ^uintptr(99), uintptr(unsafe.Pointer(to)), 0x400, 0)
	if errno != 0 {
		return &os.PathError{Op: "linkat", Path: path, Err: errno}
	}
	return syncDir(filepath.Dir(path))
}

func trustedDirectory(path string, owner uint32, test bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("path must be absolute and normalized: %s", path)
	}
	for component := path; ; component = filepath.Dir(component) {
		info, err := os.Lstat(component)
		if err != nil {
			return err
		}
		id := identify(info)
		sticky := test && component != path && id.UID == 0 && info.Mode()&os.ModeSticky != 0
		if !info.IsDir() || (id.UID != 0 && (!test || id.UID != owner)) || (info.Mode().Perm()&0022 != 0 && !sticky) {
			return fmt.Errorf("untrusted, writable or symlink directory/ancestor: %s", component)
		}
		if component == "/" {
			return nil
		}
	}
}

func fileRecord(path string) (record, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return record{}, err
	}
	r := record{identity: identify(info)}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		r.Target, err = os.Readlink(path)
	case info.Mode().IsRegular():
		if info.Sys().(*syscall.Stat_t).Nlink != 1 {
			return r, fmt.Errorf("unexpected hard link: %s", path)
		}
		file, err := openFile(path, syscall.O_RDONLY, 0)
		if err != nil {
			return r, err
		}
		defer file.Close()
		opened, err := file.Stat()
		if err != nil {
			return r, err
		}
		if identify(opened) != r.identity {
			return r, fmt.Errorf("file changed while opening: %s", path)
		}
		attribute, _ := syscall.BytePtrFromString("security.capability")
		_, _, errno := syscall.Syscall6(syscall.SYS_FGETXATTR, file.Fd(), uintptr(unsafe.Pointer(attribute)), 0, 0, 0, 0)
		if errno != syscall.ENODATA && errno != syscall.EOPNOTSUPP {
			return r, fmt.Errorf("file capabilities or unreadable attributes are not allowed: %s", path)
		}
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			return r, err
		}
		r.SHA256 = hex.EncodeToString(hash.Sum(nil))
	case !info.IsDir():
		return r, fmt.Errorf("unexpected file type: %s", path)
	}
	return r, err
}

func checkRecord(path string, expected *record, missing bool) (bool, error) {
	if missing && !exists(path) {
		return false, nil
	}
	if expected == nil {
		return false, fmt.Errorf("missing ownership record: %s", path)
	}
	actual, err := fileRecord(path)
	if err != nil {
		return false, err
	}
	if actual != *expected {
		return false, fmt.Errorf("unowned or modified path: %s", path)
	}
	return true, nil
}
