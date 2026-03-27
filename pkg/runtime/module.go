// Package runtime implements a native WASM interpreter for pure-compute embers.
// It handles exactly the opcode subset emitted by pkg/emitter/wasm, with zero
// external dependencies.
package runtime

import (
	"errors"
	"fmt"
)

// Error sentinels.
var (
	ErrInvalidMagic       = errors.New("runtime: invalid WASM magic or version")
	ErrUnsupportedSection = errors.New("runtime: unsupported WASM section")
	ErrMalformedBinary    = errors.New("runtime: malformed WASM binary")
	ErrNotImplemented     = errors.New("runtime: I/O-capable embers require Phase 5 hearth")
	ErrFunctionNotFound   = errors.New("runtime: exported function not found")
	ErrDivisionByZero     = errors.New("runtime: integer division by zero")
	ErrMemoryOutOfBounds  = errors.New("runtime: memory access out of bounds")
)

type valType byte

const (
	valTypeI32 valType = 0x7F
	valTypeI64 valType = 0x7E
	valTypeF32 valType = 0x7D
	valTypeF64 valType = 0x7C
)

type funcType struct {
	params  []valType
	results []valType
}

type export struct {
	name    string
	funcIdx uint32
}

// Instr is a pre-decoded WebAssembly instruction.
// Partner holds the pre-resolved branch target index into the same []Instr slice.
// For block/if: Partner = matching end index.
// For loop: Partner = own index.
// For else: Partner = end index.
// -1 = not applicable.
type Instr struct {
	Op      byte
	Imm1    int64  // signed: const values, local/global/func indices, br depth, memarg align
	Imm2    uint64 // unsigned: memarg offset
	Partner int    // pre-resolved control flow target; -1 = not applicable
}

type funcBody struct {
	localTypes []valType // non-parameter locals
	instrs     []Instr
}

// Module is a decoded WASM module ready for instantiation.
type Module struct {
	types     []funcType
	funcTypes []uint32 // function section: funcIdx → typeIdx
	exports   []export
	bodies    []funcBody
	memPages  uint32
	heapStart int32
	hasMemory bool
	hasGlobal bool
}

// Compile decodes a WASM binary and returns a Module ready for instantiation.
func Compile(code []byte) (*Module, error) {
	if len(code) < 8 {
		return nil, ErrInvalidMagic
	}
	magic := []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}
	for i, b := range magic {
		if code[i] != b {
			return nil, ErrInvalidMagic
		}
	}

	mod := &Module{}
	pos := 8
	for pos < len(code) {
		if pos >= len(code) {
			break
		}
		id := code[pos]
		pos++
		slen, n := readULEB128(code, pos)
		pos += n
		sectionEnd := pos + int(slen)
		if sectionEnd > len(code) {
			return nil, ErrMalformedBinary
		}

		switch id {
		case 0x01:
			if err := parseTypeSection(code[pos:sectionEnd], mod); err != nil {
				return nil, err
			}
		case 0x03:
			if err := parseFunctionSection(code[pos:sectionEnd], mod); err != nil {
				return nil, err
			}
		case 0x05:
			if err := parseMemorySection(code[pos:sectionEnd], mod); err != nil {
				return nil, err
			}
		case 0x06:
			if err := parseGlobalSection(code[pos:sectionEnd], mod); err != nil {
				return nil, err
			}
		case 0x07:
			if err := parseExportSection(code[pos:sectionEnd], mod); err != nil {
				return nil, err
			}
		case 0x0A:
			if err := parseCodeSection(code[pos:sectionEnd], mod); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("%w: section id 0x%02X", ErrUnsupportedSection, id)
		}
		pos = sectionEnd
	}

	return mod, nil
}

func parseTypeSection(data []byte, mod *Module) error {
	count, n := readULEB128(data, 0)
	pos := n
	for i := uint64(0); i < count; i++ {
		if pos >= len(data) || data[pos] != 0x60 {
			return fmt.Errorf("%w: expected func type marker 0x60", ErrMalformedBinary)
		}
		pos++

		paramCount, n := readULEB128(data, pos)
		pos += n
		params := make([]valType, paramCount)
		for j := uint64(0); j < paramCount; j++ {
			params[j] = valType(data[pos])
			pos++
		}

		resultCount, n := readULEB128(data, pos)
		pos += n
		results := make([]valType, resultCount)
		for j := uint64(0); j < resultCount; j++ {
			results[j] = valType(data[pos])
			pos++
		}

		mod.types = append(mod.types, funcType{params, results})
	}
	return nil
}

func parseFunctionSection(data []byte, mod *Module) error {
	count, n := readULEB128(data, 0)
	pos := n
	for i := uint64(0); i < count; i++ {
		typeIdx, n := readULEB128(data, pos)
		pos += n
		mod.funcTypes = append(mod.funcTypes, uint32(typeIdx))
	}
	return nil
}

func parseMemorySection(data []byte, mod *Module) error {
	// count is always 1 in our emitter
	_, n := readULEB128(data, 0)
	pos := n
	// limitKind: 0x00 = min only
	pos++ // skip limitKind
	minPages, n := readULEB128(data, pos)
	_ = n
	mod.memPages = uint32(minPages)
	mod.hasMemory = true
	return nil
}

func parseGlobalSection(data []byte, mod *Module) error {
	// count is always 1 in our emitter
	_, n := readULEB128(data, 0)
	pos := n
	// valtype
	pos++ // skip valtype (0x7F = i32)
	// mutability
	pos++ // skip mutability (0x01 = mutable)
	// init expr: i32.const + SLEB128 + end
	if pos >= len(data) || data[pos] != 0x41 {
		return fmt.Errorf("%w: expected i32.const in global init", ErrMalformedBinary)
	}
	pos++
	val, n := readSLEB128(data, pos)
	pos += n
	_ = pos // end byte follows
	mod.heapStart = int32(val)
	mod.hasGlobal = true
	return nil
}

func parseExportSection(data []byte, mod *Module) error {
	count, n := readULEB128(data, 0)
	pos := n
	for i := uint64(0); i < count; i++ {
		nameLen, n := readULEB128(data, pos)
		pos += n
		name := string(data[pos : pos+int(nameLen)])
		pos += int(nameLen)
		kind := data[pos]
		pos++
		idx, n := readULEB128(data, pos)
		pos += n
		if kind == 0x00 { // function export
			mod.exports = append(mod.exports, export{name, uint32(idx)})
		}
	}
	return nil
}

func parseCodeSection(data []byte, mod *Module) error {
	count, n := readULEB128(data, 0)
	pos := n
	mod.bodies = make([]funcBody, int(count))
	for i := uint64(0); i < count; i++ {
		bodySize, n := readULEB128(data, pos)
		pos += n
		body, err := decodeBody(data[pos : pos+int(bodySize)])
		if err != nil {
			return fmt.Errorf("function %d: %w", i, err)
		}
		mod.bodies[i] = body
		pos += int(bodySize)
	}
	return nil
}

func decodeBody(data []byte) (funcBody, error) {
	// Step 1: parse local declarations
	localDeclCount, n := readULEB128(data, 0)
	pos := n
	var localTypes []valType
	for i := uint64(0); i < localDeclCount; i++ {
		groupCount, n := readULEB128(data, pos)
		pos += n
		vtype := valType(data[pos])
		pos++
		for j := uint64(0); j < groupCount; j++ {
			localTypes = append(localTypes, vtype)
		}
	}

	// Step 2: decode instructions
	var instrs []Instr
	for pos < len(data) {
		op := data[pos]
		pos++
		instr := Instr{Op: op, Partner: -1}

		switch op {
		case 0x00, // unreachable
			0x01, // nop
			0x0F, // return
			0x05, // else
			0x0B: // end
			// no immediates

		case 0x02, 0x03, 0x04: // block, loop, if — block type byte
			instr.Imm1 = int64(data[pos])
			pos++

		case 0x0C, 0x0D: // br, br_if
			depth, n := readULEB128(data, pos)
			pos += n
			instr.Imm1 = int64(depth)

		case 0x10: // call
			idx, n := readULEB128(data, pos)
			pos += n
			instr.Imm1 = int64(idx)

		case 0x20, 0x21, 0x22: // local.get, local.set, local.tee
			idx, n := readULEB128(data, pos)
			pos += n
			instr.Imm1 = int64(idx)

		case 0x23, 0x24: // global.get, global.set
			idx, n := readULEB128(data, pos)
			pos += n
			instr.Imm1 = int64(idx)

		case 0x28, 0x29, 0x2A, 0x2B, // i32.load, i64.load, f32.load, f64.load
			0x2C, 0x2D, 0x2E, 0x2F, // i32.load8_s, i32.load8_u, i32.load16_s, i32.load16_u
			0x30, 0x31, 0x32, 0x33, 0x34, 0x35, // i64.load variants
			0x36, 0x37, 0x38, 0x39, // i32.store, i64.store, f32.store, f64.store
			0x3A, 0x3B, 0x3C, 0x3D, 0x3E: // narrow stores
			align, n1 := readULEB128(data, pos)
			pos += n1
			offset, n2 := readULEB128(data, pos)
			pos += n2
			instr.Imm1 = int64(align)
			instr.Imm2 = offset

		case 0x41: // i32.const
			val, n := readSLEB128(data, pos)
			pos += n
			instr.Imm1 = val

		case 0x42: // i64.const
			val, n := readSLEB128(data, pos)
			pos += n
			instr.Imm1 = val

		case 0x45, 0x46, 0x47, 0x48, 0x49, 0x4A, 0x4B, 0x4C, 0x4D, 0x4E, 0x4F,
			0x50, 0x51, 0x52, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59, 0x5A, 0x5B,
			0x5C, 0x5D, 0x5E, 0x5F, 0x60, 0x61, 0x62, 0x63, 0x64, 0x65, 0x66,
			0x67, 0x68, 0x69, 0x6A, 0x6B, 0x6C, 0x6D, 0x6E, 0x6F,
			0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79, 0x7A, 0x7B,
			0x7C, 0x7D, 0x7E, 0x7F, 0x80, 0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87,
			0x88, 0x89, 0x8A, 0x8B, 0x8C, 0x8D, 0x8E, 0x8F,
			0x90, 0x91, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98, 0x99, 0x9A, 0x9B,
			0x9C, 0x9D, 0x9E, 0x9F, 0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5, 0xA6,
			0xA7, 0xA8, 0xA9, 0xAA, 0xAB, 0xAC, 0xAD, 0xAE, 0xAF,
			0xB0, 0xB1, 0xB2, 0xB3, 0xB4, 0xB5, 0xB6, 0xB7, 0xB8, 0xB9, 0xBA, 0xBB:
			// no immediates

		default:
			return funcBody{}, fmt.Errorf("%w: unknown opcode 0x%02X at offset %d", ErrMalformedBinary, op, pos-1)
		}

		instrs = append(instrs, instr)
	}

	// Step 3: resolve branch partners
	if err := resolvePartners(instrs); err != nil {
		return funcBody{}, err
	}

	return funcBody{localTypes: localTypes, instrs: instrs}, nil
}

// resolvePartners resolves all branch targets in a single O(n) pass.
// After this pass:
//   - block.Partner = matching end index (br exits to after end)
//   - loop.Partner = own index (br repeats from instrIdx+1)
//   - if.Partner = else index (if else exists) or end index
//   - else.Partner = end index
//   - end.Partner = matching block/loop/if/else index
func resolvePartners(instrs []Instr) error {
	type entry struct {
		instrIdx int
		op       byte
	}
	var stack []entry

	for i := range instrs {
		switch instrs[i].Op {
		case 0x02, 0x03, 0x04: // block, loop, if
			stack = append(stack, entry{i, instrs[i].Op})

		case 0x05: // else
			if len(stack) == 0 {
				return fmt.Errorf("%w: else without matching if", ErrMalformedBinary)
			}
			top := stack[len(stack)-1]
			instrs[top.instrIdx].Partner = i // if.Partner = this else
			stack[len(stack)-1] = entry{i, 0x05}

		case 0x0B: // end
			if len(stack) == 0 {
				instrs[i].Partner = -1 // function-level end
				break
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			instrs[i].Partner = top.instrIdx // end.Partner = opening instr

			switch top.op {
			case 0x02: // block: block.Partner = end index
				instrs[top.instrIdx].Partner = i
			case 0x03: // loop: loop.Partner = self (br re-enters body)
				instrs[top.instrIdx].Partner = top.instrIdx
			case 0x04: // if without else: if.Partner = end index
				instrs[top.instrIdx].Partner = i
			case 0x05: // else: else.Partner = end index
				instrs[top.instrIdx].Partner = i
			}
		}
	}
	return nil
}

// readULEB128 reads an unsigned LEB128 integer from data at position pos.
// Returns the value and the number of bytes consumed.
func readULEB128(data []byte, pos int) (uint64, int) {
	var result uint64
	var shift uint
	n := 0
	for {
		b := data[pos+n]
		n++
		result |= uint64(b&0x7F) << shift
		shift += 7
		if b&0x80 == 0 {
			break
		}
	}
	return result, n
}

// readSLEB128 reads a signed LEB128 integer from data at position pos.
// Returns the value and the number of bytes consumed.
func readSLEB128(data []byte, pos int) (int64, int) {
	var result int64
	var shift uint
	n := 0
	for {
		b := data[pos+n]
		n++
		result |= int64(b&0x7F) << shift
		shift += 7
		if b&0x80 == 0 {
			if shift < 64 && b&0x40 != 0 {
				result |= -(1 << shift)
			}
			break
		}
	}
	return result, n
}
