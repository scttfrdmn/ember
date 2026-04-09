// Package sdk provides a high-level Go API over the full ember pipeline.
// It wraps loader, analyzer, wasm.Emitter, and hearth into three methods:
// Build, Burn, and Batch.
package sdk

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"time"
	"unicode"

	"go/types"

	"golang.org/x/tools/go/ssa"

	"github.com/scttfrdmn/ember/core/intent"
	"github.com/scttfrdmn/ember/core/ssa/loader"
	"github.com/scttfrdmn/ember/core/ssa/walker"
	"github.com/scttfrdmn/ember/pkg/analyzer"
	wasm "github.com/scttfrdmn/ember/pkg/emitter/wasm"
	"github.com/scttfrdmn/ember/pkg/hearth"
)

// ParamType classifies a Go function parameter or result type for external consumers.
type ParamType int

const (
	ParamTypeInt     ParamType = iota // int, int8…int64, uint, uint8…uint64, uintptr
	ParamTypeFloat64                  // float64
	ParamTypeFloat32                  // float32
	ParamTypeBool                     // bool
)

// String returns a human-readable name for the ParamType.
func (p ParamType) String() string {
	switch p {
	case ParamTypeInt:
		return "int"
	case ParamTypeFloat64:
		return "float64"
	case ParamTypeFloat32:
		return "float32"
	case ParamTypeBool:
		return "bool"
	default:
		return "unknown"
	}
}

// ExportSig is the type signature of one exported function from an ember.
// Extracted during Build() from SSA type information before WASM encoding discards
// Go type names. The WASM binary carries only i32/i64/f32/f64 value types.
type ExportSig struct {
	Name       string      // Go exported function name, e.g. "Add"
	Params     []ParamType // in-order parameter types
	Results    []ParamType // in-order result types
	ParamNames []string    // in-order parameter names (from SSA)
}

// Artifact is a compiled ember, ready to be burned or distributed.
type Artifact struct {
	WASM     []byte
	Manifest *intent.Manifest
	Exports  []ExportSig
}

// Job is a single execution unit for batch processing.
type Job struct {
	ID       string // caller-defined identifier; echoed in Result
	Artifact *Artifact
	Fn       string
	Args     []uint64
}

// Result is the outcome of one Job.
type Result struct {
	ID      string
	Values  []uint64
	Err     error
	Elapsed time.Duration
}

// SDK is the high-level ember API. Use New or NewWithCaps to create one.
type SDK struct {
	h *hearth.Hearth
}

// New creates an SDK backed by a Hearth with auto-detected local capabilities.
func New() *SDK {
	return &SDK{h: hearth.New()}
}

// NewWithCaps creates an SDK backed by a Hearth with the given capabilities.
func NewWithCaps(caps hearth.Capabilities) *SDK {
	return &SDK{h: hearth.NewWithCaps(caps)}
}

// Hearth returns the underlying Hearth. Used by pkg/serve to expose capabilities.
func (s *SDK) Hearth() *hearth.Hearth {
	return s.h
}

// Build loads a Go source directory, analyzes it, emits WASM, and extracts
// exported function signatures. Returns an Artifact ready for Burn or distribution.
func (s *SDK) Build(_ context.Context, sourceDir string) (*Artifact, error) {
	// Step 1: load source into SSA.
	lp, err := loader.LoadDir(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("sdk.Build(%s): load: %w", sourceDir, err)
	}

	// Step 2: analyzer walk → manifest.
	a := analyzer.New()
	if err := walker.New(a).WalkPackage(lp.MainPkg); err != nil {
		return nil, fmt.Errorf("sdk.Build(%s): analyze: %w", sourceDir, err)
	}
	m := a.Manifest()

	// Step 3: extract export signatures from SSA.
	// This MUST happen before the emitter walk, because the emitter does not
	// preserve Go parameter names or type names — only WASM value types.
	exports, err := extractExports(lp.MainPkg)
	if err != nil {
		return nil, fmt.Errorf("sdk.Build(%s): export extraction: %w", sourceDir, err)
	}

	// Step 4: emitter walk → WASM bytes.
	e := wasm.NewEmitter()
	e.AssignPackageIndices(lp.MainPkg)
	if err := walker.New(e).WalkPackage(lp.MainPkg); err != nil {
		return nil, fmt.Errorf("sdk.Build(%s): emit: %w", sourceDir, err)
	}
	wasmBytes, err := e.Bytes()
	if err != nil {
		return nil, fmt.Errorf("sdk.Build(%s): encode WASM: %w", sourceDir, err)
	}

	return &Artifact{WASM: wasmBytes, Manifest: m, Exports: exports}, nil
}

// Burn executes fn from artifact on the local hearth.
func (s *SDK) Burn(ctx context.Context, a *Artifact, fn string, args []uint64) ([]uint64, error) {
	return s.h.Burn(ctx, a.WASM, a.Manifest, fn, args)
}

// Batch executes jobs in parallel with bounded concurrency.
// Results are returned in submission order. Individual failures are captured
// in Result.Err — Batch itself always returns a nil error.
// maxConcurrency <= 0 defaults to runtime.NumCPU().
//
// Note: pkg/batch provides the same functionality as a standalone package
// (for use by cmd/ember batch). This method inlines the runner to avoid a
// circular import (pkg/batch imports pkg/sdk).
func (s *SDK) Batch(ctx context.Context, jobs []Job, maxConcurrency int) ([]Result, error) {
	concurrency := maxConcurrency
	if concurrency <= 0 {
		concurrency = runtime.NumCPU()
	}

	results := make([]Result, len(jobs))
	sem := make(chan struct{}, concurrency)

	var wg sync.WaitGroup
	for i, job := range jobs {
		i, job := i, job
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ctx.Err() != nil {
				results[i] = Result{ID: job.ID, Err: ctx.Err()}
				return
			}
			sem <- struct{}{}
			defer func() { <-sem }()
			start := time.Now()
			vals, err := s.Burn(ctx, job.Artifact, job.Fn, job.Args)
			results[i] = Result{ID: job.ID, Values: vals, Err: err, Elapsed: time.Since(start)}
		}()
	}
	wg.Wait()
	return results, nil
}

// extractExports iterates the package's members in sorted (deterministic) order
// and collects exported top-level functions with resolvable parameter types.
func extractExports(pkg *ssa.Package) ([]ExportSig, error) {
	names := make([]string, 0, len(pkg.Members))
	for name := range pkg.Members {
		names = append(names, name)
	}
	sort.Strings(names)

	var exports []ExportSig
	for _, name := range names {
		fn, ok := pkg.Members[name].(*ssa.Function)
		if !ok {
			continue
		}
		if fn.Synthetic != "" || fn.Blocks == nil {
			continue
		}
		if fn.Signature.Recv() != nil {
			continue // skip methods
		}
		runes := []rune(fn.Name())
		if len(runes) == 0 || !unicode.IsUpper(runes[0]) {
			continue // skip unexported
		}

		sig, err := extractSig(fn)
		if err != nil {
			return nil, fmt.Errorf("function %s: %w", fn.Name(), err)
		}
		exports = append(exports, sig)
	}
	return exports, nil
}

// extractSig builds an ExportSig from a single SSA function.
func extractSig(fn *ssa.Function) (ExportSig, error) {
	sig := ExportSig{Name: fn.Name()}

	for _, p := range fn.Params {
		pt, err := goTypeToParamType(p.Type())
		if err != nil {
			return ExportSig{}, fmt.Errorf("param %q: %w", p.Name(), err)
		}
		sig.Params = append(sig.Params, pt)
		sig.ParamNames = append(sig.ParamNames, p.Name())
	}

	if fn.Signature.Results() != nil {
		for i := 0; i < fn.Signature.Results().Len(); i++ {
			r := fn.Signature.Results().At(i)
			pt, err := goTypeToParamType(r.Type())
			if err != nil {
				return ExportSig{}, fmt.Errorf("result %d: %w", i, err)
			}
			sig.Results = append(sig.Results, pt)
		}
	}

	return sig, nil
}

// goTypeToParamType maps a Go types.Type to a ParamType.
// Mirrors the logic of goTypeToWASM in pkg/emitter/wasm but maps to ParamType
// instead of WASM val-type bytes. Self-contained; does not import the emitter.
func goTypeToParamType(t types.Type) (ParamType, error) {
	basic, ok := t.Underlying().(*types.Basic)
	if !ok {
		return 0, fmt.Errorf("non-basic type %s not supported", t)
	}
	switch basic.Kind() {
	case types.Bool:
		return ParamTypeBool, nil
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64,
		types.Uintptr:
		return ParamTypeInt, nil
	case types.Float32:
		return ParamTypeFloat32, nil
	case types.Float64:
		return ParamTypeFloat64, nil
	default:
		return 0, fmt.Errorf("Go type %s (kind %d) not supported", basic.Name(), basic.Kind())
	}
}
