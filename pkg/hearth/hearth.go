// Package hearth implements the minimal execution surface synthesizer.
//
// A Hearth sits on any surface where compute can happen. It knows two
// things: what it is (capability fingerprint) and whether it can burn
// a given ember (manifest matching). When asked to burn, it synthesizes
// the minimal execution surface declared by the manifest, runs the code,
// returns the result, and tears down.
//
// Phase 3: pure-compute embers execute via pkg/runtime — a purpose-built
// Go WASM interpreter with zero external dependencies. I/O-capable embers
// are deferred to Phase 5.
package hearth

import (
	"context"
	"fmt"
	"runtime"

	"github.com/scttfrdmn/ember/core/intent"
	emberruntime "github.com/scttfrdmn/ember/pkg/runtime"
)

// Capabilities describes what a hearth can provide.
type Capabilities struct {
	// MaxMemoryPages is the maximum WASM linear memory pages (64KB each)
	// this hearth can allocate for an ember.
	MaxMemoryPages uint32 `json:"max_memory_pages"`

	// Cores is the number of CPU cores available.
	Cores int `json:"cores"`

	// HasNetwork reports whether this hearth can provide network access.
	HasNetwork bool `json:"has_network"`

	// HasGPU reports whether this hearth has a GPU available.
	HasGPU bool `json:"has_gpu"`

	// Arch is the CPU architecture ("amd64", "arm64", etc.)
	Arch string `json:"arch"`
}

// Hearth is a minimal execution surface synthesizer.
// The zero value is not useful; create one with New or NewWithCaps.
type Hearth struct {
	caps Capabilities
}

// New creates a Hearth with auto-detected local capabilities.
func New() *Hearth {
	return &Hearth{
		caps: detectCapabilities(),
	}
}

// NewWithCaps creates a Hearth with the given capability set.
// Useful for testing or constrained environments.
func NewWithCaps(c Capabilities) *Hearth {
	return &Hearth{caps: c}
}

// Capabilities returns this hearth's capability fingerprint.
func (h *Hearth) Capabilities() Capabilities {
	return h.caps
}

// CanBurn reports whether this hearth can satisfy the ember's intent manifest.
// Returns false if the manifest requires capabilities the hearth lacks.
func (h *Hearth) CanBurn(m *intent.Manifest) bool {
	// Check memory: require at least enough pages for the ember's declared bound.
	if m.MaxMemoryBytes > 0 {
		requiredPages := uint32((m.MaxMemoryBytes + 65535) / 65536)
		if requiredPages > h.caps.MaxMemoryPages {
			return false
		}
	}
	// Check network capability.
	for _, cap := range m.Capabilities {
		if cap.Kind == "network" && !h.caps.HasNetwork {
			return false
		}
	}
	return true
}

// Burn executes the named exported function from the WASM binary,
// synthesizing an execution surface shaped by the intent manifest.
//
// args and return values are uint64 (the WASM representation for both
// i32 and i64 values). For int results, cast the uint64 back to int64.
//
// Phase 3: pure-compute embers run on pkg/runtime (zero external deps).
// I/O-capable embers return ErrNotImplemented (deferred to Phase 5).
func (h *Hearth) Burn(_ context.Context, code []byte, m *intent.Manifest, fn string, args []uint64) ([]uint64, error) {
	if !h.CanBurn(m) {
		return nil, fmt.Errorf("hearth cannot satisfy ember intent manifest")
	}
	if !m.IsPureCompute() {
		return nil, fmt.Errorf("%w", emberruntime.ErrNotImplemented)
	}

	mod, err := emberruntime.Compile(code)
	if err != nil {
		return nil, fmt.Errorf("compile WASM: %w", err)
	}

	inst := mod.Instantiate()
	results, err := inst.Call(fn, args...)
	if err != nil {
		return nil, fmt.Errorf("ember execution: %w", err)
	}
	return results, nil
}

// detectCapabilities auto-detects the current machine's capabilities.
func detectCapabilities() Capabilities {
	return Capabilities{
		// Default: 16MB = 256 pages. Phase 3 will query actual available memory.
		MaxMemoryPages: 256,
		Cores:          runtime.NumCPU(),
		// Phase 0: no GPU detection; no network by default.
		HasNetwork: false,
		HasGPU:     false,
		Arch:       runtime.GOARCH,
	}
}
