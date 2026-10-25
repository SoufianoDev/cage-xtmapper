package install_test

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xtr126/cage-xtmapper/internal/release"
	"github.com/Xtr126/cage-xtmapper/internal/releasebuild"
)

func rawHeader(t *testing.T, name string, kind byte, size, mode int64) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	must(t, writer.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: size, Mode: mode, Format: tar.FormatUSTAR}))
	header := bytes.Clone(buffer.Bytes()[:512])
	header[156] = kind
	copy(header[148:156], "        ")
	sum := 0
	for _, b := range header {
		sum += int(b)
	}
	copy(header[148:156], fmt.Sprintf("%06o\x00 ", sum))
	return header
}

func TestArchiveFormat(t *testing.T) {
	files, id := fixture(t, readFile(t, "/bin/true"), "v1")
	original := archiveBytes(t, files)
	path := filepath.Join(t.TempDir(), "release.tar")
	for _, trailer := range []int{0, 10240 - 512} {
		writeFile(t, path, append(bytes.Clone(original), make([]byte, trailer)...), 0644)
		manifest, got, err := release.ReadArchive(path, "")
		must(t, err)
		if manifest.ReleaseID != id || !maps.EqualFunc(files, got, bytes.Equal) {
			t.Fatal("valid archive changed")
		}
	}
	cases := map[string][]byte{
		"truncated":         original[:1024],
		"oversized payload": rawHeader(t, release.App, tar.TypeReg, release.MaxBytes+1, 0755),
		"missing directory": original[512:],
		"trailing record":   append(bytes.Clone(original), rawHeader(t, "install.sh", tar.TypeReg, 0, 0644)...),
		"excessive padding": append(bytes.Clone(original), make([]byte, 10240)...),
		"one end block":     original[:len(original)-512],
	}
	for _, kind := range []byte{tar.TypeGNULongName, tar.TypeXHeader, tar.TypeXGlobalHeader} {
		cases[fmt.Sprintf("hidden type %c", kind)] = append(rawHeader(t, "hidden-extension", kind, 0, 0644), original...)
	}
	badPadding := bytes.Clone(original)
	badPadding[1024+len(files["LICENSES/fixture.txt"])] = 1
	cases["nonzero entry padding"] = badPadding
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			writeFile(t, path, data, 0644)
			if _, _, err := release.ReadArchive(path, ""); err == nil {
				t.Fatal("accepted malformed archive")
			}
		})
	}
}

func TestInvalidArchivesNeverReachInstallation(t *testing.T) {
	h := newHarness(t, false)
	files, _ := fixture(t, h.binary, "bad")
	original := archiveBytes(t, files)
	cases := make(map[string][]byte)
	for _, name := range []string{"../escape", "/tmp/escape", "LICENSES/../../escape", "usr/local/bin/tool", "install.sh", "cage_xtmapper", "wayland-scanner", "manifest.json"} {
		data := append(bytes.Clone(original[:len(original)-1024]), rawHeader(t, name, tar.TypeReg, 0, 0644)...)
		cases[name] = append(data, make([]byte, 1024)...)
	}
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeFifo, tar.TypeChar, tar.TypeBlock, tar.TypeGNUSparse} {
		data := append(bytes.Clone(original[:len(original)-1024]), rawHeader(t, "LICENSES/link", kind, 0, 0644)...)
		cases[fmt.Sprintf("type %c", kind)] = append(data, make([]byte, 1024)...)
	}
	for _, mode := range []int64{04755, 02755, 0777, 0644} {
		data := bytes.Clone(original)
		for offset := 0; offset < len(data)-1024; {
			reader := tar.NewReader(bytes.NewReader(data[offset:]))
			header, err := reader.Next()
			must(t, err)
			if header.Name == release.App {
				copy(data[offset:offset+512], rawHeader(t, release.App, tar.TypeReg, header.Size, mode))
				break
			}
			offset += 512 + int((header.Size+511)/512)*512
		}
		cases[fmt.Sprintf("mode %o", mode)] = data
	}
	corrupt := maps.Clone(files)
	corrupt["LICENSES/fixture.txt"] = []byte("corrupted\n")
	cases["checksum"] = archiveBytes(t, corrupt)
	missing := maps.Clone(files)
	delete(missing, "manifest.json")
	cases["missing manifest"] = archiveBytes(t, missing)
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			h.t = t
			path := filepath.Join(h.workspace, "invalid.tar")
			writeFile(t, path, data, 0644)
			h.run("install", path, 1)
			assertAbsent(t, h.root)
			assertAbsent(t, filepath.Join(h.prefix, "escape"))
		})
	}
	h.t = t
}

func TestManifestAndELFValidation(t *testing.T) {
	files, _ := fixture(t, readFile(t, "/bin/true"), "v1")
	path := filepath.Join(t.TempDir(), "release.tar")
	for _, field := range []string{"main_dirty", "source_sha256", "native_commits", "native_source_digests", "native_origins", "toolchains", "build", "runtime_requirements", "licenses"} {
		t.Run("missing "+field, func(t *testing.T) {
			changed := maps.Clone(files)
			var manifest map[string]any
			must(t, json.Unmarshal(changed["manifest.json"], &manifest))
			delete(manifest, field)
			changed["manifest.json"] = jsonBytes(t, manifest)
			changed["SHA256SUMS"] = release.Checksums(changed)
			writeFile(t, path, archiveBytes(t, changed), 0644)
			if _, _, err := release.ReadArchive(path, ""); err == nil {
				t.Fatal("accepted missing provenance")
			}
		})
	}
	for field, value := range map[string]any{
		"release_id": "valid-but-unrelated-id", "elf_sha256": strings.Repeat("0", 64),
		"target": map[string]string{"os": "linux", "architecture": "wrong"},
	} {
		t.Run(field, func(t *testing.T) {
			changed := maps.Clone(files)
			var manifest map[string]any
			must(t, json.Unmarshal(changed["manifest.json"], &manifest))
			manifest[field] = value
			changed["manifest.json"] = jsonBytes(t, manifest)
			changed["SHA256SUMS"] = release.Checksums(changed)
			if _, err := release.ValidateContents(changed, ""); err == nil {
				t.Fatal("accepted invalid manifest identity")
			}
		})
	}
	binaryData := bytes.Clone(files[release.App])
	binary.LittleEndian.PutUint64(binaryData[24:], 0)
	if _, err := release.ELFArch(binaryData); err == nil {
		t.Fatal("accepted ELF without executable entry")
	}
	arch, err := release.ELFArch(files[release.App])
	must(t, err)
	other := "amd64"
	if arch == other {
		other = "arm64"
	}
	if _, err := release.ValidateContents(files, other); err == nil {
		t.Fatal("accepted wrong host architecture")
	}
}

func TestManifestSchemaVersions(t *testing.T) {
	files, _ := fixture(t, readFile(t, "/bin/true"), "v1")
	reformat := func(t *testing.T, mutate func(map[string]any)) map[string][]byte {
		t.Helper()
		changed := maps.Clone(files)
		var manifest map[string]any
		must(t, json.Unmarshal(changed["manifest.json"], &manifest))
		mutate(manifest)
		changed["manifest.json"] = jsonBytes(t, manifest)
		changed["SHA256SUMS"] = release.Checksums(changed)
		return changed
	}
	t.Run("legacy schema 1 without native digests", func(t *testing.T) {
		legacy := reformat(t, func(manifest map[string]any) {
			manifest["schema_version"] = 1
			delete(manifest, "native_source_digests")
			delete(manifest, "native_origins")
		})
		if _, err := release.ValidateContents(legacy, ""); err != nil {
			t.Fatalf("legacy manifest rejected: %v", err)
		}
	})
	t.Run("unknown future schema", func(t *testing.T) {
		future := reformat(t, func(manifest map[string]any) { manifest["schema_version"] = 99 })
		if _, err := release.ValidateContents(future, ""); err == nil {
			t.Fatal("accepted unknown schema")
		}
	})
	t.Run("invalid origin URL", func(t *testing.T) {
		insecure := reformat(t, func(manifest map[string]any) {
			origins := manifest["native_origins"].(map[string]any)
			cage := origins["cage"].(map[string]any)
			cage["url"] = "http://example.invalid/cage.git"
		})
		if _, err := release.ValidateContents(insecure, ""); err == nil {
			t.Fatal("accepted non-https origin")
		}
	})
	t.Run("non-commit native revision", func(t *testing.T) {
		short := reformat(t, func(manifest map[string]any) {
			origins := manifest["native_origins"].(map[string]any)
			wlroots := origins["wlroots"].(map[string]any)
			wlroots["revision"] = "branch-name"
		})
		if _, err := release.ValidateContents(short, ""); err == nil {
			t.Fatal("accepted non-commit revision")
		}
	})
}

func TestStrictJSON(t *testing.T) {
	for _, data := range []string{
		`{"version": 1, "version": 2}`, `{"value": NaN}`, `{"value": Infinity}`, `{"value": 1e999}`,
		strings.Repeat("[", 2000) + strings.Repeat("]", 2000), `{} {}`, "{\"value\":\"\xff\"}",
	} {
		var value any
		if err := release.DecodeJSON([]byte(data), &value); err == nil {
			t.Fatalf("accepted invalid JSON: %.40s", data)
		}
	}
}

func TestNativeLinkContract(t *testing.T) {
	text, err := releasebuild.NativeContract("/build with spaces", "/source with spaces", `clang -o cage_xtmapper 'cage object.o' -Wl,--start-group 'subprojects/libwlroots.a' /system/libtest.a -lffi -Wl,--end-group`)
	must(t, err)
	for _, required := range []string{"'/build with spaces/libcage_xtmapper.a'", "'/build with spaces/subprojects/libwlroots.a'", "'/system/libtest.a'", "'-I/source with spaces'", "-Wl,--start-group", "-Wl,--end-group"} {
		if !strings.Contains(text, required) {
			t.Fatalf("lost %s: %s", required, text)
		}
	}
	if strings.Contains(text, "object.o") {
		t.Fatal("standalone objects leaked into library link")
	}
	for _, command := range []string{`clang object.o -lm`, `clang -o cage object.o`, `clang -o cage 'unfinished`, `clang -o cage -lm; touch injected`, `clang -o cage $(touch injected)`} {
		if _, err := releasebuild.NativeContract("/build", "/source", command); err == nil {
			t.Fatalf("accepted unsafe/incomplete link command: %s", command)
		}
	}
}

func TestInstallerCLIStatuses(t *testing.T) {
	h := newHarness(t, false)
	h.run("unknown", "", 2)
	h.run("install", "", 2)
	h.run("status", h.v1, 2)
	assertAbsent(t, h.root)
}

func TestArchiveSymlinkRefused(t *testing.T) {
	h := newHarness(t, false)
	link := filepath.Join(h.workspace, "archive-link")
	must(t, os.Symlink(h.v1, link))
	h.run("install", link, 1)
	assertAbsent(t, h.root)
}
