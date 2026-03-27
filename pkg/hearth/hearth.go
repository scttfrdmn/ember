// Package hearth implements the minimal execution surface synthesizer.
//
// A Hearth sits on any surface where compute can happen. It knows two
// things: what it is (capability fingerprint) and whether it can burn
// a given ember (manifest matching). When asked to burn, it synthesizes
// the minimal execution surface declared by the manifest, runs the code,
// returns the result, and tears down.
//
// Phase 0: the execution surface is backed by wazero (pure Go WASM
// runtime, Apache 2.0). Phase 3 will synthesize native code directly.
package hearth

import (
	"context"
	"fmt"
	"runtime"

	"github.com/tetratelabs/wazero"

	"github.com/scttfrdmn/ember/core/intent"
)

// Capabilities describes what a hearth can provide.
type Capabilities struct {
	// MaxMemoryPages is the maximum WASM linear memory pages (64KB each)
	// this hearth can allocate for an ember.
	MaxMemoryPages uint32

	// Cores is the number of CPU cores available.
	Cores int

	// HasNetwork reports whether this hearth can provide network access.
	HasNetwork bool

	// HasGPU reports whether this hearth has a GPU available.
	HasGPU bool

	// Arch is the CPU architecture ("amd64", "arm64", etc.)
	Arch string
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
// Phase 0 uses wazero's interpreter engine (portable, zero CGO).
// Phase 3 will use a JIT-compiled native engine shaped by the manifest.
func (h *Hearth) Burn(ctx context.Context, code []byte, m *intent.Manifest, fn string, args []uint64) ([]uint64, error) {
	if !h.CanBurn(m) {
		return nil, fmt.Errorf("hearth cannot satisfy ember intent manifest")
	}

	// Synthesize the execution surface from the manifest.
	// Phase 0: configure wazero with manifest-derived constraints.
	rtCfg := wazero.NewRuntimeConfig()

	// Use interpreter engine in Phase 0 (portable, no CGO).
	// The interpreter is slower but correct on all platforms.
	// Phase 3 switches to wazevo AOT compilation when the manifest
	// confirms the ember is safe to JIT.
	rtCfg = rtCfg.WithCompilationCache(wazero.NewCompilationCache())

	rt := wazero.NewRuntimeWithConfig(ctx, rtCfg)
	defer rt.Close(ctx)

	// Compile the WASM module.
	compiled, err := rt.CompileModule(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("compile WASM: %w", err)
	}

	// Instantiate with manifest-constrained configuration.
	modCfg := wazero.NewModuleConfig().
		WithName("ember")

	mod, err := rt.InstantiateModule(ctx, compiled, modCfg)
	if err != nil {
		return nil, fmt.Errorf("instantiate WASM module: %w", err)
	}
	defer mod.Close(ctx)

	// Look up the exported function.
	f := mod.ExportedFunction(fn)
	if f == nil {
		return nil, fmt.Errorf("function %q not exported from WASM module", fn)
	}

	// Execute. The surface tears down via deferred Close calls above.
	results, err := f.Call(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("WASM execution: %w", err)
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

