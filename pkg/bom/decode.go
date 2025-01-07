package bom

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
)

type Decoder struct {
	r io.ReadSeeker
}

func NewDecoder(r io.ReadSeeker) *Decoder {
	return &Decoder{r}
}

func (d *Decoder) Decode(v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer {
		return fmt.Errorf("bom: Decoder.Decode(non-pointer %v)", rv.Type())
	}
	if rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("bom: Decoder.Decode(non-struct pointer %v)", rv.Type())
	}

	var hdr header
	if err := d.readHeader(&hdr); err != nil {
		return err
	}

	if _, err := d.r.Seek(int64(hdr.IndexOffset), 0); err != nil {
		return err
	}
	blocks, err := d.readIndex()
	if err != nil {
		return err
	}

	if _, err := d.r.Seek(int64(hdr.VarsOffset), 0); err != nil {
		return err
	}
	vars := make(map[string]*decodedBlock)
	if err := d.readVars(blocks, vars); err != nil {
		return err
	}

	op := newDecodeOp(vars, blocks)
	return op.decode(rv.Elem())
}

func (d *Decoder) readHeader(hdr *header) error {
	if err := binaryRead(d.r, hdr); err != nil {
		return err
	}
	if hdr.Magic != headerMagic {
		return fmt.Errorf("invalid magic %v", hdr.Magic)
	}
	if hdr.Version != 1 {
		return fmt.Errorf("invalid version %d", hdr.Version)
	}
	return nil
}

func (d *Decoder) readIndex() ([]*decodedBlock, error) {
	var blocksCount uint32
	if err := binaryRead(d.r, &blocksCount); err != nil {
		return nil, err
	}
	blocks := make([]*decodedBlock, blocksCount+1)
	for i, n := 0, int(blocksCount); i < n; i++ {
		var bp blockPointer
		if err := binaryRead(d.r, &bp); err != nil {
			return nil, err
		}
		if i == 0 && (bp.Address != 0 || bp.Length != 0) {
			return nil, errors.New("Decoder.readIndex(non-null first block)")
		}
		blocks[i] = newDecodedBlock(d.r, bp)
	}
	return blocks, nil
}

func (d *Decoder) readVars(blocks []*decodedBlock, vars map[string]*decodedBlock) error {
	var varsCount uint32
	if err := binaryRead(d.r, &varsCount); err != nil {
		return err
	}

	blocksCount := uint32(len(blocks))
	for i, n := 0, int(varsCount); i < n; i++ {
		var v variable
		if err := binaryRead(d.r, &v); err != nil {
			return err
		}
		if v.BlockIndex >= blocksCount {
			return fmt.Errorf("invalid block index %d >= %d", v.BlockIndex, blocksCount)
		}
		name := make([]byte, v.NameLength)
		if _, err := io.ReadFull(d.r, name); err != nil {
			return err
		}
		vars[string(name)] = blocks[v.BlockIndex]
	}
	return nil
}

type decodeOp struct {
	vars          map[string]*decodedBlock
	blocks        []*decodedBlock
	blockPointers map[uint32]reflect.Value
}

func newDecodeOp(vars map[string]*decodedBlock, blocks []*decodedBlock) *decodeOp {
	blockPointers := make(map[uint32]reflect.Value)
	return &decodeOp{vars, blocks, blockPointers}
}

func (op *decodeOp) decode(rv reflect.Value) error {
	t := rv.Type()
	for i, n := 0, t.NumField(); i < n; i++ {
		ft := t.Field(i)
		if ft.IsExported() {
			if name, ok := ft.Tag.Lookup("bom"); ok {
				if err := op.decodeVar(name, rv.Field(i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (op *decodeOp) decodeVar(name string, rv reflect.Value) error {
	if b, ok := op.vars[name]; ok {
		if err := b.ReadFull(); err != nil {
			return err
		}
		return op.decodeBlock(b, rv, rv.Type(), nil)
	}
	return nil
}

func (op *decodeOp) decodeBlock(b *decodedBlock, rv reflect.Value, typ reflect.Type, ctx any) error {
	switch typ.Kind() {
	case reflect.Bool:
		return decodeBool(b, rv)

	case reflect.Int8:
		return decodeInt[int8](b, rv)
	case reflect.Int16:
		return decodeInt[int16](b, rv)
	case reflect.Int32:
		return decodeInt[int32](b, rv)
	case reflect.Int64:
		return decodeInt[int64](b, rv)

	case reflect.Uint8:
		return decodeUint[uint8](b, rv)
	case reflect.Uint16:
		return decodeUint[uint16](b, rv)
	case reflect.Uint32:
		return decodeUint[uint32](b, rv)
	case reflect.Uint64:
		return decodeUint[uint64](b, rv)

	case reflect.String:
		return decodeString(b, rv)

	case reflect.Array:
		return op.decodeArray(b, rv, typ.Elem(), typ.Len(), ctx)

	case reflect.Slice:
		return op.decodeSlice(b, rv, typ.Elem(), ctx)

	case reflect.Struct:
		return op.decodeStruct(b, rv, typ)

	case reflect.Pointer:
		return op.decodePointer(b, rv, typ, ctx)

	case reflect.Interface:
		return op.decodeInterface(b, rv, ctx)

	default:
		return fmt.Errorf("cannot decode to value of kind %v", rv.Kind())
	}
}

func (op *decodeOp) decodeArray(b *decodedBlock, rv reflect.Value, itemType reflect.Type, size int, ctx any) error {
	array := reflect.New(reflect.ArrayOf(size, itemType)).Elem()
	for i := 0; i < size; i++ {
		item := reflect.New(itemType).Elem()
		if err := op.decodeBlock(b, item, item.Type(), ctx); err != nil {
			return err
		}
		array.Index(i).Set(item)
	}
	rv.Set(array)
	return nil
}

func (op *decodeOp) decodeSlice(b *decodedBlock, rv reflect.Value, itemType reflect.Type, ctx any) error {
	slice := reflect.New(reflect.SliceOf(itemType)).Elem()
	for {
		item := reflect.New(itemType).Elem()
		if err := op.decodeBlock(b, item, item.Type(), ctx); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		slice = reflect.Append(slice, item)
	}
	rv.Set(slice)
	return nil
}

func (op *decodeOp) decodeStruct(b *decodedBlock, rv reflect.Value, structType reflect.Type) error {
	structure := reflect.New(structType).Elem()
	for i, n := 0, structType.NumField(); i < n; i++ {
		if structType.Field(i).IsExported() {
			structField := structure.Field(i)
			if err := op.decodeBlock(b, structField, structField.Type(), structure.Interface()); err != nil {
				return err
			}
		}
	}
	rv.Set(structure)
	return nil
}

func (op *decodeOp) decodePointer(b *decodedBlock, rv reflect.Value, pointerType reflect.Type, ctx any) error {
	var blockIndex uint32
	if err := binaryRead(b, &blockIndex); err != nil {
		return err
	}
	if blockIndex == nilBlockIndex {
		return nil
	}

	if pointer, ok := op.blockPointers[blockIndex]; ok {
		rv.Set(pointer)
		return nil
	}

	if bi, bc := int(blockIndex), len(op.blocks); bi >= bc {
		return fmt.Errorf("invalid block index %d >= %d", bi, bc)
	}
	pb := op.blocks[blockIndex]
	if err := pb.ReadFull(); err != nil {
		return err
	}
	pointer := reflect.New(pointerType.Elem())
	value := pointer.Elem()
	op.blockPointers[blockIndex] = pointer

	if err := op.decodeBlock(pb, value, value.Type(), ctx); err != nil {
		return err
	}
	rv.Set(pointer)
	return nil
}

func (op *decodeOp) decodeInterface(b *decodedBlock, rv reflect.Value, ctx any) error {
	if resolver, ok := interfaceTypeResolvers.Load(rv.Type()); ok {
		typ, err := resolver.(InterfaceTypeResolver)(ctx)
		if err != nil {
			return err
		}
		return op.decodeBlock(b, rv, typ, ctx)
	}
	return fmt.Errorf("don't know how to decode type %v", rv.Type())
}

func decodeBool(b *decodedBlock, rv reflect.Value) error {
	var v bool
	if err := binaryRead(b, &v); err != nil {
		return err
	}
	rv.SetBool(v)
	return nil
}

func decodeInt[TInt int8 | int16 | int32 | int64](b *decodedBlock, rv reflect.Value) error {
	var v TInt
	if err := binaryRead(b, &v); err != nil {
		return err
	}
	rv.SetInt(int64(v))
	return nil
}

func decodeUint[Uint uint8 | uint16 | uint32 | uint64](b *decodedBlock, rv reflect.Value) error {
	var v Uint
	if err := binaryRead(b, &v); err != nil {
		return err
	}
	rv.SetUint(uint64(v))
	return nil
}

func decodeString(b *decodedBlock, rv reflect.Value) error {
	s, err := io.ReadAll(b)
	if err != nil {
		return err
	}
	rv.SetString(string(s))
	return nil
}

type decodedBlock struct {
	*bytes.Reader
	r       io.ReadSeeker
	offset  int64
	content []byte
}

func newDecodedBlock(r io.ReadSeeker, bp blockPointer) *decodedBlock {
	content := make([]byte, bp.Length)
	return &decodedBlock{
		Reader:  bytes.NewReader(content),
		r:       r,
		offset:  int64(bp.Address),
		content: content,
	}
}

func (db *decodedBlock) ReadFull() error {
	if _, err := db.r.Seek(db.offset, 0); err != nil {
		return err
	}
	_, err := io.ReadFull(db.r, db.content)
	return err
}

func binaryRead(r io.Reader, data any) error {
	return binary.Read(r, binary.BigEndian, data)
}

type InterfaceTypeResolver func(ctx any) (reflect.Type, error)

var interfaceTypeResolvers sync.Map // map[reflect.Type]InterfaceTypeResolver

func RegisterInterfaceTypeResolver[T any](resolver InterfaceTypeResolver) {
	interfaceTypeResolvers.Store(reflect.TypeFor[T](), resolver)
}
