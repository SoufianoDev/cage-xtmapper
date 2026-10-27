package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Xtr126/cage-xtmapper/internal/installlock"
)

type shutdownCauseKey struct{}

type shutdownCauseKind uint8

const (
	shutdownFailure shutdownCauseKind = iota
	shutdownPipeline
	shutdownSignal
)

type shutdownCause struct {
	mu    sync.Mutex
	ready chan struct{}
	set   bool
	kind  shutdownCauseKind
	code  int
}

func (c *shutdownCause) mark(code int, kind shutdownCauseKind) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.set {
		c.set = true
		c.kind = kind
		c.code = code
		close(c.ready)
		return
	}
	// A second independently observed pipeline exit can refine pipefail's
	// result, but no signal or cleanup result may replace the pipeline cause.
	if c.kind == shutdownPipeline && kind == shutdownPipeline {
		c.code = code
	}
}

func (c *shutdownCause) result() (int, bool) {
	select {
	case <-c.ready:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.code, true
	default:
		return 0, false
	}
}

func markShutdownCause(ctx context.Context, code int) {
	if cause, ok := ctx.Value(shutdownCauseKey{}).(*shutdownCause); ok {
		cause.mark(code, shutdownFailure)
	}
}

func markPipelineExitCause(ctx context.Context, code int) {
	if cause, ok := ctx.Value(shutdownCauseKey{}).(*shutdownCause); ok {
		cause.mark(code, shutdownPipeline)
	}
}

func failureResult(ctx context.Context, code int) int {
	if code != 0 {
		markShutdownCause(ctx, code)
	}
	return code
}

func startupTimeoutError(seconds int64, phase string) error {
	return fmt.Errorf("Android startup did not complete within %d seconds while %s", seconds, phase)
}

// Run owns signals before starting any external command.
func Run(cfg Config) int {
	cause := &shutdownCause{ready: make(chan struct{})}
	base := context.WithValue(context.Background(), shutdownCauseKey{}, cause)
	ctx, cancel := context.WithCancel(base)
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	done := make(chan int, 1)
	go func() { done <- run(ctx, cfg) }()
	select {
	case s := <-signals:
		cause.mark(signalStatus(s), shutdownSignal)
		cancel()
		<-done
		if initiatingCode, ok := cause.result(); ok {
			return initiatingCode
		}
		return signalStatus(s)
	case code := <-done:
		if initiatingCode, ok := cause.result(); ok {
			return initiatingCode
		}
		return code
	}
}

func run(ctx context.Context, cfg Config) int {
	ApplyEnvironment(&cfg)
	if cfg.Tools.SelfExe == "" {
		// Keep the running inode even if an installer replaces or unlinks its pathname.
		cfg.Tools.SelfExe = "/proc/self/exe"
	}
	if cfg.PrintOutputInfo {
		return printOutputInfo(ctx, cfg)
	}

	bootWait := BootWaitSeconds()
	if bootWait <= 0 {
		code := failureResult(ctx, 1)
		reportError(errors.New("XTMAPPER_BOOT_WAIT_SECONDS must be a positive integer"))
		return code
	}
	var t transport
	if cfg.UseADB {
		p, ok := findTool(cfg.Tools.ADB, "adb")
		if !ok {
			code := failureResult(ctx, 127)
			fmt.Fprintln(os.Stderr, "Error: adb was not found in PATH")
			return code
		}
		cfg.Tools.ADB = p
		t = adbTransport(&cfg)
	} else {
		p, ok := findTool(cfg.Tools.Sudo, "sudo")
		if !ok {
			code := failureResult(ctx, 127)
			fmt.Fprintln(os.Stderr, "Error: sudo was not found in PATH")
			return code
		}
		cfg.Tools.Sudo = p
		p, ok = findTool(cfg.Tools.Waydroid, "waydroid")
		if !ok {
			code := failureResult(ctx, 127)
			fmt.Fprintln(os.Stderr, "Error: waydroid was not found in PATH")
			return code
		}
		cfg.Tools.Waydroid = p
		t = waydroidTransport(&cfg)
	}

	if err := prepareStartup(ctx, t); err != nil {
		code := 1
		if errors.Is(err, ErrForegroundInterrupted) {
			code = 130
		}
		code = failureResult(ctx, code)
		reportError(err)
		return code
	}
	return supervise(ctx, cfg, t, bootWait)
}

// prepareStartup completes the Waydroid lifecycle before boot polling begins.
// The boot-wait budget starts when setup begins polling Android's boot state.
func prepareStartup(ctx context.Context, t transport) error {
	if t.stopSession != nil {
		if err := t.stopSession(ctx); err != nil {
			return err
		}
	}
	if t.startContainer != nil {
		if err := t.startContainer(ctx); err != nil {
			return err
		}
	}
	return nil
}

func reportError(err error) {
	if !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "Error: "+err.Error())
	}
}

func supervise(ctx context.Context, cfg Config, t transport, startupSeconds int64) (result int) {
	parentCtx := ctx
	ctx, cancel := context.WithCancel(ctx)
	var owned []*child
	defer func() {
		cancel() // Start all group cleanups together, including a blocked probe.
		for _, c := range owned {
			c.finish()
		}
	}()
	// Record an established failure before cleanup begins. A later SIGTERM
	// must not replace the exit status that caused the shutdown.
	defer func() {
		if result != 0 && parentCtx.Err() == nil {
			markShutdownCause(parentCtx, result)
		}
	}()

	evtRead, evtWrite, err := os.Pipe()
	if err != nil {
		code := failureResult(parentCtx, 1)
		reportError(fmt.Errorf("could not create the event pipe: %w", err))
		return code
	}
	defer evtRead.Close()
	defer evtWrite.Close()

	waydroid := cfg.Tools.Waydroid
	if waydroid == "" {
		waydroid = "waydroid"
	}
	compCmd := exec.Command(cfg.Tools.SelfExe, "--internal-cage", "-s", "-m", "last", "--", waydroid, "show-full-ui")
	installlock.Pass(compCmd, cfg.RuntimeLock)
	compCmd.Stdout = evtWrite
	compCmd.Stderr = os.Stderr
	comp, err := startGroup(ctx, compCmd)
	if err != nil {
		code := failureResult(parentCtx, 1)
		reportError(fmt.Errorf("could not start the compositor: %w", err))
		return code
	}
	owned = append(owned, comp)
	evtWrite.Close()

	// Setup cancellation follows the compositor and parent lifecycle; the boot
	// deadline is enforced only by the boot polling loop in setupWithDeadline.
	setupCtx, stopSetup := context.WithCancel(ctx)
	go func() {
		select {
		case <-comp.dead:
			if comp.code != 0 {
				stopSetup()
			}
		case <-setupCtx.Done():
		}
	}()
	remoteCmd, err := setupWithDeadline(setupCtx, t, cfg.UseADB, cfg.XtmapperArgs, startupSeconds)
	stopSetup()
	if err != nil {
		if comp.exited() && comp.code != 0 {
			markPipelineExitCause(parentCtx, comp.code)
			return comp.code
		}
		code := failureResult(parentCtx, 1)
		reportError(err)
		return code
	}
	if comp.exited() && comp.code != 0 {
		markPipelineExitCause(parentCtx, comp.code)
		return comp.code
	}
	// Both children use the kernel pipe directly; Go never reads or buffers events.
	remoteCmd.Stdin = evtRead
	remoteCmd.Stdout = os.Stdout
	remoteCmd.Stderr = os.Stderr
	remote, err := startGroup(ctx, remoteCmd)
	if err != nil {
		code := failureResult(parentCtx, 1)
		reportError(fmt.Errorf("could not launch remote XtMapper: %w", err))
		return code
	}
	owned = append(owned, remote)
	evtRead.Close()

	// Let the peer a short interval to observe EOF/broken pipe and exit
	// naturally. If it stays alive, cancel its process group and preserve
	// the first exit as the initiating cause rather than reporting our own
	// cleanup signal as the pipeline result.
	select {
	case <-comp.dead:
		markPipelineExitCause(parentCtx, comp.code)
		if waitForPeerExit(ctx, remote) {
			code := pipelineExitStatus(comp, remote)
			markPipelineExitCause(parentCtx, code)
			return code
		}
		if ctx.Err() != nil {
			return 1
		}
		if remote.exited() {
			code := pipelineExitStatus(comp, remote)
			markPipelineExitCause(parentCtx, code)
			return code
		}
		code := terminatePeerAfterRuntimeExit(cancel, remote, comp.code, "remote XtMapper")
		markPipelineExitCause(parentCtx, code)
		return code
	case <-remote.dead:
		markPipelineExitCause(parentCtx, remote.code)
		if waitForPeerExit(ctx, comp) {
			code := pipelineExitStatus(comp, remote)
			markPipelineExitCause(parentCtx, code)
			return code
		}
		if ctx.Err() != nil {
			return 1
		}
		if comp.exited() {
			code := pipelineExitStatus(comp, remote)
			markPipelineExitCause(parentCtx, code)
			return code
		}
		code := terminatePeerAfterRuntimeExit(cancel, comp, remote.code, "compositor")
		markPipelineExitCause(parentCtx, code)
		return code
	case <-ctx.Done():
		return 1 // Run preserves the initiating signal's status.
	}
}

// Give a peer enough time to drain bytes already buffered in the kernel pipe
// and observe EOF. A sub-second grace can truncate valid input when a consumer
// is delayed during startup; cleanup remains bounded when the peer ignores EOF.
const runtimePeerExitGrace = 2 * time.Second

// A canceled runtime peer is cleaned up with TERM and then KILL. Never wait for
// its status beyond that bounded cleanup window: cmd.Wait may remain blocked
// even after group cleanup has exhausted its collection period.
const runtimePeerCleanupCollectionWindow = cleanupTermGrace + cleanupKillGrace

func waitForChildExit(dead <-chan struct{}, timeout time.Duration) bool {
	if timeout <= 0 {
		select {
		case <-dead:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-dead:
		return true
	case <-timer.C:
		return false
	}
}

func terminatePeerAfterRuntimeExit(cancel context.CancelFunc, peer *child, initiatingCode int, peerName string) int {
	return terminatePeerAfterRuntimeExitWithWait(cancel, peer, initiatingCode, peerName, waitForChildExit)
}

func terminatePeerAfterRuntimeExitWithWait(
	cancel context.CancelFunc,
	peer *child,
	initiatingCode int,
	peerName string,
	wait func(<-chan struct{}, time.Duration) bool,
) int {
	cancel()
	if !wait(peer.dead, runtimePeerCleanupCollectionWindow) {
		fmt.Fprintf(os.Stderr, "Error: timed out waiting for %s process %d after cleanup; exit status collection is incomplete\\n", peerName, peer.cmd.Process.Pid)
	}
	// Forced cleanup after the natural-exit grace is not a successful pipeline
	// completion, even if the initiating child itself exited with status zero.
	if initiatingCode == 0 {
		reportError(fmt.Errorf("%s did not exit within %s after its peer exited; refusing a successful pipeline result", peerName, runtimePeerExitGrace))
		return 1
	}
	return initiatingCode
}

func waitForPeerExit(ctx context.Context, peer *child) bool {
	timer := time.NewTimer(runtimePeerExitGrace)
	defer timer.Stop()
	select {
	case <-peer.dead:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return peer.exited()
	}
}

func pipelineExitStatus(comp, remote *child) int {
	// Preserve pipefail's rightmost-nonzero precedence when both commands
	// exit independently. Cleanup-induced termination of the peer is not
	// an independent pipeline failure and must not mask the initiating exit.
	if remote.code != 0 {
		return remote.code
	}
	return comp.code
}

func signalStatus(s os.Signal) int {
	if s == os.Interrupt {
		return 130
	}
	return 143
}

func setup(ctx context.Context, t transport, useADB bool, args []string) (*exec.Cmd, error) {
	bootWait := BootWaitSeconds()
	if bootWait <= 0 {
		return nil, errors.New("XTMAPPER_BOOT_WAIT_SECONDS must be a positive integer")
	}
	return setupWithDeadline(ctx, t, useADB, args, bootWait)
}

func deadlineExpired(ctx context.Context, deadline time.Time) bool {
	return errors.Is(ctx.Err(), context.DeadlineExceeded) || !time.Now().Before(deadline)
}

func setupWithDeadline(ctx context.Context, t transport, useADB bool, args []string, startupSeconds int64) (*exec.Cmd, error) {
	if startupSeconds <= 0 {
		return nil, errors.New("XTMAPPER_BOOT_WAIT_SECONDS must be a positive integer")
	}
	// One monotonic deadline is shared by boot probes and retry sleeps only.
	// ABI and APK discovery each receive their own command-level timeout below.
	deadline := time.Now().Add(time.Duration(startupSeconds) * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			if deadlineExpired(ctx, deadline) {
				return nil, fmt.Errorf("Android did not finish booting within %d seconds", startupSeconds)
			}
			return nil, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("Android did not finish booting within %d seconds", startupSeconds)
		}
		state, _ := t.getprop(ctx, "sys.boot_completed", min(getpropProbeTimeout, remaining))
		if state == "1" && time.Now().Before(deadline) {
			break
		}
		if deadlineExpired(ctx, deadline) {
			return nil, fmt.Errorf("Android did not finish booting within %d seconds", startupSeconds)
		}
		timer := time.NewTimer(min(time.Second, max(0, time.Until(deadline))))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			if deadlineExpired(ctx, deadline) {
				return nil, fmt.Errorf("Android did not finish booting within %d seconds", startupSeconds)
			}
			return nil, ctx.Err()
		}
	}

	abi, err := t.getprop(ctx, "ro.product.cpu.abi", abiProbeTimeout)
	if err != nil || abi == "" {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, ErrTimedOut) {
			return nil, fmt.Errorf("timed out after %s while determining the Android CPU ABI", abiProbeTimeout)
		}
		return nil, errors.New("could not determine the Android CPU ABI")
	}
	for _, pkg := range PackageProbeOrder {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		apkPath, probeErr := t.packagePathProbeWithTimeout(ctx, pkg, apkProbeTimeout)
		if probeErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		if apkPath != "" {
			command := buildRemoteCommand(useADB, deriveNativeLibPath(apkPath, abi), apkPath, args)
			return t.remoteShellCmd(command), nil
		}
	}
	return nil, errors.New("no installation of XtMapper was found")
}

func printOutputInfo(ctx context.Context, cfg Config) int {
	os.Setenv("XTMAPPER_WIDTH", "1")
	os.Setenv("XTMAPPER_HEIGHT", "1")
	os.Setenv("XTMAPPER_PRINT_OUTPUT_INFO", "1")
	cmd := exec.Command(cfg.Tools.SelfExe, "--internal-cage", "--", "sleep", "1")
	installlock.Pass(cmd, cfg.RuntimeLock)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	c, err := startGroup(ctx, cmd)
	if err != nil {
		code := failureResult(ctx, 1)
		reportError(err)
		return code
	}
	defer c.finish()
	select {
	case <-c.dead:
		if c.code != 0 {
			return failureResult(ctx, c.code)
		}
		return c.code
	case <-ctx.Done():
		return 1
	}
}
