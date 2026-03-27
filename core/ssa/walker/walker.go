package walker

import (
	"fmt"
	"sort"

	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/ssa"
)

// Walker drives SSA traversal using a Visitor. Create one with New
// and call WalkPackage to process an entire package.
//
// Walk order:
//   - Package members are visited in alphabetical order (deterministic).
//   - Each non-synthetic function with a body is visited.
//   - Within a function, basic blocks are visited in dominance-order
//     (fn.DomPreorder()), ensuring each block's dominators are visited
//     before their dominated successors.
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

// WalkReachable visits all functions reachable from mainFns via RTA
// call graph analysis, including functions in imported packages.
// Functions are visited in deterministic order (sorted by RelString).
func (w *Walker) WalkReachable(pkg *ssa.Package, mainFns []*ssa.Function) error {
	result := rta.Analyze(mainFns, true)
	var fns []*ssa.Function
	for fn := range result.CallGraph.Nodes {
		if fn == nil || fn.Blocks == nil || fn.Synthetic != "" {
			continue
		}
		fns = append(fns, fn)
	}
	sort.Slice(fns, func(i, j int) bool {
		return fns[i].RelString(nil) < fns[j].RelString(nil)
	})

	if err := w.v.EnterPackage(pkg); err != nil {
		return err
	}
	for _, fn := range fns {
		if err := w.WalkFunction(fn); err != nil {
			return fmt.Errorf("function %s: %w", fn.Name(), err)
		}
	}
	return w.v.ExitPackage(pkg)
}

// WalkFunction visits a single SSA function: EnterFunction, then all
// basic blocks in dominance-order (fn.DomPreorder()), then ExitFunction.
func (w *Walker) WalkFunction(fn *ssa.Function) error {
	if err := w.v.EnterFunction(fn); err != nil {
		return err
	}
	for _, block := range fn.DomPreorder() {
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
