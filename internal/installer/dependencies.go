package installer

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/Xtr126/cage-xtmapper/internal/release"
)

var debianRuntimePackages = map[string]string{
	"libc.so.6":              "libc6",
	"libm.so.6":              "libc6",
	"libpthread.so.0":        "libc6",
	"libdl.so.2":             "libc6",
	"librt.so.1":             "libc6",
	"libresolv.so.2":         "libc6",
	"libgcc_s.so.1":          "libgcc-s1",
	"libstdc++.so.6":         "libstdc++6",
	"libz.so.1":              "zlib1g",
	"libwayland-client.so.0": "libwayland-client0",
	"libwayland-server.so.0": "libwayland-server0",
	"libwayland-cursor.so.0": "libwayland-cursor0",
	"libwayland-egl.so.1":    "libwayland-egl1",
	"libxkbcommon.so.0":      "libxkbcommon0",
	"libxkbregistry.so.0":    "libxkbregistry0",
	"libdrm.so.2":            "libdrm2",
	"libinput.so.10":         "libinput10",
	"libseat.so.1":           "libseat1",
	"libudev.so.1":           "libudev1",
	"libgbm.so.1":            "libgbm1",
	"libEGL.so.1":            "libegl1",
	"libGLESv2.so.2":         "libgles2",
	"libpixman-1.so.0":       "libpixman-1-0",
	"libdisplay-info.so.2":   "libdisplay-info2",
	"libliftoff.so.0":        "libliftoff0",
	"libsystemd.so.0":        "libsystemd0",
	"libxcb.so.1":            "libxcb1",
	"libxcb-render.so.0":     "libxcb-render0",
	"libxcb-shm.so.0":        "libxcb-shm0",
	"libX11.so.6":            "libx11-6",
	"libXau.so.6":            "libxau6",
	"libXdmcp.so.6":          "libxdmcp6",
	"libffi.so.8":            "libffi8",
	"libexpat.so.1":          "libexpat1",
	"libxml2.so.2":           "libxml2",
	"libepoxy.so.0":          "libepoxy0",
	"libgudev-1.0.so.0":      "libgudev-1.0-0",
	"libglib-2.0.so.0":       "libglib2.0-0",
	"libgobject-2.0.so.0":    "libglib2.0-0",
	"libgmodule-2.0.so.0":    "libglib2.0-0",
	"libgio-2.0.so.0":        "libglib2.0-0",
	"libpcre2-8.so.0":        "libpcre2-8-0",
}

func installedSharedLibraries() (map[string]bool, error) {
	output, err := exec.Command("ldconfig", "-p").Output()
	if err != nil {
		return nil, fmt.Errorf("cannot inspect installed shared libraries with ldconfig: %w", err)
	}
	installed := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 0 && strings.Contains(fields[0], ".so") {
			installed[fields[0]] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return installed, nil
}

func glibcVersion() (string, error) {
	output, err := exec.Command("getconf", "GNU_LIBC_VERSION").Output()
	if err != nil {
		return "", fmt.Errorf("cannot determine the host glibc version: %w", err)
	}
	fields := strings.Fields(string(output))
	if len(fields) != 2 || fields[0] != "glibc" {
		return "", fmt.Errorf("unexpected getconf GNU_LIBC_VERSION output %q", strings.TrimSpace(string(output)))
	}
	return fields[1], nil
}

func compareDottedVersions(left, right string) (int, error) {
	a, b := strings.Split(left, "."), strings.Split(right, ".")
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		var err error
		if i < len(a) {
			av, err = strconv.Atoi(a[i])
			if err != nil {
				return 0, err
			}
		}
		if i < len(b) {
			bv, err = strconv.Atoi(b[i])
			if err != nil {
				return 0, err
			}
		}
		if av < bv {
			return -1, nil
		}
		if av > bv {
			return 1, nil
		}
	}
	return 0, nil
}

func debianLike() bool {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return false
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok || (key != "ID" && key != "ID_LIKE") {
			continue
		}
		values[key] = strings.ToLower(strings.Trim(value, `"'`))
	}
	if err := scanner.Err(); err != nil {
		return false
	}
	ids := " " + values["ID"] + " " + values["ID_LIKE"] + " "
	return values["ID"] == "ubuntu" || values["ID"] == "debian" ||
		strings.Contains(ids, " debian ") || strings.Contains(ids, " ubuntu ")
}

func runApt(args ...string) error {
	command := exec.Command("apt-get", args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	if err := command.Run(); err != nil {
		return fmt.Errorf("apt-get %s failed: %w", strings.Join(args, " "), err)
	}
	return nil
}

func ensureRuntimeDependencies(requirements release.RuntimeRequirements) error {
	if requirements.Interpreter == nil || *requirements.Interpreter == "" {
		return fmt.Errorf("release does not declare an ELF interpreter")
	}
	if _, err := os.Stat(*requirements.Interpreter); err != nil {
		return fmt.Errorf("required ELF interpreter %s is not available on this system: %w", *requirements.Interpreter, err)
	}
	if requirements.GlibcMinimum != nil {
		current, err := glibcVersion()
		if err != nil {
			return err
		}
		comparison, err := compareDottedVersions(current, *requirements.GlibcMinimum)
		if err != nil {
			return fmt.Errorf("invalid glibc version: %w", err)
		}
		if comparison < 0 {
			return fmt.Errorf("this release requires glibc %s or newer; host provides %s", *requirements.GlibcMinimum, current)
		}
	}
	installed, err := installedSharedLibraries()
	if err != nil {
		return err
	}
	missing := make([]string, 0)
	for _, library := range requirements.SharedLibraries {
		if !installed[library] {
			missing = append(missing, library)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if !debianLike() {
		return fmt.Errorf("missing runtime libraries %s; automatic package installation is currently supported on Debian/Ubuntu only; install the matching packages for your distribution and retry", strings.Join(missing, ", "))
	}
	packages := make(map[string]bool)
	unknown := make([]string, 0)
	for _, library := range missing {
		name, ok := debianRuntimePackages[library]
		if !ok {
			unknown = append(unknown, library)
			continue
		}
		packages[name] = true
	}
	if len(unknown) > 0 {
		return fmt.Errorf("cannot safely map missing runtime libraries to Debian/Ubuntu packages: %s; install them manually and retry", strings.Join(unknown, ", "))
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return fmt.Errorf("apt-get is required to install missing runtime packages: %s", strings.Join(sortedKeys(packages), ", "))
	}
	fmt.Fprintln(os.Stderr, "Installing missing runtime packages:", strings.Join(sortedKeys(packages), ", "))
	if err := runApt("update", "-q"); err != nil {
		return err
	}
	if err := runApt(append([]string{"install", "-y", "-q"}, sortedKeys(packages)...)...); err != nil {
		return err
	}
	if err := exec.Command("ldconfig").Run(); err != nil {
		return fmt.Errorf("runtime packages were installed but ldconfig failed: %w", err)
	}
	installed, err = installedSharedLibraries()
	if err != nil {
		return err
	}
	var stillMissing []string
	for _, library := range missing {
		if !installed[library] {
			stillMissing = append(stillMissing, library)
		}
	}
	if len(stillMissing) > 0 {
		return fmt.Errorf("runtime packages were installed but libraries remain unavailable: %s", strings.Join(stillMissing, ", "))
	}
	return nil
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
