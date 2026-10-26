package releasebuild

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Xtr126/cage-xtmapper/internal/release"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeSourceDigest(t *testing.T) {
	files := map[string]string{"a.c": "one", "sub/b.h": "two", "sub/deep/c.c": "three"}
	first, second := t.TempDir(), t.TempDir()
	writeTree(t, first, files)
	// Create the same content in reverse order: traversal order must not matter.
	reversed := make(map[string]string, len(files))
	for name, content := range files {
		reversed[name] = content
	}
	writeTree(t, second, reversed)

	digest, err := NativeSourceDigest(first, ".")
	if err != nil {
		t.Fatal(err)
	}
	again, err := NativeSourceDigest(first, ".")
	if err != nil {
		t.Fatal(err)
	}
	if digest != again {
		t.Fatal("digest is not deterministic")
	}
	other, err := NativeSourceDigest(second, ".")
	if err != nil {
		t.Fatal(err)
	}
	if other != digest {
		t.Fatal("digest depends on traversal order")
	}

	if err := os.WriteFile(filepath.Join(first, "a.c"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := NativeSourceDigest(first, ".")
	if err != nil {
		t.Fatal(err)
	}
	if changed == digest {
		t.Fatal("digest ignores content changes")
	}
	if err := os.Chmod(filepath.Join(first, "a.c"), 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := NativeSourceDigest(first, ".")
	if err != nil {
		t.Fatal(err)
	}
	if executable == changed {
		t.Fatal("digest ignores executable bit")
	}
	if err := os.Symlink("a.c", filepath.Join(first, "link")); err != nil {
		t.Fatal(err)
	}
	linked, err := NativeSourceDigest(first, ".")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(first, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub/b.h", filepath.Join(first, "link")); err != nil {
		t.Fatal(err)
	}
	retargeted, err := NativeSourceDigest(first, ".")
	if err != nil {
		t.Fatal(err)
	}
	if linked == retargeted {
		t.Fatal("digest ignores symlink targets")
	}
}

func TestNativeSourceDigestRejectsSpecialFiles(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "native-src/cage"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(repo, "native-src/cage/fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NativeSourceDigest(repo, "native-src/cage"); err == nil {
		t.Fatal("accepted special file")
	}
}

const validOrigins = "cage 3195865db1548e8a8101de82284f03697e835f14 https://github.com/Xtr126/cage.git c317ff85f05bd3f671bbb18b6b985b0d43b62cfe\n" +
	"wlroots cd59bb46568e491b58574c5bc6fb24b97a32ae8f https://github.com/Xtr126/wlroots-xtmapper.git cd59bb46568e491b58574c5bc6fb24b97a32ae8f\n"

func TestNativeOrigins(t *testing.T) {
	repo := t.TempDir()
	originsFile := filepath.Join(repo, "native-src/ORIGINS")
	if err := os.MkdirAll(filepath.Dir(originsFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(originsFile, []byte("# provenance\n"+validOrigins), 0o644); err != nil {
		t.Fatal(err)
	}
	commits, origins, err := NativeOrigins(repo)
	if err != nil {
		t.Fatal(err)
	}
	if commits["cage"] != "3195865db1548e8a8101de82284f03697e835f14" ||
		origins["wlroots"].URL != "https://github.com/Xtr126/wlroots-xtmapper.git" {
		t.Fatalf("unexpected parse result: %v %v", commits, origins)
	}
	for name, content := range map[string]string{
		"malformed":    "cage deadbeef\n",
		"unknown key":  strings.Replace(validOrigins, "cage ", "other ", 1),
		"bad url":      strings.Replace(validOrigins, "https://", "http://", 1),
		"short commit": strings.Replace(validOrigins, "3195865db1548e8a8101de82284f03697e835f14", "3195865", 1),
		"missing":      "cage 3195865db1548e8a8101de82284f03697e835f14 https://github.com/Xtr126/cage.git c317ff85f05bd3f671bbb18b6b985b0d43b62cfe\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(originsFile, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := NativeOrigins(repo); err == nil {
				t.Fatal("accepted invalid ORIGINS")
			}
		})
	}
}

func requireClang(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
}

func inputRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeTree(t, repo, map[string]string{
		"go.mod": "module fixture\n", "build.sh": "#!/bin/sh\n", "install.sh": "#!/bin/sh\n",
		"native-src/cage/meson.build":             "project('cage')\n",
		"native-src/wlroots-xtmapper/meson.build": "project('wlroots')\n",
		"native-src/ORIGINS":                      validOrigins,
		"meson_wrapfiles/x.wrap":                  "[wrap-file]\n",
		"wlroots_deps/archive.tar.xz":             "payload\n",
	})
	// SourceDigest walks these source directories.
	for _, dir := range []string{"cmd", "internal", "tools"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestNativeInputDigest(t *testing.T) {
	requireClang(t)
	repo := inputRepo(t)
	digest, err := NativeInputDigest(repo, "release")
	if err != nil {
		t.Fatal(err)
	}
	again, err := NativeInputDigest(repo, "release")
	if err != nil {
		t.Fatal(err)
	}
	if digest != again {
		t.Fatal("digest is not deterministic")
	}
	if other, err := NativeInputDigest(repo, "debug"); err != nil || other == digest {
		t.Fatalf("build type not covered: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "build.sh"), []byte("#!/bin/sh\n# changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := NativeInputDigest(repo, "release"); err != nil || changed == digest {
		t.Fatalf("build.sh not covered: %v", err)
	}
}

func TestVerifyFresh(t *testing.T) {
	requireClang(t)
	repo := inputRepo(t)
	binary := []byte("not-an-elf-but-content-addressed")
	if err := os.MkdirAll(filepath.Join(repo, "build/go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "build/go", release.App), binary, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceDigest, err := SourceDigest(repo)
	if err != nil {
		t.Fatal(err)
	}
	inputDigest, err := NativeInputDigest(repo, "release")
	if err != nil {
		t.Fatal(err)
	}
	commits, origins, err := NativeOrigins(repo)
	if err != nil {
		t.Fatal(err)
	}
	digests := make(map[string]string, len(nativeTrees))
	for key, tree := range nativeTrees {
		digest, err := NativeSourceDigest(repo, tree)
		if err != nil {
			t.Fatal(err)
		}
		digests[key] = digest
	}
	manifest := &release.Manifest{
		SchemaVersion: release.ManifestSchemaVersion, ELFSHA256: release.Digest(binary),
		SourceSHA256: sourceDigest, NativeCommits: commits, NativeSourceDigests: digests,
		NativeOrigins: origins, Build: release.Build{
			NativeInputsSHA256: inputDigest, MesonOptions: map[string]any{"buildtype": "release"},
		},
	}
	if err := VerifyFresh(repo, manifest); err != nil {
		t.Fatalf("fresh build rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "native-src/cage/meson.build"), []byte("project('cage') # edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFresh(repo, manifest); !errors.Is(err, errStale) {
		t.Fatalf("expected stale error, got %v", err)
	}
	manifest.SchemaVersion = 1
	if err := VerifyFresh(repo, manifest); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("expected schema error, got %v", err)
	}
}

func TestNativeSourceDigestRejectsEscapingSymlink(t *testing.T) {
	repo := t.TempDir()
	tree := filepath.Join(repo, "native-src/cage")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(repo, "native-src/outside.c")
	if err := os.WriteFile(outside, []byte("external input"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside.c", filepath.Join(tree, "external.c")); err != nil {
		t.Fatal(err)
	}
	if _, err := NativeSourceDigest(repo, "native-src/cage"); err == nil ||
		!strings.Contains(err.Error(), "escapes inventoried tree") {
		t.Fatalf("expected escaping source symlink to be rejected, got %v", err)
	}
}

func TestNativeSourceDigestSymlinkTargets(t *testing.T) {
	repo := t.TempDir()
	tree := filepath.Join(repo, "native-src/cage")
	writeTree(t, tree, map[string]string{"target.c": "one"})
	link := filepath.Join(tree, "link.c")
	if err := os.Symlink("target.c", link); err != nil {
		t.Fatal(err)
	}
	first, err := NativeSourceDigest(repo, "native-src/cage")
	if err != nil {
		t.Fatalf("internal symlink rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tree, "target.c"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := NativeSourceDigest(repo, "native-src/cage"); err != nil || changed == first {
		t.Fatalf("symlink target change not tracked: %v", err)
	}
	if err := os.Remove(filepath.Join(tree, "target.c")); err != nil {
		t.Fatal(err)
	}
	if _, err := NativeSourceDigest(repo, "native-src/cage"); err == nil {
		t.Fatal("missing symlink target accepted")
	}
}

func TestNativeSourceDigestWithSymlinkedRepositoryPath(t *testing.T) {
	realRepo := t.TempDir()
	tree := filepath.Join(realRepo, "native-src/cage")
	writeTree(t, tree, map[string]string{"target.c": "inside"})
	if err := os.Symlink("target.c", filepath.Join(tree, "link.c")); err != nil {
		t.Fatal(err)
	}

	linkParent := t.TempDir()
	repoLink := filepath.Join(linkParent, "repo")
	if err := os.Symlink(realRepo, repoLink); err != nil {
		t.Fatal(err)
	}
	if _, err := NativeSourceDigest(repoLink, "native-src/cage"); err != nil {
		t.Fatalf("valid in-tree symlink rejected through symlinked repo path: %v", err)
	}
}
