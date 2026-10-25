package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Xtr126/cage-xtmapper/internal/release"
	"github.com/Xtr126/cage-xtmapper/internal/releasebuild"
)

func run(args []string) error {
	repo, err := os.Getwd()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: go run ./tools/release record-build|input-fingerprint|package|verify|link-contract")
	}
	switch args[0] {
	case "record-build":
		if len(args) != 1 {
			return fmt.Errorf("record-build takes no arguments")
		}
		return releasebuild.RecordBuild(repo)
	case "link-contract":
		if len(args) != 4 {
			return fmt.Errorf("link-contract requires build-directory source-directory link-command")
		}
		text, err := releasebuild.NativeContract(args[1], args[2], args[3])
		if err == nil {
			fmt.Print(text)
		}
		return err
	case "package":
		flags := flag.NewFlagSet("package", flag.ContinueOnError)
		version := flags.String("version", "", "release version")
		output := flags.String("output", "", "new archive path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *version == "" || *output == "" || flags.NArg() != 0 {
			return fmt.Errorf("package requires --version and --output")
		}
		if err := releasebuild.Package(repo, *version, *output); err != nil {
			return err
		}
		fmt.Println(*output)
		return nil
	case "input-fingerprint":
		flags := flag.NewFlagSet("input-fingerprint", flag.ContinueOnError)
		buildType := flags.String("build-type", "", "Meson build type")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *buildType == "" || flags.NArg() != 0 {
			return fmt.Errorf("input-fingerprint requires --build-type")
		}
		digest, err := releasebuild.NativeInputDigest(repo, *buildType)
		if err != nil {
			return err
		}
		fmt.Println(digest)
		return nil
	case "verify":
		if len(args) != 2 {
			return fmt.Errorf("verify requires an archive path")
		}
		manifest, _, err := release.ReadArchive(args[1], "")
		if err == nil {
			fmt.Printf("Verified %s (integrity, not publisher authenticity)\n", manifest.ReleaseID)
		}
		return err
	default:
		return fmt.Errorf("unknown operation: %s", args[0])
	}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}
