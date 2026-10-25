package main

import (
	"os"

	"github.com/Xtr126/cage-xtmapper/internal/installer"
)

func main() {
	os.Exit(installer.Run(os.Args[1:], os.Stdout, os.Stderr))
}
