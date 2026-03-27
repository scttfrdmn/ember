package runtime

import (
	"fmt"
	"math"
	"math/bits"
)

// label tracks an active structured control flow entry (block/loop/if/else).
type label struct {
	kind     byte // 0x02=block, 0x03=loop, 0x04=if, 0x05=else
	instrIdx int  // index of the block/loop/if/else instruction
	endIdx   int  // for block/if/else: matching end index; for loop: instrIdx (br re-enters body)
}

// frame is a single activation record on the call stack.
type frame struct {
	funcIdx    uint32
	instrs     []Instr
	locals     []uint64
	pc         int
	labelStack []label
}

// Instance is a live instantiation of a Module, ready to execute.
type Instance struct {
	mod     *Module
	mem     *linearMemory
	globals []uint64
}

// Instantiate creates a new Instance from a compiled Module.
func (m *Module) Instantiate() *Instance {
	inst := &Instance{mod: m}
	if m.hasMemory {
		inst.mem = newLinearMemory(m.memPages)
	}
	if m.hasGlobal {
		inst.globals = []uint64{uint64(m.heapStart)}
	}
	return inst
}

// Call invokes an exported function by name with the given arguments.
func (inst *Instance) Call(name string, args ...uint64) ([]uint64, error) {
	var funcIdx uint32
	found := false
	for _, e := range inst.mod.exports {
		if e.name == name {
			funcIdx = e.funcIdx
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: %q", ErrFunctionNotFound, name)
	}

	body := &inst.mod.bodies[funcIdx]
	ft := inst.mod.types[inst.mod.funcTypes[funcIdx]]
	locals := make([]uint64, len(ft.params)+len(body.localTypes))
	copy(locals, args)

	var valStack []uint64
	callStack := []frame{{
		funcIdx: funcIdx,
		instrs:  body.instrs,
		locals:  locals,
	}}

	return inst.run(&callStack, &valStack)
}

func (inst *Instance) run(callStack *[]frame, valStack *[]uint64) ([]uint64, error) {
	for len(*callStack) > 0 {
		f := &(*callStack)[len(*callStack)-1]

		if f.pc >= len(f.instrs) {
			// Implicit function return at end of body
			ft := inst.mod.types[inst.mod.funcTypes[f.funcIdx]]
			results := popN(valStack, len(ft.results))
			*callStack = (*callStack)[:len(*callStack)-1]
			if len(*callStack) == 0 {
				return results, nil
			}
			for _, r := range results {
				push(valStack, r)
			}
			continue
		}

		instr := f.instrs[f.pc]
		f.pc++

		switch instr.Op {
		case 0x00: // unreachable
			return nil, fmt.Errorf("%w: unreachable executed", ErrMalformedBinary)

		case 0x01: // nop
			// nothing

		// --- Constants ---
		case 0x41: // i32.const
			push(valStack, uint64(uint32(int32(instr.Imm1))))
		case 0x42: // i64.const
			push(valStack, uint64(instr.Imm1))

		// --- Locals ---
		case 0x20: // local.get
			push(valStack, f.locals[instr.Imm1])
		case 0x21: // local.set
			f.locals[instr.Imm1] = pop(valStack)
		case 0x22: // local.tee
			f.locals[instr.Imm1] = peek(valStack)

		// --- Globals ---
		case 0x23: // global.get
			push(valStack, inst.globals[instr.Imm1])
		case 0x24: // global.set
			inst.globals[instr.Imm1] = pop(valStack)

		// --- Control flow ---
		case 0x02: // block
			f.labelStack = append(f.labelStack, label{
				kind:     0x02,
				instrIdx: f.pc - 1,
				endIdx:   instr.Partner,
			})

		case 0x03: // loop
			f.labelStack = append(f.labelStack, label{
				kind:     0x03,
				instrIdx: f.pc - 1,
				endIdx:   f.pc - 1, // br re-enters body at instrIdx+1
			})

		case 0x04: // if
			cond := pop(valStack)
			if cond != 0 {
				f.labelStack = append(f.labelStack, label{
					kind:     0x04,
					instrIdx: f.pc - 1,
					endIdx:   instr.Partner,
				})
			} else {
				target := instr.Partner // = else or end index
				if target >= 0 && target < len(f.instrs) && f.instrs[target].Op == 0x05 {
					// else exists: jump to else body
					f.pc = target + 1
					f.labelStack = append(f.labelStack, label{
						kind:     0x05,
						instrIdx: target,
						endIdx:   f.instrs[target].Partner,
					})
				} else {
					// no else: skip past end
					f.pc = target + 1
				}
			}

		case 0x05: // else — reached when true-branch falls through
			top := f.labelStack[len(f.labelStack)-1]
			f.pc = top.endIdx + 1 // jump past end
			f.labelStack = f.labelStack[:len(f.labelStack)-1]

		case 0x0B: // end
			if len(f.labelStack) == 0 {
				// Function-level end → implicit return
				ft := inst.mod.types[inst.mod.funcTypes[f.funcIdx]]
				results := popN(valStack, len(ft.results))
				*callStack = (*callStack)[:len(*callStack)-1]
				if len(*callStack) == 0 {
					return results, nil
				}
				for _, r := range results {
					push(valStack, r)
				}
				// Reset f to point to new top of call stack
				continue
			}
			f.labelStack = f.labelStack[:len(f.labelStack)-1]

		case 0x0C: // br
			inst.doBr(f, int(instr.Imm1))

		case 0x0D: // br_if
			if pop(valStack) != 0 {
				inst.doBr(f, int(instr.Imm1))
			}

		case 0x0F: // return
			ft := inst.mod.types[inst.mod.funcTypes[f.funcIdx]]
			results := popN(valStack, len(ft.results))
			f.labelStack = nil
			*callStack = (*callStack)[:len(*callStack)-1]
			if len(*callStack) == 0 {
				return results, nil
			}
			for _, r := range results {
				push(valStack, r)
			}
			continue

		case 0x10: // call
			calleeIdx := uint32(instr.Imm1)
			if int(calleeIdx) >= len(inst.mod.bodies) {
				return nil, fmt.Errorf("%w: call to function index %d out of range", ErrMalformedBinary, calleeIdx)
			}
			callBody := &inst.mod.bodies[calleeIdx]
			callFT := inst.mod.types[inst.mod.funcTypes[calleeIdx]]
			callArgs := popN(valStack, len(callFT.params))
			callLocals := make([]uint64, len(callFT.params)+len(callBody.localTypes))
			copy(callLocals, callArgs)
			*callStack = append(*callStack, frame{
				funcIdx: calleeIdx,
				instrs:  callBody.instrs,
				locals:  callLocals,
			})
			continue

		// --- Memory loads ---
		case 0x28: // i32.load
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			v, err := inst.mem.loadI32(addr)
			if err != nil {
				return nil, err
			}
			push(valStack, uint64(v))
		case 0x29: // i64.load
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			v, err := inst.mem.loadI64(addr)
			if err != nil {
				return nil, err
			}
			push(valStack, v)
		case 0x2A: // f32.load
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			v, err := inst.mem.loadF32(addr)
			if err != nil {
				return nil, err
			}
			push(valStack, uint64(v))
		case 0x2B: // f64.load
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			v, err := inst.mem.loadF64(addr)
			if err != nil {
				return nil, err
			}
			push(valStack, v)
		case 0x2C: // i32.load8_s
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			v, err := inst.mem.loadI32Load8S(addr)
			if err != nil {
				return nil, err
			}
			push(valStack, uint64(v))
		case 0x2D: // i32.load8_u
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			v, err := inst.mem.loadI32Load8U(addr)
			if err != nil {
				return nil, err
			}
			push(valStack, uint64(v))
		case 0x2E: // i32.load16_s
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			v, err := inst.mem.loadI32Load16S(addr)
			if err != nil {
				return nil, err
			}
			push(valStack, uint64(v))
		case 0x2F: // i32.load16_u
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			v, err := inst.mem.loadI32Load16U(addr)
			if err != nil {
				return nil, err
			}
			push(valStack, uint64(v))
		case 0x30: // i64.load8_s
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			v, err := inst.mem.loadI64Load8S(addr)
			if err != nil {
				return nil, err
			}
			push(valStack, v)
		case 0x31: // i64.load8_u
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			v, err := inst.mem.loadI64Load8U(addr)
			if err != nil {
				return nil, err
			}
			push(valStack, v)
		case 0x32: // i64.load16_s
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			v, err := inst.mem.loadI64Load16S(addr)
			if err != nil {
				return nil, err
			}
			push(valStack, v)
		case 0x33: // i64.load16_u
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			v, err := inst.mem.loadI64Load16U(addr)
			if err != nil {
				return nil, err
			}
			push(valStack, v)
		case 0x34: // i64.load32_s
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			v, err := inst.mem.loadI64Load32S(addr)
			if err != nil {
				return nil, err
			}
			push(valStack, v)
		case 0x35: // i64.load32_u
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			v, err := inst.mem.loadI64Load32U(addr)
			if err != nil {
				return nil, err
			}
			push(valStack, v)

		// --- Memory stores ---
		case 0x36: // i32.store
			val := uint32(pop(valStack))
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			if err := inst.mem.storeI32(addr, val); err != nil {
				return nil, err
			}
		case 0x37: // i64.store
			val := pop(valStack)
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			if err := inst.mem.storeI64(addr, val); err != nil {
				return nil, err
			}
		case 0x38: // f32.store
			val := uint32(pop(valStack))
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			if err := inst.mem.storeF32(addr, val); err != nil {
				return nil, err
			}
		case 0x39: // f64.store
			val := pop(valStack)
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			if err := inst.mem.storeF64(addr, val); err != nil {
				return nil, err
			}
		case 0x3A: // i32.store8
			val := uint32(pop(valStack))
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			if err := inst.mem.storeI32Store8(addr, val); err != nil {
				return nil, err
			}
		case 0x3B: // i32.store16
			val := uint32(pop(valStack))
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			if err := inst.mem.storeI32Store16(addr, val); err != nil {
				return nil, err
			}
		case 0x3C: // i64.store8
			val := pop(valStack)
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			if err := inst.mem.storeI64Store8(addr, val); err != nil {
				return nil, err
			}
		case 0x3D: // i64.store16
			val := pop(valStack)
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			if err := inst.mem.storeI64Store16(addr, val); err != nil {
				return nil, err
			}
		case 0x3E: // i64.store32
			val := pop(valStack)
			addr := uint32(pop(valStack)) + uint32(instr.Imm2)
			if err := inst.mem.storeI64Store32(addr, val); err != nil {
				return nil, err
			}

		// --- i32 comparisons ---
		case 0x45: // i32.eqz
			a := int32(pop(valStack))
			pushBool(valStack, a == 0)
		case 0x46: // i32.eq
			b, a := int32(pop(valStack)), int32(pop(valStack))
			pushBool(valStack, a == b)
		case 0x47: // i32.ne
			b, a := int32(pop(valStack)), int32(pop(valStack))
			pushBool(valStack, a != b)
		case 0x48: // i32.lt_s
			b, a := int32(pop(valStack)), int32(pop(valStack))
			pushBool(valStack, a < b)
		case 0x49: // i32.lt_u
			b, a := uint32(pop(valStack)), uint32(pop(valStack))
			pushBool(valStack, a < b)
		case 0x4A: // i32.gt_s
			b, a := int32(pop(valStack)), int32(pop(valStack))
			pushBool(valStack, a > b)
		case 0x4B: // i32.gt_u
			b, a := uint32(pop(valStack)), uint32(pop(valStack))
			pushBool(valStack, a > b)
		case 0x4C: // i32.le_s
			b, a := int32(pop(valStack)), int32(pop(valStack))
			pushBool(valStack, a <= b)
		case 0x4D: // i32.le_u
			b, a := uint32(pop(valStack)), uint32(pop(valStack))
			pushBool(valStack, a <= b)
		case 0x4E: // i32.ge_s
			b, a := int32(pop(valStack)), int32(pop(valStack))
			pushBool(valStack, a >= b)
		case 0x4F: // i32.ge_u
			b, a := uint32(pop(valStack)), uint32(pop(valStack))
			pushBool(valStack, a >= b)

		// --- i64 comparisons ---
		case 0x50: // i64.eqz
			a := int64(pop(valStack))
			pushBool(valStack, a == 0)
		case 0x51: // i64.eq
			b, a := int64(pop(valStack)), int64(pop(valStack))
			pushBool(valStack, a == b)
		case 0x52: // i64.ne
			b, a := int64(pop(valStack)), int64(pop(valStack))
			pushBool(valStack, a != b)
		case 0x53: // i64.lt_s
			b, a := int64(pop(valStack)), int64(pop(valStack))
			pushBool(valStack, a < b)
		case 0x54: // i64.lt_u
			b, a := pop(valStack), pop(valStack)
			pushBool(valStack, a < b)
		case 0x55: // i64.gt_s
			b, a := int64(pop(valStack)), int64(pop(valStack))
			pushBool(valStack, a > b)
		case 0x56: // i64.gt_u
			b, a := pop(valStack), pop(valStack)
			pushBool(valStack, a > b)
		case 0x57: // i64.le_s
			b, a := int64(pop(valStack)), int64(pop(valStack))
			pushBool(valStack, a <= b)
		case 0x58: // i64.le_u
			b, a := pop(valStack), pop(valStack)
			pushBool(valStack, a <= b)
		case 0x59: // i64.ge_s
			b, a := int64(pop(valStack)), int64(pop(valStack))
			pushBool(valStack, a >= b)
		case 0x5A: // i64.ge_u
			b, a := pop(valStack), pop(valStack)
			pushBool(valStack, a >= b)

		// --- f32 comparisons ---
		case 0x5B: // f32.eq
			b := math.Float32frombits(uint32(pop(valStack)))
			a := math.Float32frombits(uint32(pop(valStack)))
			pushBool(valStack, a == b)
		case 0x5C: // f32.ne
			b := math.Float32frombits(uint32(pop(valStack)))
			a := math.Float32frombits(uint32(pop(valStack)))
			pushBool(valStack, a != b)
		case 0x5D: // f32.lt
			b := math.Float32frombits(uint32(pop(valStack)))
			a := math.Float32frombits(uint32(pop(valStack)))
			pushBool(valStack, a < b)
		case 0x5E: // f32.gt
			b := math.Float32frombits(uint32(pop(valStack)))
			a := math.Float32frombits(uint32(pop(valStack)))
			pushBool(valStack, a > b)
		case 0x5F: // f32.le
			b := math.Float32frombits(uint32(pop(valStack)))
			a := math.Float32frombits(uint32(pop(valStack)))
			pushBool(valStack, a <= b)
		case 0x60: // f32.ge
			b := math.Float32frombits(uint32(pop(valStack)))
			a := math.Float32frombits(uint32(pop(valStack)))
			pushBool(valStack, a >= b)

		// --- f64 comparisons ---
		case 0x61: // f64.eq
			b := math.Float64frombits(pop(valStack))
			a := math.Float64frombits(pop(valStack))
			pushBool(valStack, a == b)
		case 0x62: // f64.ne
			b := math.Float64frombits(pop(valStack))
			a := math.Float64frombits(pop(valStack))
			pushBool(valStack, a != b)
		case 0x63: // f64.lt
			b := math.Float64frombits(pop(valStack))
			a := math.Float64frombits(pop(valStack))
			pushBool(valStack, a < b)
		case 0x64: // f64.gt
			b := math.Float64frombits(pop(valStack))
			a := math.Float64frombits(pop(valStack))
			pushBool(valStack, a > b)
		case 0x65: // f64.le
			b := math.Float64frombits(pop(valStack))
			a := math.Float64frombits(pop(valStack))
			pushBool(valStack, a <= b)
		case 0x66: // f64.ge
			b := math.Float64frombits(pop(valStack))
			a := math.Float64frombits(pop(valStack))
			pushBool(valStack, a >= b)

		// --- i32 arithmetic ---
		case 0x67: // i32.clz
			a := uint32(pop(valStack))
			push(valStack, uint64(bits.LeadingZeros32(a)))
		case 0x68: // i32.ctz
			a := uint32(pop(valStack))
			push(valStack, uint64(bits.TrailingZeros32(a)))
		case 0x69: // i32.popcnt
			a := uint32(pop(valStack))
			push(valStack, uint64(bits.OnesCount32(a)))
		case 0x6A: // i32.add
			b, a := int32(pop(valStack)), int32(pop(valStack))
			push(valStack, uint64(uint32(a+b)))
		case 0x6B: // i32.sub
			b, a := int32(pop(valStack)), int32(pop(valStack))
			push(valStack, uint64(uint32(a-b)))
		case 0x6C: // i32.mul
			b, a := int32(pop(valStack)), int32(pop(valStack))
			push(valStack, uint64(uint32(a*b)))
		case 0x6D: // i32.div_s
			b, a := int32(pop(valStack)), int32(pop(valStack))
			if b == 0 {
				return nil, ErrDivisionByZero
			}
			push(valStack, uint64(uint32(a/b)))
		case 0x6E: // i32.div_u
			b, a := uint32(pop(valStack)), uint32(pop(valStack))
			if b == 0 {
				return nil, ErrDivisionByZero
			}
			push(valStack, uint64(a/b))
		case 0x6F: // i32.rem_s
			b, a := int32(pop(valStack)), int32(pop(valStack))
			if b == 0 {
				return nil, ErrDivisionByZero
			}
			push(valStack, uint64(uint32(a%b)))
		case 0x70: // i32.rem_u
			b, a := uint32(pop(valStack)), uint32(pop(valStack))
			if b == 0 {
				return nil, ErrDivisionByZero
			}
			push(valStack, uint64(a%b))
		case 0x71: // i32.and
			b, a := uint32(pop(valStack)), uint32(pop(valStack))
			push(valStack, uint64(a&b))
		case 0x72: // i32.or
			b, a := uint32(pop(valStack)), uint32(pop(valStack))
			push(valStack, uint64(a|b))
		case 0x73: // i32.xor
			b, a := uint32(pop(valStack)), uint32(pop(valStack))
			push(valStack, uint64(a^b))
		case 0x74: // i32.shl
			b, a := uint32(pop(valStack)), uint32(pop(valStack))
			push(valStack, uint64(a<<(b&31)))
		case 0x75: // i32.shr_s
			b, a := uint32(pop(valStack)), int32(pop(valStack))
			push(valStack, uint64(uint32(a>>(b&31))))
		case 0x76: // i32.shr_u
			b, a := uint32(pop(valStack)), uint32(pop(valStack))
			push(valStack, uint64(a>>(b&31)))
		case 0x77: // i32.rotl
			b, a := uint32(pop(valStack)), uint32(pop(valStack))
			push(valStack, uint64(bits.RotateLeft32(a, int(b))))
		case 0x78: // i32.rotr
			b, a := uint32(pop(valStack)), uint32(pop(valStack))
			push(valStack, uint64(bits.RotateLeft32(a, -int(b))))

		// --- i64 arithmetic ---
		case 0x79: // i64.clz
			a := pop(valStack)
			push(valStack, uint64(bits.LeadingZeros64(a)))
		case 0x7A: // i64.ctz
			a := pop(valStack)
			push(valStack, uint64(bits.TrailingZeros64(a)))
		case 0x7B: // i64.popcnt
			a := pop(valStack)
			push(valStack, uint64(bits.OnesCount64(a)))
		case 0x7C: // i64.add
			b, a := int64(pop(valStack)), int64(pop(valStack))
			push(valStack, uint64(a+b))
		case 0x7D: // i64.sub
			b, a := int64(pop(valStack)), int64(pop(valStack))
			push(valStack, uint64(a-b))
		case 0x7E: // i64.mul
			b, a := int64(pop(valStack)), int64(pop(valStack))
			push(valStack, uint64(a*b))
		case 0x7F: // i64.div_s
			b, a := int64(pop(valStack)), int64(pop(valStack))
			if b == 0 {
				return nil, ErrDivisionByZero
			}
			push(valStack, uint64(a/b))
		case 0x80: // i64.div_u
			b, a := pop(valStack), pop(valStack)
			if b == 0 {
				return nil, ErrDivisionByZero
			}
			push(valStack, a/b)
		case 0x81: // i64.rem_s
			b, a := int64(pop(valStack)), int64(pop(valStack))
			if b == 0 {
				return nil, ErrDivisionByZero
			}
			push(valStack, uint64(a%b))
		case 0x82: // i64.rem_u
			b, a := pop(valStack), pop(valStack)
			if b == 0 {
				return nil, ErrDivisionByZero
			}
			push(valStack, a%b)
		case 0x83: // i64.and
			b, a := pop(valStack), pop(valStack)
			push(valStack, a&b)
		case 0x84: // i64.or
			b, a := pop(valStack), pop(valStack)
			push(valStack, a|b)
		case 0x85: // i64.xor
			b, a := pop(valStack), pop(valStack)
			push(valStack, a^b)
		case 0x86: // i64.shl
			b, a := pop(valStack), pop(valStack)
			push(valStack, a<<(b&63))
		case 0x87: // i64.shr_s
			b, a := pop(valStack), int64(pop(valStack))
			push(valStack, uint64(a>>(b&63)))
		case 0x88: // i64.shr_u
			b, a := pop(valStack), pop(valStack)
			push(valStack, a>>(b&63))
		case 0x89: // i64.rotl
			b, a := pop(valStack), pop(valStack)
			push(valStack, bits.RotateLeft64(a, int(b)))
		case 0x8A: // i64.rotr
			b, a := pop(valStack), pop(valStack)
			push(valStack, bits.RotateLeft64(a, -int(b)))

		// --- f32 arithmetic ---
		case 0x8B: // f32.abs
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(math.Float32bits(float32(math.Abs(float64(a))))))
		case 0x8C: // f32.neg
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(math.Float32bits(-a)))
		case 0x8D: // f32.ceil
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(math.Float32bits(float32(math.Ceil(float64(a))))))
		case 0x8E: // f32.floor
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(math.Float32bits(float32(math.Floor(float64(a))))))
		case 0x8F: // f32.trunc
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(math.Float32bits(float32(math.Trunc(float64(a))))))
		case 0x90: // f32.nearest
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(math.Float32bits(float32(math.RoundToEven(float64(a))))))
		case 0x91: // f32.sqrt
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(math.Float32bits(float32(math.Sqrt(float64(a))))))
		case 0x92: // f32.add
			b := math.Float32frombits(uint32(pop(valStack)))
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(math.Float32bits(a+b)))
		case 0x93: // f32.sub
			b := math.Float32frombits(uint32(pop(valStack)))
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(math.Float32bits(a-b)))
		case 0x94: // f32.mul
			b := math.Float32frombits(uint32(pop(valStack)))
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(math.Float32bits(a*b)))
		case 0x95: // f32.div
			b := math.Float32frombits(uint32(pop(valStack)))
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(math.Float32bits(a/b)))
		case 0x96: // f32.min
			b := math.Float32frombits(uint32(pop(valStack)))
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(math.Float32bits(float32(math.Min(float64(a), float64(b))))))
		case 0x97: // f32.max
			b := math.Float32frombits(uint32(pop(valStack)))
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(math.Float32bits(float32(math.Max(float64(a), float64(b))))))
		case 0x98: // f32.copysign
			b := math.Float32frombits(uint32(pop(valStack)))
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(math.Float32bits(float32(math.Copysign(float64(a), float64(b))))))

		// --- f64 arithmetic ---
		case 0x99: // f64.abs
			a := math.Float64frombits(pop(valStack))
			push(valStack, math.Float64bits(math.Abs(a)))
		case 0x9A: // f64.neg
			a := math.Float64frombits(pop(valStack))
			push(valStack, math.Float64bits(-a))
		case 0x9B: // f64.ceil
			a := math.Float64frombits(pop(valStack))
			push(valStack, math.Float64bits(math.Ceil(a)))
		case 0x9C: // f64.floor
			a := math.Float64frombits(pop(valStack))
			push(valStack, math.Float64bits(math.Floor(a)))
		case 0x9D: // f64.trunc
			a := math.Float64frombits(pop(valStack))
			push(valStack, math.Float64bits(math.Trunc(a)))
		case 0x9E: // f64.nearest
			a := math.Float64frombits(pop(valStack))
			push(valStack, math.Float64bits(math.RoundToEven(a)))
		case 0x9F: // f64.sqrt
			a := math.Float64frombits(pop(valStack))
			push(valStack, math.Float64bits(math.Sqrt(a)))
		case 0xA0: // f64.add
			b := math.Float64frombits(pop(valStack))
			a := math.Float64frombits(pop(valStack))
			push(valStack, math.Float64bits(a+b))
		case 0xA1: // f64.sub
			b := math.Float64frombits(pop(valStack))
			a := math.Float64frombits(pop(valStack))
			push(valStack, math.Float64bits(a-b))
		case 0xA2: // f64.mul
			b := math.Float64frombits(pop(valStack))
			a := math.Float64frombits(pop(valStack))
			push(valStack, math.Float64bits(a*b))
		case 0xA3: // f64.div
			b := math.Float64frombits(pop(valStack))
			a := math.Float64frombits(pop(valStack))
			push(valStack, math.Float64bits(a/b))
		case 0xA4: // f64.min
			b := math.Float64frombits(pop(valStack))
			a := math.Float64frombits(pop(valStack))
			push(valStack, math.Float64bits(math.Min(a, b)))
		case 0xA5: // f64.max
			b := math.Float64frombits(pop(valStack))
			a := math.Float64frombits(pop(valStack))
			push(valStack, math.Float64bits(math.Max(a, b)))
		case 0xA6: // f64.copysign
			b := math.Float64frombits(pop(valStack))
			a := math.Float64frombits(pop(valStack))
			push(valStack, math.Float64bits(math.Copysign(a, b)))

		// --- Type conversions ---
		case 0xA7: // i32.wrap_i64
			push(valStack, uint64(uint32(pop(valStack))))
		case 0xA8: // i32.trunc_f32_s
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(uint32(int32(a))))
		case 0xA9: // i32.trunc_f32_u
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(uint32(a)))
		case 0xAA: // i32.trunc_f64_s
			a := math.Float64frombits(pop(valStack))
			push(valStack, uint64(uint32(int32(a))))
		case 0xAB: // i32.trunc_f64_u
			a := math.Float64frombits(pop(valStack))
			push(valStack, uint64(uint32(a)))
		case 0xAC: // i64.extend_i32_s
			push(valStack, uint64(int64(int32(pop(valStack)))))
		case 0xAD: // i64.extend_i32_u
			push(valStack, uint64(uint32(pop(valStack))))
		case 0xAE: // i64.trunc_f32_s
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(int64(a)))
		case 0xAF: // i64.trunc_f32_u
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, uint64(a))
		case 0xB0: // i64.trunc_f64_s
			a := math.Float64frombits(pop(valStack))
			push(valStack, uint64(int64(a)))
		case 0xB1: // i64.trunc_f64_u
			a := math.Float64frombits(pop(valStack))
			push(valStack, uint64(a))
		case 0xB2: // f32.convert_i32_s
			a := int32(pop(valStack))
			push(valStack, uint64(math.Float32bits(float32(a))))
		case 0xB3: // f32.convert_i32_u
			a := uint32(pop(valStack))
			push(valStack, uint64(math.Float32bits(float32(a))))
		case 0xB4: // f32.convert_i64_s
			a := int64(pop(valStack))
			push(valStack, uint64(math.Float32bits(float32(a))))
		case 0xB5: // f32.convert_i64_u
			a := pop(valStack)
			push(valStack, uint64(math.Float32bits(float32(a))))
		case 0xB6: // f32.demote_f64
			a := math.Float64frombits(pop(valStack))
			push(valStack, uint64(math.Float32bits(float32(a))))
		case 0xB7: // f64.convert_i32_s
			a := int32(pop(valStack))
			push(valStack, math.Float64bits(float64(a)))
		case 0xB8: // f64.convert_i32_u
			a := uint32(pop(valStack))
			push(valStack, math.Float64bits(float64(a)))
		case 0xB9: // f64.convert_i64_s
			a := int64(pop(valStack))
			push(valStack, math.Float64bits(float64(a)))
		case 0xBA: // f64.convert_i64_u
			a := pop(valStack)
			push(valStack, math.Float64bits(float64(a)))
		case 0xBB: // f64.promote_f32
			a := math.Float32frombits(uint32(pop(valStack)))
			push(valStack, math.Float64bits(float64(a)))

		default:
			return nil, fmt.Errorf("%w: unhandled opcode 0x%02X at pc=%d", ErrMalformedBinary, instr.Op, f.pc-1)
		}
	}

	return nil, fmt.Errorf("%w: call stack exhausted without return", ErrMalformedBinary)
}

// doBr implements WASM br/br_if semantics.
// depth is the label stack depth to target (0 = innermost).
func (inst *Instance) doBr(f *frame, depth int) {
	targetIdx := len(f.labelStack) - 1 - depth
	lbl := f.labelStack[targetIdx]
	if lbl.kind == 0x03 { // loop: re-enter body
		f.pc = lbl.instrIdx + 1
		f.labelStack = f.labelStack[:targetIdx+1] // keep loop label
	} else { // block/if/else: exit past end
		f.pc = lbl.endIdx + 1
		f.labelStack = f.labelStack[:targetIdx]
	}
}

// Stack helpers.
func push(s *[]uint64, v uint64)   { *s = append(*s, v) }
func pop(s *[]uint64) uint64       { n := len(*s) - 1; v := (*s)[n]; *s = (*s)[:n]; return v }
func peek(s *[]uint64) uint64      { return (*s)[len(*s)-1] }
func pushBool(s *[]uint64, b bool) { if b { push(s, 1) } else { push(s, 0) } }

// popN pops n values from the stack and returns them in left-to-right order.
// (The top of the stack is the last/rightmost result.)
func popN(s *[]uint64, n int) []uint64 {
	results := make([]uint64, n)
	for i := n - 1; i >= 0; i-- {
		results[i] = pop(s)
	}
	return results
}
