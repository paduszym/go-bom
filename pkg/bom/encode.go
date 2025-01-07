package bom

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"reflect"
	"sync"
)

type Encoder struct {
	w io.Writer
}

func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w}
}

func (e *Encoder) Encode(v any) error {
	rv := reflect.Indirect(reflect.ValueOf(v))
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("bom: Encoder.Encode(non-struct %v)", rv.Type())
	}

	op := newEncodeOp()
	if err := op.encode(rv); err != nil {
		return err
	}

	hdr := op.createHeader()
	if err := binaryWrite(e.w, hdr); err != nil {
		return err
	}

	if err := e.writeVars(op.vars); err != nil {
		return err
	}

	if err := e.writeIndex(op.blocks, hdr.IndexOffset+hdr.IndexLength); err != nil {
		return err
	}

	return e.writeBlocks(op.blocks)
}

func (e *Encoder) writeVars(vars map[string]*encodedBlock) error {
	if err := binaryWrite(e.w, uint32(len(vars))); err != nil {
		return err
	}
	for name, b := range vars {
		if err := e.writeVar(name, b); err != nil {
			return err
		}
	}
	return nil
}

func (e *Encoder) writeVar(name string, b *encodedBlock) error {
	if err := binaryWrite(e.w, variable{b.index, uint8(len(name))}); err != nil {
		return err
	}
	_, err := io.WriteString(e.w, name)
	return err
}

func (e *Encoder) writeIndex(blocks []*encodedBlock, startAddress uint32) error {
	if err := binaryWrite(e.w, uint32(len(blocks)+1)); err != nil {
		return err
	}
	if err := binaryWrite(e.w, nilBlockPointer); err != nil {
		return err
	}
	address := startAddress
	for _, b := range blocks {
		length := uint32(b.Len())
		if err := binaryWrite(e.w, blockPointer{address, length}); err != nil {
			return err
		}
		address += length
	}
	return nil
}

func (e *Encoder) writeBlocks(blocks []*encodedBlock) error {
	for _, b := range blocks {
		if _, err := b.WriteTo(e.w); err != nil {
			return err
		}
	}
	return nil
}

type encodeOp struct {
	vars          map[string]*encodedBlock
	varsLength    int
	blocks        []*encodedBlock
	pointerBlocks sync.Map // map[*any]*encodedBlock
}

func newEncodeOp() *encodeOp {
	return &encodeOp{
		vars: make(map[string]*encodedBlock),
	}
}

func (op *encodeOp) encode(rv reflect.Value) error {
	t := rv.Type()
	for i, n := 0, t.NumField(); i < n; i++ {
		ft := t.Field(i)
		if ft.IsExported() {
			if name, ok := ft.Tag.Lookup("bom"); ok {
				return op.encodeVar(name, rv.Field(i))
			}
		}
	}
	return nil
}

func (op *encodeOp) encodeVar(name string, rv reflect.Value) error {
	vb := op.createBlock()
	if err := op.encodeBlock(vb, rv); err != nil {
		return err
	}
	op.vars[name] = vb
	op.varsLength += len(name)
	return nil
}

func (op *encodeOp) encodeBlock(b *encodedBlock, rv reflect.Value) error {
	switch rv.Kind() {
	case reflect.Bool,
		reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return binaryWrite(b, rv.Interface())

	case reflect.String:
		_, _ = b.WriteString(rv.String())
		return nil

	case reflect.Array, reflect.Slice:
		return op.encodeList(b, rv)

	case reflect.Struct:
		return op.encodeStruct(b, rv)

	case reflect.Pointer:
		return op.encodePointer(b, rv)

	case reflect.Interface:
		return op.encodeInterface(b, rv)

	default:
		return fmt.Errorf("cannot encode value of kind %v", rv.Kind())
	}
}

func (op *encodeOp) encodeList(b *encodedBlock, rv reflect.Value) error {
	for i, n := 0, rv.Len(); i < n; i++ {
		if err := op.encodeBlock(b, rv.Index(i)); err != nil {
			return err
		}
	}
	return nil
}

func (op *encodeOp) encodeStruct(b *encodedBlock, rv reflect.Value) error {
	t := rv.Type()
	for i, n := 0, t.NumField(); i < n; i++ {
		if t.Field(i).IsExported() {
			if err := op.encodeBlock(b, rv.Field(i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (op *encodeOp) encodePointer(b *encodedBlock, rv reflect.Value) error {
	if rv.IsNil() {
		return binaryWrite(b, nilBlockIndex)
	}
	vb, created := op.blockOf(rv)
	_ = binaryWrite(b, vb.index)
	if created {
		return op.encodeBlock(vb, rv.Elem())
	}
	return nil
}

func (op *encodeOp) encodeInterface(b *encodedBlock, rv reflect.Value) error {
	if rv.IsNil() {
		return binaryWrite(b, nilBlockIndex)
	}
	return op.encodeBlock(b, rv.Elem())
}

func (op *encodeOp) blockOf(v reflect.Value) (*encodedBlock, bool) {
	if v.Kind() == reflect.Pointer {
		if b, ok := op.pointerBlocks.Load(v.Interface()); ok {
			return b.(*encodedBlock), false
		}
		b := op.createBlock()
		op.pointerBlocks.Store(v.Interface(), b)
		return b, true
	}
	return op.createBlock(), true
}

func (op *encodeOp) createBlock() *encodedBlock {
	b := newEncodedBlock(uint32(len(op.blocks) + 1))
	op.blocks = append(op.blocks, b)
	return b
}

func (op *encodeOp) createHeader() *header {
	varsLength := uint32(op.varsLength + variableSize*len(op.vars) + 4)
	return &header{
		Magic:          headerMagic,
		Version:        1,
		NumberOfBlocks: uint32(len(op.blocks)),
		IndexOffset:    headerSize + varsLength,
		IndexLength:    uint32(blockPointerSize*(len(op.blocks)+1) + 4),
		VarsOffset:     headerSize,
		VarsLength:     varsLength,
	}
}

type encodedBlock struct {
	*bytes.Buffer
	index uint32
}

func newEncodedBlock(index uint32) *encodedBlock {
	return &encodedBlock{
		Buffer: bytes.NewBuffer(nil),
		index:  index,
	}
}

func binaryWrite(w io.Writer, data any) error {
	return binary.Write(w, binary.BigEndian, data)
}
