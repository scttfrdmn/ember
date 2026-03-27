// Package shadow provides a minimal allocation tracker used during SSA
// analysis. It records stack and heap allocations seen during the walk
// to compute the memory bound for the intent manifest.
//
// This is intentionally simpler than Giri's full shadow memory system:
// Phase 0 only needs allocation counting, not full provenance tracking.
// Provenance tracking is introduced in Phase 1.
package shadow

import "golang.org/x/tools/go/ssa"

// AllocKind distinguishes stack allocations from heap escapes.
type AllocKind uint8

const (
	// AllocStack is a stack-allocated variable (ssa.Alloc with Heap=false).
	AllocStack AllocKind = iota
	// AllocHeap is a heap-allocated variable (ssa.Alloc with Heap=true).
	// These require the garbage collector.
	AllocHeap
)

// Allocation records a single ssa.Alloc instruction observed during the walk.
type Allocation struct {
	Instr     *ssa.Alloc
	Kind      AllocKind
	SizeBytes int64 // computed via types.StdSizes{WordSize:8, MaxAlign:8}
}

// Memory accumulates allocation state during an SSA walk.
// It is populated by the analyzer via Record() calls.
// The zero value is ready to use.
type Memory struct {
	allocs []Allocation
}

// Record adds an allocation to the tracker.
func (m *Memory) Record(a Allocation) {
	m.allocs = append(m.allocs, a)
}

// HeapBound returns the upper bound on heap-allocated bytes:
// the sum of SizeBytes for all AllocHeap entries.
// Returns 0 for functions with no proven heap allocations.
func (m *Memory) HeapBound() int64 {
	var total int64
	for _, a := range m.allocs {
		if a.Kind == AllocHeap {
			total += a.SizeBytes
		}
	}
	return total
}

// HasHeapAllocs reports whether any heap allocations were recorded.
func (m *Memory) HasHeapAllocs() bool {
	for _, a := range m.allocs {
		if a.Kind == AllocHeap {
			return true
		}
	}
	return false
}

// Allocations returns a copy of all recorded allocations.
func (m *Memory) Allocations() []Allocation {
	result := make([]Allocation, len(m.allocs))
	copy(result, m.allocs)
	return result
}

// Reset clears all recorded state. Useful when reusing a Memory
// across multiple function walks within a single analysis session.
func (m *Memory) Reset() {
	m.allocs = m.allocs[:0]
}
