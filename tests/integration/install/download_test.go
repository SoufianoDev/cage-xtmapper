package install_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Xtr126/cage-xtmapper/internal/release"
)

func TestDownloadedArchive(t *testing.T) {
	for _, override := range []bool{false, true} {
		name := "downloaded helper"
		if override {
			name = "local helper"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, false)
			downloads := filepath.Join(h.workspace, "downloads")
			mockBin := filepath.Join(h.workspace, "mock-bin")
			temporary := filepath.Join(h.workspace, "temporary downloads")
			for _, directory := range []string{downloads, mockBin, temporary} {
				must(t, os.Mkdir(directory, 0700))
			}
			writeAsset := func(name string, data []byte) {
				writeFile(t, filepath.Join(downloads, name), data, 0644)
				writeFile(t, filepath.Join(downloads, name+".sha256"), []byte(release.Digest(data)+"  "+name+"\n"), 0644)
			}
			archiveName := "cage-xtmapper-v1-" + runtime.GOARCH + ".tar"
			writeAsset(archiveName, readFile(t, h.v1))
			writeAsset("cage-xtmapper-installer-linux-"+runtime.GOARCH, readFile(t, h.installer))
			writeFile(t, filepath.Join(mockBin, "curl"), []byte(`#!/usr/bin/env bash
set -euo pipefail
output= url= effective=false
while (($#)); do
    case "$1" in
        --output) output="$2"; shift 2 ;;
        --write-out) effective=true; shift 2 ;;
        --retry|--connect-timeout|--max-time) shift 2 ;;
        --fail|--location|--silent|--show-error) shift ;;
        https://github.com/fixture/repo/releases/*) url="$1"; shift ;;
        *) exit 2 ;;
    esac
done
cp -- "$DOWNLOAD_FIXTURES/${url##*/}" "$output"
if [[ "$effective" == true ]]; then
    printf 'https://github.com/fixture/repo/releases/download/v1/%s' "${url##*/}"
fi
`), 0755)
			t.Setenv("PATH", mockBin+":"+os.Getenv("PATH"))
			t.Setenv("TMPDIR", temporary)
			t.Setenv("DOWNLOAD_FIXTURES", downloads)
			t.Setenv("XTMAPPER_INSTALL_REPO", "fixture/repo")
			t.Setenv("XTMAPPER_INSTALL_VERSION", "")
			args := h.args("", "")
			env := cleanEnv()
			if override {
				args = append(args, "--version", "v1")
				env = append(env, "XTMAPPER_INSTALLER_BIN="+h.installer)
			}
			run := func(code int) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, filepath.Join(h.repo, "install.sh"), args...)
				cmd.Env = env
				output, err := cmd.CombinedOutput()
				if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != code {
					t.Fatalf("downloaded archive: want exit %d, got %v\n%s", code, err, output)
				}
				entries, err := os.ReadDir(temporary)
				must(t, err)
				if len(entries) != 0 {
					t.Fatalf("downloaded assets were not cleaned up: %v", entries)
				}
			}
			run(0)
			if h.active() != "releases/"+h.id1 {
				t.Fatal("downloaded archive was not installed")
			}
			// A valid outer checksum lets malformed content reach the real helper.
			writeAsset(archiveName, []byte("invalid archive"))
			run(1)
			h.onlyRelease(h.id1)
		})
	}
}
