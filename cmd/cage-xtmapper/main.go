package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/Xtr126/cage-xtmapper/internal/installlock"
	"github.com/Xtr126/cage-xtmapper/internal/launcher"
	"github.com/Xtr126/cage-xtmapper/internal/native"
)

func main() {
	os.Exit(run())
}

func run() int {
	internalMode := native.InternalMode()
	lock, err := installlock.Acquire(internalMode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if lock != nil {
		defer lock.Close()
	}
	if internalMode {
		return native.RunInternal()
	}

	if launcher.BootWaitSeconds() <= 0 {
		fmt.Fprintln(os.Stderr, "Error: XTMAPPER_BOOT_WAIT_SECONDS must be a positive integer")
		return 2
	}

	cfg, help, err := launcher.Parse(os.Args[1:], launcher.Tools{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		var ce *launcher.CliError
		if errors.As(err, &ce) && ce.ReportUsage() {
			launcher.UsageToStderr()
		}
		return 2
	}
	if help {
		launcher.Usage()
		return 0
	}
	cfg.RuntimeLock = lock
	return launcher.Run(cfg)
}
