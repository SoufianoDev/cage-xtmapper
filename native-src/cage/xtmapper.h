/*
 * Cage XtMapper native integration ABI.
 *
 * cage_xtmapper is a Go application that links this project as native
 * code. The Go launcher re-executes the same ELF in embedded mode
 * (internal dispatch) and calls cage_xtmapper_main() instead of
 * running the standalone cage_xtmapper binary.
 */
#ifndef CAGE_XTMAPPER_NATIVE_API_H
#define CAGE_XTMAPPER_NATIVE_API_H

struct cage_xtmapper_options {
	/* Termination control channel for embedded (Go-owned signal) mode.
	 * A readable pipe fd; any readable information on it terminates
	 * the compositor through the supported Wayland event-loop path.
	 * Set to -1 to keep Cage's own SIGINT/SIGTERM handlers. */
	int control_fd;
};

/*
 * Run Cage exactly like the standalone main() with the given arguments
 * (getopt-style argv, argv[0] reserved for the program name, optional
 * "--" then APPLICATION... arguments for the primary client), and
 * return the compositor exit status after full native cleanup. This
 * function must run on its own OS thread owned by the caller and
 * blocks until the compositor terminates.
 */
int cage_xtmapper_main(int argc, char *argv[], const struct cage_xtmapper_options *options);

#endif
