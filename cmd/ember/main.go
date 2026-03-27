// ember is the command-line interface for the Ember intent-driven
// compute system.
//
// Usage:
//
//	ember <subcommand> [flags] [args]
//
// Subcommands:
//
//	build <source.go>   Analyze and compile a Go source file to an ember
//	                    (.wasm + .intent files).
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "build":
		if err := runBuild(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "ember build: %v\n", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "ember: unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `ember — intent-driven minimal compute

Usage:
  ember build [flags] <source.go>   Compile Go source to ember (.wasm + .intent)

Flags (build):
  -o <prefix>    Output prefix (default: source filename without .go)
  -v             Verbose: print manifest to stderr

`)
}
