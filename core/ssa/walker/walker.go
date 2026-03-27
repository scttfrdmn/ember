package walker

import (
	"fmt"
	"sort"

	"golang.org/x/tools/go/ssa"
)

// Walker drives SSA traversal using a Visitor. Create one with New
// and call WalkPackage to process an entire package.
//
// Walk order:
//   - Package members are visited in alphabetical order (deterministic).
//   - Each non-synthetic function with a body is visited.
//   - Within a function, basic blocks are visited in fn.Blocks slice order.
//     TODO(phase1): switch to fn.DomPreorder() for dominance-order traversal
//     once Phi node support is added to the emitter.
//   - Within a block, instructions are visited in order.
type Walker struct {
	v Visitor
}

// New creates a Walker that will call methods on v during traversal.
func New(v Visitor) *Walker {
	return &Walker{v: v}
}

// WalkPackage visits all exported, non-synthetic functions in pkg that
// have a body (Blocks != nil). Members are visited alphabetically.
//
// Call graph traversal (following callees into other packages) is
// deferred to Phase 1.
func (w *Walker) WalkPackage(pkg *ssa.Package) error {
	if err := w.v.EnterPackage(pkg); err != nil {
		return err
	}

	// Collect and sort member names for deterministic ordering.
	names := make([]string, 0, len(pkg.Members))
	for name := range pkg.Members {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		member := pkg.Members[name]
		fn, ok := member.(*ssa.Function)
		if !ok {
			continue
		}
		// Skip synthetic wrappers (interface method wrappers, etc.)
		if fn.Synthetic != "" {
			continue
		}
		// Skip functions with no body (external declarations).
		if fn.Blocks == nil {
			continue
		}
		if err := w.WalkFunction(fn); err != nil {
			return fmt.Errorf("function %s: %w", fn.Name(), err)
		}
	}

	return w.v.ExitPackage(pkg)
}

// WalkFunction visits a single SSA function: EnterFunction, then all
// basic blocks in order, then ExitFunction.
func (w *Walker) WalkFunction(fn *ssa.Function) error {
	if err := w.v.EnterFunction(fn); err != nil {
		return err
	}
	// TODO(phase1): use fn.DomPreorder() for dominance-order traversal
	// once the emitter supports Phi nodes and control flow.
	for _, block := range fn.Blocks {
		if err := w.WalkBlock(block); err != nil {
			return err
		}
	}
	return w.v.ExitFunction(fn)
}

// WalkBlock visits a single basic block: EnterBlock, then each
// instruction in order, then ExitBlock.
func (w *Walker) WalkBlock(block *ssa.BasicBlock) error {
	if err := w.v.EnterBlock(block); err != nil {
		return err
	}
	for _, instr := range block.Instrs {
		if err := w.v.VisitInstruction(instr); err != nil {
			return err
		}
	}
	return w.v.ExitBlock(block)
}
