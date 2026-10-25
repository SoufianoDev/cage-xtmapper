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

	var t transport
	if cfg.UseADB {
		p, ok := findTool(cfg.Tools.ADB, "adb")
		if !ok {
			fmt.Fprintln(os.Stderr, "Error: adb was not found in PATH")
			return 127
		}
		cfg.Tools.ADB = p
		t = adbTransport(&cfg)
	} else {
		p, ok := findTool(cfg.Tools.Sudo, "sudo")
		if !ok {
			fmt.Fprintln(os.Stderr, "Error: sudo was not found in PATH")
			return 127
		}
		cfg.Tools.Sudo = p
		p, ok = findTool(cfg.Tools.Waydroid, "waydroid")
		if !ok {
			fmt.Fprintln(os.Stderr, "Error: waydroid was not found in PATH")
			return 127
		}
		cfg.Tools.Waydroid = p
		t = waydroidTransport(&cfg)
	}

	if !cfg.UseADB {
		if err := t.stopSession(ctx); err != nil {
			reportError(err)
			return 1
		}
		if err := t.startContainer(ctx); err != nil {
			reportError(err)
			return 1
		}
	}
	return supervise(ctx, cfg, t)
}

func reportError(err error) {
	if !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "Error: "+err.Error())
	}
}

func supervise(ctx context.Context, cfg Config, t transport) (result int) {
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
		reportError(fmt.Errorf("could not create the event pipe: %w", err))
		return 1
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
		reportError(fmt.Errorf("could not start the compositor: %w", err))
		return 1
	}
	owned = append(owned, comp)
	evtWrite.Close()

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
	remoteCmd, err := setup(setupCtx, t, cfg.UseADB, cfg.XtmapperArgs)
	stopSetup()
	if err != nil {
		if comp.exited() && comp.code != 0 {
			return comp.code
		}
		reportError(err)
		return 1
	}
	if comp.exited() && comp.code != 0 {
		return comp.code
	}

	// Both children use the kernel pipe directly; Go never reads or buffers events.
	remoteCmd.Stdin = evtRead
	remoteCmd.Stdout = os.Stdout
	remoteCmd.Stderr = os.Stderr
	remote, err := startGroup(ctx, remoteCmd)
	if err != nil {
		reportError(fmt.Errorf("could not launch remote XtMapper: %w", err))
		return 1
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
		code := comp.code
		markPipelineExitCause(parentCtx, code)
		cancel()
		<-remote.dead
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
		code := remote.code
		markPipelineExitCause(parentCtx, code)
		cancel()
		<-comp.dead
		return code
	case <-ctx.Done():
		return 1 // Run preserves the initiating signal's status.
	}
}

const runtimePeerExitGrace = 250 * time.Millisecond

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
	deadline := time.Now().Add(time.Duration(bootWait) * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("Android did not finish booting within %d seconds", bootWait)
		}
		state, _ := t.getprop(ctx, "sys.boot_completed", min(getpropProbeTimeout, remaining))
		if state == "1" && time.Now().Before(deadline) {
			break
		}
		timer := time.NewTimer(min(time.Second, max(0, time.Until(deadline))))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}

	abi, err := t.getprop(ctx, "ro.product.cpu.abi", abiProbeTimeout)
	if err != nil || abi == "" {
		return nil, errors.New("could not determine the Android CPU ABI")
	}
	for _, pkg := range PackageProbeOrder {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		apkPath, _ := t.packagePathProbe(ctx, pkg)
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
		reportError(err)
		return 1
	}
	defer c.finish()
	select {
	case <-c.dead:
		return c.code
	case <-ctx.Done():
		return 1
	}
}
