package releasebuild

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/Xtr126/cage-xtmapper/internal/release"
)

// errStale is returned when recorded provenance no longer matches the tree.
var errStale = errors.New("build provenance is stale; run ./build.sh")

var nativeTrees = map[string]string{
	"cage":    "native-src/cage",
	"wlroots": "native-src/wlroots-xtmapper",
}

// collectInventory records every input below roots as sorted NUL-separated
// records: "path\0f\0(x|-)\0<digest>" for files, "path\0l\0<target>" for
// symlinks. Only executable bits of the mode are recorded so umask and
// ownership cannot change the result; other file types are rejected.
func collectInventory(repo string, roots []string) ([]string, error) {
	record := func(path, scopeRoot string) (string, error) {
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		relative, err := filepath.Rel(repo, path)
		if err != nil {
			return "", err
		}
		relative = filepath.ToSlash(relative)
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return "", err
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return "", fmt.Errorf("cannot resolve source symlink %s: %w", relative, err)
			}
			scopeAbs, err := filepath.Abs(scopeRoot)
			if err != nil {
				return "", err
			}
			targetAbs, err := filepath.Abs(resolved)
			if err != nil {
				return "", err
			}
			within, err := filepath.Rel(scopeAbs, targetAbs)
			if err != nil {
				return "", err
			}
			if within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
				return "", fmt.Errorf("source symlink escapes inventoried tree: %s -> %s", relative, target)
			}
			return relative + "\x00l\x00" + target, nil
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			exec := "-"
			if info.Mode().Perm()&0o111 != 0 {
				exec = "x"
			}
			sum := sha256.Sum256(data)
			return relative + "\x00f\x00" + exec + "\x00" + hex.EncodeToString(sum[:]), nil
		default:
			return "", fmt.Errorf("unsupported file type: %s", relative)
		}
	}
	var records []string
	for _, root := range roots {
		full := filepath.Join(repo, root)
		info, err := os.Lstat(full)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			recorded, err := record(full, filepath.Dir(full))
			if err != nil {
				return nil, err
			}
			records = append(records, recorded)
			continue
		}
		err = filepath.WalkDir(full, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			recorded, err := record(path, full)
			if err != nil {
				return err
			}
			records = append(records, recorded)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(records)
	return records, nil
}

func recordsDigest(records []string) string {
	hash := sha256.New()
	for _, record := range records {
		hash.Write([]byte(record))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// NativeSourceDigest fingerprints one native tree by content, paths,
// executable bits and symlink targets.
func NativeSourceDigest(repo, tree string) (string, error) {
	records, err := collectInventory(repo, []string{tree})
	if err != nil {
		return "", err
	}
	return recordsDigest(records), nil
}

// NativeInputDigest fingerprints every input that can change the native build
// and the final ELF. build.sh stages the same trees it fingerprints, so the
// digest always describes the source the build consumes.
func NativeInputDigest(repo, buildType string) (string, error) {
	if buildType == "" {
		return "", fmt.Errorf("missing build type")
	}
	records, err := collectInventory(repo, []string{
		"native-src/cage", "native-src/wlroots-xtmapper", "native-src/ORIGINS",
		"meson_wrapfiles", "wlroots_deps", "build.sh",
	})
	if err != nil {
		return "", err
	}
	clang, err := command(repo, "clang", "--version")
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, record := range records {
		hash.Write([]byte(record))
		hash.Write([]byte{0})
	}
	fmt.Fprintf(hash, "buildtype\x00%s\x00clang\x00%s\x00go\x00%s", buildType, clang, runtime.Version())
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// NativeOrigins reads native-src/ORIGINS into native_commits and
// native_origins manifest values.
func NativeOrigins(repo string) (map[string]string, map[string]release.NativeOrigin, error) {
	data, err := os.ReadFile(filepath.Join(repo, "native-src/ORIGINS"))
	if err != nil {
		return nil, nil, err
	}
	commits := make(map[string]string)
	origins := make(map[string]release.NativeOrigin)
	for number, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 {
			return nil, nil, fmt.Errorf("native-src/ORIGINS:%d: expected 4 fields", number+1)
		}
		key, commit, url, revision := fields[0], fields[1], fields[2], fields[3]
		if key != "cage" && key != "wlroots" {
			return nil, nil, fmt.Errorf("native-src/ORIGINS:%d: unknown native source %q", number+1, key)
		}
		if _, duplicate := commits[key]; duplicate {
			return nil, nil, fmt.Errorf("native-src/ORIGINS:%d: duplicate entry for %s", number+1, key)
		}
		if !release.ValidCommit(commit) || !release.ValidCommit(revision) {
			return nil, nil, fmt.Errorf("native-src/ORIGINS:%d: invalid commit id", number+1)
		}
		if !release.ValidOriginURL(url) {
			return nil, nil, fmt.Errorf("native-src/ORIGINS:%d: invalid origin URL", number+1)
		}
		commits[key] = commit
		origins[key] = release.NativeOrigin{URL: url, Revision: revision}
	}
	if len(commits) != 2 {
		return nil, nil, fmt.Errorf("native-src/ORIGINS: expected entries for cage and wlroots")
	}
	return commits, origins, nil
}

// VerifyFresh re-derives the recorded build inputs and reports whether
// build-info.json still describes the staged application.
func VerifyFresh(repo string, manifest *release.Manifest) error {
	if manifest.SchemaVersion != release.ManifestSchemaVersion {
		return fmt.Errorf("unsupported build metadata schema %d; run ./build.sh", manifest.SchemaVersion)
	}
	binary, err := os.ReadFile(filepath.Join(repo, "build/go", release.App))
	if err != nil {
		return err
	}
	if release.Digest(binary) != manifest.ELFSHA256 {
		return errStale
	}
	sourceDigest, err := SourceDigest(repo)
	if err != nil {
		return err
	}
	if sourceDigest != manifest.SourceSHA256 {
		return errStale
	}
	buildType, ok := manifest.Build.MesonOptions["buildtype"].(string)
	if !ok || buildType == "" {
		return errStale
	}
	nativeDigest, err := NativeInputDigest(repo, buildType)
	if err != nil {
		return err
	}
	if nativeDigest != manifest.Build.NativeInputsSHA256 {
		return errStale
	}
	for key, tree := range nativeTrees {
		digest, err := NativeSourceDigest(repo, tree)
		if err != nil {
			return err
		}
		if digest != manifest.NativeSourceDigests[key] {
			return errStale
		}
	}
	commits, origins, err := NativeOrigins(repo)
	if err != nil {
		return err
	}
	if !maps.Equal(commits, manifest.NativeCommits) || !maps.Equal(origins, manifest.NativeOrigins) {
		return errStale
	}
	return nil
}
