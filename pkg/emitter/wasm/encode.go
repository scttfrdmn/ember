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
