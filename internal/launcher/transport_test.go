package launcher

import (
	"reflect"
	"testing"
)

func TestWaydroidRemoteShellUsesAndroidShellAndUHIDIdentity(t *testing.T) {
	const remoteCommand = "exec env BOOTCLASSPATH=$BOOTCLASSPATH '/system/bin/app_process' -Djava.library.path='/data/app/xt mapper/lib/x86_64' -Djava.class.path='/data/app/xt mapper/base.apk' / xtr.keymapper.server.RemoteServiceShell --wayland-client"

	cfg := &Config{Tools: Tools{Sudo: "sudo", Waydroid: "waydroid"}}
	got := waydroidTransport(cfg).remoteShellCmd(remoteCommand).Args
	want := []string{
		"sudo",
		"-n",
		"waydroid",
		"shell",
		"--uid=2000",
		"--gid=3011",
		"--",
		"sh",
		"-c",
		remoteCommand,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Waydroid remote shell argv = %#v, want %#v", got, want)
	}
}

func TestWaydroidProbeDoesNotOverrideIdentity(t *testing.T) {
	cfg := &Config{Tools: Tools{Sudo: "sudo", Waydroid: "waydroid"}}
	got := waydroidTransport(cfg).probeCmd("getprop", "sys.boot_completed").Args
	want := []string{
		"sudo",
		"-n",
		"waydroid",
		"shell",
		"getprop",
		"sys.boot_completed",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Waydroid probe argv = %#v, want %#v", got, want)
	}
}

func TestADBRemoteShellDoesNotUseWaydroidUID(t *testing.T) {
	const remoteCommand = "exec '/system/bin/app_process' / xtr.keymapper.server.RemoteServiceShell --wayland-client"

	cfg := &Config{Tools: Tools{ADB: "adb"}}
	got := adbTransport(cfg).remoteShellCmd(remoteCommand).Args
	want := []string{"adb", "shell", remoteCommand}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ADB remote shell argv = %#v, want %#v", got, want)
	}
}
