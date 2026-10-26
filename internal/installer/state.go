package installer

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"syscall"

	"github.com/Xtr126/cage-xtmapper/internal/release"
)

type metadata struct {
	SchemaVersion  int      `json:"schema_version"`
	Application    string   `json:"application"`
	InstallationID string   `json:"installation_id"`
	Self           identity `json:"self"`
}

func (m *metadata) header() *metadata { return m }

type document interface{ header() *metadata }

type ownedRelease struct {
	ID        string            `json:"id"`
	Inventory map[string]record `json:"inventory"`
}

type state struct {
	metadata
	OwnerUID         uint32        `json:"owner_uid"`
	Root             string        `json:"root"`
	BinDir           string        `json:"bin_dir"`
	TestPrefix       *string       `json:"test_prefix"`
	RootIdentity     record        `json:"root_identity"`
	ReleasesIdentity record        `json:"releases_identity"`
	LockIdentity     record        `json:"lock_identity"`
	Release          *ownedRelease `json:"release"`
	Current          *record       `json:"current"`
	Launcher         *record       `json:"launcher"`
}

type legacyMove struct {
	Name   string `json:"name"`
	Backup string `json:"backup"`
	Record record `json:"record"`
}

type transaction struct {
	metadata
	ID           string        `json:"id"`
	Operation    string        `json:"operation"`
	Phase        string        `json:"phase"`
	Stage        string        `json:"stage"`
	OldRelease   *ownedRelease `json:"old_release"`
	OldCurrent   *record       `json:"old_current"`
	OldLauncher  *record       `json:"old_launcher"`
	NewRelease   *ownedRelease `json:"new_release"`
	NextCurrent  *record       `json:"next_current"`
	NextLauncher *record       `json:"next_launcher"`
	LegacyMoves  []legacyMove  `json:"legacy_moves,omitempty"`
}

type manager struct {
	prefix, root, base, bin string
	owner                   uint32
	lock                    *os.File
	state                   *state
	tx                      *transaction
	removed                 []string
	migrated                []string
	upToDate                bool
}

func newManager(prefix string) (*manager, error) {
	m := &manager{prefix: prefix, root: "/opt/" + release.App, bin: "/usr/local/bin"}
	if prefix != "" {
		m.owner = uint32(os.Geteuid())
		if err := trustedDirectory(prefix, m.owner, true); err != nil {
			return nil, err
		}
		info, err := os.Lstat(prefix)
		if err != nil {
			return nil, err
		}
		if identify(info).UID != m.owner || info.Mode() != os.ModeDir|0700 {
			return nil, fmt.Errorf("test prefix must be an existing private (0700) directory owned by the caller")
		}
		m.root = filepath.Join(prefix, "opt", release.App)
		m.bin = filepath.Join(prefix, "usr/local/bin")
	}
	m.base = m.root
	for _, path := range []string{filepath.Dir(m.root), m.bin} {
		if err := trustedDirectory(path, m.owner, prefix != ""); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *manager) checkpoint(name string) error {
	// Fault injection is restricted to an explicitly private test prefix.
	if m.prefix != "" && os.Getenv("CAGE_XTMAPPER_TEST_PAUSE") == name {
		continued := make(chan os.Signal, 1)
		signal.Notify(continued, syscall.SIGCONT)
		defer signal.Stop(continued)
		if err := os.WriteFile(filepath.Join(m.prefix, "checkpoint"), []byte(name), 0600); err != nil {
			return err
		}
		if err := syscall.Kill(os.Getpid(), syscall.SIGSTOP); err != nil {
			return err
		}
		// Group SIGSTOP can reach another Go thread before stopping this one.
		<-continued
	}
	return nil
}

func (m *manager) readMetadata(name string, value document) ([]byte, error) {
	path := filepath.Join(m.base, name)
	file, err := openFile(path, syscall.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	id := identify(info)
	if id.UID != m.owner || id.Mode != syscall.S_IFREG|0644 || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		return nil, fmt.Errorf("unsafe metadata: %s", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if err := release.DecodeJSON(data, value); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	h := value.header()
	if h.Self != id || h.SchemaVersion != 1 || h.Application != release.App || !validID(h.InstallationID) {
		return nil, fmt.Errorf("metadata identity or format mismatch: %s", path)
	}
	return data, nil
}

func (m *manager) save(name string, value document) error {
	path := filepath.Join(m.base, name)
	var previous document = &state{}
	if name == "transaction.json" {
		previous = &transaction{}
	}
	var previousData []byte
	if exists(path) {
		data, err := m.readMetadata(name, previous)
		if err != nil {
			return err
		}
		if previous.header().InstallationID != value.header().InstallationID {
			return fmt.Errorf("refusing to replace unowned metadata: %s", path)
		}
		previousData = data
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	temporary := filepath.Join(m.base, ".metadata-"+id)
	file, err := temporaryFile(m.base)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0644); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	value.header().Self = identify(info)
	defer func() {
		if info, err := os.Lstat(temporary); err == nil && identify(info) == value.header().Self {
			os.Remove(temporary)
		}
	}()
	data, err := release.JSON(value)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := publish(file, temporary); err != nil {
		return err
	}
	if previousData != nil {
		current, err := m.readMetadata(name, previous)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, previousData) {
			return fmt.Errorf("metadata changed during transaction: %s", path)
		}
		err = os.Rename(temporary, path)
	} else {
		err = renameNew(temporary, path)
	}
	if err != nil {
		return err
	}
	return syncDir(m.base)
}

func (m *manager) saveTx() error { return m.save("transaction.json", m.tx) }

func (m *manager) checkLayout() error {
	for _, path := range []string{m.base, filepath.Join(m.base, "releases"), m.bin} {
		if err := trustedDirectory(path, m.owner, m.prefix != ""); err != nil {
			return err
		}
	}
	s := m.state
	if s.RootIdentity.Mode != syscall.S_IFDIR|0755 || s.ReleasesIdentity.Mode != syscall.S_IFDIR|0755 || s.LockIdentity.Mode != syscall.S_IFREG|0644 ||
		s.RootIdentity.UID != m.owner || s.ReleasesIdentity.UID != m.owner || s.LockIdentity.UID != m.owner || s.RootIdentity.Dev != s.ReleasesIdentity.Dev {
		return fmt.Errorf("unsafe managed layout identities/filesystem")
	}
	for path, expected := range map[string]*record{
		m.base: &s.RootIdentity, filepath.Join(m.base, "releases"): &s.ReleasesIdentity,
		filepath.Join(m.base, ".install.lock"): &s.LockIdentity,
	} {
		if _, err := checkRecord(path, expected, false); err != nil {
			return err
		}
	}
	return nil
}

var errBusy = fmt.Errorf("installation is busy: an application or another administrative operation holds .install.lock")

func (m *manager) load(exclusive bool) error {
	if err := trustedDirectory(m.base, m.owner, m.prefix != ""); err != nil {
		return err
	}
	s := &state{}
	if _, err := m.readMetadata("state.json", s); err != nil {
		return err
	}
	if s.Root != m.root || s.BinDir != m.bin || s.OwnerUID != m.owner ||
		(m.prefix == "" && s.TestPrefix != nil) || (m.prefix != "" && (s.TestPrefix == nil || *s.TestPrefix != m.prefix)) {
		return fmt.Errorf("installation paths, ownership or scope do not match the ownership record")
	}
	m.state = s
	if err := m.checkLayout(); err != nil {
		return err
	}
	lock, err := openFile(filepath.Join(m.base, ".install.lock"), syscall.O_RDONLY, 0)
	if err != nil {
		return err
	}
	m.lock = lock
	info, err := lock.Stat()
	if err != nil {
		return err
	}
	if identify(info) != s.LockIdentity.identity {
		return fmt.Errorf("lock changed while opening")
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(lock.Fd()), mode|syscall.LOCK_NB); err != nil {
		if err == syscall.EWOULDBLOCK {
			return errBusy
		}
		return err
	}
	// An installer may have committed between the first read and flock.
	fresh := &state{}
	if _, err := m.readMetadata("state.json", fresh); err != nil {
		return err
	}
	oldFixed, newFixed := *s, *fresh
	oldFixed.Self, newFixed.Self = identity{}, identity{}
	oldFixed.Release, newFixed.Release = nil, nil
	oldFixed.Current, newFixed.Current = nil, nil
	oldFixed.Launcher, newFixed.Launcher = nil, nil
	if !reflect.DeepEqual(oldFixed, newFixed) {
		return fmt.Errorf("installation identity changed while acquiring lock")
	}
	m.state = fresh
	if err := m.checkLayout(); err != nil {
		return err
	}
	if err := validRelease(fresh.Release); err != nil {
		return err
	}
	if exists(filepath.Join(m.base, "transaction.json")) {
		tx := &transaction{}
		if _, err := m.readMetadata("transaction.json", tx); err != nil {
			return err
		}
		if tx.InstallationID != fresh.InstallationID || !validID(tx.ID) || tx.Stage != ".stage-"+tx.ID ||
			(tx.Operation != "install" && tx.Operation != "update" && tx.Operation != "uninstall" && tx.Operation != "legacy-migrate") {
			return fmt.Errorf("invalid transaction or installation identity")
		}
		if err := validRelease(tx.OldRelease); err != nil {
			return err
		}
		if err := validRelease(tx.NewRelease); err != nil {
			return err
		}
		noReleaseOperation := tx.Operation == "uninstall" || tx.Operation == "legacy-migrate"
		if noReleaseOperation != (tx.NewRelease == nil) {
			return fmt.Errorf("invalid transaction release")
		}
		if len(tx.LegacyMoves) > 2 || (len(tx.LegacyMoves) > 0 && tx.Operation != "install" && tx.Operation != "update" && tx.Operation != "legacy-migrate") {
			return fmt.Errorf("invalid legacy migration plan")
		}
		for i, move := range tx.LegacyMoves {
			wantName := "cage_xtmapper.sh"
			if i == 1 {
				wantName = "cage_xtmapper"
			}
			if move.Name != wantName || move.Backup != move.Name+".legacy-backup-"+tx.ID ||
				move.Record.Mode&uint32(syscall.S_IFMT) != uint32(syscall.S_IFREG) ||
				move.Record.UID != m.owner || !release.ValidDigest(move.Record.SHA256) {
				return fmt.Errorf("invalid legacy migration record")
			}
		}
		if tx.Operation == "legacy-migrate" && (tx.OldRelease == nil || tx.OldCurrent == nil || len(tx.LegacyMoves) == 0) {
			return fmt.Errorf("invalid legacy migration transaction")
		}
		m.tx = tx
	}
	return nil
}
