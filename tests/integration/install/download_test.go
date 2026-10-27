package install_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
output= headers= url=
while (($#)); do
    case "$1" in
        --output) output="$2"; shift 2 ;;
        --dump-header) headers="$2"; shift 2 ;;
        --retry|--connect-timeout|--max-time) shift 2 ;;
        --fail|--location|--silent|--show-error) shift ;;
        https://github.com/fixture/repo/releases/*) url="$1"; shift ;;
        *) exit 2 ;;
    esac
done
cp -- "$DOWNLOAD_FIXTURES/${url##*/}" "$output"
if [[ -n "$headers" ]]; then
    printf 'HTTP/2 302\r\nLocation: https://github.com/fixture/repo/releases/download/v1/%s\r\n\r\nHTTP/2 302\r\nLocation: https://release-assets.githubusercontent.com/assets/%s\r\n\r\nHTTP/2 200\r\n\r\n' "${url##*/}" "${url##*/}" > "$headers"
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

func TestReleaseTagHeaderFallback(t *testing.T) {
	cases := []struct {
		name       string
		headerMode string
		pinned     bool
		wantExit   int
	}{
		{name: "missing tag header fails closed", headerMode: "missing", wantExit: 1},
		{name: "invalid tag header fails closed", headerMode: "invalid", wantExit: 1},
		{name: "missing tag header falls back to pinned version", headerMode: "missing", pinned: true, wantExit: 0},
		{name: "invalid tag header falls back to pinned version", headerMode: "invalid", pinned: true, wantExit: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			helperName := "cage-xtmapper-installer-linux-" + runtime.GOARCH
			writeAsset(archiveName, readFile(t, h.v1))
			writeAsset(helperName, readFile(t, h.installer))

			mockCurl := `#!/usr/bin/env bash
set -euo pipefail
output= headers= url=
while (($#)); do
    case "$1" in
        --output) output="$2"; shift 2 ;;
        --dump-header) headers="$2"; shift 2 ;;
        --retry|--connect-timeout|--max-time) shift 2 ;;
        --fail|--location|--silent|--show-error) shift ;;
        https://github.com/fixture/repo/releases/*) url="$1"; shift ;;
        *) exit 2 ;;
    esac
done
printf "%s\\n" "$url" >> "$CURL_LOG"
if [[ "$url" == *"/releases/latest/download/"* && "$MOCK_HEADERS_MODE" != valid ]]; then
    printf "stale latest helper payload\\n" > "$output"
else
    cp -- "$DOWNLOAD_FIXTURES/${url##*/}" "$output"
fi
if [[ -n "$headers" ]]; then
    case "$MOCK_HEADERS_MODE" in
        valid)
            printf "HTTP/2 302\\r\\nLocation: https://github.com/fixture/repo/releases/download/v1/%s\\r\\n\\r\\nHTTP/2 302\\r\\nLocation: https://release-assets.githubusercontent.com/assets/%s\\r\\n\\r\\nHTTP/2 200\\r\\n\\r\\n" "${url##*/}" "${url##*/}" > "$headers"
            ;;
        missing)
            printf "HTTP/2 302\\r\\nLocation: https://release-assets.githubusercontent.com/assets/%s\\r\\n\\r\\nHTTP/2 200\\r\\n\\r\\n" "${url##*/}" > "$headers"
            ;;
        invalid)
            printf "HTTP/2 302\\r\\nLocation: https://github.com/fixture/repo/releases/download/not-a-tag/%s\\r\\n\\r\\nHTTP/2 302\\r\\nLocation: https://release-assets.githubusercontent.com/assets/%s\\r\\n\\r\\nHTTP/2 200\\r\\n\\r\\n" "${url##*/}" "${url##*/}" > "$headers"
            ;;
        *) exit 3 ;;
    esac
fi
`
			writeFile(t, filepath.Join(mockBin, "curl"), []byte(mockCurl), 0755)
			t.Setenv("PATH", mockBin+":"+os.Getenv("PATH"))
			t.Setenv("TMPDIR", temporary)
			t.Setenv("DOWNLOAD_FIXTURES", downloads)
			t.Setenv("XTMAPPER_INSTALL_REPO", "fixture/repo")
			t.Setenv("XTMAPPER_INSTALL_VERSION", "")
			t.Setenv("MOCK_HEADERS_MODE", tc.headerMode)
			curlLog := filepath.Join(h.workspace, "curl.log")
			t.Setenv("CURL_LOG", curlLog)

			args := h.args("", "")
			if tc.pinned {
				args = append(args, "--version", "v1")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, filepath.Join(h.repo, "install.sh"), args...)
			cmd.Env = cleanEnv()
			output, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.wantExit {
				t.Fatalf("header mode %s (pinned=%t): want exit %d, got %v\n%s", tc.headerMode, tc.pinned, tc.wantExit, err, output)
			}

			curlCalls := string(readFile(t, curlLog))
			pinnedHelperURL := "https://github.com/fixture/repo/releases/download/v1/" + helperName
			if strings.Contains(curlCalls, pinnedHelperURL) != tc.pinned {
				t.Fatalf("pinned helper fallback mismatch (pinned=%t):\n%s", tc.pinned, curlCalls)
			}
			if tc.pinned {
				if !strings.Contains(string(output), "falling back to pinned version v1") {
					t.Fatalf("expected explicit fallback diagnostic:\n%s", output)
				}
				if h.active() != "releases/"+h.id1 {
					t.Fatal("pinned release archive was not installed")
				}
				h.onlyRelease(h.id1)
			} else {
				if !strings.Contains(string(output), "could not find a published release installer asset") {
					t.Fatalf("expected missing/invalid tag failure:\n%s", output)
				}
				assertAbsent(t, h.root)
			}
			entries, err := os.ReadDir(temporary)
			must(t, err)
			if len(entries) != 0 {
				t.Fatalf("temporary downloads were not cleaned up: %v", entries)
			}
		})
	}
}
