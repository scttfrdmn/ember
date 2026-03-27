package shadow

import (
	"testing"
)

func TestHeapBound(t *testing.T) {
	var m Memory
	m.Record(Allocation{Kind: AllocStack, SizeBytes: 100})
	m.Record(Allocation{Kind: AllocHeap, SizeBytes: 200})
	m.Record(Allocation{Kind: AllocHeap, SizeBytes: 50})

	if got := m.HeapBound(); got != 250 {
		t.Errorf("HeapBound() = %d, want 250", got)
	}
}

func TestHeapBoundZeroWhenNoHeap(t *testing.T) {
	var m Memory
	m.Record(Allocation{Kind: AllocStack, SizeBytes: 1024})
	if got := m.HeapBound(); got != 0 {
		t.Errorf("HeapBound() = %d, want 0", got)
	}
}

func TestHasHeapAllocs(t *testing.T) {
	var m Memory
	if m.HasHeapAllocs() {
		t.Error("empty Memory should have no heap allocs")
	}
	m.Record(Allocation{Kind: AllocStack, SizeBytes: 8})
	if m.HasHeapAllocs() {
		t.Error("Memory with only stack allocs should report no heap allocs")
	}
	m.Record(Allocation{Kind: AllocHeap, SizeBytes: 16})
	if !m.HasHeapAllocs() {
		t.Error("Memory with heap alloc should report HasHeapAllocs=true")
	}
}

func TestReset(t *testing.T) {
	var m Memory
	m.Record(Allocation{Kind: AllocHeap, SizeBytes: 64})
	m.Reset()
	if m.HasHeapAllocs() {
		t.Error("after Reset, HasHeapAllocs should be false")
	}
	if got := m.HeapBound(); got != 0 {
		t.Errorf("after Reset, HeapBound() = %d, want 0", got)
	}
}

func TestAllocations(t *testing.T) {
	var m Memory
	m.Record(Allocation{Kind: AllocStack, SizeBytes: 8})
	m.Record(Allocation{Kind: AllocHeap, SizeBytes: 16})
	allocs := m.Allocations()
	if len(allocs) != 2 {
		t.Fatalf("Allocations() len = %d, want 2", len(allocs))
	}
	// Verify it's a copy
	allocs[0].SizeBytes = 9999
	if m.Allocations()[0].SizeBytes == 9999 {
		t.Error("Allocations() returned a reference, not a copy")
	}
}
