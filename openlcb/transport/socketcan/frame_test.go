package socketcan_test

import (
	"testing"

	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/transport/socketcan"
)

func TestMarshalUnmarshal(t *testing.T) {
	f := can.New(0x195B4123, []byte{1, 2, 3, 4, 5, 6, 7, 8})
	b := socketcan.Marshal(f)
	want := [16]byte{0x23, 0x41, 0x5B, 0x99, 8, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}
	if b != want {
		t.Fatalf("Marshal = % X, want % X", b, want)
	}
	back, ok := socketcan.Unmarshal(b[:])
	if !ok || back != f {
		t.Fatalf("Unmarshal = %+v, %v; want %+v", back, ok, f)
	}
	// A standard frame keeps its 11 bit identifier and no flag.
	std := can.Frame{ID: 0x123, Len: 2, Data: [8]byte{0xAA, 0xBB}}
	b = socketcan.Marshal(std)
	if b[0] != 0x23 || b[1] != 0x01 || b[3] != 0 || b[4] != 2 {
		t.Errorf("standard frame = % X", b)
	}
	if back, ok := socketcan.Unmarshal(b[:]); !ok || back.Extended || back.ID != 0x123 || back.Len != 2 {
		t.Errorf("standard frame back = %+v, %v", back, ok)
	}
	// Error and remote frames are not for us.
	b = socketcan.Marshal(f)
	b[3] |= 0x20 // ERR flag
	if _, ok := socketcan.Unmarshal(b[:]); ok {
		t.Error("an error frame should be refused")
	}
	if _, ok := socketcan.Unmarshal(b[:10]); ok {
		t.Error("a short buffer should be refused")
	}
}
