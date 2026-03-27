// Package intent defines the IntentManifest — the machine-readable
// declaration of what an ember actually needs, discovered by static
// analysis of its SSA form.
//
// The manifest is not authored by a human. It is extracted by the
// analyzer from the code's SSA form. "The code tells you what it
// needs; you just have to listen."
package intent

import "sort"

// RuntimeStrip identifies a Go runtime subsystem that can be eliminated
// when compiling this ember to minimal WASM.
type RuntimeStrip string

const (
	StripScheduler  RuntimeStrip = "scheduler"
	StripGC         RuntimeStrip = "gc"
	StripChannels   RuntimeStrip = "channels"
	StripDefer      RuntimeStrip = "defer"
	StripReflection RuntimeStrip = "reflection"
	StripNetIO      RuntimeStrip = "net_io"
	StripFileIO     RuntimeStrip = "file_io"
)

// Capability describes an external resource the ember requires.
// Phase 0: pure arithmetic embers have no capabilities.
type Capability struct {
	Kind   string `json:"kind"`             // e.g. "s3_read", "http_get"
	Target string `json:"target,omitempty"` // e.g. "s3://bucket/prefix"
}

// Manifest is the intent declaration for an ember. It describes what
// the code actually uses, discovered by static analysis. The hearth
// uses this to synthesize the minimal execution surface — exactly what
// is declared and nothing more.
//
// JSON field names are snake_case for cross-language readability.
type Manifest struct {
	// MaxMemoryBytes is the upper bound on heap-allocated bytes.
	// 0 means no heap allocations were proven (pure stack/register computation).
	MaxMemoryBytes int64 `json:"max_memory_bytes"`

	// HasGoroutines is true if the code spawns goroutines (ssa.Go).
	// False means the scheduler can be stripped.
	HasGoroutines bool `json:"has_goroutines"`

	// HasGC is true if the code requires the garbage collector
	// (has proven heap allocations).
	HasGC bool `json:"has_gc"`

	// HasChannels is true if the code uses channels (MakeChan/Send/Select/receive).
	HasChannels bool `json:"has_channels"`

	// HasDefer is true if the code uses defer (ssa.Defer / ssa.RunDefers).
	HasDefer bool `json:"has_defer"`

	// HasPanic is true if the code contains an explicit panic (ssa.Panic).
	// Note: does not capture nil-pointer or bounds-check panics.
	HasPanic bool `json:"has_panic"`

	// HasReflection is true if the code uses reflect or requires runtime
	// type metadata (MakeInterface / TypeAssert).
	HasReflection bool `json:"has_reflection"`

	// HasNetIO is true if the code calls into net or net/* packages.
	HasNetIO bool `json:"has_net_io"`

	// HasFileIO is true if the code calls into os or io/fs packages.
	HasFileIO bool `json:"has_file_io"`

	// HasProcessIO is true if the code calls into os/exec or syscall packages.
	HasProcessIO bool `json:"has_process_io"`

	// Capabilities is the set of external resources the ember requires.
	// Empty for pure computational embers (the common case in Phase 0).
	Capabilities []Capability `json:"capabilities,omitempty"`

	// InstructionCount is the total number of SSA instructions visited.
	InstructionCount int `json:"instruction_count"`

	// RuntimeStrips lists Go runtime subsystems that can be stripped
	// from this ember's execution surface. Populated by ComputeRuntimeStrips.
	// Sorted alphabetically.
	RuntimeStrips []string `json:"runtime_strips,omitempty"`
}

// ComputeRuntimeStrips populates RuntimeStrips based on the boolean fields.
// Must be called after the analysis walk is complete.
func (m *Manifest) ComputeRuntimeStrips() {
	var strips []string
	if !m.HasGoroutines {
		strips = append(strips, string(StripScheduler))
	}
	if !m.HasGC {
		strips = append(strips, string(StripGC))
	}
	if !m.HasChannels {
		strips = append(strips, string(StripChannels))
	}
	if !m.HasDefer {
		strips = append(strips, string(StripDefer))
	}
	if !m.HasReflection {
		strips = append(strips, string(StripReflection))
	}
	if !m.HasNetIO {
		strips = append(strips, string(StripNetIO))
	}
	if !m.HasFileIO {
		strips = append(strips, string(StripFileIO))
	}
	sort.Strings(strips)
	m.RuntimeStrips = strips
}

// IsPureCompute reports whether this ember is a pure computation:
// no goroutines, no GC, no channels, no defer, no reflection, no I/O, no capabilities.
// Pure compute embers are the simplest execution surface target.
func (m *Manifest) IsPureCompute() bool {
	return !m.HasGoroutines &&
		!m.HasGC &&
		!m.HasChannels &&
		!m.HasDefer &&
		!m.HasPanic &&
		!m.HasReflection &&
		!m.HasNetIO &&
		!m.HasFileIO &&
		!m.HasProcessIO &&
		len(m.Capabilities) == 0
}
