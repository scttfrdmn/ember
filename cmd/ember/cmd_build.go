package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/scttfrdmn/ember/core/intent"
	"github.com/scttfrdmn/ember/core/ssa/loader"
	"github.com/scttfrdmn/ember/core/ssa/walker"
	"github.com/scttfrdmn/ember/pkg/analyzer"
	wasm "github.com/scttfrdmn/ember/pkg/emitter/wasm"
)

func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	output := fs.String("o", "", "output prefix (default: source name without .go)")
	verbose := fs.Bool("v", false, "verbose: print manifest to stderr")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: ember build [flags] <source.go>")
	}

	sourceFile := fs.Arg(0)
	if _, err := os.Stat(sourceFile); err != nil {
		return fmt.Errorf("cannot access %s: %w", sourceFile, err)
	}

	// Determine output prefix.
	prefix := *output
	if prefix == "" {
		base := filepath.Base(sourceFile)
		prefix = strings.TrimSuffix(base, filepath.Ext(base))
		// Place output in the same directory as the source file.
		prefix = filepath.Join(filepath.Dir(sourceFile), prefix)
	}

	// Load source into SSA form.
	lp, err := loader.LoadFile(sourceFile)
	if err != nil {
		return fmt.Errorf("load %s: %w", sourceFile, err)
	}

	// Phase 1: analyze → manifest.
	a := analyzer.New()
	w := walker.New(a)
	if err := w.WalkPackage(lp.MainPkg); err != nil {
		return fmt.Errorf("analyze: %w", err)
	}
	m := a.Manifest()

	if *verbose {
		data, _ := intent.Marshal(m)
		fmt.Fprintf(os.Stderr, "Intent manifest:\n%s\n", data)
	}

	// Phase 2: emit WASM.
	e := wasm.NewEmitter()
	w2 := walker.New(e)
	if err := w2.WalkPackage(lp.MainPkg); err != nil {
		return fmt.Errorf("emit WASM: %w", err)
	}
	wasmBytes, err := e.Bytes()
	if err != nil {
		return fmt.Errorf("encode WASM: %w", err)
	}

	// Write outputs.
	wasmPath := prefix + ".wasm"
	if err := os.WriteFile(wasmPath, wasmBytes, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", wasmPath, err)
	}

	intentPath := prefix + ".intent"
	if err := intent.WriteFile(intentPath, m); err != nil {
		return fmt.Errorf("write %s: %w", intentPath, err)
	}

	fmt.Printf("ember: wrote %s (%d bytes)\n", wasmPath, len(wasmBytes))
	fmt.Printf("ember: wrote %s\n", intentPath)
	if m.IsPureCompute() {
		fmt.Printf("ember: pure compute (%d runtime strips: %v)\n",
			len(m.RuntimeStrips), m.RuntimeStrips)
	}

	return nil
}
