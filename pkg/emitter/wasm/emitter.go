package wasm

import (
	"errors"
	"fmt"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"

	"github.com/scttfrdmn/ember/core/ssa/walker"
)

// ErrUnsupportedInstruction is returned when the emitter encounters an
// SSA instruction it cannot lower to WASM. This is a deliberate feature
// gate: the verified arithmetic subset is intentionally narrow in Phase 0.
//
// Callers that see this error know the ember requires a feature not yet
// supported; they should reject it rather than producing incorrect output.
var ErrUnsupportedInstruction = errors.New("unsupported SSA instruction for WASM emission")

// Emitter implements walker.Visitor and produces WASM binary output.
//
// Phase 0 supported subset:
//   - *ssa.BinOp with integer (i64) arithmetic operators
//   - *ssa.Return (single value)
//   - *ssa.DebugRef (silently ignored)
//
// All other instructions return ErrUnsupportedInstruction.
//
// Usage:
//
//	e := wasm.NewEmitter()
//	w := walker.New(e)
//	if err := w.WalkPackage(pkg); err != nil { ... }
//	wasmBytes, err := e.Bytes()
type Emitter struct {
	walker.BaseVisitor

	functions []funcState
	current   *funcState
	firstErr  error
}

// funcState holds per-function encoding state.
type funcState struct {
	name string
	sig  *types.Signature

	// locals maps SSA Values to WASM local indices.
	// Parameters occupy indices 0..len(params)-1.
	// Instruction-produced values (BinOp results, etc.) start at len(params).
	locals map[ssa.Value]uint32

	// nextLocal is the index of the next local to allocate.
	nextLocal uint32

	// localTypes lists the ValType byte for each non-parameter local.
	// Parameters are not in this list (they're declared in the func type).
	localTypes []byte

	// code is the accumulating instruction bytecode (no trailing 'end').
	code []byte
}

// NewEmitter creates a fresh Emitter ready for a walk.
func NewEmitter() *Emitter {
	return &Emitter{}
}

// EnterFunction initializes per-function state and assigns WASM local
// indices to all function parameters.
func (e *Emitter) EnterFunction(fn *ssa.Function) error {
	if e.firstErr != nil {
		return e.firstErr
	}
	fs := &funcState{
		name:      fn.Name(),
		sig:       fn.Signature,
		locals:    make(map[ssa.Value]uint32),
		nextLocal: 0,
	}
	// Assign parameter locals first (indices 0..n-1).
	for _, p := range fn.Params {
		fs.locals[p] = fs.nextLocal
		fs.nextLocal++
	}
	e.current = fs
	return nil
}

// ExitFunction finalizes the current function and appends it to the
// completed function list.
func (e *Emitter) ExitFunction(_ *ssa.Function) error {
	if e.firstErr != nil {
		return e.firstErr
	}
	if e.current != nil {
		e.functions = append(e.functions, *e.current)
		e.current = nil
	}
	return nil
}

// VisitInstruction implements walker.Visitor. It type-switches to emit
// WASM instructions for each supported SSA instruction type.
func (e *Emitter) VisitInstruction(instr ssa.Instruction) error {
	if e.firstErr != nil {
		return e.firstErr
	}
	if e.current == nil {
		return fmt.Errorf("VisitInstruction called outside of a function")
	}

	switch v := instr.(type) {
	case *ssa.BinOp:
		return e.emitBinOp(v)
	case *ssa.Return:
		return e.emitReturn(v)
	case *ssa.DebugRef:
		// Debug references carry no semantic content; ignore them.
		return nil
	case *ssa.Phi:
		e.firstErr = fmt.Errorf("%w: *ssa.Phi requires dominance-order walk (phase 1)", ErrUnsupportedInstruction)
		return e.firstErr
	default:
		e.firstErr = fmt.Errorf("%w: %T", ErrUnsupportedInstruction, instr)
		return e.firstErr
	}
}

// emitBinOp lowers an ssa.BinOp to WASM.
//
// Pattern (integer arithmetic):
//
//	local.get <x_local>
//	local.get <y_local>
//	<opcode>             // e.g. i64.add
//	local.set <result>   // allocate new local for result
func (e *Emitter) emitBinOp(v *ssa.BinOp) error {
	// Determine WASM type from the result type.
	wasmType, err := goTypeToWASM(v.Type())
	if err != nil {
		e.firstErr = fmt.Errorf("BinOp result type: %w", err)
		return e.firstErr
	}

	// Look up WASM opcode for this operator + type combination.
	opcode, err := tokenToOpcode(v.Op, wasmType)
	if err != nil {
		e.firstErr = fmt.Errorf("BinOp operator: %w", err)
		return e.firstErr
	}

	// Resolve operand locals.
	xLocal, err := e.localIndex(v.X)
	if err != nil {
		e.firstErr = fmt.Errorf("BinOp X operand: %w", err)
		return e.firstErr
	}
	yLocal, err := e.localIndex(v.Y)
	if err != nil {
		e.firstErr = fmt.Errorf("BinOp Y operand: %w", err)
		return e.firstErr
	}

	// Allocate a new local for the result.
	resultLocal := e.current.nextLocal
	e.current.locals[v] = resultLocal
	e.current.nextLocal++
	e.current.localTypes = append(e.current.localTypes, wasmType)

	// Emit: local.get x; local.get y; opcode; local.set result
	e.current.code = append(e.current.code, OpcodeLocalGet)
	e.current.code = append(e.current.code, uleb128(xLocal)...)
	e.current.code = append(e.current.code, OpcodeLocalGet)
	e.current.code = append(e.current.code, uleb128(yLocal)...)
	e.current.code = append(e.current.code, opcode)
	e.current.code = append(e.current.code, OpcodeLocalSet)
	e.current.code = append(e.current.code, uleb128(resultLocal)...)

	return nil
}

// emitReturn lowers an ssa.Return to WASM.
//
// Phase 0: single return value only.
// Pattern:
//
//	local.get <result_local>
//	(implicit end from funcBody)
func (e *Emitter) emitReturn(v *ssa.Return) error {
	if len(v.Results) == 0 {
		// void return — nothing to push
		return nil
	}
	if len(v.Results) > 1 {
		e.firstErr = fmt.Errorf("%w: multi-value return (phase 1+)", ErrUnsupportedInstruction)
		return e.firstErr
	}

	resultLocal, err := e.localIndex(v.Results[0])
	if err != nil {
		e.firstErr = fmt.Errorf("Return result: %w", err)
		return e.firstErr
	}

	// Push the return value onto the stack; funcBody will append 'end'.
	e.current.code = append(e.current.code, OpcodeLocalGet)
	e.current.code = append(e.current.code, uleb128(resultLocal)...)

	return nil
}

// localIndex returns the WASM local index for an SSA value.
// Returns an error if the value has not been assigned a local index,
// which indicates an SSA instruction ordering problem or an unsupported value.
func (e *Emitter) localIndex(v ssa.Value) (uint32, error) {
	idx, ok := e.current.locals[v]
	if !ok {
		return 0, fmt.Errorf("%w: SSA value %s (%T) has no WASM local", ErrUnsupportedInstruction, v.Name(), v)
	}
	return idx, nil
}

// Bytes assembles and returns the complete WASM module binary.
// Call after WalkPackage completes successfully.
//
// Returns ErrUnsupportedInstruction (wrapped) if any instruction was
// rejected during the walk.
func (e *Emitter) Bytes() ([]byte, error) {
	if e.firstErr != nil {
		return nil, e.firstErr
	}
	if len(e.functions) == 0 {
		return nil, fmt.Errorf("no functions emitted")
	}

	// Build the four sections needed for a simple function module:
	// Type, Function, Export, Code.

	var (
		typeEntries [][]byte
		codeEntries [][]byte
		exportItems [][]byte
	)

	for i, fs := range e.functions {
		// Build function type signature.
		paramTypes, resultTypes, err := sigToWASMTypes(fs.sig)
		if err != nil {
			return nil, fmt.Errorf("function %s: %w", fs.name, err)
		}
		typeEntries = append(typeEntries, funcType(paramTypes, resultTypes))

		// Build function body.
		body := funcBody(fs.localTypes, fs.code)
		codeEntries = append(codeEntries, body)

		// Build export entry: name + func descriptor + index.
		exportEntry := encodeName(fs.name)
		exportEntry = append(exportEntry, exportFunc)
		exportEntry = append(exportEntry, uleb128(uint32(i))...)
		exportItems = append(exportItems, exportEntry)
	}

	// Type section: vec of func types.
	typeSection := section(sectionType, vec(typeEntries))

	// Function section: vec of type indices (one per function, index i → typeEntries[i]).
	var funcIndices [][]byte
	for i := range e.functions {
		funcIndices = append(funcIndices, uleb128(uint32(i)))
	}
	funcSection := section(sectionFunction, vec(funcIndices))

	// Export section: vec of export entries.
	exportSection := section(sectionExport, vec(exportItems))

	// Code section: vec of function bodies.
	codeSection := section(sectionCode, vec(codeEntries))

	// Assemble the full module.
	var module []byte
	module = append(module, wasmMagic...)
	module = append(module, typeSection...)
	module = append(module, funcSection...)
	module = append(module, exportSection...)
	module = append(module, codeSection...)

	return module, nil
}

// goTypeToWASM maps a Go types.Type to a WASM value type byte.
// Phase 0: all integer types → i64; float32 → f32; float64 → f64.
func goTypeToWASM(t types.Type) (byte, error) {
	basic, ok := t.Underlying().(*types.Basic)
	if !ok {
		return 0, fmt.Errorf("non-basic type %s not supported in phase 0", t)
	}
	switch basic.Kind() {
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64,
		types.Uintptr:
		return ValTypeI64, nil
	case types.Float32:
		return ValTypeF32, nil
	case types.Float64:
		return ValTypeF64, nil
	default:
		return 0, fmt.Errorf("Go type %s (kind %d) not supported in phase 0", basic.Name(), basic.Kind())
	}
}

// sigToWASMTypes converts a Go function signature to WASM param and result type slices.
func sigToWASMTypes(sig *types.Signature) (params []byte, results []byte, err error) {
	for i := 0; i < sig.Params().Len(); i++ {
		t := sig.Params().At(i).Type()
		wt, e := goTypeToWASM(t)
		if e != nil {
			return nil, nil, fmt.Errorf("param %d: %w", i, e)
		}
		params = append(params, wt)
	}
	if sig.Results() != nil {
		for i := 0; i < sig.Results().Len(); i++ {
			t := sig.Results().At(i).Type()
			wt, e := goTypeToWASM(t)
			if e != nil {
				return nil, nil, fmt.Errorf("result %d: %w", i, e)
			}
			results = append(results, wt)
		}
	}
	return params, results, nil
}

// tokenToOpcode maps a Go token.Token binary operator to the WASM opcode
// for the given value type. Returns an error for unsupported operators.
func tokenToOpcode(op token.Token, valType byte) (byte, error) {
	switch valType {
	case ValTypeI64:
		return tokenToI64Opcode(op)
	case ValTypeI32:
		return tokenToI32Opcode(op)
	case ValTypeF64:
		return tokenToF64Opcode(op)
	case ValTypeF32:
		return tokenToF32Opcode(op)
	default:
		return 0, fmt.Errorf("unsupported WASM type 0x%02X", valType)
	}
}

func tokenToI64Opcode(op token.Token) (byte, error) {
	switch op {
	case token.ADD:
		return OpcodeI64Add, nil
	case token.SUB:
		return OpcodeI64Sub, nil
	case token.MUL:
		return OpcodeI64Mul, nil
	case token.QUO:
		return OpcodeI64DivS, nil
	case token.REM:
		return OpcodeI64RemS, nil
	case token.AND:
		return OpcodeI64And, nil
	case token.OR:
		return OpcodeI64Or, nil
	case token.XOR:
		return OpcodeI64Xor, nil
	case token.SHL:
		return OpcodeI64Shl, nil
	case token.SHR:
		return OpcodeI64ShrS, nil
	default:
		return 0, fmt.Errorf("unsupported i64 operator %s", op)
	}
}

func tokenToI32Opcode(op token.Token) (byte, error) {
	switch op {
	case token.ADD:
		return OpcodeI32Add, nil
	case token.SUB:
		return OpcodeI32Sub, nil
	case token.MUL:
		return OpcodeI32Mul, nil
	case token.QUO:
		return OpcodeI32DivS, nil
	case token.REM:
		return OpcodeI32RemS, nil
	case token.AND:
		return OpcodeI32And, nil
	case token.OR:
		return OpcodeI32Or, nil
	case token.XOR:
		return OpcodeI32Xor, nil
	case token.SHL:
		return OpcodeI32Shl, nil
	case token.SHR:
		return OpcodeI32ShrS, nil
	default:
		return 0, fmt.Errorf("unsupported i32 operator %s", op)
	}
}

func tokenToF64Opcode(op token.Token) (byte, error) {
	switch op {
	case token.ADD:
		return OpcodeF64Add, nil
	case token.SUB:
		return OpcodeF64Sub, nil
	case token.MUL:
		return OpcodeF64Mul, nil
	case token.QUO:
		return OpcodeF64Div, nil
	default:
		return 0, fmt.Errorf("unsupported f64 operator %s", op)
	}
}

func tokenToF32Opcode(op token.Token) (byte, error) {
	switch op {
	case token.ADD:
		return OpcodeF32Add, nil
	case token.SUB:
		return OpcodeF32Sub, nil
	case token.MUL:
		return OpcodeF32Mul, nil
	case token.QUO:
		return OpcodeF32Div, nil
	default:
		return 0, fmt.Errorf("unsupported f32 operator %s", op)
	}
}
