// Package wasm provides a self-contained WASM binary encoder and the
// SSA-to-WASM emitter visitor.
package wasm

// WASM binary section IDs (WebAssembly Core Spec 1.0 §5.5).
const (
	sectionType     = byte(0x01)
	sectionImport   = byte(0x02)
	sectionFunction = byte(0x03)
	sectionTable    = byte(0x04)
	sectionMemory   = byte(0x05)
	sectionGlobal   = byte(0x06)
	sectionExport   = byte(0x07)
	sectionStart    = byte(0x08)
	sectionElement  = byte(0x09)
	sectionCode     = byte(0x0A)
	sectionData     = byte(0x0B)
)

// WASM value types (WebAssembly Core Spec §2.3.1).
const (
	ValTypeI32 = byte(0x7F)
	ValTypeI64 = byte(0x7E)
	ValTypeF32 = byte(0x7D)
	ValTypeF64 = byte(0x7C)
)

// WASM instruction opcodes.
// (WebAssembly Core Spec §5.4)
const (
	OpcodeUnreachable = byte(0x00)
	OpcodeNop         = byte(0x01)
	OpcodeReturn      = byte(0x0F)
	OpcodeEnd         = byte(0x0B)
	OpcodeLocalGet    = byte(0x20)
	OpcodeLocalSet    = byte(0x21)
	OpcodeLocalTee    = byte(0x22)

	// Control flow (Phase 1)
	OpcodeBlock = byte(0x02)
	OpcodeLoop  = byte(0x03)
	OpcodeIf    = byte(0x04)
	OpcodeElse  = byte(0x05)
	OpcodeBr    = byte(0x0C)
	OpcodeBrIf  = byte(0x0D)
	OpcodeCall  = byte(0x10)

	// Block types (empty = void, i32, i64)
	BlockTypeEmpty = byte(0x40)
	BlockTypeI32   = byte(0x7F)
	BlockTypeI64   = byte(0x7E)

	// Constants
	OpcodeI32Const = byte(0x41)
	OpcodeI64Const = byte(0x42)

	// Type conversion
	OpcodeI32WrapI64 = byte(0xA7)

	// Boolean operations
	OpcodeI32Eqz = byte(0x45)

	// i32 comparisons (produce i32: 0 or 1)
	OpcodeI32Eq  = byte(0x46)
	OpcodeI32Ne  = byte(0x47)
	OpcodeI32LtS = byte(0x48)
	OpcodeI32GtS = byte(0x4A)
	OpcodeI32LeS = byte(0x4C)
	OpcodeI32GeS = byte(0x4E)

	// i64 comparisons (produce i32: 0 or 1)
	OpcodeI64Eq  = byte(0x51)
	OpcodeI64Ne  = byte(0x52)
	OpcodeI64LtS = byte(0x53)
	OpcodeI64GtS = byte(0x55)
	OpcodeI64LeS = byte(0x57)
	OpcodeI64GeS = byte(0x59)

	// i32 arithmetic
	OpcodeI32Clz  = byte(0x67)
	OpcodeI32Add  = byte(0x6A)
	OpcodeI32Sub  = byte(0x6B)
	OpcodeI32Mul  = byte(0x6C)
	OpcodeI32DivS = byte(0x6D)
	OpcodeI32DivU = byte(0x6E)
	OpcodeI32RemS = byte(0x6F)
	OpcodeI32RemU = byte(0x70)
	OpcodeI32And  = byte(0x71)
	OpcodeI32Or   = byte(0x72)
	OpcodeI32Xor  = byte(0x73)
	OpcodeI32Shl  = byte(0x74)
	OpcodeI32ShrS = byte(0x75)
	OpcodeI32ShrU = byte(0x76)

	// i64 arithmetic
	OpcodeI64Add  = byte(0x7C)
	OpcodeI64Sub  = byte(0x7D)
	OpcodeI64Mul  = byte(0x7E)
	OpcodeI64DivS = byte(0x7F)
	OpcodeI64DivU = byte(0x80)
	OpcodeI64RemS = byte(0x81)
	OpcodeI64RemU = byte(0x82)
	OpcodeI64And  = byte(0x83)
	OpcodeI64Or   = byte(0x84)
	OpcodeI64Xor  = byte(0x85)
	OpcodeI64Shl  = byte(0x86)
	OpcodeI64ShrS = byte(0x87)
	OpcodeI64ShrU = byte(0x88)

	// f64 arithmetic
	OpcodeF64Add = byte(0xA0)
	OpcodeF64Sub = byte(0xA1)
	OpcodeF64Mul = byte(0xA2)
	OpcodeF64Div = byte(0xA3)

	// f32 arithmetic
	OpcodeF32Add = byte(0x92)
	OpcodeF32Sub = byte(0x93)
	OpcodeF32Mul = byte(0x94)
	OpcodeF32Div = byte(0x95)

	// Global variable access
	OpcodeGlobalGet = byte(0x23)
	OpcodeGlobalSet = byte(0x24)

	// Memory load instructions (each followed by memarg: align + offset uleb128)
	OpcodeI32Load   = byte(0x28)
	OpcodeI64Load   = byte(0x29)
	OpcodeF32Load   = byte(0x2A)
	OpcodeF64Load   = byte(0x2B)
	OpcodeI32Load8S  = byte(0x2C)
	OpcodeI32Load8U  = byte(0x2D)
	OpcodeI32Load16S = byte(0x2E)
	OpcodeI32Load16U = byte(0x2F)
	OpcodeI64Load8S  = byte(0x30)
	OpcodeI64Load8U  = byte(0x31)
	OpcodeI64Load16S = byte(0x32)
	OpcodeI64Load16U = byte(0x33)
	OpcodeI64Load32S = byte(0x34)
	OpcodeI64Load32U = byte(0x35)

	// Memory store instructions (each followed by memarg: align + offset uleb128)
	OpcodeI32Store   = byte(0x36)
	OpcodeI64Store   = byte(0x37)
	OpcodeF32Store   = byte(0x38)
	OpcodeF64Store   = byte(0x39)
	OpcodeI32Store8  = byte(0x3A)
	OpcodeI32Store16 = byte(0x3B)
	OpcodeI64Store8  = byte(0x3C)
	OpcodeI64Store16 = byte(0x3D)
	OpcodeI64Store32 = byte(0x3E)

	// Type conversion opcodes
	OpcodeI64ExtendI32S  = byte(0xAC)
	OpcodeI64ExtendI32U  = byte(0xAD)
	OpcodeI64TruncF32S   = byte(0xAE)
	OpcodeI64TruncF32U   = byte(0xAF)
	OpcodeI64TruncF64S   = byte(0xB0)
	OpcodeI64TruncF64U   = byte(0xB1)
	OpcodeF32ConvertI32S = byte(0xB2)
	OpcodeF32ConvertI32U = byte(0xB3)
	OpcodeF32ConvertI64S = byte(0xB4)
	OpcodeF32ConvertI64U = byte(0xB5)
	OpcodeF32DemoteF64   = byte(0xB6)
	OpcodeF64ConvertI32S = byte(0xB7)
	OpcodeF64ConvertI32U = byte(0xB8)
	OpcodeF64ConvertI64S = byte(0xB9)
	OpcodeF64ConvertI64U = byte(0xBA)
	OpcodeF64PromoteF32  = byte(0xBB)
)

// Export descriptor kinds.
const (
	exportFunc   = byte(0x00)
	exportTable  = byte(0x01)
	exportMem    = byte(0x02)
	exportGlobal = byte(0x03)
)

// wasmMagic is the 8-byte module preamble: magic number + version 1.
var wasmMagic = []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}

// sleb128 encodes v as a signed LEB128 byte sequence.
func sleb128(v int64) []byte {
	var buf []byte
	for {
		b := byte(v & 0x7F)
		v >>= 7
		if (v == 0 && b&0x40 == 0) || (v == -1 && b&0x40 != 0) {
			buf = append(buf, b)
			break
		}
		buf = append(buf, b|0x80)
	}
	return buf
}

// uleb128 encodes v as an unsigned LEB128 byte sequence.
func uleb128(v uint32) []byte {
	var buf []byte
	for {
		b := byte(v & 0x7F)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		buf = append(buf, b)
		if v == 0 {
			break
		}
	}
	return buf
}

// section encodes a WASM section: id byte + LEB128(len(body)) + body.
func section(id byte, body []byte) []byte {
	out := []byte{id}
	out = append(out, uleb128(uint32(len(body)))...)
	out = append(out, body...)
	return out
}

// vec encodes a WASM vector: LEB128(len(items)) + concat(items).
func vec(items [][]byte) []byte {
	var body []byte
	body = append(body, uleb128(uint32(len(items)))...)
	for _, item := range items {
		body = append(body, item...)
	}
	return body
}

// encodeString encodes a UTF-8 name as a WASM name: LEB128(len) + bytes.
func encodeName(s string) []byte {
	b := []byte(s)
	out := uleb128(uint32(len(b)))
	return append(out, b...)
}

// funcType encodes a WASM function type: 0x60 + param types vec + result types vec.
func funcType(params []byte, results []byte) []byte {
	out := []byte{0x60}
	out = append(out, uleb128(uint32(len(params)))...)
	out = append(out, params...)
	out = append(out, uleb128(uint32(len(results)))...)
	out = append(out, results...)
	return out
}

// funcBody encodes a WASM function body:
// LEB128(body_size) + local_decls_vec + instructions + end.
//
// localTypes lists the types of non-parameter locals (each type as ValType byte).
// instructions is the raw instruction bytecode (without the final 'end').
func funcBody(localTypes []byte, instructions []byte) []byte {
	// Build locals declaration: group consecutive same-type locals.
	localDecls := encodeLocalDecls(localTypes)

	// Function body content: locals + instructions + end
	content := append(localDecls, instructions...)
	content = append(content, OpcodeEnd)

	// Prefix with LEB128-encoded size.
	sizePrefix := uleb128(uint32(len(content)))
	return append(sizePrefix, content...)
}

// memarg encodes a WASM memory instruction argument: align + offset (both uleb128).
// For natural alignment with no inline offset use memarg(0, 0).
func memarg(align, offset uint32) []byte {
	return append(uleb128(align), uleb128(offset)...)
}

// memorySection encodes a WASM Memory section with a single memory (no max limit).
func memorySection(minPages uint32) []byte {
	body := []byte{0x01}        // vec of 1 memory
	body = append(body, 0x00)   // limit kind: min only (no max)
	body = append(body, uleb128(minPages)...)
	return section(sectionMemory, body)
}

// globalSection encodes a WASM Global section with a single mutable i32 global
// initialized to initValue. Used for the __heap_ptr bump allocator pointer.
func globalSection(initValue int32) []byte {
	var body []byte
	body = append(body, 0x01)                         // vec of 1 global
	body = append(body, ValTypeI32, 0x01)              // globaltype: i32, mutable
	body = append(body, OpcodeI32Const)               // init expr: i32.const
	body = append(body, sleb128(int64(initValue))...) // initValue
	body = append(body, OpcodeEnd)                    // end init expr
	return section(sectionGlobal, body)
}

// allocFuncBody returns the encoded body of the __alloc(size i32) → i32 function.
// It implements a simple bump allocator:
//
//	addr = heap_ptr
//	heap_ptr = (heap_ptr + size + 7) & ^7   // align to 8 bytes
//	return addr
//
// The function body is length-prefixed (ready for the Code section vec entry).
func allocFuncBody() []byte {
	var body []byte
	// 1 local declaration: 1 × i32 (the saved addr, local index 1; param is local 0)
	body = append(body, 0x01, 0x01, ValTypeI32)
	// global.get 0   → old heap_ptr (= return address)
	body = append(body, OpcodeGlobalGet, 0x00)
	// local.tee 1    → save addr AND keep on stack
	body = append(body, OpcodeLocalTee, 0x01)
	// local.get 0    → size param
	body = append(body, OpcodeLocalGet, 0x00)
	// i32.add        → heap_ptr + size
	body = append(body, OpcodeI32Add)
	// i32.const 7
	body = append(body, OpcodeI32Const)
	body = append(body, sleb128(7)...)
	// i32.add        → + 7
	body = append(body, OpcodeI32Add)
	// i32.const -8   → alignment mask
	body = append(body, OpcodeI32Const)
	body = append(body, sleb128(-8)...)
	// i32.and        → align to 8 bytes
	body = append(body, OpcodeI32And)
	// global.set 0   → update heap_ptr
	body = append(body, OpcodeGlobalSet, 0x00)
	// local.get 1    → return saved addr
	body = append(body, OpcodeLocalGet, 0x01)
	// end
	body = append(body, OpcodeEnd)
	// Prefix with size (this is a Code section entry, not a funcBody call)
	return append(uleb128(uint32(len(body))), body...)
}

// encodeLocalDecls converts a flat list of ValType bytes into the WASM
// locals declaration format: vec of (count, type) pairs.
// Consecutive locals of the same type are grouped.
func encodeLocalDecls(localTypes []byte) []byte {
	if len(localTypes) == 0 {
		return uleb128(0) // zero local entries
	}

	type localGroup struct {
		count uint32
		vtype byte
	}
	var groups []localGroup
	cur := localGroup{count: 1, vtype: localTypes[0]}
	for _, t := range localTypes[1:] {
		if t == cur.vtype {
			cur.count++
		} else {
			groups = append(groups, cur)
			cur = localGroup{count: 1, vtype: t}
		}
	}
	groups = append(groups, cur)

	// Encode as a vector of (count uleb128, type byte) pairs.
	var body []byte
	body = append(body, uleb128(uint32(len(groups)))...)
	for _, g := range groups {
		body = append(body, uleb128(g.count)...)
		body = append(body, g.vtype)
	}
	return body
}
