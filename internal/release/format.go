// Package release implements the data-only, allowlisted release format.
package release

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

const App = "cage-xtmapper"

// shortcut: 64-bit Linux, 512 MiB and 256 members; add streaming/ABI validation before widening.
const MaxBytes = 512 << 20
const maxMembers = 256

var ID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var originPattern = regexp.MustCompile(`^https://\S+$`)
var glibcPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)+$`)

func ValidCommit(s string) bool { return commitPattern.MatchString(s) }

func ValidDigest(s string) bool { return digestPattern.MatchString(s) }

func ValidOriginURL(s string) bool { return originPattern.MatchString(s) }

// ManifestSchemaVersion is the current manifest schema; schema 1 predates
// native digests and origins and is accepted only as legacy input.
const ManifestSchemaVersion = 2

type Manifest struct {
	SchemaVersion       int                     `json:"schema_version"`
	Application         string                  `json:"application"`
	Version             string                  `json:"version"`
	ReleaseID           string                  `json:"release_id"`
	Target              map[string]string       `json:"target"`
	MainCommit          string                  `json:"main_commit"`
	MainDirty           *bool                   `json:"main_dirty"`
	NativeCommits       map[string]string       `json:"native_commits"`
	NativeSourceDigests map[string]string       `json:"native_source_digests"`
	NativeOrigins       map[string]NativeOrigin `json:"native_origins"`
	SourceSHA256        string                  `json:"source_sha256"`
	ELFSHA256           string                  `json:"elf_sha256"`
	Toolchains          map[string]string       `json:"toolchains"`
	Build               Build                   `json:"build"`
	Dependencies        []Dependency            `json:"dependencies"`
	RuntimeRequirements RuntimeRequirements     `json:"runtime_requirements"`
	Licenses            []License               `json:"licenses"`
}

type NativeOrigin struct {
	URL      string `json:"url"`
	Revision string `json:"revision"`
}

type Build struct {
	CGOEnabled         bool           `json:"cgo_enabled"`
	GoFlags            []string       `json:"go_flags"`
	GoBuildInfo        string         `json:"go_build_info"`
	NativeInputsSHA256 string         `json:"native_inputs_sha256"`
	MesonOptions       map[string]any `json:"meson_options"`
}

type Dependency struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Selection string `json:"selection"`
}

type RuntimeRequirements struct {
	Interpreter     *string  `json:"interpreter"`
	SharedLibraries []string `json:"shared_libraries"`
	GlibcMinimum    *string  `json:"glibc_minimum"`
	Commands        []string `json:"commands"`
	Android         string   `json:"android"`
	BuildHost       string   `json:"build_host"`
}

type License struct {
	Component string `json:"component"`
	License   string `json:"license"`
	Path      string `json:"path"`
}

func Digest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func ValidGLIBCVersion(version string) bool {
	return glibcPattern.MatchString(version)
}

func SafeName(name string) bool {
	return name == App || name == "manifest.json" || name == "SHA256SUMS" ||
		(strings.HasPrefix(name, "LICENSES/") && ID.MatchString(strings.TrimPrefix(name, "LICENSES/")))
}

func ELFArch(data []byte) (string, error) {
	if len(data) < 64 || !bytes.Equal(data[:7], []byte("\x7fELF\x02\x01\x01")) {
		return "", fmt.Errorf("expected a 64-bit little-endian ELF")
	}
	b := binary.LittleEndian
	kind, machine := b.Uint16(data[16:]), b.Uint16(data[18:])
	arch := map[uint16]string{62: "amd64", 183: "arm64"}[machine]
	if (kind != 2 && kind != 3) || b.Uint32(data[20:]) != 1 || arch == "" {
		return "", fmt.Errorf("unsupported ELF type or architecture")
	}
	phoff, phsize, phnum := b.Uint64(data[32:]), uint64(b.Uint16(data[54:])), uint64(b.Uint16(data[56:]))
	if b.Uint16(data[52:]) != 64 || phsize != 56 || phnum == 0 || phnum == 65535 ||
		phoff < 64 || phoff > uint64(len(data)) || phsize*phnum > uint64(len(data))-phoff {
		return "", fmt.Errorf("invalid or truncated ELF program headers")
	}
	entry, executableEntry := b.Uint64(data[24:]), false
	for i := uint64(0); i < phnum; i++ {
		p := data[phoff+i*phsize:]
		segment, flags := b.Uint32(p), b.Uint32(p[4:])
		offset, address, size, memorySize := b.Uint64(p[8:]), b.Uint64(p[16:]), b.Uint64(p[32:]), b.Uint64(p[40:])
		if offset > uint64(len(data)) || size > uint64(len(data))-offset {
			return "", fmt.Errorf("truncated ELF segment")
		}
		if segment == 1 {
			if size > memorySize {
				return "", fmt.Errorf("invalid ELF load segment")
			}
			executableEntry = executableEntry || (flags&1 != 0 && entry >= address && entry-address < size)
		}
	}
	if entry == 0 || !executableEntry {
		return "", fmt.Errorf("ELF has no executable entry point")
	}
	return arch, nil
}

func HostArch() (string, error) {
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return "", fmt.Errorf("unsupported host: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return runtime.GOARCH, nil
}

func ValidateContents(files map[string][]byte, expectedArch string) (*Manifest, error) {
	for _, name := range []string{App, "manifest.json", "SHA256SUMS"} {
		if _, ok := files[name]; !ok {
			return nil, fmt.Errorf("release is missing %s", name)
		}
	}
	for name := range files {
		if !SafeName(name) {
			return nil, fmt.Errorf("unexpected release file: %s", name)
		}
	}
	var m Manifest
	if err := DecodeJSON(files["manifest.json"], &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if (m.SchemaVersion != 1 && m.SchemaVersion != ManifestSchemaVersion) || m.Application != App ||
		!ID.MatchString(m.Version) || !ID.MatchString(m.ReleaseID) {
		return nil, fmt.Errorf("unknown manifest format or invalid release identity")
	}
	if !commitPattern.MatchString(m.MainCommit) || m.MainDirty == nil || !digestPattern.MatchString(m.SourceSHA256) ||
		len(m.NativeCommits) != 2 || !commitPattern.MatchString(m.NativeCommits["cage"]) || !commitPattern.MatchString(m.NativeCommits["wlroots"]) {
		return nil, fmt.Errorf("missing or invalid source provenance")
	}
	if m.SchemaVersion == ManifestSchemaVersion {
		if len(m.NativeSourceDigests) != 2 || len(m.NativeOrigins) != 2 {
			return nil, fmt.Errorf("missing native source digests or origins")
		}
		for _, key := range []string{"cage", "wlroots"} {
			origin, ok := m.NativeOrigins[key]
			if !digestPattern.MatchString(m.NativeSourceDigests[key]) || !ok ||
				!originPattern.MatchString(origin.URL) || !commitPattern.MatchString(origin.Revision) {
				return nil, fmt.Errorf("invalid native provenance for %s", key)
			}
		}
	}
	for _, name := range []string{"go", "meson", "c"} {
		if m.Toolchains[name] == "" {
			return nil, fmt.Errorf("missing toolchain version: %s", name)
		}
	}
	if !m.Build.CGOEnabled || len(m.Build.MesonOptions) == 0 || m.Build.GoBuildInfo == "" || !digestPattern.MatchString(m.Build.NativeInputsSHA256) {
		return nil, fmt.Errorf("missing native/Go build configuration")
	}
	r := m.RuntimeRequirements
	for _, values := range [][]string{m.Build.GoFlags, r.SharedLibraries, r.Commands} {
		if values == nil || slices.Contains(values, "") {
			return nil, fmt.Errorf("invalid build flags/runtime requirements")
		}
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(files["manifest.json"], &shape); err != nil {
		return nil, err
	}
	var runtimeShape map[string]json.RawMessage
	if err := json.Unmarshal(shape["runtime_requirements"], &runtimeShape); err != nil {
		return nil, err
	}
	if runtimeShape["interpreter"] == nil || runtimeShape["glibc_minimum"] == nil ||
		(r.Interpreter != nil && !strings.HasPrefix(*r.Interpreter, "/")) ||
		(r.GlibcMinimum != nil && !ValidGLIBCVersion(*r.GlibcMinimum)) || r.Android == "" || r.BuildHost == "" {
		return nil, fmt.Errorf("missing or invalid runtime compatibility information")
	}
	if len(m.Dependencies) == 0 {
		return nil, fmt.Errorf("missing dependency selection")
	}
	for _, dep := range m.Dependencies {
		if dep.Name == "" || dep.Version == "" || dep.Selection == "" {
			return nil, fmt.Errorf("invalid dependency selection")
		}
	}
	arch, err := ELFArch(files[App])
	if err != nil {
		return nil, err
	}
	if len(m.Target) != 2 || m.Target["os"] != "linux" || m.Target["architecture"] != arch || (expectedArch != "" && expectedArch != arch) {
		return nil, fmt.Errorf("manifest/ELF/host architecture mismatch")
	}
	if m.ELFSHA256 != Digest(files[App]) || m.ReleaseID != m.Version+"-"+arch+"-"+m.ELFSHA256[:16] {
		return nil, fmt.Errorf("release identity or ELF checksum mismatch")
	}
	licenses := make(map[string]bool)
	for _, license := range m.Licenses {
		if license.Component == "" || license.License == "" || !strings.HasPrefix(license.Path, "LICENSES/") ||
			!SafeName(license.Path) || licenses[license.Path] || len(files[license.Path]) == 0 {
			return nil, fmt.Errorf("invalid license inventory")
		}
		licenses[license.Path] = true
	}
	if len(licenses) == 0 {
		return nil, fmt.Errorf("missing license information")
	}
	for name := range files {
		if strings.HasPrefix(name, "LICENSES/") && !licenses[name] {
			return nil, fmt.Errorf("license inventory mismatch")
		}
	}
	sums := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSuffix(string(files["SHA256SUMS"]), "\n"), "\n") {
		if len(line) < 67 || line[64:66] != "  " || !digestPattern.MatchString(line[:64]) {
			return nil, fmt.Errorf("invalid SHA256SUMS line")
		}
		name := line[66:]
		data, ok := files[name]
		if !ok || !SafeName(name) || name == "SHA256SUMS" || sums[name] || Digest(data) != line[:64] {
			return nil, fmt.Errorf("invalid checksum entry or checksum mismatch: %s", name)
		}
		sums[name] = true
	}
	if len(sums) != len(files)-1 {
		return nil, fmt.Errorf("checksum inventory mismatch")
	}
	return &m, nil
}

func ReadArchive(path, expectedArch string) (*Manifest, map[string][]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	archive := os.NewFile(uintptr(fd), path)
	defer archive.Close()
	st, err := archive.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > MaxBytes+maxMembers*1024+10240 {
		return nil, nil, fmt.Errorf("archive must be a regular file within the size limit")
	}
	files, err := readTar(archive)
	if err != nil {
		return nil, nil, err
	}
	manifest, err := ValidateContents(files, expectedArch)
	return manifest, files, err
}

func readTar(archive io.Reader) (map[string][]byte, error) {
	files, seen := make(map[string][]byte), make(map[string]bool)
	var total int64
	for {
		header := make([]byte, 512)
		if _, err := io.ReadFull(archive, header); err != nil {
			return nil, fmt.Errorf("truncated tar header/end marker: %w", err)
		}
		if allZero(header) {
			trailer, err := io.ReadAll(io.LimitReader(archive, 10241))
			if err != nil || len(trailer) < 512 || len(trailer) > 10240 || len(trailer)%512 != 0 || !allZero(trailer) {
				return nil, fmt.Errorf("invalid tar end markers or trailing data")
			}
			break
		}
		// Inspect raw records before tar.Reader can consume GNU/PAX extensions.
		if string(header[257:265]) != "ustar\x0000" || (header[156] != tar.TypeReg && header[156] != tar.TypeDir) {
			return nil, fmt.Errorf("expected regular entries in an uncompressed USTAR archive")
		}
		member, err := tar.NewReader(bytes.NewReader(header)).Next()
		if err != nil {
			return nil, err
		}
		name := member.Name
		if member.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if len(seen) >= maxMembers || seen[name] || member.Linkname != "" || member.Uid != 0 || member.Gid != 0 {
			return nil, fmt.Errorf("duplicate, excessive or unsafe archive entry: %s", name)
		}
		seen[name] = true
		if name == "LICENSES" {
			rawName := string(bytes.TrimRight(header[:100], "\x00"))
			if member.Typeflag != tar.TypeDir || member.Mode != 0755 || member.Size != 0 ||
				(rawName != "LICENSES" && rawName != "LICENSES/") || !allZero(header[345:500]) {
				return nil, fmt.Errorf("invalid LICENSES directory")
			}
			continue
		}
		mode := int64(0644)
		if name == App {
			mode = 0755
		}
		if !SafeName(name) || member.Typeflag != tar.TypeReg || member.Mode != mode {
			return nil, fmt.Errorf("unexpected or unsafe archive entry: %s", name)
		}
		if member.Size < 0 || member.Size > MaxBytes-total {
			return nil, fmt.Errorf("release exceeds size limit")
		}
		total += member.Size
		data := make([]byte, member.Size)
		if _, err := io.ReadFull(archive, data); err != nil {
			return nil, fmt.Errorf("truncated archive entry %s: %w", name, err)
		}
		padding := make([]byte, (512-member.Size%512)%512)
		if _, err := io.ReadFull(archive, padding); err != nil || !allZero(padding) {
			return nil, fmt.Errorf("invalid tar padding: %s", name)
		}
		files[name] = data
	}
	if !seen["LICENSES"] {
		return nil, fmt.Errorf("missing LICENSES directory")
	}
	return files, nil
}

func allZero(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return false
		}
	}
	return true
}

func Checksums(files map[string][]byte) []byte {
	names := make([]string, 0, len(files))
	for name := range files {
		if name != "SHA256SUMS" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var sums strings.Builder
	for _, name := range names {
		fmt.Fprintf(&sums, "%s  %s\n", Digest(files[name]), name)
	}
	return []byte(sums.String())
}

func WriteArchive(output io.Writer, files map[string][]byte) error {
	archive := tar.NewWriter(output)
	if err := archive.WriteHeader(&tar.Header{Name: "LICENSES/", Typeflag: tar.TypeDir, Mode: 0755, Format: tar.FormatUSTAR}); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		mode := int64(0644)
		if name == App {
			mode = 0755
		}
		if err := archive.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(files[name])), Format: tar.FormatUSTAR}); err != nil {
			return err
		}
		if _, err := archive.Write(files[name]); err != nil {
			return err
		}
	}
	return archive.Close()
}
