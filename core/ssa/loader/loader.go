// Package loader loads Go source into SSA form using
// golang.org/x/tools/go/ssa and golang.org/x/tools/go/packages.
//
// The LoadedProgram it returns is the entry point for all subsequent
// SSA analysis: walker, analyzer, and emitter all operate on it.
package loader

import (
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// ErrNoPackages is returned when no well-typed packages are found.
var ErrNoPackages = errors.New("no well-typed packages found")

// LoadedProgram is the result of loading Go source into SSA form.
type LoadedProgram struct {
	// Program is the SSA representation of the whole loaded program.
	// All packages reachable from the initial set are included.
	Program *ssa.Program

	// Packages are the initial (requested) packages in SSA form.
	// Entries may be nil if a package failed to type-check.
	Packages []*ssa.Package

	// MainPkg is the first well-typed package in Packages.
	// For single-package loads this is the package of interest.
	MainPkg *ssa.Package
}

// loadMode is the packages.Config.Mode required to build SSA.
// LoadAllSyntax is needed so that SSA can be constructed for all
// transitively imported packages.
const loadMode = packages.LoadAllSyntax

// LoadDir loads the Go package in the given directory into SSA form.
// The directory must contain a valid, type-checkable Go package.
func LoadDir(dir string) (*LoadedProgram, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve dir %s: %w", dir, err)
	}
	cfg := &packages.Config{
		Mode: loadMode,
		Dir:  abs,
	}
	return load(cfg, ".")
}

// LoadFile loads the Go source file at the given path into SSA form.
// The file is loaded as the sole member of the package it declares.
func LoadFile(filename string) (*LoadedProgram, error) {
	abs, err := filepath.Abs(filename)
	if err != nil {
		return nil, fmt.Errorf("resolve file %s: %w", filename, err)
	}
	cfg := &packages.Config{
		Mode: loadMode,
		Dir:  filepath.Dir(abs),
	}
	return load(cfg, "file="+abs)
}

// load is the shared implementation.
func load(cfg *packages.Config, patterns ...string) (*LoadedProgram, error) {
	initial, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("packages.Load: %w", err)
	}
	if len(initial) == 0 {
		return nil, ErrNoPackages
	}

	// Report the first package-level error encountered.
	for _, p := range initial {
		for _, e := range p.Errors {
			return nil, fmt.Errorf("package %s: %v", p.ID, e)
		}
	}

	prog, pkgs := ssautil.Packages(initial, ssa.BuilderMode(0))
	prog.Build()

	lp := &LoadedProgram{
		Program:  prog,
		Packages: pkgs,
	}
	for _, pkg := range pkgs {
		if pkg != nil {
			lp.MainPkg = pkg
			break
		}
	}
	if lp.MainPkg == nil {
		return nil, ErrNoPackages
	}
	return lp, nil
}
