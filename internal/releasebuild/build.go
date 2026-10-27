// Package releasebuild produces release archives and build provenance.
package releasebuild

import (
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Xtr126/cage-xtmapper/internal/release"
)

type buildInfo struct {
	release.Manifest
	LicenseTexts map[string]string `json:"license_texts"`
}

func command(repo string, args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	data, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(data)), nil
}

func SourceDigest(repo string) (string, error) {
	paths := []string{"go.mod", "build.sh", "install.sh"}
	for _, directory := range []string{"cmd", "internal", "tools"} {
		err := filepath.WalkDir(filepath.Join(repo, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && slices.Contains([]string{".go", ".c", ".h", ".sh"}, filepath.Ext(path)) && !strings.HasSuffix(path, "_test.go") {
				relative, err := filepath.Rel(repo, path)
				if err != nil {
					return err
				}
				paths = append(paths, relative)
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	slices.Sort(paths)
	hash := sha256.New()
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(repo, path))
		if err != nil {
			return "", err
		}
		hash.Write([]byte(path + "\x00"))
		hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type pluginLicenseRule struct {
	component string
	license   string
	header    string
}

func littleCMSPluginLicenseRule(archive string) (pluginLicenseRule, bool) {
	switch archive {
	case "liblcms2_fast_float.a":
		return pluginLicenseRule{component: "LittleCMS-fast-float", license: "GPL-3.0-or-later", header: "lcms2_fast_float.h"}, true
	case "liblcms2_threaded.a":
		return pluginLicenseRule{component: "LittleCMS-threaded", license: "GPL-3.0-or-later", header: "lcms2_threaded.h"}, true
	default:
		return pluginLicenseRule{}, false
	}
}

// pluginEntrySymbols maps a LittleCMS GPL plugin archive to the registration
// symbol that proves its code linked into the produced ELF. Debian's lcms2
// package lists both plugin archives on every lcms2 link line, but the linker
// pulls a member only to satisfy a reference, so presence on the link line
// alone must not create a license component for the distribution.
var pluginEntrySymbols = map[string]string{
	"liblcms2_fast_float.a": "cmsFastFloatExtensions",
	"liblcms2_threaded.a":   "cmsThreadedExtensions",
}

func pluginEntryLinked(binary, entry string) (bool, error) {
	file, err := elf.Open(binary)
	if err != nil {
		return false, err
	}
	defer file.Close()
	for _, table := range []func() ([]elf.Symbol, error){file.Symbols, file.DynamicSymbols} {
		symbols, err := table()
		if errors.Is(err, elf.ErrNoSymbols) {
			continue
		}
		if err != nil {
			return false, err
		}
		for _, symbol := range symbols {
			if symbol.Name == entry {
				return true, nil
			}
		}
	}
	return false, nil
}

func licenseTexts(repo, binary string, archives []string) ([]release.License, map[string]string, error) {
	source := filepath.Join(repo, "build/source/cage-0.3.0")
	type rule struct{ component, license, path string }
	rules := map[string]rule{
		"libcage_xtmapper.a":  {"Cage", "MIT", "LICENSE"},
		"libwlroots-0.20.a":   {"wlroots", "MIT", "subprojects/wlroots/LICENSE"},
		"libwayland-server.a": {"Wayland", "MIT", "subprojects/wayland-1.24.0/COPYING"},
		"libwayland-client.a": {"Wayland", "MIT", "subprojects/wayland-1.24.0/COPYING"},
		"libxkbcommon.a":      {"libxkbcommon", "MIT and source notices", "subprojects/libxkbcommon-1.8.0/LICENSE"},
		"libpixman-1.a":       {"Pixman", "MIT and source notices", "subprojects/pixman-0.46.0/COPYING"},
		"libdisplay-info.a":   {"libdisplay-info", "MIT", "subprojects/libdisplay-info-0.2.0/LICENSE"},
		"libliftoff.a":        {"libliftoff", "MIT", "subprojects/libliftoff-0.5.0/LICENSE"},
		"libseat.a":           {"seatd", "MIT", "subprojects/seatd-0.9.1/LICENSE"},
	}
	texts := make(map[string]string)
	var licenses []release.License
	add := func(component, license string, data []byte) {
		path := "LICENSES/" + component + ".txt"
		if _, ok := texts[path]; !ok {
			texts[path] = string(data)
			licenses = append(licenses, release.License{Component: component, License: license, Path: path})
		}
	}
	addFile := func(component, license, path string) error {
		data, err := os.ReadFile(path)
		if err == nil {
			add(component, license, data)
		}
		return err
	}
	if err := addFile(release.App, "MIT", filepath.Join(repo, "LICENSE")); err != nil {
		return nil, nil, err
	}
	goroot, err := command(repo, "go", "env", "GOROOT")
	if err != nil {
		return nil, nil, err
	}
	goNotice, err := firstFile(
		filepath.Join(goroot, "LICENSE"),
		filepath.Join(goroot, "src", "LICENSE"),
		filepath.Join("/usr/share/doc", "golang-"+strings.TrimPrefix(filepath.Base(goroot), "go-")+"-go", "copyright"),
		"/usr/share/doc/golang-go/copyright",
		"/usr/share/doc/go/copyright",
		"/usr/share/licenses/go/LICENSE",
		"/usr/share/licenses/golang/LICENSE",
	)
	if err != nil {
		return nil, nil, err
	}
	if err := addFile("Go", "BSD-3-Clause and component notices", goNotice); err != nil {
		return nil, nil, err
	}
	for _, archive := range archives {
		name := filepath.Base(archive)
		if rule, ok := rules[name]; ok {
			if err := addFile(rule.component, rule.license, filepath.Join(source, rule.path)); err != nil {
				return nil, nil, err
			}
			continue
		}
		switch name {
		case "libdrm.a":
			drm := filepath.Join(source, "subprojects/libdrm-2.4.129")
			var paths []string
			for _, pattern := range []string{"*.c", "*.h"} {
				matches, err := filepath.Glob(filepath.Join(drm, pattern))
				if err != nil {
					return nil, nil, err
				}
				paths = append(paths, matches...)
			}
			if err := filepath.WalkDir(filepath.Join(drm, "include"), func(path string, entry fs.DirEntry, err error) error {
				if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".h") {
					paths = append(paths, path)
				}
				return err
			}); err != nil {
				return nil, nil, err
			}
			slices.Sort(paths)
			var notices strings.Builder
			comment := regexp.MustCompile(`(?s)^\s*(/\*.*?\*/)`)
			for _, path := range paths {
				text, err := os.ReadFile(path)
				if err != nil {
					return nil, nil, err
				}
				if match := comment.FindSubmatch(text); match != nil {
					fmt.Fprintf(&notices, "%s\n%s\n\n", strings.TrimPrefix(path, drm+"/"), match[1])
				}
			}
			if notices.Len() == 0 {
				return nil, nil, fmt.Errorf("missing libdrm source notices")
			}
			add("libdrm", "MIT and source notices", []byte(notices.String()))
		case "liblcms2_fast_float.a", "liblcms2_threaded.a":
			rule, ok := littleCMSPluginLicenseRule(name)
			if !ok {
				return nil, nil, fmt.Errorf("no license rule for LittleCMS plugin archive: %s", name)
			}
			linked, err := pluginEntryLinked(binary, pluginEntrySymbols[name])
			if err != nil {
				return nil, nil, err
			}
			if !linked {
				// The archive rides the link line via pkg-config, but the
				// linker pulled no code from it: it is not part of the ELF.
				continue
			}
			notice, err := firstFile(
				filepath.Join("/usr/include", rule.header),
				filepath.Join("/usr/include/lcms2", rule.header),
			)
			if err != nil {
				return nil, nil, err
			}
			if err := addFile(rule.component, rule.license, notice); err != nil {
				return nil, nil, err
			}
			if err := addFile("GPL-3", "GPL-3.0 text", "/usr/share/common-licenses/GPL-3"); err != nil {
				return nil, nil, err
			}
		default:
			return nil, nil, fmt.Errorf("no license rule for linked static archive: %s", archive)
		}
	}
	if err := addFile("wayland-protocols", "MIT and protocol notices", filepath.Join(source, "subprojects/wayland-protocols-1.47/COPYING")); err != nil {
		return nil, nil, err
	}
	return licenses, texts, nil
}

func firstFile(paths ...string) (string, error) {
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", fmt.Errorf("missing license notice: %s", strings.Join(paths, " or "))
}

func runtimeRequirements(binary string) (release.RuntimeRequirements, error) {
	r := release.RuntimeRequirements{
		SharedLibraries: []string{}, Commands: []string{"waydroid", "sudo (Waydroid transport)", "adb (ADB transport)"},
		Android: "XtMapper APK and /system/bin/app_process", BuildHost: "unknown",
	}
	file, err := elf.Open(binary)
	if err != nil {
		return r, err
	}
	defer file.Close()
	for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH} {
		paths, err := file.DynString(tag)
		if err != nil || len(paths) != 0 {
			return r, fmt.Errorf("ELF has invalid dynamic metadata or an RPATH/RUNPATH")
		}
	}
	libraries, err := file.ImportedLibraries()
	if err != nil {
		return r, err
	}
	for _, lib := range libraries {
		if strings.Contains(lib, "wlroots") || strings.Contains(lib, "cage") {
			return r, fmt.Errorf("ELF depends on an external project library: %s", lib)
		}
		r.SharedLibraries = append(r.SharedLibraries, lib)
	}
	slices.Sort(r.SharedLibraries)
	for _, program := range file.Progs {
		if program.Type == elf.PT_INTERP {
			data, err := io.ReadAll(io.LimitReader(program.Open(), 4097))
			if err != nil || len(data) > 4096 {
				return r, fmt.Errorf("invalid ELF interpreter")
			}
			interpreter := strings.TrimRight(string(data), "\x00")
			r.Interpreter = &interpreter
		}
	}
	symbols, err := file.ImportedSymbols()
	if err != nil && !errors.Is(err, elf.ErrNoSymbols) {
		return r, err
	}
	for _, symbol := range symbols {
		version := strings.TrimPrefix(symbol.Version, "GLIBC_")
		if version != symbol.Version && release.ValidGLIBCVersion(version) && (r.GlibcMinimum == nil || versionLess(*r.GlibcMinimum, version)) {
			r.GlibcMinimum = &version
		}
	}
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				r.BuildHost = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"'`)
			}
		}
	}
	return r, nil
}

func versionLess(a, b string) bool {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(left), len(right)); i++ {
		var l, r int
		if i < len(left) {
			l, _ = strconv.Atoi(left[i])
		}
		if i < len(right) {
			r, _ = strconv.Atoi(right[i])
		}
		if l != r {
			return l < r
		}
	}
	return false
}

func RecordBuild(repo string) error {
	build := filepath.Join(repo, "build")
	binary := filepath.Join(build, "go", release.App)
	data, err := os.ReadFile(binary)
	if err != nil {
		return err
	}
	arch, err := release.ELFArch(data)
	if err != nil {
		return err
	}
	pc, err := os.ReadFile(filepath.Join(build, "native/pkgconfig/cage-xtmapper-native.pc"))
	if err != nil {
		return err
	}
	var archives []string
	for _, line := range strings.Split(string(pc), "\n") {
		if !strings.HasPrefix(line, "Libs: ") {
			continue
		}
		tokens, err := shellWords(strings.TrimPrefix(line, "Libs: "))
		if err != nil {
			return err
		}
		for _, token := range tokens {
			if strings.HasSuffix(token, ".a") {
				archives = append(archives, token)
			}
		}
	}
	if len(archives) == 0 {
		return fmt.Errorf("missing native static archive inputs")
	}
	licenses, texts, err := licenseTexts(repo, binary, archives)
	if err != nil {
		return err
	}
	provenanceData, err := os.ReadFile(filepath.Join(build, "go/provenance.txt"))
	if err != nil {
		return err
	}
	provenance := make(map[string]string)
	for _, line := range strings.Split(string(provenanceData), "\n") {
		if name, value, ok := strings.Cut(line, "="); ok {
			provenance[name] = value
		}
	}
	if !release.ValidDigest(provenance["fingerprint"]) {
		return fmt.Errorf("missing or invalid input fingerprint in provenance.txt; rerun ./build.sh")
	}
	commits, origins, err := NativeOrigins(repo)
	if err != nil {
		return err
	}
	sourceDigests := make(map[string]string, len(nativeTrees))
	for key, tree := range nativeTrees {
		digest, err := NativeSourceDigest(repo, tree)
		if err != nil {
			return err
		}
		sourceDigests[key] = digest
	}
	readInfo := func(name string, value any) error {
		data, err := os.ReadFile(filepath.Join(build, "meson/meson-info", name))
		if err != nil {
			return err
		}
		return json.Unmarshal(data, value)
	}
	var projects struct {
		Subprojects []struct{ Name, Version string }
	}
	var dependencies []struct{ Name, Version, Type string }
	var options []struct {
		Name  string
		Value any
	}
	var compilers struct {
		Host struct {
			C struct {
				FullVersion string `json:"full_version"`
			}
		}
	}
	for name, value := range map[string]any{
		"intro-projectinfo.json": &projects, "intro-dependencies.json": &dependencies,
		"intro-buildoptions.json": &options, "intro-compilers.json": &compilers,
	} {
		if err := readInfo(name, value); err != nil {
			return err
		}
	}
	info := buildInfo{Manifest: release.Manifest{
		SchemaVersion: release.ManifestSchemaVersion, Application: release.App, Target: map[string]string{"os": "linux", "architecture": arch},
		ELFSHA256: release.Digest(data), Licenses: licenses, NativeCommits: commits,
		NativeSourceDigests: sourceDigests, NativeOrigins: origins,
		Toolchains: map[string]string{"c": compilers.Host.C.FullVersion},
		Build:      release.Build{CGOEnabled: true, GoFlags: []string{"-a"}, NativeInputsSHA256: provenance["fingerprint"], MesonOptions: make(map[string]any)},
	}, LicenseTexts: texts}
	for _, project := range projects.Subprojects {
		info.Dependencies = append(info.Dependencies, release.Dependency{Name: project.Name, Version: project.Version, Selection: "bundled"})
	}
	for _, dep := range dependencies {
		version := dep.Version
		if version == "" {
			version = "unspecified"
		}
		info.Dependencies = append(info.Dependencies, release.Dependency{Name: dep.Name, Version: version, Selection: dep.Type})
	}
	for _, option := range options {
		info.Build.MesonOptions[option.Name] = option.Value
	}
	if info.SourceSHA256, err = SourceDigest(repo); err != nil {
		return err
	}
	if info.RuntimeRequirements, err = runtimeRequirements(binary); err != nil {
		return err
	}
	if info.MainCommit, err = command(repo, "git", "rev-parse", "HEAD"); err != nil {
		return err
	}
	status, err := command(repo, "git", "status", "--porcelain")
	if err != nil {
		return err
	}
	dirty := status != ""
	info.MainDirty = &dirty
	if info.Toolchains["go"], err = command(repo, "go", "version"); err != nil {
		return err
	}
	if info.Toolchains["meson"], err = command(repo, "meson", "--version"); err != nil {
		return err
	}
	if info.Build.GoBuildInfo, err = command(repo, "go", "version", "-m", binary); err != nil {
		return err
	}
	data, err = release.JSON(info)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(build, "go/build-info.json"), data, 0644)
}

func Package(repo, version, output string) error {
	if !release.ID.MatchString(version) {
		return fmt.Errorf("invalid release version")
	}
	data, err := os.ReadFile(filepath.Join(repo, "build/go/build-info.json"))
	if err != nil {
		return err
	}
	var info buildInfo
	if err := release.DecodeJSON(data, &info); err != nil {
		return err
	}
	if err := VerifyFresh(repo, &info.Manifest); err != nil {
		return err
	}
	binary, err := os.ReadFile(filepath.Join(repo, "build/go", release.App))
	if err != nil {
		return err
	}
	info.Version = version
	info.ReleaseID = version + "-" + info.Target["architecture"] + "-" + info.ELFSHA256[:16]
	manifest, err := release.JSON(info.Manifest)
	if err != nil {
		return err
	}
	files := map[string][]byte{release.App: binary, "manifest.json": manifest}
	for name, text := range info.LicenseTexts {
		files[name] = []byte(text)
	}
	files["SHA256SUMS"] = release.Checksums(files)
	if _, err := release.ValidateContents(files, ""); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(output), ".release-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if err := release.WriteArchive(temporary, files); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if _, _, err := release.ReadArchive(temporary.Name(), ""); err != nil {
		return err
	}
	// Link publication never replaces an unrelated existing archive.
	return os.Link(temporary.Name(), output)
}
