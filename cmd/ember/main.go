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
//	serve               Start an HTTP server exposing build/burn/batch over HTTP.
//	batch               Execute a batch of embers from NDJSON stdin (Slurm-compatible).
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
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "ember serve: %v\n", err)
			os.Exit(1)
		}
	case "batch":
		if err := runBatch(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "ember batch: %v\n", err)
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
  ember serve [flags]               Start HTTP server (build/burn/batch/health)
  ember batch [flags]               Execute NDJSON jobs from stdin (Slurm-compatible)

Flags (build):
  -o <prefix>            Output prefix (default: source filename without .go)
  -v                     Verbose: print manifest to stderr

Flags (serve):
  --port <int>           Listen port (default: 8080)
  --max-memory-pages <n> Max WASM memory pages, 64KB each (default: 256 = 16MB)
  --verbose              Log requests to stderr

Flags (batch):
  --concurrency <int>    Max parallel jobs (default: NumCPU)
  --timeout <duration>   Total execution timeout (default: 30s)
  --format json|tsv      Output format (default: json)

`)
}
