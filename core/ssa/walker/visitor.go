// Package walker provides a generic SSA traversal framework based on
// the Visitor pattern. Callers implement Visitor (or embed BaseVisitor
// and override relevant methods) and pass it to Walker.
//
// The Visitor interface is intentionally minimal: a single
// VisitInstruction method receives every SSA instruction, and
// implementations dispatch internally via type assertions. This matches
// the philosophy of go/ast.Visitor and avoids a 40+ method interface.
package walker

import "golang.org/x/tools/go/ssa"

// Visitor is the interface implemented by any type that wants to
// process an SSA program during a walk. The walker calls these methods
// in traversal order.
//
// Implementations should embed BaseVisitor and override only the methods
// they care about.
//
// Returning a non-nil error from any method stops traversal immediately
// and causes WalkPackage/WalkFunction to return that error.
//
// VisitInstruction receives every SSA instruction in block order.
// Implementations use type assertions to dispatch to specific handlers:
//
//	func (v *MyVisitor) VisitInstruction(instr ssa.Instruction) error {
//	    switch i := instr.(type) {
//	    case *ssa.BinOp:
//	        return v.handleBinOp(i)
//	    case *ssa.Return:
//	        return v.handleReturn(i)
//	    }
//	    return nil
//	}
type Visitor interface {
	EnterPackage(pkg *ssa.Package) error
	ExitPackage(pkg *ssa.Package) error
	EnterFunction(fn *ssa.Function) error
	ExitFunction(fn *ssa.Function) error
	EnterBlock(block *ssa.BasicBlock) error
	ExitBlock(block *ssa.BasicBlock) error
	VisitInstruction(instr ssa.Instruction) error
}

// BaseVisitor provides no-op implementations of every Visitor method.
// Embed it in concrete visitors and override only the methods you need.
//
//	type MyVisitor struct {
//	    walker.BaseVisitor
//	}
//
//	func (v *MyVisitor) VisitInstruction(instr ssa.Instruction) error {
//	    // only this method is overridden; all others are no-ops
//	    ...
//	}
type BaseVisitor struct{}

func (b *BaseVisitor) EnterPackage(_ *ssa.Package) error      { return nil }
func (b *BaseVisitor) ExitPackage(_ *ssa.Package) error       { return nil }
func (b *BaseVisitor) EnterFunction(_ *ssa.Function) error    { return nil }
func (b *BaseVisitor) ExitFunction(_ *ssa.Function) error     { return nil }
func (b *BaseVisitor) EnterBlock(_ *ssa.BasicBlock) error     { return nil }
func (b *BaseVisitor) ExitBlock(_ *ssa.BasicBlock) error      { return nil }
func (b *BaseVisitor) VisitInstruction(_ ssa.Instruction) error { return nil }
