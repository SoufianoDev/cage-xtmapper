package installer

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"

	"github.com/Xtr126/cage-xtmapper/internal/release"
)

func validRelease(r *ownedRelease) error {
	if r != nil && (!release.ID.MatchString(r.ID) || r.Inventory == nil) {
		return fmt.Errorf("invalid recorded release ID/inventory")
	}
	return nil
}

func (m *manager) releasePath(r *ownedRelease) string {
	return filepath.Join(m.base, "releases", r.ID)
}

func (m *manager) currentTemp() string    { return filepath.Join(m.base, ".current-"+m.tx.ID) }
func (m *manager) launcherTemp() string   { return filepath.Join(m.bin, ".cage-xtmapper-"+m.tx.ID) }
func (m *manager) launcherTarget() string { return filepath.Join(m.root, "current", release.App) }

func (m *manager) begin(operation string, manifest *release.Manifest) error {
	if m.tx != nil {
		return fmt.Errorf("incomplete transaction exists; run recover")
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	tx := &transaction{
		metadata: metadata{SchemaVersion: 1, Application: release.App, InstallationID: m.state.InstallationID},
		ID:       id, Operation: operation, Phase: "allocating", Stage: ".stage-" + id,
		OldRelease: m.state.Release, OldCurrent: m.state.Current, OldLauncher: m.state.Launcher,
	}
	if manifest != nil {
		tx.NewRelease = &ownedRelease{ID: manifest.ReleaseID, Inventory: make(map[string]record)}
		if exists(m.releasePath(tx.NewRelease)) {
			return fmt.Errorf("release destination already exists; it will not be adopted or replaced")
		}
	}
	if manifest != nil || operation == "legacy-migrate" {
		tx.LegacyMoves, err = m.planLegacyMoves(id)
		if err != nil {
			return err
		}
		if operation == "legacy-migrate" && len(tx.LegacyMoves) == 0 {
			return fmt.Errorf("no recognized legacy launcher remains to migrate")
		}
	}
	for _, path := range []string{filepath.Join(m.base, tx.Stage), filepath.Join(m.base, ".current-"+id), filepath.Join(m.bin, ".cage-xtmapper-"+id)} {
		if exists(path) {
			return fmt.Errorf("transaction reservation already exists: %s", path)
		}
	}
	m.tx = tx
	return m.saveTx()
}

func (m *manager) bootstrapCandidates() ([]string, error) {
	return filepath.Glob(filepath.Join(filepath.Dir(m.root), ".cage-xtmapper-bootstrap-*"))
}

func (m *manager) bootstrap(manifest *release.Manifest) error {
	if exists(m.root) {
		return fmt.Errorf("installation root already exists and is not managed")
	}
	candidates, err := m.bootstrapCandidates()
	if err != nil {
		return err
	}
	if len(candidates) != 0 {
		return fmt.Errorf("unfinished bootstrap exists; inspect with status and run recover")
	}
	m.base, err = os.MkdirTemp(filepath.Dir(m.root), ".cage-xtmapper-bootstrap-*")
	if err != nil {
		return err
	}
	if err := os.Chmod(m.base, 0755); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(m.base, "releases"), 0755); err != nil {
		return err
	}
	m.lock, err = openFile(filepath.Join(m.base, ".install.lock"), syscall.O_CREAT|syscall.O_EXCL|syscall.O_RDWR, 0644)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(m.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(m.lock, release.App+" "+id); err != nil {
		return err
	}
	if err := m.lock.Sync(); err != nil {
		return err
	}
	m.state = &state{
		metadata: metadata{SchemaVersion: 1, Application: release.App, InstallationID: id},
		OwnerUID: m.owner, Root: m.root, BinDir: m.bin,
	}
	if m.prefix != "" {
		m.state.TestPrefix = &m.prefix
	}
	for path, record := range map[string]*record{
		m.base: &m.state.RootIdentity, filepath.Join(m.base, "releases"): &m.state.ReleasesIdentity,
		filepath.Join(m.base, ".install.lock"): &m.state.LockIdentity,
	} {
		*record, err = fileRecord(path)
		if err != nil {
			return err
		}
	}
	if err := m.save("state.json", m.state); err != nil {
		return err
	}
	if err := m.begin("install", manifest); err != nil {
		return err
	}
	for _, directory := range []string{m.base, filepath.Dir(m.root)} {
		if err := syncDir(directory); err != nil {
			return err
		}
	}
	if err := m.checkpoint("bootstrap"); err != nil {
		return err
	}
	return m.publishBootstrap()
}

func (m *manager) publishBootstrap() error {
	if err := renameNew(m.base, m.root); err != nil {
		return err
	}
	m.base = m.root
	return syncDir(filepath.Dir(m.root))
}

func (m *manager) recoverBootstrap() error {
	candidates, err := m.bootstrapCandidates()
	if err != nil {
		return err
	}
	if len(candidates) != 1 {
		return fmt.Errorf("no single recoverable bootstrap; unknown/ambiguous staging data is preserved")
	}
	m.base = candidates[0]
	if err := m.load(true); err != nil {
		return err
	}
	if m.tx == nil || m.tx.Operation != "install" || m.state.Release != nil {
		return fmt.Errorf("unrecognized bootstrap; preserved")
	}
	return m.publishBootstrap()
}

func knownLegacyShell(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	shebang := strings.HasPrefix(content, "#!/usr/bin/env bash") || strings.HasPrefix(content, "#!/bin/bash")
	return shebang && strings.Contains(content, "cage_xtmapper") &&
		strings.Contains(content, "XTMAPPER_BOOT_WAIT_SECONDS must be a positive integer")
}

func knownLegacyBinary(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 ||
		info.Size() < 64 || info.Size() > release.MaxBytes {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	_, err = release.ELFArch(data)
	return err == nil
}

func (m *manager) legacyConflicts() error {
	legacyScript := filepath.Join(m.bin, "cage_xtmapper.sh")
	scriptRecognized := knownLegacyShell(legacyScript)
	for _, name := range []string{"cage_xtmapper", "cage_xtmapper.sh"} {
		path := filepath.Join(m.bin, name)
		if !exists(path) {
			continue
		}
		if name == "cage_xtmapper.sh" && scriptRecognized {
			continue
		}
		if name == "cage_xtmapper" && scriptRecognized && knownLegacyBinary(path) {
			continue
		}
		return fmt.Errorf("legacy conflict: %s is not a verified legacy installation file and will be preserved; inspect it explicitly before installation", path)
	}
	return nil
}

func (m *manager) planLegacyMoves(id string) ([]legacyMove, error) {
	script := filepath.Join(m.bin, "cage_xtmapper.sh")
	binary := filepath.Join(m.bin, "cage_xtmapper")
	if !exists(script) {
		return nil, nil
	}
	if !knownLegacyShell(script) {
		return nil, fmt.Errorf("legacy launcher changed or is not recognized; preserved: %s", script)
	}
	paths := []string{script}
	if exists(binary) {
		if !knownLegacyBinary(binary) {
			return nil, fmt.Errorf("legacy binary is not a recognized executable ELF; preserved: %s", binary)
		}
		paths = append(paths, binary)
	}
	moves := make([]legacyMove, 0, len(paths))
	for _, path := range paths {
		recorded, err := fileRecord(path)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(path)
		backup := name + ".legacy-backup-" + id
		if exists(filepath.Join(m.bin, backup)) {
			return nil, fmt.Errorf("legacy backup path already exists; preserved: %s", filepath.Join(m.bin, backup))
		}
		moves = append(moves, legacyMove{Name: name, Backup: backup, Record: recorded})
	}
	return moves, nil
}

// migrateLegacyMoves resumes a recorded rename after interruption. Each rename
// is atomic, and the journal identifies the expected inode at both names.
func (m *manager) migrateLegacyMoves(tx *transaction) error {
	for _, move := range tx.LegacyMoves {
		source := filepath.Join(m.bin, move.Name)
		backup := filepath.Join(m.bin, move.Backup)
		sourceExists, backupExists := exists(source), exists(backup)
		switch {
		case sourceExists && !backupExists:
			if _, err := checkRecord(source, &move.Record, false); err != nil {
				return err
			}
			if err := renameNew(source, backup); err != nil {
				return fmt.Errorf("could not preserve legacy file %s: %w", source, err)
			}
			if err := syncDir(m.bin); err != nil {
				return err
			}
			m.migrated = append(m.migrated, backup)
			if err := m.checkpoint("legacy-migrated"); err != nil {
				return err
			}
		case !sourceExists && backupExists:
			if _, err := checkRecord(backup, &move.Record, false); err != nil {
				return err
			}
			m.migrated = append(m.migrated, backup)
		default:
			return fmt.Errorf("legacy migration state is ambiguous; source and backup are preserved: %s", source)
		}
	}
	return nil
}

func sameReleaseFiles(r *ownedRelease, files map[string][]byte) bool {
	if r == nil || len(r.Inventory) != len(files)+2 {
		return false
	}
	for name, data := range files {
		record, ok := r.Inventory[name]
		if !ok || record.SHA256 != release.Digest(data) {
			return false
		}
	}
	return r.Inventory["."].Mode != 0 && r.Inventory["LICENSES"].Mode != 0
}

func (m *manager) tree(base string, r *ownedRelease, partial bool) error {
	if err := validRelease(r); err != nil || r == nil {
		return fmt.Errorf("invalid release ownership inventory")
	}
	for name, record := range r.Inventory {
		mode := uint32(syscall.S_IFREG | 0644)
		switch name {
		case ".", "LICENSES":
			mode = syscall.S_IFDIR | 0755
		case release.App:
			mode = syscall.S_IFREG | 0755
		default:
			if !release.SafeName(name) {
				return fmt.Errorf("unsafe ownership inventory: %s", name)
			}
		}
		if record.Mode != mode || record.UID != m.owner || record.Dev != m.state.RootIdentity.Dev {
			return fmt.Errorf("invalid release ownership record: %s", name)
		}
	}
	if !exists(base) {
		if partial {
			return nil
		}
		return fmt.Errorf("owned release is missing: %s", base)
	}
	root, ok := r.Inventory["."]
	if !ok {
		return fmt.Errorf("missing release directory ownership: %s", base)
	}
	if _, err := checkRecord(base, &root, false); err != nil {
		return err
	}
	count := 0
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		expected, ok := r.Inventory[name]
		if !ok {
			return fmt.Errorf("unowned entry in %s; preserved: %s", base, name)
		}
		count++
		_, err = checkRecord(path, &expected, false)
		return err
	})
	if err != nil {
		return err
	}
	if !partial && count != len(r.Inventory) {
		return fmt.Errorf("owned release is incomplete: %s", base)
	}
	return nil
}

func (m *manager) active() error {
	if err := m.checkLayout(); err != nil {
		return err
	}
	s := m.state
	current, launcher := filepath.Join(m.base, "current"), filepath.Join(m.bin, release.App)
	if s.Release == nil {
		if exists(current) || exists(launcher) || s.Current != nil || s.Launcher != nil {
			return fmt.Errorf("unowned current or launcher path exists; refusing to adopt it")
		}
		return nil
	}
	if err := m.tree(m.releasePath(s.Release), s.Release, false); err != nil {
		return err
	}
	for path, expected := range map[string]*record{current: s.Current, launcher: s.Launcher} {
		if _, err := checkRecord(path, expected, false); err != nil {
			return err
		}
	}
	if s.Current.Target != "releases/"+s.Release.ID || s.Launcher.Target != m.launcherTarget() {
		return fmt.Errorf("active release or launcher target mismatch")
	}
	return nil
}

func (m *manager) stage(files map[string][]byte) error {
	base := filepath.Join(m.base, m.tx.Stage)
	if err := os.Mkdir(base, 0755); err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(base, "LICENSES"), 0755); err != nil {
		return err
	}
	if err := m.checkpoint("stage-created"); err != nil {
		return err
	}
	r := m.tx.NewRelease
	for _, name := range []string{".", "LICENSES"} {
		record, err := fileRecord(filepath.Join(base, name))
		if err != nil {
			return err
		}
		r.Inventory[name] = record
	}
	for _, directory := range []string{base, m.base} {
		if err := syncDir(directory); err != nil {
			return err
		}
	}
	m.tx.Phase = "staging"
	if err := m.saveTx(); err != nil {
		return err
	}
	if err := m.checkpoint("staging"); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := m.stageFile(base, name, files[name]); err != nil {
			return err
		}
		if err := m.checkpoint("staged-file"); err != nil {
			return err
		}
	}
	if err := m.tree(base, r, false); err != nil {
		return err
	}
	if err := renameNew(base, m.releasePath(r)); err != nil {
		return err
	}
	for _, directory := range []string{filepath.Join(m.base, "releases"), m.base} {
		if err := syncDir(directory); err != nil {
			return err
		}
	}
	m.tx.Phase = "prepared"
	return m.saveTx()
}

func (m *manager) stageFile(base, name string, data []byte) error {
	path := filepath.Join(base, name)
	file, err := temporaryFile(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	mode := os.FileMode(0644)
	if name == release.App {
		mode = 0755
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	m.tx.NewRelease.Inventory[name] = record{identity: identify(info), SHA256: release.Digest(data)}
	// Persist inode ownership before publishing the complete file.
	if err := m.saveTx(); err != nil {
		return err
	}
	if err := m.checkpoint("file-recorded"); err != nil {
		return err
	}
	return publish(file, path)
}

func (m *manager) reserveLinks() error {
	if err := os.Symlink("releases/"+m.tx.NewRelease.ID, m.currentTemp()); err != nil {
		return err
	}
	current, err := fileRecord(m.currentTemp())
	if err != nil {
		return err
	}
	m.tx.NextCurrent = &current
	if m.tx.OldLauncher == nil {
		if err := os.Symlink(m.launcherTarget(), m.launcherTemp()); err != nil {
			return err
		}
		launcher, err := fileRecord(m.launcherTemp())
		if err != nil {
			return err
		}
		m.tx.NextLauncher = &launcher
	}
	if err := m.checkpoint("links-created"); err != nil {
		return err
	}
	if err := m.saveTx(); err != nil {
		return err
	}
	for _, directory := range []string{m.base, m.bin} {
		if err := syncDir(directory); err != nil {
			return err
		}
	}
	return nil
}

func (m *manager) isCommitted() (bool, error) {
	path := filepath.Join(m.base, "current")
	if m.tx.Operation == "legacy-migrate" {
		if m.tx.NewRelease != nil || m.tx.OldRelease == nil || m.tx.OldCurrent == nil {
			return false, fmt.Errorf("invalid legacy migration transaction")
		}
		if _, err := checkRecord(path, m.tx.OldCurrent, false); err != nil {
			return false, err
		}
		return true, nil
	}
	if m.tx.Operation == "uninstall" {
		if !exists(path) {
			return true, nil
		}
	} else if m.tx.NextCurrent != nil && exists(path) {
		actual, err := fileRecord(path)
		if err != nil {
			return false, err
		}
		if actual == *m.tx.NextCurrent {
			return true, nil
		}
	}
	if m.tx.OldCurrent != nil {
		_, err := checkRecord(path, m.tx.OldCurrent, false)
		return false, err
	}
	if exists(path) {
		return false, fmt.Errorf("unowned current symlink; recovery refused")
	}
	return false, nil
}

func (m *manager) removeLink(path string, expected *record) error {
	if expected == nil {
		if exists(path) {
			return fmt.Errorf("unowned link is preserved: %s", path)
		}
		return nil
	}
	if expected.Mode != syscall.S_IFLNK|0777 || expected.UID != m.owner {
		return fmt.Errorf("invalid link ownership record: %s", path)
	}
	present, err := checkRecord(path, expected, true)
	if err != nil || !present {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	m.removed = append(m.removed, path)
	return syncDir(filepath.Dir(path))
}

func (m *manager) removeTree(path string, r *ownedRelease) error {
	if err := m.tree(path, r, true); err != nil {
		return err
	}
	if !exists(path) {
		return nil
	}
	names := make([]string, 0, len(r.Inventory))
	for name := range r.Inventory {
		if name != "." {
			names = append(names, name)
		}
	}
	slices.SortFunc(names, func(a, b string) int {
		if depth := strings.Count(b, "/") - strings.Count(a, "/"); depth != 0 {
			return depth
		}
		return strings.Compare(b, a)
	})
	for _, name := range names {
		target, expected := filepath.Join(path, name), r.Inventory[name]
		present, err := checkRecord(target, &expected, true)
		if err != nil {
			return err
		}
		if !present {
			continue
		}
		if err := os.Remove(target); err != nil {
			return err
		}
		m.removed = append(m.removed, target)
		if err := syncDir(filepath.Dir(target)); err != nil {
			return err
		}
		if err := m.checkpoint("cleanup-file"); err != nil {
			return err
		}
	}
	root := r.Inventory["."]
	if _, err := checkRecord(path, &root, false); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	m.removed = append(m.removed, path)
	return syncDir(filepath.Dir(path))
}

func (m *manager) finish() error {
	if err := m.checkLayout(); err != nil {
		return err
	}
	committed, err := m.isCommitted()
	if err != nil {
		return err
	}
	tx, s := m.tx, m.state
	if committed {
		if tx.Operation == "legacy-migrate" {
			if tx.NewRelease != nil || tx.OldRelease == nil ||
				!reflect.DeepEqual(tx.OldRelease, s.Release) ||
				!reflect.DeepEqual(tx.OldCurrent, s.Current) ||
				!reflect.DeepEqual(tx.OldLauncher, s.Launcher) {
				return fmt.Errorf("legacy migration no longer matches the active installation")
			}
			if err := m.active(); err != nil {
				return err
			}
			tx.Phase = "cleanup"
			if err := m.saveTx(); err != nil {
				return err
			}
			if err := m.checkpoint("cleanup"); err != nil {
				return err
			}
			if err := m.migrateLegacyMoves(tx); err != nil {
				return err
			}
		} else {
			if tx.NewRelease != nil {
				if err := m.tree(m.releasePath(tx.NewRelease), tx.NewRelease, false); err != nil {
					return err
				}
				launcher := filepath.Join(m.bin, release.App)
				if tx.OldLauncher != nil {
					if _, err := checkRecord(launcher, tx.OldLauncher, false); err != nil {
						return err
					}
				} else if !exists(launcher) {
					if _, err := checkRecord(m.launcherTemp(), tx.NextLauncher, false); err != nil {
						return err
					}
					if err := renameNew(m.launcherTemp(), launcher); err != nil {
						return err
					}
					if err := syncDir(m.bin); err != nil {
						return err
					}
				} else if _, err := checkRecord(launcher, tx.NextLauncher, false); err != nil {
					return err
				}
				s.Release, s.Current, s.Launcher = tx.NewRelease, tx.NextCurrent, tx.OldLauncher
				if s.Launcher == nil {
					s.Launcher = tx.NextLauncher
				}
			} else {
				if err := m.removeLink(filepath.Join(m.bin, release.App), tx.OldLauncher); err != nil {
					return err
				}
				s.Release, s.Current, s.Launcher = nil, nil, nil
			}
			if err := m.save("state.json", s); err != nil {
				return err
			}
			tx.Phase = "cleanup"
			if err := m.saveTx(); err != nil {
				return err
			}
			if err := m.checkpoint("cleanup"); err != nil {
				return err
			}
			// Preserve the transaction journal while completing all post-commit
			// work, so a later invocation can safely resume after SIGKILL.
			if len(tx.LegacyMoves) > 0 {
				if err := m.migrateLegacyMoves(tx); err != nil {
					return err
				}
			}
			if old := tx.OldRelease; old != nil {
				if tx.NewRelease != nil && old.ID == tx.NewRelease.ID {
					return fmt.Errorf("refusing to delete the active release")
				}
				if err := m.removeTree(m.releasePath(old), old); err != nil {
					return err
				}
			}
		}
	} else {
		if old := tx.OldRelease; old != nil {
			if err := m.tree(m.releasePath(old), old, false); err != nil {
				return err
			}
			if _, err := checkRecord(filepath.Join(m.bin, release.App), tx.OldLauncher, false); err != nil {
				return err
			}
		} else if exists(filepath.Join(m.bin, release.App)) {
			return fmt.Errorf("unowned launcher; recovery refused")
		}
		if next := tx.NewRelease; next != nil {
			stage := filepath.Join(m.base, tx.Stage)
			if len(next.Inventory) == 0 && exists(stage) {
				if err := m.recoverScaffolding(stage, next); err != nil {
					return err
				}
			}
			for _, path := range []string{stage, m.releasePath(next)} {
				if err := m.removeTree(path, next); err != nil {
					return err
				}
			}
		}
		s.Release, s.Current, s.Launcher = tx.OldRelease, tx.OldCurrent, tx.OldLauncher
		if err := m.save("state.json", s); err != nil {
			return err
		}
	}
	target := ""
	if tx.NewRelease != nil {
		target = "releases/" + tx.NewRelease.ID
	}
	for _, link := range []struct {
		path, target string
		record       **record
	}{{m.currentTemp(), target, &tx.NextCurrent}, {m.launcherTemp(), m.launcherTarget(), &tx.NextLauncher}} {
		if exists(link.path) && *link.record == nil {
			r, err := fileRecord(link.path)
			if err != nil {
				return err
			}
			if r.Mode != syscall.S_IFLNK|0777 || r.UID != m.owner || r.Target != link.target {
				return fmt.Errorf("modified reserved symlink: %s", link.path)
			}
			*link.record = &r
			if err := m.saveTx(); err != nil {
				return err
			}
		}
		if err := m.removeLink(link.path, *link.record); err != nil {
			return err
		}
	}
	var actual transaction
	if _, err := m.readMetadata("transaction.json", &actual); err != nil {
		return err
	}
	if !reflect.DeepEqual(&actual, tx) {
		return fmt.Errorf("transaction changed during cleanup")
	}
	if err := os.Remove(filepath.Join(m.base, "transaction.json")); err != nil {
		return err
	}
	if err := syncDir(m.base); err != nil {
		return err
	}
	m.tx = nil
	return nil
}

func (m *manager) recoverScaffolding(stage string, r *ownedRelease) error {
	// The durable reservation predates mkdir; only empty scaffolding is eligible.
	if err := trustedDirectory(stage, m.owner, m.prefix != ""); err != nil {
		return err
	}
	entries, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "LICENSES" {
			return fmt.Errorf("unowned staging contents; preserved")
		}
	}
	if exists(filepath.Join(stage, "LICENSES")) {
		path := filepath.Join(stage, "LICENSES")
		if err := trustedDirectory(path, m.owner, m.prefix != ""); err != nil {
			return err
		}
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			return fmt.Errorf("unowned or unreadable staging contents; preserved")
		}
		r.Inventory["LICENSES"], err = fileRecord(path)
		if err != nil {
			return err
		}
	}
	r.Inventory["."], err = fileRecord(stage)
	if err != nil {
		return err
	}
	return m.saveTx()
}

// auto resolves a recorded interrupted transaction before converging the install
// to the supplied release. Unknown or modified state is never force-cleaned.
func (m *manager) auto(archive string) error {
	operation := "install"

	if !exists(m.root) {
		candidates, err := m.bootstrapCandidates()
		if err != nil {
			return err
		}
		if len(candidates) != 0 {
			if err := m.recover(); err != nil {
				return fmt.Errorf("automatic recovery failed: %w", err)
			}
			if m.state != nil && m.state.Release != nil {
				operation = "update"
			}
			if m.lock != nil {
				if err := m.lock.Close(); err != nil {
					return err
				}
				m.lock = nil
			}
			return m.change(operation, archive)
		}
		return m.change(operation, archive)
	}

	if err := m.load(true); err != nil {
		return err
	}
	if m.tx != nil {
		if err := m.finish(); err != nil {
			return fmt.Errorf("automatic recovery failed: %w", err)
		}
	} else if err := m.active(); err != nil {
		return err
	}
	if m.state != nil && m.state.Release != nil {
		operation = "update"
	}
	if m.lock != nil {
		if err := m.lock.Close(); err != nil {
			return err
		}
		m.lock = nil
	}
	return m.change(operation, archive)
}

func (m *manager) change(operation, archive string) error {
	if err := m.legacyConflicts(); err != nil {
		return err
	}
	arch, err := release.HostArch()
	if err != nil {
		return err
	}
	manifest, files, err := release.ReadArchive(archive, arch)
	if err != nil {
		return err
	}
	if !exists(m.root) {
		if operation != "install" {
			return fmt.Errorf("update requires an existing managed installation")
		}
		if exists(filepath.Join(m.bin, release.App)) {
			return fmt.Errorf("launcher path already exists and is not owned by this installation")
		}
		// No managed installation lock exists yet during first installation.
		if m.prefix == "" {
			if err := ensureRuntimeDependencies(manifest.RuntimeRequirements); err != nil {
				return err
			}
		}
		if err := m.bootstrap(manifest); err != nil {
			return err
		}
	} else {
		if err := m.load(true); err != nil {
			return err
		}
		if m.tx != nil {
			return fmt.Errorf("incomplete transaction exists; run recover")
		}
		if err := m.active(); err != nil {
			return err
		}
		if m.state.Release != nil && operation == "install" {
			operation = "update"
		}
		if (m.state.Release != nil) != (operation == "update") {
			return fmt.Errorf("invalid %s for the current installation state", operation)
		}
		// The exclusive installation lock is held before inspecting or changing
		// system packages; a running app must cause an immediate busy failure.
		if m.prefix == "" {
			if err := ensureRuntimeDependencies(manifest.RuntimeRequirements); err != nil {
				return err
			}
		}
		if m.state.Release != nil && m.state.Release.ID == manifest.ReleaseID {
			if sameReleaseFiles(m.state.Release, files) {
				if exists(filepath.Join(m.bin, "cage_xtmapper.sh")) {
					if err := m.begin("legacy-migrate", nil); err != nil {
						return err
					}
					if err := m.finish(); err != nil {
						return err
					}
				}
				m.upToDate = true
				return nil
			}
			return fmt.Errorf("release ID is already active but archive content differs; refusing replacement")
		}
		if err := m.begin(operation, manifest); err != nil {
			return err
		}
	}
	if err := m.stage(files); err != nil {
		return err
	}
	if err := m.reserveLinks(); err != nil {
		return err
	}
	if err := m.checkpoint("before-commit"); err != nil {
		return err
	}
	if err := m.legacyConflicts(); err != nil {
		return err
	}
	if err := m.active(); err != nil {
		return err
	}
	if err := m.tree(m.releasePath(m.tx.NewRelease), m.tx.NewRelease, false); err != nil {
		return err
	}
	if _, err := checkRecord(m.currentTemp(), m.tx.NextCurrent, false); err != nil {
		return err
	}
	if m.tx.OldCurrent != nil {
		err = os.Rename(m.currentTemp(), filepath.Join(m.base, "current"))
	} else {
		err = renameNew(m.currentTemp(), filepath.Join(m.base, "current"))
	}
	if err != nil {
		return err
	}
	// Commit point: current's atomic rename, made durable by the directory sync.
	if err := syncDir(m.base); err != nil {
		return err
	}
	if err := m.checkpoint("after-commit"); err != nil {
		return err
	}
	if err := m.finish(); err != nil {
		return err
	}
	return nil
}

func (m *manager) uninstall() error {
	if err := m.load(true); err != nil {
		return err
	}
	if m.tx != nil {
		return fmt.Errorf("incomplete transaction exists; run recover")
	}
	if m.state.Release == nil {
		return fmt.Errorf("no installed release to uninstall")
	}
	if err := m.active(); err != nil {
		return err
	}
	if err := m.begin("uninstall", nil); err != nil {
		return err
	}
	if err := m.checkpoint("before-commit"); err != nil {
		return err
	}
	if err := m.active(); err != nil {
		return err
	}
	if _, err := checkRecord(filepath.Join(m.base, "current"), m.tx.OldCurrent, false); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(m.base, "current")); err != nil {
		return err
	}
	if err := syncDir(m.base); err != nil {
		return err
	}
	if err := m.checkpoint("after-commit"); err != nil {
		return err
	}
	return m.finish()
}

func (m *manager) recover() error {
	if !exists(m.root) {
		if err := m.recoverBootstrap(); err != nil {
			return err
		}
	} else if err := m.load(true); err != nil {
		return err
	}
	if m.tx != nil {
		return m.finish()
	}
	return m.active()
}
