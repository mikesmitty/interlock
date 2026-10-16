package can

import (
	"testing"

	"github.com/mikesmitty/interlock/openlcb"
)

const (
	testNode  openlcb.NodeID = 0x050101012200
	testAlias openlcb.Alias  = 0x123
)

func TestControlFrames(t *testing.T) {
	tests := []struct {
		name  string
		frame Frame
		want  string
	}{
		{"CID1", CIDFrame(1, testNode, testAlias), ":X17050123N;"},
		{"CID2", CIDFrame(2, testNode, testAlias), ":X16101123N;"},
		{"CID3", CIDFrame(3, testNode, testAlias), ":X15012123N;"},
		{"CID4", CIDFrame(4, testNode, testAlias), ":X14200123N;"},
		{"RID", RIDFrame(testAlias), ":X10700123N;"},
		{"AMD", AMDFrame(testAlias, testNode), ":X10701123N050101012200;"},
		{"AME all", AMEFrame(testAlias, 0), ":X10702123N;"},
		{"AME one", AMEFrame(testAlias, testNode), ":X10702123N050101012200;"},
		{"AMR", AMRFrame(testAlias, testNode), ":X10703123N050101012200;"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.frame.String(); got != tt.want {
				t.Fatalf("frame = %s, want %s", got, tt.want)
			}
			if !tt.frame.IsControl() || tt.frame.IsOpenLCB() {
				t.Error("should be a control frame")
			}
			if tt.frame.SourceAlias() != testAlias {
				t.Errorf("SourceAlias = %v", tt.frame.SourceAlias())
			}
		})
	}

	cid := CIDFrame(1, testNode, testAlias)
	if !cid.IsCID() || cid.ControlSequence() != 7 || cid.ControlField() != 0x7050 {
		t.Errorf("CID1: IsCID=%v seq=%d field=%#x", cid.IsCID(), cid.ControlSequence(), cid.ControlField())
	}
	rid := RIDFrame(testAlias)
	if rid.IsCID() || rid.ControlField() != ControlRID {
		t.Errorf("RID: IsCID=%v field=%#x", rid.IsCID(), rid.ControlField())
	}
	amd := AMDFrame(testAlias, testNode)
	if id, ok := amd.PayloadNodeID(); !ok || id != testNode {
		t.Errorf("AMD PayloadNodeID = %v, %v", id, ok)
	}
}

func TestCIDFrameRange(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("CIDFrame(5) did not panic")
		}
	}()
	CIDFrame(5, testNode, testAlias)
}

func TestGlobalFrame(t *testing.T) {
	f := GlobalFrame(openlcb.MTIInitializationComplete, testAlias, testNode.AsSlice())
	if got, want := f.String(), ":X19100123N050101012200;"; got != want {
		t.Fatalf("frame = %s, want %s", got, want)
	}
	if !f.IsOpenLCB() || f.Type() != FrameTypeGlobalAddressed || f.MTI() != openlcb.MTIInitializationComplete {
		t.Errorf("decode: openlcb=%v type=%d mti=%v", f.IsOpenLCB(), f.Type(), f.MTI())
	}
	event := GlobalFrame(openlcb.MTIEventReport, 0xABC, openlcb.EventID(0x0501010122000001).AsSlice())
	if got, want := event.String(), ":X195B4ABCN0501010122000001;"; got != want {
		t.Fatalf("event frame = %s, want %s", got, want)
	}
}

func TestAddressedFrames(t *testing.T) {
	tests := []struct {
		name    string
		mti     openlcb.MTI
		payload []byte
		want    []string
	}{
		{
			name: "empty payload", mti: openlcb.MTISimpleNodeIdentInfoRequest,
			want: []string{":X19DE8123N0456;"},
		},
		{
			name: "one frame", mti: openlcb.MTIDatagramOK, payload: []byte{0x80},
			want: []string{":X19A28123N045680;"},
		},
		{
			name: "exactly six", mti: openlcb.MTIProtocolSupportReply, payload: []byte{0xD4, 0x18, 0x20, 0, 0, 0},
			want: []string{":X19668123N0456D41820000000;"},
		},
		{
			name: "two frames", mti: openlcb.MTIVerifiedNodeID, payload: []byte{1, 2, 3, 4, 5, 6, 7},
			want: []string{":X19170123N1456010203040506;", ":X19170123N245607;"},
		},
		{
			name: "two full frames", mti: openlcb.MTISimpleNodeIdentInfoReply, payload: []byte("\x04mikesmitty\x00"),
			want: []string{":X19A08123N1456046D696B6573;", ":X19A08123N24566D6974747900;"},
		},
		{
			name: "three frames", mti: openlcb.MTISimpleNodeIdentInfoReply, payload: []byte("\x04mikesmitty\x00X"),
			want: []string{":X19A08123N1456046D696B6573;", ":X19A08123N34566D6974747900;", ":X19A08123N245658;"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frames := AddressedFrames(tt.mti, testAlias, 0x456, tt.payload)
			if len(frames) != len(tt.want) {
				t.Fatalf("got %d frames %v, want %d", len(frames), frames, len(tt.want))
			}
			for i, f := range frames {
				if got := f.String(); got != tt.want[i] {
					t.Errorf("frame %d = %s, want %s", i, got, tt.want[i])
				}
				if dst, ok := f.AddressedDest(); !ok || dst != 0x456 {
					t.Errorf("frame %d AddressedDest = %v, %v", i, dst, ok)
				}
			}
		})
	}
}

func TestDatagramFrames(t *testing.T) {
	short := DatagramFrames(testAlias, 0x456, []byte{0x20, 0x43, 0, 0, 0, 0, 0xFF, 64})
	if len(short) != 1 || short[0].String() != ":X1A456123N204300000000FF40;" {
		t.Fatalf("short datagram = %v", short)
	}
	if short[0].Type() != FrameTypeDatagramOnly || short[0].DatagramDest() != 0x456 {
		t.Errorf("decode: type=%d dst=%v", short[0].Type(), short[0].DatagramDest())
	}

	long := make([]byte, 20)
	for i := range long {
		long[i] = byte(i)
	}
	frames := DatagramFrames(testAlias, 0x456, long)
	wantTypes := []FrameType{FrameTypeDatagramFirst, FrameTypeDatagramMiddle, FrameTypeDatagramLast}
	wantText := []string{":X1B456123N0001020304050607;", ":X1C456123N08090A0B0C0D0E0F;", ":X1D456123N10111213;"}
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3", len(frames))
	}
	for i, f := range frames {
		if f.Type() != wantTypes[i] || f.String() != wantText[i] {
			t.Errorf("frame %d = %s (type %d), want %s", i, f, f.Type(), wantText[i])
		}
	}

	exact := DatagramFrames(testAlias, 0x456, make([]byte, 16))
	if len(exact) != 2 || exact[1].Type() != FrameTypeDatagramLast || exact[1].Len != 8 {
		t.Errorf("16-byte datagram = %v", exact)
	}
}
