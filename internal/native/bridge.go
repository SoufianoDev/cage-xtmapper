// Package native runs the embedded Cage compositor inside this same ELF.
package native

// #cgo pkg-config: cage-xtmapper-native
// #include <stdio.h>
// #include <stdlib.h>
// #include "bridge.h"
import "C"

import (
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

// Run executes the compositor with a getopt-style argument list.
func Run(args []string, controlFD int) int {
	// POSIX argv vectors must be NULL-terminated; argc excludes that terminator.
	cArgv := make([]*C.char, len(args)+1)
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
	code := int(C.cage_xtmapper_main(C.int(len(args)), &cArgv[0], &opts))
	// main exits through os.Exit in Go, which does not flush C stdio buffers.
	C.fflush(nil)
	return code
}

// InternalMode reports whether the process is running in the embedded-compositor dispatch.
func InternalMode() bool { return len(os.Args) > 1 && os.Args[1] == "--internal-cage" }

// RunInternal runs the embedded Cage compositor in-process.
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

func appArgs() []string {
	args := make([]string, 0, len(os.Args)+1)
	args = append(args, "cage-xtmapper")
	return append(args, os.Args[2:]...)
}

// terminate sends one shutdown request on the control pipe.
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
