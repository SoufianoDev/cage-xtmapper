package launcher

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
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
	sudoAuthCheckTimeout  = 5 * time.Second
	getpropProbeTimeout   = 5 * time.Second
	abiProbeTimeout       = 10 * time.Second
	apkProbeTimeout       = 10 * time.Second
)

func waydroidTransport(cfg *Config) transport {
	sudo := cfg.Tools.Sudo
	waydroid := cfg.Tools.Waydroid
	runNonInteractive := func(args []string) *exec.Cmd {
		argv := append([]string{sudo, "-n", waydroid}, args...)
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
			if terminalForeground() {
				// Check this exact command without prompting first. NOPASSWD rules
				// (or existing valid credentials) can start the container directly.
				// Otherwise, let this Waydroid command prompt in an isolated process
				// group that temporarily owns the terminal foreground.
				check := exec.Command(sudo, "-n", "-l", "--", waydroid, "container", "start")
				code, err := runBounded(ctx, check, sudoAuthCheckTimeout)
				if err != nil {
					return fmt.Errorf("could not check passwordless sudo access for Waydroid container start: %w", err)
				}
				if code != 0 {
					cmd := exec.Command(sudo, waydroid, "container", "start")
					code, err = runInheritTerminalForeground(ctx, cmd, containerStartTimeout)
					if err != nil {
						return fmt.Errorf("could not start the Waydroid container: %w", err)
					}
					if code == 128+int(syscall.SIGINT) {
						return fmt.Errorf("Waydroid container start was interrupted: %w", ErrForegroundInterrupted)
					}
					if code != 0 {
						return errChildExit("could not start the Waydroid container", code)
					}
					return nil
				}
			}

			// Keep probes and non-terminal launches noninteractive. The common
			// password-free foreground path avoids an unnecessary sudo -v call.
			cmd := runNonInteractive([]string{"container", "start"})
			code, err := runInherit(ctx, cmd, containerStartTimeout)
			if err != nil {
				return fmt.Errorf("could not start the Waydroid container: %w", err)
			}
			if code != 0 {
				return errChildExit("could not start the Waydroid container", code)
			}
			return nil
		},
		probeCmd: func(verb string, args ...string) *exec.Cmd {
			return runNonInteractive(append([]string{"shell", verb}, args...))
		},
		remoteShellCmd: func(cmd string) *exec.Cmd {
			return runNonInteractive([]string{"shell", "--", "sh", "-c", cmd})
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
	return t.packagePathProbeWithTimeout(ctx, pkg, apkProbeTimeout)
}

func (t transport) packagePathProbeWithTimeout(ctx context.Context, pkg string, timeout time.Duration) (string, error) {
	out, err := runCapture(ctx, t.probeCmd("pm", "path", pkg), timeout)
	if err != nil {
		return "", err
	}
	return parsePmPath(out), nil
}
