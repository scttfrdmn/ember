package runtime

import (
	"errors"
	"testing"

	"github.com/scttfrdmn/ember/core/ssa/loader"
	"github.com/scttfrdmn/ember/core/ssa/walker"
	wasm "github.com/scttfrdmn/ember/pkg/emitter/wasm"
)

// testdata lives at ember/testdata/; this package is ember/pkg/runtime/
// so we need to go up four directory levels.
const testdataBase = "../../testdata/"

// compileFixture loads, emits, and compiles a testdata fixture to a Module.
func compileFixture(t *testing.T, fixture string) *Module {
	t.Helper()
	dir := testdataBase + fixture
	lp, err := loader.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir(%s): %v", dir, err)
	}
	e := wasm.NewEmitter()
	e.AssignPackageIndices(lp.MainPkg)
	if err := walker.New(e).WalkPackage(lp.MainPkg); err != nil {
		t.Fatalf("WalkPackage: %v", err)
	}
	wasmBytes, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes(): %v", err)
	}
	mod, err := Compile(wasmBytes)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return mod
}

func TestCompile_InvalidMagic(t *testing.T) {
	_, err := Compile([]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	if !errors.Is(err, ErrInvalidMagic) {
		t.Errorf("expected ErrInvalidMagic, got %v", err)
	}
}

func TestCompile_TooShort(t *testing.T) {
	_, err := Compile([]byte{0x00, 0x61})
	if !errors.Is(err, ErrInvalidMagic) {
		t.Errorf("expected ErrInvalidMagic, got %v", err)
	}
}

func TestResolvePartners_BlockEnd(t *testing.T) {
	// block(0x40) nop end — block.Partner should point to end
	instrs := []Instr{
		{Op: 0x02, Imm1: 0x40, Partner: -1}, // 0: block
		{Op: 0x01, Partner: -1},              // 1: nop
		{Op: 0x0B, Partner: -1},              // 2: end
	}
	if err := resolvePartners(instrs); err != nil {
		t.Fatalf("resolvePartners: %v", err)
	}
	if instrs[0].Partner != 2 {
		t.Errorf("block.Partner = %d, want 2", instrs[0].Partner)
	}
	if instrs[2].Partner != 0 {
		t.Errorf("end.Partner = %d, want 0", instrs[2].Partner)
	}
}

func TestResolvePartners_LoopSelf(t *testing.T) {
	// loop nop end — loop.Partner should be its own index
	instrs := []Instr{
		{Op: 0x03, Imm1: 0x40, Partner: -1}, // 0: loop
		{Op: 0x01, Partner: -1},              // 1: nop
		{Op: 0x0B, Partner: -1},              // 2: end
	}
	if err := resolvePartners(instrs); err != nil {
		t.Fatalf("resolvePartners: %v", err)
	}
	if instrs[0].Partner != 0 {
		t.Errorf("loop.Partner = %d, want 0 (self)", instrs[0].Partner)
	}
}

func TestResolvePartners_IfElse(t *testing.T) {
	// if nop else nop end
	instrs := []Instr{
		{Op: 0x04, Imm1: 0x40, Partner: -1}, // 0: if
		{Op: 0x01, Partner: -1},              // 1: nop (true branch)
		{Op: 0x05, Partner: -1},              // 2: else
		{Op: 0x01, Partner: -1},              // 3: nop (false branch)
		{Op: 0x0B, Partner: -1},              // 4: end
	}
	if err := resolvePartners(instrs); err != nil {
		t.Fatalf("resolvePartners: %v", err)
	}
	if instrs[0].Partner != 2 {
		t.Errorf("if.Partner = %d, want 2 (else)", instrs[0].Partner)
	}
	if instrs[2].Partner != 4 {
		t.Errorf("else.Partner = %d, want 4 (end)", instrs[2].Partner)
	}
	if instrs[4].Partner != 2 {
		t.Errorf("end.Partner = %d, want 2 (else)", instrs[4].Partner)
	}
}

func TestCall_Add(t *testing.T) {
	mod := compileFixture(t, "add")
	inst := mod.Instantiate()
	results, err := inst.Call("Add", uint64(3), uint64(4))
	if err != nil {
		t.Fatalf("Call Add: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Add returned %d results, want 1", len(results))
	}
	if int64(results[0]) != 7 {
		t.Errorf("Add(3, 4) = %d, want 7", int64(results[0]))
	}
}

func TestCall_Max(t *testing.T) {
	mod := compileFixture(t, "max")
	tests := []struct{ a, b, want int64 }{
		{3, 1, 3},
		{1, 3, 3},
		{-2, -1, -1},
		{5, 5, 5},
	}
	for _, tc := range tests {
		inst := mod.Instantiate()
		results, err := inst.Call("Max", uint64(tc.a), uint64(tc.b))
		if err != nil {
			t.Fatalf("Call Max(%d,%d): %v", tc.a, tc.b, err)
		}
		if int64(results[0]) != tc.want {
			t.Errorf("Max(%d,%d) = %d, want %d", tc.a, tc.b, int64(results[0]), tc.want)
		}
	}
}

func TestCall_Fib(t *testing.T) {
	mod := compileFixture(t, "fib")
	tests := []struct{ n, want int64 }{
		{0, 0},
		{1, 1},
		{5, 5},
		{10, 55},
	}
	for _, tc := range tests {
		inst := mod.Instantiate()
		results, err := inst.Call("Fib", uint64(tc.n))
		if err != nil {
			t.Fatalf("Call Fib(%d): %v", tc.n, err)
		}
		if int64(results[0]) != tc.want {
			t.Errorf("Fib(%d) = %d, want %d", tc.n, int64(results[0]), tc.want)
		}
	}
}

func TestCall_SumN(t *testing.T) {
	mod := compileFixture(t, "sumloop")
	tests := []struct{ n, want int64 }{
		{0, 0},
		{1, 0},
		{5, 10},
		{10, 45},
	}
	for _, tc := range tests {
		inst := mod.Instantiate()
		results, err := inst.Call("SumN", uint64(tc.n))
		if err != nil {
			t.Fatalf("Call SumN(%d): %v", tc.n, err)
		}
		if int64(results[0]) != tc.want {
			t.Errorf("SumN(%d) = %d, want %d", tc.n, int64(results[0]), tc.want)
		}
	}
}

func TestCall_SumFields(t *testing.T) {
	mod := compileFixture(t, "point")
	inst := mod.Instantiate()
	results, err := inst.Call("SumFields")
	if err != nil {
		t.Fatalf("Call SumFields: %v", err)
	}
	if int64(results[0]) != 7 {
		t.Errorf("SumFields() = %d, want 7", int64(results[0]))
	}
}

func TestCall_DivMod(t *testing.T) {
	mod := compileFixture(t, "divmod")
	inst := mod.Instantiate()
	results, err := inst.Call("DivMod", uint64(10), uint64(3))
	if err != nil {
		t.Fatalf("Call DivMod: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("DivMod returned %d results, want 2", len(results))
	}
	q, r := int64(results[0]), int64(results[1])
	if q != 3 || r != 1 {
		t.Errorf("DivMod(10, 3) = (%d, %d), want (3, 1)", q, r)
	}
}

func TestCall_FunctionNotFound(t *testing.T) {
	mod := compileFixture(t, "add")
	inst := mod.Instantiate()
	_, err := inst.Call("NonExistent")
	if !errors.Is(err, ErrFunctionNotFound) {
		t.Errorf("expected ErrFunctionNotFound, got %v", err)
	}
}
