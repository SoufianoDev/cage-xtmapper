package installer

import "testing"

func TestDebianRuntimePackageMapping(t *testing.T) {
	for library, want := range map[string]string{
		"libc.so.6": "libc6",
		"libwayland-client.so.0": "libwayland-client0",
		"libxkbcommon.so.0": "libxkbcommon0",
		"libdrm.so.2": "libdrm2",
		"libinput.so.10": "libinput10",
		"libseat.so.1": "libseat1",
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
