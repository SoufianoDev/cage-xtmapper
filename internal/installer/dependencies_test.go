package installer

import (
	"strings"
	"testing"
)

func TestDebianRuntimePackageMapping(t *testing.T) {
	for library, want := range map[string]string{
		"libc.so.6":              "libc6",
		"libwayland-client.so.0": "libwayland-client0",
		"libxkbcommon.so.0":      "libxkbcommon0",
		"libdrm.so.2":            "libdrm2",
		"libinput.so.10":         "libinput10",
		"libseat.so.1":           "libseat1",
	} {
		if got := debianRuntimePackages[library]; got != want {
			t.Errorf("package for %s = %q, want %q", library, got, want)
		}
	}
}

func TestCompareDottedVersions(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"2.39", "2.39", 0},
		{"2.39", "2.40", -1},
		{"2.40", "2.39", 1},
		{"2.40.1", "2.40", 1},
		{"2.39.7", "2.39.7", 0},
	} {
		got, err := compareDottedVersions(test.left, test.right)
		if err != nil {
			t.Fatalf("compareDottedVersions(%q, %q): %v", test.left, test.right, err)
		}
		if got != test.want {
			t.Errorf("compareDottedVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestLDConfigEntryMatchesArch(t *testing.T) {
	for _, test := range []struct {
		name  string
		entry string
		arch  string
		want  bool
	}{
		{name: "amd64 matches x86-64", entry: "(libc6,x86-64)", arch: "amd64", want: true},
		{name: "amd64 matches x86-64 variant", entry: "(libc6,x86-64-v3)", arch: "amd64", want: true},
		{name: "amd64 rejects AArch64", entry: "(libc6,AArch64)", arch: "amd64", want: false},
		{name: "arm64 matches AArch64", entry: "(libc6,AArch64)", arch: "arm64", want: true},
		{name: "arm64 accepts arm64 label", entry: "(libc6,ARM64)", arch: "arm64", want: true},
		{name: "arm64 rejects x86-64", entry: "(libc6,x86-64)", arch: "arm64", want: false},
		{name: "unknown architecture rejected", entry: "(libc6)", arch: "amd64", want: false},
		{name: "unsupported host rejected", entry: "(libc6,x86-64)", arch: "386", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ldconfigEntryMatchesArch(test.entry, test.arch); got != test.want {
				t.Errorf("ldconfigEntryMatchesArch(%q, %q) = %t, want %t", test.entry, test.arch, got, test.want)
			}
		})
	}
}

func TestParseInstalledSharedLibrariesFiltersArchitecture(t *testing.T) {
	output := strings.Join([]string{
		"libamd64-only.so.1 (libc6,x86-64) => /lib/x86_64-linux-gnu/libamd64-only.so.1",
		"libarm64-only.so.1 (libc6,AArch64) => /lib/aarch64-linux-gnu/libarm64-only.so.1",
		"libno-arch.so.1 => /lib/libno-arch.so.1",
		"",
	}, "\n")
	for _, test := range []struct {
		arch        string
		wantPresent string
		wantAbsent  string
	}{
		{arch: "amd64", wantPresent: "libamd64-only.so.1", wantAbsent: "libarm64-only.so.1"},
		{arch: "arm64", wantPresent: "libarm64-only.so.1", wantAbsent: "libamd64-only.so.1"},
	} {
		installed, err := parseInstalledSharedLibraries(output, test.arch)
		if err != nil {
			t.Fatalf("parseInstalledSharedLibraries(%q): %v", test.arch, err)
		}
		if !installed[test.wantPresent] {
			t.Errorf("%s: expected %s to be installed", test.arch, test.wantPresent)
		}
		if installed[test.wantAbsent] {
			t.Errorf("%s: incompatible %s was accepted", test.arch, test.wantAbsent)
		}
		if installed["libno-arch.so.1"] {
			t.Errorf("%s: entry without an architecture qualifier was accepted", test.arch)
		}
	}
}
