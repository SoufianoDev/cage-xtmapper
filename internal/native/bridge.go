// Package native runs the embedded Cage compositor inside this same ELF.
package native

// #cgo pkg-config: cage-xtmapper-native
// #include <stdlib.h>
// #include "bridge.h"
import "C"

import (
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

// Run runs the compositor with the given getopt-style argument list
// (argv[0] is a placeholder program name; "--" then the client
// command follows) and returns the native exit status. controlFD is
// the readable termination channel (-1 to use Cage's own signal
// handlers). Blocks until the compositor finishes.
func Run(args []string, controlFD int) int {
	cArgv := make([]*C.char, len(args))
	for i, arg := range args {
		cArgv[i] = C.CString(arg)
	}
	defer func() {
		for i := range cArgv {
			C.free(unsafe.Pointer(cArgv[i]))
		}
	}()
	opts := C.struct_cage_xtmapper_options{}
	opts.control_fd = C.int(controlFD)
	return int(C.cage_xtmapper_main(C.int(len(args)), &cArgv[0], &opts))
}

// InternalMode reports whether the process was started in the private
// embedded-compositor dispatch mode. The flag itself is kept at the
// front of the argument list and never reaches Cage's parser.
func InternalMode() bool { return len(os.Args) > 1 && os.Args[1] == "--internal-cage" }

// RunInternal runs Cage as native code inside this process, taking
// the arguments after --internal-cage (getopt-style, including the
// "--" separator used for the primary client command).
//
// This process owns termination requests: SIGINT/SIGTERM arrive
// through the Go runtime and are forwarded over a control channel
// into the native Wayland event loop, which cleans up and returns the
// native status. A signal-initiated shutdown reports 130 (SIGINT) or
// 143 (SIGTERM). Blocks until the compositor exits.
func RunInternal() int {
	rfd, wfd, err := pipeTerm()
	if err != nil {
		return 1
	}
	defer closeFd(rfd)
	defer closeFd(wfd)

	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	done := make(chan int, 1)
	go func() { done <- Run(appArgs(), rfd) }()

	firstSig := 0
	for {
		select {
		case s := <-sigCh:
			if firstSig == 0 {
				if s == os.Interrupt {
					firstSig = 130
				} else {
					firstSig = 143
				}
			}
			terminate(wfd, firstSig)
		case status := <-done:
			if firstSig != 0 {
				return firstSig
			}
			return status
		}
	}
}

// appArgs builds the getopt argv for the native entry: a placeholder
// program name plus every argument after --internal-cage.
func appArgs() []string {
	args := make([]string, 0, len(os.Args)+1)
	args = append(args, "cage-xtmapper")
	return append(args, os.Args[2:]...)
}

// terminate writes one request byte on the control channel and
// returns the shared status choice. The pipe is nonblocking; EAGAIN
// only happens with an already-notified, undrained loop, so the write
// may be dropped without losing the termination semantics. Repeated
// requests are drained and idempotent in the native loop.
func terminate(wfd, status int) int {
	var buf [1]byte
	for retry := 0; retry < 3; retry++ {
		_, err := syscall.Write(wfd, buf[:])
		if err != syscall.EAGAIN {
			break
		}
	}
	return status
}

func pipeTerm() (int, int, error) {
	var fds [2]int
	err := syscall.Pipe2(fds[:], syscall.O_CLOEXEC|syscall.O_NONBLOCK)
	return fds[0], fds[1], err
}

func closeFd(fd int) {
	if fd >= 0 {
		syscall.Close(fd)
	}
}
