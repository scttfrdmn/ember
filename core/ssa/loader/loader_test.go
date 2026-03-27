package loader

import (
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestLoadDir_Add(t *testing.T) {
	lp, err := LoadDir("../../../testdata/add")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if lp.MainPkg == nil {
		t.Fatal("MainPkg is nil")
	}
	if lp.Program == nil {
		t.Fatal("Program is nil")
	}

	// The add package should have an Add member that is a function.
	member, ok := lp.MainPkg.Members["Add"]
	if !ok {
		t.Fatal("Add member not found in package")
	}
	fn, ok := member.(*ssa.Function)
	if !ok {
		t.Fatalf("Add member is %T, want *ssa.Function", member)
	}
	if fn.Name() != "Add" {
		t.Errorf("fn.Name() = %q, want %q", fn.Name(), "Add")
	}
	// Add(a, b int) int has 2 parameters.
	if len(fn.Params) != 2 {
		t.Errorf("Add has %d params, want 2", len(fn.Params))
	}
	// A pure arithmetic function with no control flow has exactly 1 block.
	if len(fn.Blocks) != 1 {
		t.Errorf("Add has %d blocks, want 1", len(fn.Blocks))
	}
}

func TestLoadDir_MissingDir(t *testing.T) {
	_, err := LoadDir("../../../testdata/nonexistent")
	if err == nil {
		t.Fatal("expected error for missing directory, got nil")
	}
}

func TestLoadFile_Add(t *testing.T) {
	lp, err := LoadFile("../../../testdata/add/add.go")
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if lp.MainPkg == nil {
		t.Fatal("MainPkg is nil")
	}
	if _, ok := lp.MainPkg.Members["Add"]; !ok {
		t.Fatal("Add member not found")
	}
}
