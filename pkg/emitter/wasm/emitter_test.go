package wasm

import (
	"testing"

	"github.com/scttfrdmn/ember/core/ssa/loader"
	"github.com/scttfrdmn/ember/core/ssa/walker"
	emberruntime "github.com/scttfrdmn/ember/pkg/runtime"
)

func TestEmitter_Add_ProducesValidWASM(t *testing.T) {
	lp, err := loader.LoadDir("../../../testdata/add")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	e := NewEmitter()
	w := walker.New(e)
	if err := w.WalkPackage(lp.MainPkg); err != nil {
		t.Fatalf("WalkPackage: %v", err)
	}

	wasmBytes, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes(): %v", err)
	}

	// WASM module must start with magic + version.
	if len(wasmBytes) < 8 {
		t.Fatalf("WASM output too short: %d bytes", len(wasmBytes))
	}
	want := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
	for i, b := range want {
		if wasmBytes[i] != b {
			t.Errorf("wasmBytes[%d] = 0x%02X, want 0x%02X", i, wasmBytes[i], b)
		}
	}

	// Module must contain type, function, export, and code sections.
	hasSections := func(data []byte, ids ...byte) bool {
		for _, id := range ids {
			found := false
			for _, b := range data {
				if b == id {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	if !hasSections(wasmBytes, sectionType, sectionFunction, sectionExport, sectionCode) {
		t.Errorf("WASM output missing required sections, bytes: %x", wasmBytes)
	}

	t.Logf("WASM output (%d bytes): %x", len(wasmBytes), wasmBytes)
}

func TestLEB128_Encoding(t *testing.T) {
	tests := []struct {
		v    uint32
		want []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{63, []byte{0x3F}},
		{127, []byte{0x7F}},
		{128, []byte{0x80, 0x01}},
		{300, []byte{0xAC, 0x02}},
	}
	for _, tc := range tests {
		got := uleb128(tc.v)
		if len(got) != len(tc.want) {
			t.Errorf("uleb128(%d) = %x, want %x", tc.v, got, tc.want)
			continue
		}
		for i, b := range tc.want {
			if got[i] != b {
				t.Errorf("uleb128(%d)[%d] = 0x%02X, want 0x%02X", tc.v, i, got[i], b)
			}
		}
	}
}

// execWASM is a test helper that compiles and runs a WASM binary via pkg/runtime,
// calling the named function with args and returning the first result.
func execWASM(t *testing.T, wasmBytes []byte, fn string, args ...uint64) uint64 {
	t.Helper()
	mod, err := emberruntime.Compile(wasmBytes)
	if err != nil {
		t.Fatalf("runtime.Compile: %v", err)
	}
	inst := mod.Instantiate()
	results, err := inst.Call(fn, args...)
	if err != nil {
		t.Fatalf("Call %s(%v): %v", fn, args, err)
	}
	if len(results) == 0 {
		return 0
	}
	return results[0]
}

func TestEmitter_Max(t *testing.T) {
	lp, err := loader.LoadDir("../../../testdata/max")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	e := NewEmitter()
	e.AssignPackageIndices(lp.MainPkg)
	w := walker.New(e)
	if err := w.WalkPackage(lp.MainPkg); err != nil {
		t.Fatalf("WalkPackage: %v", err)
	}
	wasmBytes, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes(): %v", err)
	}
	t.Logf("Max WASM (%d bytes): %x", len(wasmBytes), wasmBytes)

	tests := []struct{ a, b, want int64 }{
		{3, 1, 3},
		{1, 3, 3},
		{5, 5, 5},
		{-1, 0, 0},
	}
	for _, tc := range tests {
		got := int64(execWASM(t, wasmBytes, "Max", uint64(tc.a), uint64(tc.b)))
		if got != tc.want {
			t.Errorf("Max(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestEmitter_Sum(t *testing.T) {
	lp, err := loader.LoadDir("../../../testdata/sum")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	e := NewEmitter()
	e.AssignPackageIndices(lp.MainPkg)
	w := walker.New(e)
	if err := w.WalkPackage(lp.MainPkg); err != nil {
		t.Fatalf("WalkPackage: %v", err)
	}
	wasmBytes, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes(): %v", err)
	}
	t.Logf("Sum WASM (%d bytes): %x", len(wasmBytes), wasmBytes)

	got := int64(execWASM(t, wasmBytes, "Sum", uint64(1), uint64(2), uint64(3)))
	if got != 6 {
		t.Errorf("Sum(1, 2, 3) = %d, want 6", got)
	}
}

func TestEmitter_SumLoop(t *testing.T) {
	lp, err := loader.LoadDir("../../../testdata/sumloop")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	e := NewEmitter()
	e.AssignPackageIndices(lp.MainPkg)
	w := walker.New(e)
	if err := w.WalkPackage(lp.MainPkg); err != nil {
		t.Fatalf("WalkPackage: %v", err)
	}
	wasmBytes, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes(): %v", err)
	}
	t.Logf("SumLoop WASM (%d bytes): %x", len(wasmBytes), wasmBytes)

	tests := []struct{ n, want int64 }{
		{0, 0},
		{1, 0},
		{5, 10},
		{10, 45},
	}
	for _, tc := range tests {
		got := int64(execWASM(t, wasmBytes, "SumN", uint64(tc.n)))
		if got != tc.want {
			t.Errorf("SumN(%d) = %d, want %d", tc.n, got, tc.want)
		}
	}
}

func TestEmitter_Point(t *testing.T) {
	lp, err := loader.LoadDir("../../../testdata/point")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	e := NewEmitter()
	e.AssignPackageIndices(lp.MainPkg)
	w := walker.New(e)
	if err := w.WalkPackage(lp.MainPkg); err != nil {
		t.Fatalf("WalkPackage: %v", err)
	}
	wasmBytes, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes(): %v", err)
	}
	t.Logf("Point WASM (%d bytes): %x", len(wasmBytes), wasmBytes)

	got := int64(execWASM(t, wasmBytes, "SumFields"))
	if got != 7 {
		t.Errorf("SumFields() = %d, want 7", got)
	}
}

func TestEmitter_DivMod(t *testing.T) {
	lp, err := loader.LoadDir("../../../testdata/divmod")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	e := NewEmitter()
	e.AssignPackageIndices(lp.MainPkg)
	w := walker.New(e)
	if err := w.WalkPackage(lp.MainPkg); err != nil {
		t.Fatalf("WalkPackage: %v", err)
	}
	wasmBytes, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes(): %v", err)
	}
	t.Logf("DivMod WASM (%d bytes): %x", len(wasmBytes), wasmBytes)

	mod, err := emberruntime.Compile(wasmBytes)
	if err != nil {
		t.Fatalf("runtime.Compile: %v", err)
	}
	inst := mod.Instantiate()
	results, err := inst.Call("DivMod", uint64(10), uint64(3))
	if err != nil {
		t.Fatalf("Call DivMod(10, 3): %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("DivMod returned %d results, want 2", len(results))
	}
	q, r := int64(results[0]), int64(results[1])
	if q != 3 || r != 1 {
		t.Errorf("DivMod(10, 3) = (%d, %d), want (3, 1)", q, r)
	}
	t.Logf("DivMod(10, 3) = (%d, %d) ✓", q, r)
}

func TestEncodeLocalDecls_Grouping(t *testing.T) {
	// Two i64 locals should be grouped into a single entry: (2, i64)
	localTypes := []byte{ValTypeI64, ValTypeI64}
	decls := encodeLocalDecls(localTypes)
	// Expected: [1 entry] [(2, 0x7E)] = [0x01, 0x02, 0x7E]
	want := []byte{0x01, 0x02, ValTypeI64}
	if len(decls) != len(want) {
		t.Fatalf("encodeLocalDecls(%x) = %x, want %x", localTypes, decls, want)
	}
	for i, b := range want {
		if decls[i] != b {
			t.Errorf("encodeLocalDecls[%d] = 0x%02X, want 0x%02X", i, decls[i], b)
		}
	}
}
