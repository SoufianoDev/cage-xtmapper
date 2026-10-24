package main

import (
	"fmt"
	"os"

	"github.com/Xtr126/cage-xtmapper/internal/native"
)

func main() {
	// Phase 1 skeleton: only the embedded-compositor dispatch exists.
	if native.InternalMode() {
		os.Exit(native.RunInternal())
	}
	fmt.Fprintln(os.Stderr, "launcher not migrated yet")
	os.Exit(1)
}
