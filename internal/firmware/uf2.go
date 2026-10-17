package firmware

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// UF2 is the container the Raspberry Pi tools produce: 512 byte blocks,
// each carrying up to 476 bytes of the image and the flash address they
// belong at. Interlock unpacks it into the flat image the node writes to
// its flash, filling any gaps between blocks with erased flash (0xFF).

const (
	uf2BlockSize   = 512
	uf2Magic0      = 0x0A324655 // "UF2\n"
	uf2Magic1      = 0x9E5D5157
	uf2MagicEnd    = 0x0AB16F30
	uf2FlagNoFlash = 0x00000001 // the block is not for the main flash
	uf2MaxPayload  = 476
)

// IsUF2 reports whether data starts like a UF2 file.
func IsUF2(data []byte) bool {
	return len(data) >= 8 && binary.LittleEndian.Uint32(data) == uf2Magic0 && binary.LittleEndian.Uint32(data[4:]) == uf2Magic1
}

type uf2Block struct {
	address uint32
	data    []byte
}

// UnpackUF2 turns a UF2 file into a flat image starting at its lowest
// address.
func UnpackUF2(data []byte) ([]byte, error) {
	if len(data)%uf2BlockSize != 0 {
		return nil, fmt.Errorf("a UF2 file is made of 512 byte blocks; this one has %d bytes", len(data))
	}
	var blocks []uf2Block
	for off := 0; off < len(data); off += uf2BlockSize {
		b := data[off : off+uf2BlockSize]
		if binary.LittleEndian.Uint32(b) != uf2Magic0 || binary.LittleEndian.Uint32(b[4:]) != uf2Magic1 ||
			binary.LittleEndian.Uint32(b[508:]) != uf2MagicEnd {
			return nil, fmt.Errorf("UF2 block %d is damaged", off/uf2BlockSize)
		}
		flags := binary.LittleEndian.Uint32(b[8:])
		if flags&uf2FlagNoFlash != 0 {
			continue
		}
		size := binary.LittleEndian.Uint32(b[16:])
		if size == 0 || size > uf2MaxPayload {
			return nil, fmt.Errorf("UF2 block %d carries %d bytes, which is not possible", off/uf2BlockSize, size)
		}
		blocks = append(blocks, uf2Block{address: binary.LittleEndian.Uint32(b[12:]), data: b[32 : 32+size]})
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("the UF2 file holds no flash data")
	}
	sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].address < blocks[j].address })
	base := blocks[0].address
	var image []byte
	for _, b := range blocks {
		at := int(b.address - base)
		if at < len(image) {
			return nil, fmt.Errorf("UF2 blocks overlap at address 0x%X", b.address)
		}
		if at > MaxImage {
			return nil, fmt.Errorf("the UF2 file spans more than %d MB of flash", MaxImage>>20)
		}
		for len(image) < at {
			image = append(image, 0xFF)
		}
		image = append(image, b.data...)
	}
	return image, nil
}
