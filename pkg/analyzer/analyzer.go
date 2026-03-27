// Package analyzer implements the SSA Visitor that extracts an intent
// manifest from a Go package's SSA form.
//
// The analyzer walks every instruction and accumulates:
//   - Allocation tracking (stack vs heap) via core/shadow
//   - Detection of goroutines, channels, defer, panic, reflection
//   - Instruction count
//
// After the walk, call Manifest() to get the populated *intent.Manifest.
//
// Verification and intent extraction are the same pass: "this code
// doesn't use the network" is both a safety property and a capability
// declaration.
package analyzer

import (
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"

	"github.com/scttfrdmn/ember/core/intent"
	"github.com/scttfrdmn/ember/core/shadow"
	"github.com/scttfrdmn/ember/core/ssa/walker"
)

// Package sets for I/O capability detection.
var (
	networkPkgs    = map[string]bool{"net": true, "net/http": true, "net/rpc": true, "net/smtp": true}
	filesystemPkgs = map[string]bool{"os": true, "io/fs": true}
	processPkgs    = map[string]bool{"os/exec": true, "syscall": true}
)

// Analyzer implements walker.Visitor and populates an intent.Manifest
// from SSA analysis.
//
// Usage:
//
//	a := analyzer.New()
//	w := walker.New(a)
//	if err := w.WalkPackage(pkg); err != nil { ... }
//	manifest := a.Manifest()
type Analyzer struct {
	walker.BaseVisitor
	manifest intent.Manifest
	memory   shadow.Memory
	sizes    types.Sizes
}

// New creates a fresh Analyzer ready for a walk.
func New() *Analyzer {
	return &Analyzer{
		sizes: &types.StdSizes{WordSize: 8, MaxAlign: 8},
	}
}

// Manifest returns the completed manifest. Call this after WalkPackage.
// It finalizes the memory bounds and calls ComputeRuntimeStrips.
func (a *Analyzer) Manifest() *intent.Manifest {
	a.manifest.MaxMemoryBytes = a.memory.HeapBound()
	a.manifest.HasGC = a.memory.HasHeapAllocs()
	a.manifest.ComputeRuntimeStrips()
	return &a.manifest
}

// VisitInstruction implements walker.Visitor. It type-switches over
// every SSA instruction to accumulate intent signals.
func (a *Analyzer) VisitInstruction(instr ssa.Instruction) error {
	a.manifest.InstructionCount++

	switch v := instr.(type) {
	case *ssa.Alloc:
		return a.visitAlloc(v)

	case *ssa.Go:
		// Goroutine spawn — requires the scheduler.
		a.manifest.HasGoroutines = true

	case *ssa.MakeChan:
		// Channel creation.
		a.manifest.HasChannels = true

	case *ssa.Send:
		// Channel send.
		a.manifest.HasChannels = true

	case *ssa.Select:
		// Select over channels.
		a.manifest.HasChannels = true

	case *ssa.UnOp:
		// Channel receive: UnOp with ARROW operator.
		if v.Op == token.ARROW {
			a.manifest.HasChannels = true
		}

	case *ssa.Defer:
		// Deferred call.
		a.manifest.HasDefer = true

	case *ssa.RunDefers:
		// Execute deferred calls.
		a.manifest.HasDefer = true

	case *ssa.Panic:
		// Explicit panic.
		a.manifest.HasPanic = true

	case *ssa.MakeInterface:
		// Boxing a value into an interface requires runtime type metadata.
		a.manifest.HasReflection = true

	case *ssa.TypeAssert:
		// Type assertion requires runtime type metadata.
		a.manifest.HasReflection = true

	case *ssa.Call:
		a.visitCall(v)
	}

	return nil
}

func (a *Analyzer) visitCall(v *ssa.Call) {
	callee := v.Call.StaticCallee()
	if callee == nil {
		return // dynamic dispatch; can't statically determine package
	}
	pkg := callee.Package()
	if pkg == nil {
		return // builtin
	}
	path := pkg.Pkg.Path()
	if networkPkgs[path] {
		a.manifest.HasNetIO = true
		a.addCapability(intent.Capability{Kind: "network"})
	}
	if filesystemPkgs[path] {
		a.manifest.HasFileIO = true
		a.addCapability(intent.Capability{Kind: "filesystem"})
	}
	if processPkgs[path] {
		a.manifest.HasProcessIO = true
		a.addCapability(intent.Capability{Kind: "process"})
	}
}

func (a *Analyzer) addCapability(cap intent.Capability) {
	for _, c := range a.manifest.Capabilities {
		if c.Kind == cap.Kind {
			return // already present
		}
	}
	a.manifest.Capabilities = append(a.manifest.Capabilities, cap)
}

func (a *Analyzer) visitAlloc(v *ssa.Alloc) error {
	kind := shadow.AllocStack
	if v.Heap {
		kind = shadow.AllocHeap
	}

	// Compute the size of the allocated element type.
	// ssa.Alloc always has pointer type *T; we want sizeof(T).
	ptr, ok := v.Type().Underlying().(*types.Pointer)
	if !ok {
		// Should not happen for a well-typed SSA program.
		return nil
	}
	sizeBytes := a.sizes.Sizeof(ptr.Elem())

	a.memory.Record(shadow.Allocation{
		Instr:     v,
		Kind:      kind,
		SizeBytes: sizeBytes,
	})
	return nil
}
