package bom

const (
	headerSize       = 512
	blockPointerSize = 8
	variableSize     = 5
)

var (
	headerMagic     = [8]byte{'B', 'O', 'M', 'S', 't', 'o', 'r', 'e'}
	nilBlockIndex   = uint32(0)
	nilBlockPointer = &blockPointer{}
)

type header struct {
	Magic          [8]byte
	Version        uint32
	NumberOfBlocks uint32
	IndexOffset    uint32
	IndexLength    uint32
	VarsOffset     uint32
	VarsLength     uint32
	_              [480]byte
}

type blockPointer struct {
	Address uint32
	Length  uint32
}

type variable struct {
	BlockIndex uint32
	NameLength uint8
}
