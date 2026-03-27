package runtime

import (
	"encoding/binary"
	"fmt"
)

type linearMemory struct {
	data []byte
}

func newLinearMemory(numPages uint32) *linearMemory {
	return &linearMemory{data: make([]byte, uint64(numPages)*65536)}
}

func (m *linearMemory) checkBounds(addr uint32, size int) error {
	if uint64(addr)+uint64(size) > uint64(len(m.data)) {
		return fmt.Errorf("%w: addr=%d size=%d memSize=%d", ErrMemoryOutOfBounds, addr, size, len(m.data))
	}
	return nil
}

// All loads/stores use little-endian (WASM spec §4.2.8).

func (m *linearMemory) loadI32(addr uint32) (uint32, error) {
	if err := m.checkBounds(addr, 4); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(m.data[addr:]), nil
}

func (m *linearMemory) loadI64(addr uint32) (uint64, error) {
	if err := m.checkBounds(addr, 8); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(m.data[addr:]), nil
}

func (m *linearMemory) loadF32(addr uint32) (uint32, error) {
	return m.loadI32(addr)
}

func (m *linearMemory) loadF64(addr uint32) (uint64, error) {
	return m.loadI64(addr)
}

func (m *linearMemory) loadI32Load8S(addr uint32) (uint32, error) {
	if err := m.checkBounds(addr, 1); err != nil {
		return 0, err
	}
	return uint32(int32(int8(m.data[addr]))), nil
}

func (m *linearMemory) loadI32Load8U(addr uint32) (uint32, error) {
	if err := m.checkBounds(addr, 1); err != nil {
		return 0, err
	}
	return uint32(m.data[addr]), nil
}

func (m *linearMemory) loadI32Load16S(addr uint32) (uint32, error) {
	if err := m.checkBounds(addr, 2); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint16(m.data[addr:])
	return uint32(int32(int16(v))), nil
}

func (m *linearMemory) loadI32Load16U(addr uint32) (uint32, error) {
	if err := m.checkBounds(addr, 2); err != nil {
		return 0, err
	}
	return uint32(binary.LittleEndian.Uint16(m.data[addr:])), nil
}

func (m *linearMemory) loadI64Load8S(addr uint32) (uint64, error) {
	if err := m.checkBounds(addr, 1); err != nil {
		return 0, err
	}
	return uint64(int64(int8(m.data[addr]))), nil
}

func (m *linearMemory) loadI64Load8U(addr uint32) (uint64, error) {
	if err := m.checkBounds(addr, 1); err != nil {
		return 0, err
	}
	return uint64(m.data[addr]), nil
}

func (m *linearMemory) loadI64Load16S(addr uint32) (uint64, error) {
	if err := m.checkBounds(addr, 2); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint16(m.data[addr:])
	return uint64(int64(int16(v))), nil
}

func (m *linearMemory) loadI64Load16U(addr uint32) (uint64, error) {
	if err := m.checkBounds(addr, 2); err != nil {
		return 0, err
	}
	return uint64(binary.LittleEndian.Uint16(m.data[addr:])), nil
}

func (m *linearMemory) loadI64Load32S(addr uint32) (uint64, error) {
	if err := m.checkBounds(addr, 4); err != nil {
		return 0, err
	}
	v := binary.LittleEndian.Uint32(m.data[addr:])
	return uint64(int64(int32(v))), nil
}

func (m *linearMemory) loadI64Load32U(addr uint32) (uint64, error) {
	if err := m.checkBounds(addr, 4); err != nil {
		return 0, err
	}
	return uint64(binary.LittleEndian.Uint32(m.data[addr:])), nil
}

func (m *linearMemory) storeI32(addr uint32, v uint32) error {
	if err := m.checkBounds(addr, 4); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(m.data[addr:], v)
	return nil
}

func (m *linearMemory) storeI64(addr uint32, v uint64) error {
	if err := m.checkBounds(addr, 8); err != nil {
		return err
	}
	binary.LittleEndian.PutUint64(m.data[addr:], v)
	return nil
}

func (m *linearMemory) storeF32(addr uint32, v uint32) error {
	return m.storeI32(addr, v)
}

func (m *linearMemory) storeF64(addr uint32, v uint64) error {
	return m.storeI64(addr, v)
}

func (m *linearMemory) storeI32Store8(addr uint32, v uint32) error {
	if err := m.checkBounds(addr, 1); err != nil {
		return err
	}
	m.data[addr] = byte(v)
	return nil
}

func (m *linearMemory) storeI32Store16(addr uint32, v uint32) error {
	if err := m.checkBounds(addr, 2); err != nil {
		return err
	}
	binary.LittleEndian.PutUint16(m.data[addr:], uint16(v))
	return nil
}

func (m *linearMemory) storeI64Store8(addr uint32, v uint64) error {
	if err := m.checkBounds(addr, 1); err != nil {
		return err
	}
	m.data[addr] = byte(v)
	return nil
}

func (m *linearMemory) storeI64Store16(addr uint32, v uint64) error {
	if err := m.checkBounds(addr, 2); err != nil {
		return err
	}
	binary.LittleEndian.PutUint16(m.data[addr:], uint16(v))
	return nil
}

func (m *linearMemory) storeI64Store32(addr uint32, v uint64) error {
	if err := m.checkBounds(addr, 4); err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(m.data[addr:], uint32(v))
	return nil
}
