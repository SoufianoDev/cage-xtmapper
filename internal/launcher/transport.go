package launcher

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// transport abstracts the Waydroid/ADB command surface. Local
// commands always use argument arrays; only the final remote XtMapper
// launch receives one pre-assembled quoted command string, as before
// (`waydroid shell -- sh -c "..."` / `adb shell "..."`).
type transport struct {
	// stopSession stops the container session (nil on the adb path).
	stopSession func(context.Context) error
	// startContainer starts the waydroid container (nil on the adb path).
	startContainer func(context.Context) error
	// probeCmd runs a probe command directly in the Android shell:
	// the verb (getprop/pm) plus words, one argv element each.
	probeCmd func(verb string, args ...string) *exec.Cmd
	// remoteShellCmd returns the local command that runs one
	// pre-assembled remote command string on Android.
	remoteShellCmd func(cmd string) *exec.Cmd
}

const (
	waydroidStopTimeout   = 30 * time.Second
	containerStartTimeout = 120 * time.Second
	getpropProbeTimeout   = 5 * time.Second
	abiProbeTimeout       = 10 * time.Second
	apkProbeTimeout       = 10 * time.Second
)

func waydroidTransport(cfg *Config) transport {
	sudo := cfg.Tools.Sudo
	waydroid := cfg.Tools.Waydroid
	run := func(args []string) *exec.Cmd {
		argv := append([]string{sudo, waydroid}, args...)
		return exec.Command(argv[0], argv[1:]...)
	}
	return transport{
		stopSession: func(ctx context.Context) error {
			// The old script ran `waydroid session stop` without sudo.
			stop := exec.Command(cfg.Tools.Waydroid, "session", "stop")
			code, err := runInherit(ctx, stop, waydroidStopTimeout)
			if err != nil {
				return fmt.Errorf("could not stop the Waydroid session: %w", err)
			}
			if code != 0 {
				return errChildExit("could not stop the Waydroid session", code)
			}
			return nil
		},
		startContainer: func(ctx context.Context) error {
			code, err := runInherit(ctx, run([]string{"container", "start"}), containerStartTimeout)
			if err != nil {
				return fmt.Errorf("could not start the Waydroid container: %w", err)
			}
			if code != 0 {
				return errChildExit("could not start the Waydroid container", code)
			}
			return nil
		},
		probeCmd: func(verb string, args ...string) *exec.Cmd {
			return run(append([]string{"shell", verb}, args...))
		},
		remoteShellCmd: func(cmd string) *exec.Cmd {
			return run([]string{"shell", "--", "sh", "-c", cmd})
		},
	}
}

func adbTransport(cfg *Config) transport {
	adb := cfg.Tools.ADB
	return transport{
		stopSession:    nil, // stop/container timeouts apply only on the Waydroid path
		startContainer: nil,
		probeCmd: func(verb string, args ...string) *exec.Cmd {
			return exec.Command(adb, append([]string{"shell", verb}, args...)...)
		},
		remoteShellCmd: func(cmd string) *exec.Cmd {
			return exec.Command(adb, "shell", cmd)
		},
	}
}

// getprop probes an Android property with the given deadline, value
// CR-stripped like the shell pipeline did.
func (t transport) getprop(ctx context.Context, name string, timeout time.Duration) (string, error) {
	out, err := runCapture(ctx, t.probeCmd("getprop", name), timeout)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(strings.ReplaceAll(out, "\r", ""), "\n"), nil
}

// packagePathProbe returns the first `package:<path>` line of a
// `pm path` probe ("" when absent).
func (t transport) packagePathProbe(ctx context.Context, pkg string) (string, error) {
	out, err := runCapture(ctx, t.probeCmd("pm", "path", pkg), apkProbeTimeout)
	if err != nil {
		return "", err
	}
	return parsePmPath(out), nil
}
