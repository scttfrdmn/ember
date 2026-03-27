package walker

import (
	"errors"
	"testing"

	"golang.org/x/tools/go/ssa"

	"github.com/scttfrdmn/ember/core/ssa/loader"
)

// blockOrderVisitor records the block indices in EnterBlock call order.
type blockOrderVisitor struct {
	BaseVisitor
	funcName   string
	blockOrder []int
}

func (v *blockOrderVisitor) EnterFunction(fn *ssa.Function) error {
	v.funcName = fn.Name()
	v.blockOrder = nil
	return nil
}

func (v *blockOrderVisitor) EnterBlock(block *ssa.BasicBlock) error {
	v.blockOrder = append(v.blockOrder, block.Index)
	return nil
}

// countingVisitor counts calls to each Visitor method.
type countingVisitor struct {
	BaseVisitor
	enterPackage int
	exitPackage  int
	enterFunc    int
	exitFunc     int
	enterBlock   int
	exitBlock    int
	instructions int
	funcNames    []string
}

func (v *countingVisitor) EnterPackage(_ *ssa.Package) error { v.enterPackage++; return nil }
func (v *countingVisitor) ExitPackage(_ *ssa.Package) error  { v.exitPackage++; return nil }
func (v *countingVisitor) EnterFunction(fn *ssa.Function) error {
	v.enterFunc++
	v.funcNames = append(v.funcNames, fn.Name())
	return nil
}
func (v *countingVisitor) ExitFunction(_ *ssa.Function) error   { v.exitFunc++; return nil }
func (v *countingVisitor) EnterBlock(_ *ssa.BasicBlock) error   { v.enterBlock++; return nil }
func (v *countingVisitor) ExitBlock(_ *ssa.BasicBlock) error    { v.exitBlock++; return nil }
func (v *countingVisitor) VisitInstruction(_ ssa.Instruction) error { v.instructions++; return nil }

func TestWalkPackage_Add(t *testing.T) {
	lp, err := loader.LoadDir("../../../testdata/add")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	cv := &countingVisitor{}
	w := New(cv)
	if err := w.WalkPackage(lp.MainPkg); err != nil {
		t.Fatalf("WalkPackage: %v", err)
	}

	if cv.enterPackage != 1 {
		t.Errorf("EnterPackage called %d times, want 1", cv.enterPackage)
	}
	if cv.exitPackage != 1 {
		t.Errorf("ExitPackage called %d times, want 1", cv.exitPackage)
	}
	if cv.enterFunc != 1 {
		t.Errorf("EnterFunction called %d times, want 1 (only Add)", cv.enterFunc)
	}
	if len(cv.funcNames) != 1 || cv.funcNames[0] != "Add" {
		t.Errorf("visited functions = %v, want [Add]", cv.funcNames)
	}
	if cv.enterFunc != cv.exitFunc {
		t.Errorf("EnterFunction (%d) != ExitFunction (%d)", cv.enterFunc, cv.exitFunc)
	}
	if cv.enterBlock != cv.exitBlock {
		t.Errorf("EnterBlock (%d) != ExitBlock (%d)", cv.enterBlock, cv.exitBlock)
	}
	// Add has 1 block with at least 2 instructions (BinOp + Return).
	if cv.instructions < 2 {
		t.Errorf("VisitInstruction called %d times, want >= 2", cv.instructions)
	}
}

func TestWalkPackage_ErrorStopsTraversal(t *testing.T) {
	lp, err := loader.LoadDir("../../../testdata/add")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	sentinel := errors.New("stop")
	var called int
	v := &errorOnFirstInstr{sentinel: sentinel, called: &called}
	w := New(v)
	err = w.WalkPackage(lp.MainPkg)
	if !errors.Is(err, sentinel) {
		t.Errorf("WalkPackage error = %v, want sentinel", err)
	}
	if called != 1 {
		t.Errorf("VisitInstruction called %d times after error, want 1", called)
	}
}

type errorOnFirstInstr struct {
	BaseVisitor
	sentinel error
	called   *int
}

func (v *errorOnFirstInstr) VisitInstruction(_ ssa.Instruction) error {
	*v.called++
	return v.sentinel
}

func TestWalkFunction_DomPreorder(t *testing.T) {
	lp, err := loader.LoadDir("../../../testdata/max")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	bv := &blockOrderVisitor{}
	w := New(bv)
	if err := w.WalkPackage(lp.MainPkg); err != nil {
		t.Fatalf("WalkPackage: %v", err)
	}

	// Entry block (index 0) must always be visited first.
	if len(bv.blockOrder) == 0 {
		t.Fatal("no blocks visited")
	}
	if bv.blockOrder[0] != 0 {
		t.Errorf("first visited block index = %d, want 0 (entry)", bv.blockOrder[0])
	}
	// Max has 3 blocks: entry (if), true branch, false branch.
	if len(bv.blockOrder) < 2 {
		t.Errorf("expected at least 2 blocks for Max, got %d", len(bv.blockOrder))
	}
}

func TestWalkReachable_Sum(t *testing.T) {
	lp, err := loader.LoadDir("../../../testdata/sum")
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	sumFn, ok := lp.MainPkg.Members["Sum"].(*ssa.Function)
	if !ok {
		t.Fatal("Sum not found in package")
	}

	cv := &countingVisitor{}
	w := New(cv)
	if err := w.WalkReachable(lp.MainPkg, []*ssa.Function{sumFn}); err != nil {
		t.Fatalf("WalkReachable: %v", err)
	}

	// Should visit both Sum and add.
	if cv.enterFunc < 2 {
		t.Errorf("WalkReachable visited %d functions, want >= 2 (Sum + add)", cv.enterFunc)
	}
	found := make(map[string]bool)
	for _, name := range cv.funcNames {
		found[name] = true
	}
	for _, want := range []string{"Sum", "add"} {
		if !found[want] {
			t.Errorf("WalkReachable did not visit function %q; visited: %v", want, cv.funcNames)
		}
	}
}
