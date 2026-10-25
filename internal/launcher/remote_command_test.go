package launcher

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRemoteCommandRoundTripsArgumentsThroughShell(t *testing.T) {
	for _, useADB := range []bool{true, false} {
		name := "waydroid"
		if useADB {
			name = "adb"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			fixture := filepath.Join(dir, "app_process")
			argvFile := filepath.Join(dir, "argv.bin")
			bootFile := filepath.Join(dir, "bootclasspath")
			injected := filepath.Join(dir, "should-not-exist")
			fixtureScript := "#!/bin/sh\n" +
				"printf '%s\\0' \"$@\" > \"$REMOTE_ARGV\"\n" +
				"printf '%s' \"$BOOTCLASSPATH\" > \"$REMOTE_BOOTCLASSPATH\"\n"
			if err := os.WriteFile(fixture, []byte(fixtureScript), 0o755); err != nil {
				t.Fatal(err)
			}

			nativeLibPath := "/data/app/xt mapper/o'brien/lib/x86_64"
			apkPath := "/data/app/xt mapper/o'brien/base.apk"
			args := []string{
				"--wayland-client",
				"",
				"argument with spaces",
				"single'quote",
				"; touch " + injected,
				"$HOME",
				"$(touch " + injected + ")",
				"line one\nline two",
				"equals=a=b",
				"雪",
			}
			command := buildRemoteCommandWithExecutable(useADB, nativeLibPath, apkPath, args, fixture)
			cmd := exec.Command("/bin/sh", "-c", command)
			cmd.Env = append(os.Environ(),
				"REMOTE_ARGV="+argvFile,
				"REMOTE_BOOTCLASSPATH="+bootFile,
				"BOOTCLASSPATH=runtime-base",
			)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("remote command failed: %v\n%s\ncommand: %s", err, output, command)
			}

			data, err := os.ReadFile(argvFile)
			if err != nil {
				t.Fatal(err)
			}
			gotBytes := bytes.Split(data, []byte{0})
			if len(gotBytes) > 0 && len(gotBytes[len(gotBytes)-1]) == 0 {
				gotBytes = gotBytes[:len(gotBytes)-1]
			}
			got := make([]string, len(gotBytes))
			for i, arg := range gotBytes {
				got[i] = string(arg)
			}
			want := []string{
				"-Djava.library.path=" + nativeLibPath,
				"-Djava.class.path=" + apkPath,
				"/",
				"xtr.keymapper.server.RemoteServiceShell",
			}
			want = append(want, args...)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("remote shell changed the argv:\n got: %#v\nwant: %#v", got, want)
			}

			gotBoot, err := os.ReadFile(bootFile)
			if err != nil {
				t.Fatal(err)
			}
			wantBoot := "runtime-base"
			if !useADB {
				wantBoot += ":" + bootClasspath
			}
			if string(gotBoot) != wantBoot {
				t.Fatalf("remote BOOTCLASSPATH = %q, want %q", gotBoot, wantBoot)
			}
			if _, err := os.Stat(injected); !os.IsNotExist(err) {
				t.Fatalf("shell metacharacters executed an injected command: stat error = %v", err)
			}
		})
	}
}
