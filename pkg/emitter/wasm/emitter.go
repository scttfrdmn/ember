package wasm

import (
	"errors"
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"sort"

	"golang.org/x/tools/go/ssa"

	"github.com/scttfrdmn/ember/core/ssa/walker"
)

// ErrUnsupportedInstruction is returned when the emitter encounters an
// SSA instruction it cannot lower to WASM. This is a deliberate feature
// gate: the verified subset is intentionally narrow in early phases.
//
// Callers that see this error know the ember requires a feature not yet
// supported; they should reject it rather than producing incorrect output.
var ErrUnsupportedInstruction = errors.New("unsupported SSA instruction for WASM emission")

// Emitter implements walker.Visitor and produces WASM binary output.
//
// Phase 2 supported subset (cumulative):
//   - *ssa.BinOp: integer arithmetic + comparison operators
//   - *ssa.UnOp: integer negation (token.SUB), bitwise not (token.XOR), pointer load (token.MUL)
//   - *ssa.Return: single or multi-value
//   - *ssa.If: conditional branch (simple if/else, loop condition)
//   - *ssa.Call: static local function calls; multi-value results via Extract
//   - *ssa.Phi: pre-allocated locals, assigned in ExitBlock
//   - *ssa.Jump: no-op (control flow handled in assembleFunction)
//   - *ssa.DebugRef: silently ignored
//   - *ssa.Alloc: bump allocator via __alloc; sets hasMemory
//   - *ssa.FieldAddr: struct field address computation
//   - *ssa.Store: memory store
//   - *ssa.Convert: type conversion opcodes
//   - *ssa.Extract: tuple element extraction from multi-value calls
//   - *ssa.IndexAddr: array element address (stretch)
//
// Usage:
//
//	e := wasm.NewEmitter()
//	e.AssignPackageIndices(pkg) // required for multi-function packages
//	w := walker.New(e)
//	if err := w.WalkPackage(pkg); err != nil { ... }
//	wasmBytes, err := e.Bytes()
type Emitter struct {
	walker.BaseVisitor

	functions    []funcState
	current      *funcState
	currentBlock *ssa.BasicBlock

	// funcIndex maps SSA functions to their WASM function index.
	// When hasAllocSlot is true, indices start at 1 (index 0 = __alloc).
	// Populated by AssignFunctionIndices before walking multi-function modules.
	funcIndex map[*ssa.Function]uint32

	// hasAllocSlot is true when AssignFunctionIndices was called, reserving
	// WASM function index 0 for the internal __alloc bump allocator.
	// Bytes() prepends the __alloc function, memory section, and global section.
	hasAllocSlot bool

	// hasMemory is true when at least one ssa.Alloc was encountered.
	// Controls whether Memory and Global sections are emitted.
	hasMemory bool

	firstErr error
}

// funcState holds per-function encoding state.
type funcState struct {
	name string
	sig  *types.Signature
	fn   *ssa.Function

	// locals maps SSA Values to WASM local indices.
	// Parameters occupy indices 0..n-1.
	// Phi locals are pre-allocated in EnterFunction.
	// BinOp/Call result locals are allocated at emission time.
	locals map[ssa.Value]uint32

	// nextLocal is the index of the next local to allocate.
	nextLocal uint32

	// localTypes lists the ValType byte for each non-parameter local.
	// Parameters are not in this list (they're declared in the func type).
	localTypes []byte

	// phiLocals maps Phi nodes to their pre-allocated WASM local index.
	phiLocals map[*ssa.Phi]uint32

	// tupleLocals maps multi-return Call values to per-result local indices.
	// Used by ssa.Extract to unpack individual results.
	tupleLocals map[ssa.Value][]uint32

	// blockCode holds per-block instruction bytecode, keyed by block index.
	blockCode map[int][]byte

	// assembled is the final assembled function body, set in ExitFunction.
	assembled []byte
}

// NewEmitter creates a fresh Emitter ready for a walk.
func NewEmitter() *Emitter {
	return &Emitter{
		funcIndex: make(map[*ssa.Function]uint32),
	}
}

// AssignFunctionIndices pre-assigns WASM function indices to all functions
// that will be emitted. Call before WalkReachable for multi-function modules.
// This ensures callees have indices before callers emit call instructions.
//
// Indices start at 1: index 0 is reserved for the internal __alloc bump
// allocator (emitted unconditionally when AssignFunctionIndices is called).
func (e *Emitter) AssignFunctionIndices(fns []*ssa.Function) {
	for i, fn := range fns {
		e.funcIndex[fn] = uint32(i + 1) // +1: index 0 = __alloc
	}
	e.hasAllocSlot = true
}

// AssignPackageIndices pre-assigns WASM function indices to all non-synthetic
// functions with bodies in pkg, in the same alphabetical order used by WalkPackage.
// Call this before WalkPackage when the package contains internal function calls.
func (e *Emitter) AssignPackageIndices(pkg *ssa.Package) {
	names := make([]string, 0, len(pkg.Members))
	for name := range pkg.Members {
		names = append(names, name)
	}
	sort.Strings(names)
	var fns []*ssa.Function
	for _, name := range names {
		fn, ok := pkg.Members[name].(*ssa.Function)
		if !ok || fn.Synthetic != "" || fn.Blocks == nil {
			continue
		}
		fns = append(fns, fn)
	}
	e.AssignFunctionIndices(fns)
}

// EnterBlock records the current block for subsequent appendCode calls.
func (e *Emitter) EnterBlock(block *ssa.BasicBlock) error {
	e.currentBlock = block
	if e.current.blockCode == nil {
		e.current.blockCode = make(map[int][]byte)
	}
	return nil
}

// ExitBlock emits Phi node assignments for successor blocks that this
// block is a predecessor of, writing into this block's code buffer.
func (e *Emitter) ExitBlock(block *ssa.BasicBlock) error {
	if e.firstErr != nil {
		return e.firstErr
	}
	return e.emitPhiAssignments(block)
}

// appendCode appends bytes to the current block's code buffer.
func (e *Emitter) appendCode(b ...byte) {
	if e.currentBlock == nil || e.current == nil {
		return
	}
	idx := e.currentBlock.Index
	e.current.blockCode[idx] = append(e.current.blockCode[idx], b...)
}

// EnterFunction initializes per-function state: assigns parameter locals,
// then pre-allocates locals for all Phi nodes in the function.
func (e *Emitter) EnterFunction(fn *ssa.Function) error {
	if e.firstErr != nil {
		return e.firstErr
	}
	fs := &funcState{
		name:        fn.Name(),
		sig:         fn.Signature,
		fn:          fn,
		locals:      make(map[ssa.Value]uint32),
		phiLocals:   make(map[*ssa.Phi]uint32),
		tupleLocals: make(map[ssa.Value][]uint32),
		blockCode:   make(map[int][]byte),
		nextLocal:   0,
	}
	// Assign parameter locals (indices 0..n-1).
	for _, p := range fn.Params {
		fs.locals[p] = fs.nextLocal
		fs.nextLocal++
	}
	// Pre-allocate locals for all Phi nodes (scan all blocks).
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			phi, ok := instr.(*ssa.Phi)
			if !ok {
				continue
			}
			wt, err := goTypeToWASM(phi.Type())
			if err != nil {
				continue // unsupported type; will error at use site
			}
			local := fs.nextLocal
			fs.locals[phi] = local
			fs.phiLocals[phi] = local
			fs.nextLocal++
			fs.localTypes = append(fs.localTypes, wt)
		}
	}
	e.current = fs
	return nil
}

// ExitFunction assembles the per-block code buffers into the final function
// body and appends the function to the completed list.
func (e *Emitter) ExitFunction(fn *ssa.Function) error {
	if e.firstErr != nil {
		return e.firstErr
	}
	if e.current == nil {
		return nil
	}
	code, err := e.assembleFunction(fn)
	if err != nil {
		e.firstErr = err
		return err
	}
	e.current.assembled = code
	e.functions = append(e.functions, *e.current)
	e.current = nil
	return nil
}

// VisitInstruction implements walker.Visitor.
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
	case *ssa.UnOp:
		return e.emitUnOp(v)
	case *ssa.Return:
		return e.emitReturn(v)
	case *ssa.If:
		return e.emitIf(v)
	case *ssa.Call:
		return e.emitCall(v)
	case *ssa.Alloc:
		return e.emitAlloc(v)
	case *ssa.FieldAddr:
		return e.emitFieldAddr(v)
	case *ssa.Store:
		return e.emitStore(v)
	case *ssa.Convert:
		return e.emitConvert(v)
	case *ssa.Extract:
		return e.emitExtract(v)
	case *ssa.IndexAddr:
		return e.emitIndexAddr(v)
	case *ssa.Phi:
		return nil // pre-allocated in EnterFunction; assigned in ExitBlock
	case *ssa.Jump:
		return nil // unconditional jump; handled by assembleFunction
	case *ssa.DebugRef:
		return nil // no semantic content
	default:
		e.firstErr = fmt.Errorf("%w: %T", ErrUnsupportedInstruction, instr)
		return e.firstErr
	}
}

// emitBinOp lowers an ssa.BinOp to WASM.
//
// For arithmetic operators: operand type determines the opcode family.
// For comparison operators: operand type determines opcode family, but
// result is stored as i32 (WASM comparisons produce 0 or 1 as i32).
func (e *Emitter) emitBinOp(v *ssa.BinOp) error {
	resultWASMType, err := goTypeToWASM(v.Type())
	if err != nil {
		e.firstErr = fmt.Errorf("BinOp result type: %w", err)
		return e.firstErr
	}

	// For comparison ops: the opcode family is determined by operand type,
	// not result type (which is bool → i32).
	opcodeType := resultWASMType
	if isComparisonOp(v.Op) {
		opcodeType, err = goTypeToWASM(v.X.Type())
		if err != nil {
			e.firstErr = fmt.Errorf("BinOp operand type: %w", err)
			return e.firstErr
		}
	}

	opcode, err := tokenToOpcode(v.Op, opcodeType)
	if err != nil {
		e.firstErr = fmt.Errorf("BinOp operator: %w", err)
		return e.firstErr
	}

	// Allocate result local with the declared result type.
	resultLocal := e.current.nextLocal
	e.current.locals[v] = resultLocal
	e.current.nextLocal++
	e.current.localTypes = append(e.current.localTypes, resultWASMType)

	// Emit: push X; push Y; opcode; local.set result
	if err := e.emitValuePush(v.X); err != nil {
		e.firstErr = fmt.Errorf("BinOp X: %w", err)
		return e.firstErr
	}
	if err := e.emitValuePush(v.Y); err != nil {
		e.firstErr = fmt.Errorf("BinOp Y: %w", err)
		return e.firstErr
	}
	e.appendCode(opcode)
	e.appendCode(OpcodeLocalSet)
	e.appendCode(uleb128(resultLocal)...)

	return nil
}

// emitUnOp lowers an ssa.UnOp to WASM.
// Supports: token.SUB (negation), token.XOR (bitwise not).
func (e *Emitter) emitUnOp(v *ssa.UnOp) error {
	wt, err := goTypeToWASM(v.Type())
	if err != nil {
		e.firstErr = fmt.Errorf("UnOp type: %w", err)
		return e.firstErr
	}

	resultLocal := e.current.nextLocal
	e.current.locals[v] = resultLocal
	e.current.nextLocal++
	e.current.localTypes = append(e.current.localTypes, wt)

	switch v.Op {
	case token.SUB:
		// Negate: 0 - x
		switch wt {
		case ValTypeI64:
			e.appendCode(OpcodeI64Const)
			e.appendCode(sleb128(0)...)
		case ValTypeI32:
			e.appendCode(OpcodeI32Const)
			e.appendCode(sleb128(0)...)
		default:
			e.firstErr = fmt.Errorf("%w: UnOp SUB for type 0x%02X", ErrUnsupportedInstruction, wt)
			return e.firstErr
		}
		if err := e.emitValuePush(v.X); err != nil {
			e.firstErr = fmt.Errorf("UnOp SUB operand: %w", err)
			return e.firstErr
		}
		switch wt {
		case ValTypeI64:
			e.appendCode(OpcodeI64Sub)
		case ValTypeI32:
			e.appendCode(OpcodeI32Sub)
		}
	case token.XOR:
		// Bitwise not: x ^ -1 (all ones)
		if err := e.emitValuePush(v.X); err != nil {
			e.firstErr = fmt.Errorf("UnOp XOR operand: %w", err)
			return e.firstErr
		}
		switch wt {
		case ValTypeI64:
			e.appendCode(OpcodeI64Const)
			e.appendCode(sleb128(-1)...)
			e.appendCode(OpcodeI64Xor)
		case ValTypeI32:
			e.appendCode(OpcodeI32Const)
			e.appendCode(sleb128(-1)...)
			e.appendCode(OpcodeI32Xor)
		default:
			e.firstErr = fmt.Errorf("%w: UnOp XOR for type 0x%02X", ErrUnsupportedInstruction, wt)
			return e.firstErr
		}
	case token.NOT:
		// Boolean not: i32.eqz
		if err := e.emitValuePush(v.X); err != nil {
			e.firstErr = fmt.Errorf("UnOp NOT operand: %w", err)
			return e.firstErr
		}
		e.appendCode(OpcodeI32Eqz)
	case token.MUL:
		// Pointer dereference: load from memory address.
		// Result local already allocated above; delegate to emitLoad.
		// emitLoad allocates its own local, so release the one we allocated.
		e.current.nextLocal--
		e.current.localTypes = e.current.localTypes[:len(e.current.localTypes)-1]
		delete(e.current.locals, v)
		return e.emitLoad(v)
	default:
		e.firstErr = fmt.Errorf("%w: UnOp %s", ErrUnsupportedInstruction, v.Op)
		return e.firstErr
	}

	e.appendCode(OpcodeLocalSet)
	e.appendCode(uleb128(resultLocal)...)
	return nil
}

// emitReturn lowers an ssa.Return to WASM.
// Emits explicit return opcode required inside if/else and loop blocks.
// Supports both single and multi-value returns.
func (e *Emitter) emitReturn(v *ssa.Return) error {
	if len(v.Results) == 0 {
		e.appendCode(OpcodeReturn)
		return nil
	}
	for _, result := range v.Results {
		if err := e.emitValuePush(result); err != nil {
			e.firstErr = fmt.Errorf("Return result: %w", err)
			return e.firstErr
		}
	}
	e.appendCode(OpcodeReturn)
	return nil
}

// emitIf lowers an ssa.If to WASM: pushes the condition only.
// The OpcodeIf (or br_if for loops) is emitted by assembleFunction, which
// owns all structured control flow opcodes.
func (e *Emitter) emitIf(v *ssa.If) error {
	if err := e.emitValuePush(v.Cond); err != nil {
		e.firstErr = fmt.Errorf("If cond: %w", err)
		return e.firstErr
	}
	return nil
}

// emitCall lowers an ssa.Call to WASM.
// Phase 1: static callees only (no dynamic dispatch).
func (e *Emitter) emitCall(v *ssa.Call) error {
	callee := v.Call.StaticCallee()
	if callee == nil {
		e.firstErr = fmt.Errorf("%w: dynamic call not supported in phase 1", ErrUnsupportedInstruction)
		return e.firstErr
	}
	idx, ok := e.funcIndex[callee]
	if !ok {
		e.firstErr = fmt.Errorf("%w: callee %s not in funcIndex (call AssignFunctionIndices first)", ErrUnsupportedInstruction, callee.Name())
		return e.firstErr
	}

	// Push all arguments.
	for _, arg := range v.Call.Args {
		if err := e.emitValuePush(arg); err != nil {
			e.firstErr = fmt.Errorf("call arg: %w", err)
			return e.firstErr
		}
	}

	e.appendCode(OpcodeCall)
	e.appendCode(uleb128(idx)...)

	// Handle result(s).
	if tuple, ok := v.Type().(*types.Tuple); ok && tuple.Len() > 1 {
		// Multi-value result: allocate one local per result, pop in reverse order.
		resultLocals := make([]uint32, tuple.Len())
		for i := 0; i < tuple.Len(); i++ {
			wt, werr := goTypeToWASM(tuple.At(i).Type())
			if werr != nil {
				e.firstErr = fmt.Errorf("call result %d type: %w", i, werr)
				return e.firstErr
			}
			local := e.current.nextLocal
			resultLocals[i] = local
			e.current.nextLocal++
			e.current.localTypes = append(e.current.localTypes, wt)
		}
		e.current.tupleLocals[v] = resultLocals
		// WASM stack has [r0, r1, ..., rN-1] with rN-1 on top; pop in reverse.
		for i := tuple.Len() - 1; i >= 0; i-- {
			e.appendCode(OpcodeLocalSet)
			e.appendCode(uleb128(resultLocals[i])...)
		}
	} else if !isVoidCall(v) {
		// Single-value result.
		wt, err := goTypeToWASM(v.Type())
		if err != nil {
			e.firstErr = fmt.Errorf("call result type: %w", err)
			return e.firstErr
		}
		resultLocal := e.current.nextLocal
		e.current.locals[v] = resultLocal
		e.current.nextLocal++
		e.current.localTypes = append(e.current.localTypes, wt)
		e.appendCode(OpcodeLocalSet)
		e.appendCode(uleb128(resultLocal)...)
	}
	return nil
}

// emitAlloc lowers an ssa.Alloc to a call to the internal __alloc bump allocator.
// Sets e.hasMemory so Bytes() emits the Memory and Global sections.
func (e *Emitter) emitAlloc(v *ssa.Alloc) error {
	e.hasMemory = true

	ptrType, ok := v.Type().Underlying().(*types.Pointer)
	if !ok {
		e.firstErr = fmt.Errorf("%w: Alloc type %s is not a pointer", ErrUnsupportedInstruction, v.Type())
		return e.firstErr
	}
	elem := ptrType.Elem()
	sizes := types.StdSizes{WordSize: 8, MaxAlign: 8}
	size := sizes.Sizeof(elem)
	alignedSize := (size + 7) &^ 7

	// Call __alloc(alignedSize) → addr (i32)
	e.appendCode(OpcodeI32Const)
	e.appendCode(sleb128(int64(alignedSize))...)
	e.appendCode(OpcodeCall)
	e.appendCode(0x00) // __alloc is always at function index 0

	resultLocal := e.current.nextLocal
	e.current.locals[v] = resultLocal
	e.current.nextLocal++
	e.current.localTypes = append(e.current.localTypes, ValTypeI32)
	e.appendCode(OpcodeLocalSet)
	e.appendCode(uleb128(resultLocal)...)
	return nil
}

// emitFieldAddr lowers an ssa.FieldAddr to an i32 address computation:
// result = base_addr + field_offset.
func (e *Emitter) emitFieldAddr(v *ssa.FieldAddr) error {
	ptrType, ok := v.X.Type().Underlying().(*types.Pointer)
	if !ok {
		e.firstErr = fmt.Errorf("%w: FieldAddr base type %s is not a pointer", ErrUnsupportedInstruction, v.X.Type())
		return e.firstErr
	}
	structType, ok := ptrType.Elem().Underlying().(*types.Struct)
	if !ok {
		e.firstErr = fmt.Errorf("%w: FieldAddr pointee %s is not a struct", ErrUnsupportedInstruction, ptrType.Elem())
		return e.firstErr
	}

	sizes := types.StdSizes{WordSize: 8, MaxAlign: 8}
	fields := make([]*types.Var, structType.NumFields())
	for i := 0; i < structType.NumFields(); i++ {
		fields[i] = structType.Field(i)
	}
	offsets := sizes.Offsetsof(fields)
	fieldOffset := offsets[v.Field]

	if err := e.emitValuePush(v.X); err != nil {
		return fmt.Errorf("FieldAddr base: %w", err)
	}
	if fieldOffset != 0 {
		e.appendCode(OpcodeI32Const)
		e.appendCode(sleb128(fieldOffset)...)
		e.appendCode(OpcodeI32Add)
	}

	resultLocal := e.current.nextLocal
	e.current.locals[v] = resultLocal
	e.current.nextLocal++
	e.current.localTypes = append(e.current.localTypes, ValTypeI32)
	e.appendCode(OpcodeLocalSet)
	e.appendCode(uleb128(resultLocal)...)
	return nil
}

// emitLoad lowers a pointer dereference (ssa.UnOp{token.MUL}) to a WASM load.
func (e *Emitter) emitLoad(v *ssa.UnOp) error {
	loadType := v.Type()
	wt, err := goTypeToWASM(loadType)
	if err != nil {
		return fmt.Errorf("load result type: %w", err)
	}
	opcode, err := loadOpcodeFor(loadType)
	if err != nil {
		return err
	}
	if err := e.emitValuePush(v.X); err != nil {
		return fmt.Errorf("load addr: %w", err)
	}
	e.appendCode(opcode)
	e.appendCode(memarg(0, 0)...)

	resultLocal := e.current.nextLocal
	e.current.locals[v] = resultLocal
	e.current.nextLocal++
	e.current.localTypes = append(e.current.localTypes, wt)
	e.appendCode(OpcodeLocalSet)
	e.appendCode(uleb128(resultLocal)...)
	return nil
}

// loadOpcodeFor returns the WASM load opcode for the given Go type.
func loadOpcodeFor(t types.Type) (byte, error) {
	basic, ok := t.Underlying().(*types.Basic)
	if !ok {
		return 0, fmt.Errorf("%w: non-basic load type %s", ErrUnsupportedInstruction, t)
	}
	switch basic.Kind() {
	case types.Int, types.Int64, types.Uint, types.Uint64, types.Uintptr:
		return OpcodeI64Load, nil
	case types.Int8:
		return OpcodeI64Load8S, nil
	case types.Uint8:
		return OpcodeI64Load8U, nil
	case types.Int16:
		return OpcodeI64Load16S, nil
	case types.Uint16:
		return OpcodeI64Load16U, nil
	case types.Int32:
		return OpcodeI64Load32S, nil
	case types.Uint32:
		return OpcodeI64Load32U, nil
	case types.Float64:
		return OpcodeF64Load, nil
	case types.Float32:
		return OpcodeF32Load, nil
	default:
		return 0, fmt.Errorf("%w: unsupported load type %s", ErrUnsupportedInstruction, basic.Name())
	}
}

// emitStore lowers an ssa.Store to a WASM store instruction.
// Store has no result value, so no local is allocated.
func (e *Emitter) emitStore(v *ssa.Store) error {
	opcode, err := storeOpcodeFor(v.Val.Type())
	if err != nil {
		return err
	}
	if err := e.emitValuePush(v.Addr); err != nil {
		return fmt.Errorf("store addr: %w", err)
	}
	if err := e.emitValuePush(v.Val); err != nil {
		return fmt.Errorf("store value: %w", err)
	}
	e.appendCode(opcode)
	e.appendCode(memarg(0, 0)...)
	return nil
}

// storeOpcodeFor returns the WASM store opcode for the given Go value type.
func storeOpcodeFor(t types.Type) (byte, error) {
	basic, ok := t.Underlying().(*types.Basic)
	if !ok {
		return 0, fmt.Errorf("%w: non-basic store type %s", ErrUnsupportedInstruction, t)
	}
	switch basic.Kind() {
	case types.Int, types.Int64, types.Uint, types.Uint64, types.Uintptr:
		return OpcodeI64Store, nil
	case types.Int8, types.Uint8:
		return OpcodeI64Store8, nil
	case types.Int16, types.Uint16:
		return OpcodeI64Store16, nil
	case types.Int32, types.Uint32:
		return OpcodeI64Store32, nil
	case types.Float64:
		return OpcodeF64Store, nil
	case types.Float32:
		return OpcodeF32Store, nil
	default:
		return 0, fmt.Errorf("%w: unsupported store type %s", ErrUnsupportedInstruction, basic.Name())
	}
}

// emitConvert lowers an ssa.Convert to WASM type conversion opcodes.
func (e *Emitter) emitConvert(v *ssa.Convert) error {
	dstWASM, err := goTypeToWASM(v.Type())
	if err != nil {
		return fmt.Errorf("convert dst type: %w", err)
	}

	resultLocal := e.current.nextLocal
	e.current.locals[v] = resultLocal
	e.current.nextLocal++
	e.current.localTypes = append(e.current.localTypes, dstWASM)

	if err := e.emitValuePush(v.X); err != nil {
		return fmt.Errorf("convert src: %w", err)
	}

	opcode, err := conversionOpcode(v.X.Type(), v.Type())
	if err != nil {
		return err
	}
	if opcode != 0 {
		e.appendCode(opcode)
	}

	e.appendCode(OpcodeLocalSet)
	e.appendCode(uleb128(resultLocal)...)
	return nil
}

// conversionOpcode returns the WASM opcode to convert from src to dst,
// or 0 if no opcode is needed (same WASM storage class).
func conversionOpcode(src, dst types.Type) (byte, error) {
	srcBasic, srcOK := src.Underlying().(*types.Basic)
	dstBasic, dstOK := dst.Underlying().(*types.Basic)
	if !srcOK || !dstOK {
		return 0, fmt.Errorf("%w: non-basic convert %s → %s", ErrUnsupportedInstruction, src, dst)
	}
	sk, dk := srcBasic.Kind(), dstBasic.Kind()

	isFloat := func(k types.BasicKind) bool {
		return k == types.Float32 || k == types.Float64
	}
	isInt := func(k types.BasicKind) bool { return !isFloat(k) }

	switch {
	case isInt(sk) && isInt(dk):
		// All integers are stored as i64 — no conversion opcode needed.
		return 0, nil
	case isInt(sk) && dk == types.Float64:
		return OpcodeF64ConvertI64S, nil
	case isInt(sk) && dk == types.Float32:
		return OpcodeF32ConvertI64S, nil
	case sk == types.Float64 && isInt(dk):
		return OpcodeI64TruncF64S, nil
	case sk == types.Float32 && isInt(dk):
		return OpcodeI64TruncF32S, nil
	case sk == types.Float32 && dk == types.Float64:
		return OpcodeF64PromoteF32, nil
	case sk == types.Float64 && dk == types.Float32:
		return OpcodeF32DemoteF64, nil
	default:
		return 0, nil // same type; no-op
	}
}

// emitExtract unpacks one element from a multi-value call result.
func (e *Emitter) emitExtract(v *ssa.Extract) error {
	tupleLocals, ok := e.current.tupleLocals[v.Tuple]
	if !ok {
		e.firstErr = fmt.Errorf("%w: Extract from %s has no tuple locals (not a multi-value call?)", ErrUnsupportedInstruction, v.Tuple.Name())
		return e.firstErr
	}
	if v.Index >= len(tupleLocals) {
		e.firstErr = fmt.Errorf("%w: Extract index %d out of range for tuple of %d", ErrUnsupportedInstruction, v.Index, len(tupleLocals))
		return e.firstErr
	}
	srcLocal := tupleLocals[v.Index]
	wt, err := goTypeToWASM(v.Type())
	if err != nil {
		return fmt.Errorf("extract type: %w", err)
	}

	resultLocal := e.current.nextLocal
	e.current.locals[v] = resultLocal
	e.current.nextLocal++
	e.current.localTypes = append(e.current.localTypes, wt)

	e.appendCode(OpcodeLocalGet)
	e.appendCode(uleb128(srcLocal)...)
	e.appendCode(OpcodeLocalSet)
	e.appendCode(uleb128(resultLocal)...)
	return nil
}

// emitIndexAddr lowers an ssa.IndexAddr to a base + index*elemSize i32 computation.
func (e *Emitter) emitIndexAddr(v *ssa.IndexAddr) error {
	ptrType, ok := v.Type().Underlying().(*types.Pointer)
	if !ok {
		e.firstErr = fmt.Errorf("%w: IndexAddr result type %s is not a pointer", ErrUnsupportedInstruction, v.Type())
		return e.firstErr
	}
	elemType := ptrType.Elem()
	sizes := types.StdSizes{WordSize: 8, MaxAlign: 8}
	elemSize := sizes.Sizeof(elemType)

	if err := e.emitValuePush(v.X); err != nil {
		return fmt.Errorf("IndexAddr base: %w", err)
	}
	if err := e.emitValuePush(v.Index); err != nil {
		return fmt.Errorf("IndexAddr index: %w", err)
	}
	// Convert i64 index to i32 for address arithmetic.
	e.appendCode(OpcodeI32WrapI64)
	e.appendCode(OpcodeI32Const)
	e.appendCode(sleb128(elemSize)...)
	e.appendCode(OpcodeI32Mul)
	e.appendCode(OpcodeI32Add)

	resultLocal := e.current.nextLocal
	e.current.locals[v] = resultLocal
	e.current.nextLocal++
	e.current.localTypes = append(e.current.localTypes, ValTypeI32)
	e.appendCode(OpcodeLocalSet)
	e.appendCode(uleb128(resultLocal)...)
	return nil
}

// emitPhiAssignments emits Phi node assignments for all successors of pred,
// writing into pred's code buffer. Called from ExitBlock.
func (e *Emitter) emitPhiAssignments(pred *ssa.BasicBlock) error {
	for _, succ := range pred.Succs {
		// Find which predecessor index this pred is in succ.
		predIdx := -1
		for i, p := range succ.Preds {
			if p == pred {
				predIdx = i
				break
			}
		}
		if predIdx < 0 {
			continue
		}
		for _, instr := range succ.Instrs {
			phi, ok := instr.(*ssa.Phi)
			if !ok {
				break // Phis are always at block start; stop on first non-Phi
			}
			edgeVal := phi.Edges[predIdx]
			phiLocal := e.current.phiLocals[phi]

			// Emit into pred's block buffer (currentBlock already = pred from ExitBlock).
			if err := e.emitValuePush(edgeVal); err != nil {
				return fmt.Errorf("phi edge value: %w", err)
			}
			e.appendCode(OpcodeLocalSet)
			e.appendCode(uleb128(phiLocal)...)
		}
	}
	return nil
}

// emitValuePush emits code to push an SSA value onto the WASM stack.
// For constants, emits an inline const instruction.
// For SSA values with assigned locals, emits local.get.
func (e *Emitter) emitValuePush(v ssa.Value) error {
	if c, ok := v.(*ssa.Const); ok {
		return e.emitConst(c)
	}
	idx, err := e.localIndex(v)
	if err != nil {
		return err
	}
	e.appendCode(OpcodeLocalGet)
	e.appendCode(uleb128(idx)...)
	return nil
}

// emitConst emits an inline WASM constant instruction for an ssa.Const.
func (e *Emitter) emitConst(c *ssa.Const) error {
	wt, err := goTypeToWASM(c.Type())
	if err != nil {
		return fmt.Errorf("const type: %w", err)
	}
	switch wt {
	case ValTypeI64:
		val, _ := constant.Int64Val(c.Value)
		e.appendCode(OpcodeI64Const)
		e.appendCode(sleb128(val)...)
	case ValTypeI32:
		val, _ := constant.Int64Val(c.Value)
		e.appendCode(OpcodeI32Const)
		e.appendCode(sleb128(val)...)
	default:
		return fmt.Errorf("%w: const of WASM type 0x%02X not supported", ErrUnsupportedInstruction, wt)
	}
	return nil
}

// assembleFunction assembles per-block code buffers into the final function body.
// It detects back edges (loop headers) and dispatches to assembleSimpleLoop,
// or falls back to assembleIfElse / linear DomPreorder concatenation.
func (e *Emitter) assembleFunction(fn *ssa.Function) ([]byte, error) {
	if len(fn.Blocks) == 1 {
		return e.current.blockCode[0], nil
	}

	// Detect back edges: a successor that dominates its predecessor.
	type backEdge struct{ body, header *ssa.BasicBlock }
	var backEdges []backEdge
	for _, block := range fn.Blocks {
		for _, succ := range block.Succs {
			if succ.Dominates(block) {
				backEdges = append(backEdges, backEdge{block, succ})
			}
		}
	}

	if len(backEdges) == 1 {
		return e.assembleSimpleLoop(fn, backEdges[0].body, backEdges[0].header)
	}
	if len(backEdges) > 1 {
		return nil, fmt.Errorf("%w: nested/multiple loops not supported in phase 2", ErrUnsupportedInstruction)
	}

	// No back edges: check entry terminator.
	entry := fn.Blocks[0]
	if len(entry.Instrs) == 0 {
		return nil, fmt.Errorf("empty entry block in %s", fn.Name())
	}
	last := entry.Instrs[len(entry.Instrs)-1]
	if _, ok := last.(*ssa.If); ok {
		return e.assembleIfElse(fn, entry)
	}

	// Linear fallback: concatenate block codes in DomPreorder order.
	var code []byte
	for _, block := range fn.DomPreorder() {
		code = append(code, e.current.blockCode[block.Index]...)
	}
	return code, nil
}

// assembleSimpleLoop assembles a single-level loop:
//
//	entry (init) → block { loop { header(cond) → body → back } } → exit
//
// body is the block with the back edge to header.
// The loop uses WASM block/loop/br_if/br structured control flow.
func (e *Emitter) assembleSimpleLoop(fn *ssa.Function, body, header *ssa.BasicBlock) ([]byte, error) {
	entry := fn.Blocks[0]

	// Find exit block: header's successor that is NOT the loop body.
	var exitBlock *ssa.BasicBlock
	for _, s := range header.Succs {
		if s != body {
			exitBlock = s
			break
		}
	}
	if exitBlock == nil {
		return nil, fmt.Errorf("loop header has no exit block in %s", fn.Name())
	}

	// Succs[0] of an If-terminated block is the "true" branch.
	// If Succs[0] == body, then condition true = continue looping.
	trueIsBody := len(header.Succs) > 0 && header.Succs[0] == body

	var code []byte
	// Entry block: init code + initial Phi assignments (from ExitBlock(entry)).
	code = append(code, e.current.blockCode[entry.Index]...)

	code = append(code, OpcodeBlock, BlockTypeEmpty) // outer block (br depth 1 = exit)
	code = append(code, OpcodeLoop, BlockTypeEmpty)  // inner loop  (br depth 0 = continue)

	// Header: push loop condition (emitIf no longer emits OpcodeIf).
	code = append(code, e.current.blockCode[header.Index]...)

	if trueIsBody {
		// Condition true = continue; we exit when false → negate, then br_if exit.
		code = append(code, OpcodeI32Eqz)
		code = append(code, OpcodeBrIf, 0x01) // br_if depth 1 (outer block)
	} else {
		// Condition true = exit.
		code = append(code, OpcodeBrIf, 0x01)
	}

	// Body block: body instructions + back-edge Phi assignments (from ExitBlock(body)).
	code = append(code, e.current.blockCode[body.Index]...)
	code = append(code, OpcodeBr, 0x00) // unconditional back to loop (depth 0)

	code = append(code, OpcodeEnd) // end loop
	code = append(code, OpcodeEnd) // end block

	// Exit block.
	code = append(code, e.current.blockCode[exitBlock.Index]...)
	return code, nil
}

// assembleIfElse assembles a simple if/else pattern:
// entry (with If terminator) → true block + false block (both must Return).
func (e *Emitter) assembleIfElse(fn *ssa.Function, entry *ssa.BasicBlock) ([]byte, error) {
	if len(entry.Succs) != 2 {
		return nil, fmt.Errorf("%w: If block must have exactly 2 successors in %s", ErrUnsupportedInstruction, fn.Name())
	}
	trueBlock := entry.Succs[0]
	falseBlock := entry.Succs[1]

	// Verify both successors return (simple if/else — no merge block in phase 1).
	if !blockReturns(trueBlock) || !blockReturns(falseBlock) {
		return nil, fmt.Errorf("%w: if/else branches must both return in %s (merge blocks not yet supported)", ErrUnsupportedInstruction, fn.Name())
	}

	var code []byte
	// Entry block code ends with: push cond (emitIf no longer emits OpcodeIf)
	code = append(code, e.current.blockCode[entry.Index]...)
	code = append(code, OpcodeIf, BlockTypeEmpty)
	// True branch
	code = append(code, e.current.blockCode[trueBlock.Index]...)
	// else
	code = append(code, OpcodeElse)
	// False branch
	code = append(code, e.current.blockCode[falseBlock.Index]...)
	// end
	code = append(code, OpcodeEnd)
	// Both branches return, so post-if code is unreachable.
	// OpcodeUnreachable keeps WASM validators happy.
	code = append(code, OpcodeUnreachable)
	return code, nil
}

// blockReturns reports whether a basic block terminates with a Return instruction.
func blockReturns(block *ssa.BasicBlock) bool {
	if len(block.Instrs) == 0 {
		return false
	}
	_, ok := block.Instrs[len(block.Instrs)-1].(*ssa.Return)
	return ok
}

// localIndex returns the WASM local index for an SSA value.
func (e *Emitter) localIndex(v ssa.Value) (uint32, error) {
	idx, ok := e.current.locals[v]
	if !ok {
		return 0, fmt.Errorf("%w: SSA value %s (%T) has no WASM local", ErrUnsupportedInstruction, v.Name(), v)
	}
	return idx, nil
}

// isVoidCall reports whether a Call instruction has a void return type.
func isVoidCall(v *ssa.Call) bool {
	t, ok := v.Type().(*types.Tuple)
	return ok && t.Len() == 0
}

// isComparisonOp reports whether op is a comparison operator.
func isComparisonOp(op token.Token) bool {
	switch op {
	case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
		return true
	}
	return false
}

// Bytes assembles and returns the complete WASM module binary.
// Call after WalkPackage completes successfully.
//
// When AssignFunctionIndices was called (hasAllocSlot), the __alloc internal
// function is always prepended at index 0. When hasMemory is also true, the
// Memory and Global sections are emitted so __alloc can operate.
func (e *Emitter) Bytes() ([]byte, error) {
	if e.firstErr != nil {
		return nil, e.firstErr
	}
	if len(e.functions) == 0 {
		return nil, fmt.Errorf("no functions emitted")
	}

	var (
		typeEntries [][]byte
		codeEntries [][]byte
		exportItems [][]byte
	)

	// indexOffset is 1 when __alloc occupies index 0; 0 otherwise.
	var indexOffset uint32
	if e.hasAllocSlot {
		indexOffset = 1
		// Prepend __alloc type: (i32) → i32
		typeEntries = append(typeEntries, funcType([]byte{ValTypeI32}, []byte{ValTypeI32}))
		// Prepend __alloc body
		codeEntries = append(codeEntries, allocFuncBody())
	}

	for i, fs := range e.functions {
		paramTypes, resultTypes, err := sigToWASMTypes(fs.sig)
		if err != nil {
			return nil, fmt.Errorf("function %s: %w", fs.name, err)
		}
		typeEntries = append(typeEntries, funcType(paramTypes, resultTypes))

		body := funcBody(fs.localTypes, fs.assembled)
		codeEntries = append(codeEntries, body)

		funcIdx := uint32(i) + indexOffset
		exportEntry := encodeName(fs.name)
		exportEntry = append(exportEntry, exportFunc)
		exportEntry = append(exportEntry, uleb128(funcIdx)...)
		exportItems = append(exportItems, exportEntry)
	}

	typeSection := section(sectionType, vec(typeEntries))

	// Function section: maps func index → type index.
	// Each function maps to its own unique type (one type per function).
	var funcTypeIndices [][]byte
	for i := range typeEntries {
		funcTypeIndices = append(funcTypeIndices, uleb128(uint32(i)))
	}
	funcSection := section(sectionFunction, vec(funcTypeIndices))
	exportSection := section(sectionExport, vec(exportItems))
	codeSection := section(sectionCode, vec(codeEntries))

	var module []byte
	module = append(module, wasmMagic...)
	module = append(module, typeSection...)
	module = append(module, funcSection...)
	// Emit Memory and Global sections whenever __alloc is present (hasAllocSlot),
	// because __alloc's body references global 0 (heap_ptr) and must be valid WASM
	// even when not called. hasMemory is a subset: alloc is present iff hasAllocSlot.
	if e.hasAllocSlot {
		module = append(module, memorySection(1)...)    // 1 page = 64 KB
		module = append(module, globalSection(4096)...) // heap starts at 4 KB
	}
	module = append(module, exportSection...)
	module = append(module, codeSection...)

	return module, nil
}

// goTypeToWASM maps a Go types.Type to a WASM value type byte.
// Pointer types → i32 (WASM memory addresses are 32-bit).
// All integer types → i64; bool → i32 (comparison results); float32 → f32; float64 → f64.
func goTypeToWASM(t types.Type) (byte, error) {
	// Pointer types map to i32 (WASM linear memory addresses are 32-bit).
	if _, ok := t.Underlying().(*types.Pointer); ok {
		return ValTypeI32, nil
	}
	basic, ok := t.Underlying().(*types.Basic)
	if !ok {
		return 0, fmt.Errorf("non-basic type %s not supported", t)
	}
	switch basic.Kind() {
	case types.Bool:
		return ValTypeI32, nil
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64,
		types.Uintptr:
		return ValTypeI64, nil
	case types.Float32:
		return ValTypeF32, nil
	case types.Float64:
		return ValTypeF64, nil
	default:
		return 0, fmt.Errorf("Go type %s (kind %d) not supported", basic.Name(), basic.Kind())
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

// tokenToOpcode maps a binary operator to the WASM opcode for the given value type.
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
	// Comparisons (produce i32 result, but opcode is in the i64 family)
	case token.EQL:
		return OpcodeI64Eq, nil
	case token.NEQ:
		return OpcodeI64Ne, nil
	case token.LSS:
		return OpcodeI64LtS, nil
	case token.GTR:
		return OpcodeI64GtS, nil
	case token.LEQ:
		return OpcodeI64LeS, nil
	case token.GEQ:
		return OpcodeI64GeS, nil
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
	// Comparisons
	case token.EQL:
		return OpcodeI32Eq, nil
	case token.NEQ:
		return OpcodeI32Ne, nil
	case token.LSS:
		return OpcodeI32LtS, nil
	case token.GTR:
		return OpcodeI32GtS, nil
	case token.LEQ:
		return OpcodeI32LeS, nil
	case token.GEQ:
		return OpcodeI32GeS, nil
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
