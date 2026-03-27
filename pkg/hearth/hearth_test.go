package hearth

import (
	"context"
	"math"
	"testing"

	"github.com/scttfrdmn/ember/core/intent"
	"github.com/scttfrdmn/ember/core/ssa/loader"
	"github.com/scttfrdmn/ember/core/ssa/walker"
	"github.com/scttfrdmn/ember/pkg/analyzer"
	wasm "github.com/scttfrdmn/ember/pkg/emitter/wasm"
)

// TestBurnAdd is the Phase 0 end-to-end proof:
//
//	testdata/add/add.go: func Add(a, b int) int { return a + b }
//	→ loader → SSA
//	→ walker + Analyzer → Manifest (pure compute)
//	→ walker + Emitter → WASM bytes
//	→ Hearth.Burn("Add", [3, 4]) → [7]
func TestBurnAdd(t *testing.T) {
	ctx := context.Background()

	// Step 1: Load source into SSA.
	lp, err := loader.LoadDir("../../testdata/add")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	// Step 2: Extract intent manifest.
	a := analyzer.New()
	w := walker.New(a)
	if err := w.WalkPackage(lp.MainPkg); err != nil {
		t.Fatalf("WalkPackage (analyzer): %v", err)
	}
	m := a.Manifest()

	// Verify manifest declares pure compute.
	if !m.IsPureCompute() {
		t.Fatalf("Add should be pure compute; manifest: %+v", m)
	}

	// Step 3: Emit WASM.
	e := wasm.NewEmitter()
	w2 := walker.New(e)
	if err := w2.WalkPackage(lp.MainPkg); err != nil {
		t.Fatalf("WalkPackage (emitter): %v", err)
	}
	wasmBytes, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes(): %v", err)
	}
	t.Logf("WASM module: %d bytes", len(wasmBytes))

	// Step 4: Burn on a hearth.
	h := New()
	if !h.CanBurn(m) {
		t.Fatal("hearth cannot burn Add; capability mismatch")
	}

	results, err := h.Burn(ctx, wasmBytes, m, "Add", []uint64{3, 4})
	if err != nil {
		t.Fatalf("Burn: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Burn returned %d results, want 1", len(results))
	}

	// WASM i64 results are uint64; reinterpret as int64 for signed values.
	result := int64(results[0])
	if result != 7 {
		t.Errorf("Add(3, 4) = %d, want 7", result)
	}
	t.Logf("Add(3, 4) = %d ✓", result)
}

func TestBurnAdd_LargerValues(t *testing.T) {
	ctx := context.Background()

	lp, err := loader.LoadDir("../../testdata/add")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	a := analyzer.New()
	walker.New(a).WalkPackage(lp.MainPkg) //nolint:errcheck
	m := a.Manifest()

	e := wasm.NewEmitter()
	walker.New(e).WalkPackage(lp.MainPkg) //nolint:errcheck
	wasmBytes, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes(): %v", err)
	}

	h := New()
	tests := []struct{ a, b, want int64 }{
		{0, 0, 0},
		{100, 200, 300},
		{-5, 3, -2},
		{math.MaxInt32, 1, math.MaxInt32 + 1},
	}
	for _, tc := range tests {
		args := []uint64{uint64(tc.a), uint64(tc.b)}
		results, err := h.Burn(ctx, wasmBytes, m, "Add", args)
		if err != nil {
			t.Errorf("Burn(%d, %d): %v", tc.a, tc.b, err)
			continue
		}
		if got := int64(results[0]); got != tc.want {
			t.Errorf("Add(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// burnFixture is a test helper that loads, analyzes, emits, and burns a fixture.
func burnFixture(t *testing.T, dir, fn string, args []uint64) (int64, *intent.Manifest) {
	t.Helper()
	ctx := context.Background()

	lp, err := loader.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir(%s): %v", dir, err)
	}

	a := analyzer.New()
	if err := walker.New(a).WalkPackage(lp.MainPkg); err != nil {
		t.Fatalf("WalkPackage (analyzer): %v", err)
	}
	m := a.Manifest()

	e := wasm.NewEmitter()
	e.AssignPackageIndices(lp.MainPkg)
	if err := walker.New(e).WalkPackage(lp.MainPkg); err != nil {
		t.Fatalf("WalkPackage (emitter): %v", err)
	}
	wasmBytes, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes(): %v", err)
	}

	h := New()
	results, err := h.Burn(ctx, wasmBytes, m, fn, args)
	if err != nil {
		t.Fatalf("Burn(%s, %v): %v", fn, args, err)
	}
	if len(results) == 0 {
		return 0, m
	}
	return int64(results[0]), m
}

func TestBurnMax(t *testing.T) {
	tests := []struct{ a, b, want int64 }{
		{3, 1, 3},
		{1, 3, 3},
		{5, 5, 5},
		{-2, -1, -1},
	}
	for _, tc := range tests {
		got, _ := burnFixture(t, "../../testdata/max", "Max", []uint64{uint64(tc.a), uint64(tc.b)})
		if got != tc.want {
			t.Errorf("Max(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestBurnAbs(t *testing.T) {
	tests := []struct{ x, want int64 }{
		{5, 5},
		{-5, 5},
		{0, 0},
		{-100, 100},
	}
	for _, tc := range tests {
		got, _ := burnFixture(t, "../../testdata/abs", "Abs", []uint64{uint64(tc.x)})
		if got != tc.want {
			t.Errorf("Abs(%d) = %d, want %d", tc.x, got, tc.want)
		}
	}
}

func TestBurnSum(t *testing.T) {
	got, m := burnFixture(t, "../../testdata/sum", "Sum", []uint64{1, 2, 3})
	if got != 6 {
		t.Errorf("Sum(1, 2, 3) = %d, want 6", got)
	}
	if !m.IsPureCompute() {
		t.Errorf("Sum should be pure compute; manifest: %+v", m)
	}
}

func TestCanBurn_ManifestChecks(t *testing.T) {
	h := NewWithCaps(Capabilities{MaxMemoryPages: 10})

	// Pure compute with no memory should pass.
	m := &intent.Manifest{}
	if !h.CanBurn(m) {
		t.Error("should be able to burn zero-memory manifest")
	}

	// Manifest requiring more memory than hearth has should fail.
	m2 := &intent.Manifest{MaxMemoryBytes: 11 * 65536} // 11 pages
	if h.CanBurn(m2) {
		t.Error("should not be able to burn manifest requiring 11 pages on 10-page hearth")
	}
}
