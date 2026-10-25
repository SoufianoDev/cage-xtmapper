// Package installer manages system-wide releases using durable ownership records.
package installer

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Xtr126/cage-xtmapper/internal/release"
)

func (m *manager) status() (map[string]any, error) {
	if !exists(m.root) {
		candidates, err := m.bootstrapCandidates()
		if candidates == nil {
			candidates = []string{}
		}
		return map[string]any{"state": "not installed", "active_release": nil, "transaction": nil, "bootstrap_candidates": candidates}, err
	}
	if err := m.load(false); err != nil {
		return nil, err
	}
	result := map[string]any{"state": "uninstalled", "active_release": nil, "transaction": nil}
	if exists(filepath.Join(m.base, "current")) {
		current, err := os.Readlink(filepath.Join(m.base, "current"))
		if err != nil {
			return nil, err
		}
		result["state"], result["active_release"] = "installed", current
	}
	if m.tx != nil {
		committed, err := m.isCommitted()
		if err != nil {
			return nil, err
		}
		result["state"] = "incomplete transaction"
		result["transaction"] = map[string]any{"operation": m.tx.Operation, "phase": m.tx.Phase, "committed": committed}
	} else if err := m.active(); err != nil {
		return nil, err
	}
	return result, nil
}

func (m *manager) report(output io.Writer) error {
	for _, path := range m.removed {
		fmt.Fprintln(output, "Removed:", path)
	}
	active := "none"
	if m.state != nil {
		if m.state.Release != nil {
			active = m.state.Release.ID
		}
		fmt.Fprintln(output, "Active release:", active)
		fmt.Fprintln(output, "Preserved stable lock and ownership state:", m.root)
	}
	known := map[string]bool{"releases": true, "current": true, "state.json": true, "transaction.json": true, ".install.lock": true}
	for _, directory := range []string{m.base, filepath.Join(m.base, "releases")} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if (directory == m.base && !known[entry.Name()]) || (directory != m.base && entry.Name() != active) {
				fmt.Fprintln(output, "Preserved unowned path:", filepath.Join(directory, entry.Name()))
			}
		}
	}
	for _, name := range []string{"cage_xtmapper", "cage_xtmapper.sh"} {
		if path := filepath.Join(m.bin, name); exists(path) {
			fmt.Fprintln(output, "Preserved unowned legacy path:", path)
		}
	}
	fmt.Fprintln(output, "Preserved: Waydroid/APK data, shared libraries/tools, Steam entries and desktop/user configuration.")
	return nil
}

// Run returns the documented installer status, including busy (3) and recovery (4).
func Run(args []string, output, diagnostics io.Writer) int {
	usage := func() {
		fmt.Fprintln(diagnostics, "Usage: install.sh install|update --archive FILE [--test-prefix DIR]")
		fmt.Fprintln(diagnostics, "       install.sh status|recover|uninstall [--test-prefix DIR]")
	}
	if len(args) == 0 {
		usage()
		return 2
	}
	operation := args[0]
	if operation == "--help" || operation == "-h" {
		usage()
		return 0
	}
	if operation != "install" && operation != "update" && operation != "status" && operation != "recover" && operation != "uninstall" {
		usage()
		return 2
	}
	flags := flag.NewFlagSet("install.sh "+operation, flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	archive := flags.String("archive", "", "explicit local release .tar (install/update only)")
	prefix := flags.String("test-prefix", "", "existing private test root containing opt/ and usr/local/bin/")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || (*archive != "") != (operation == "install" || operation == "update") {
		usage()
		return 2
	}
	if operation != "status" && *prefix == "" && os.Geteuid() != 0 {
		fmt.Fprintln(diagnostics, "install: system-wide mutations require root; use --test-prefix for isolated unprivileged tests")
		return 1
	}
	mask := syscall.Umask(0022)
	defer syscall.Umask(mask)
	m, err := newManager(*prefix)
	if m != nil {
		defer func() {
			if m.lock != nil {
				m.lock.Close()
			}
		}()
	}
	if err == nil {
		switch operation {
		case "install", "update":
			err = m.change(operation, *archive)
		case "uninstall":
			err = m.uninstall()
		case "recover":
			err = m.recover()
		case "status":
			var status map[string]any
			status, err = m.status()
			if err == nil {
				var data []byte
				data, err = release.JSON(status)
				if err == nil {
					_, err = output.Write(data)
				}
			}
		}
		if err == nil && operation != "status" {
			err = m.report(output)
		}
	}
	if err == nil {
		return 0
	}
	fmt.Fprintln(diagnostics, "install:", err)
	if errors.Is(err, errBusy) {
		return 3
	}
	if m != nil && m.tx != nil && exists(filepath.Join(m.base, "transaction.json")) {
		fmt.Fprintln(diagnostics, "Incomplete transaction retained; inspect status and run recover. Modified/unowned files are preserved.")
		return 4
	}
	return 1
}
