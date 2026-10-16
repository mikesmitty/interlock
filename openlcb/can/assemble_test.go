package can

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
)

func mustParse(t *testing.T, s string) Frame {
	t.Helper()
	f, err := ParseGridConnect(s)
	if err != nil {
		t.Fatalf("%s: %v", s, err)
	}
	return f
}

func TestAssemblerSingleFrames(t *testing.T) {
	cache := NewAliasCache()
	cache.Add(0x123, testNode)
	a := NewAssembler(cache.Lookup)
	now := time.Now()

	msg, err := a.Push(mustParse(t, ":X19100123N050101012200;"), now)
	if err != nil || msg == nil {
		t.Fatalf("Push = %v, %v", msg, err)
	}
	if msg.MTI != openlcb.MTIInitializationComplete || msg.Source.Alias != 0x123 || msg.Source.ID != testNode {
		t.Errorf("message = %v", msg)
	}
	if !bytes.Equal(msg.Payload, testNode.AsSlice()) {
		t.Errorf("payload = %X", msg.Payload)
	}

	msg, err = a.Push(mustParse(t, ":X19DE8456N0123;"), now)
	if err != nil || msg == nil {
		t.Fatalf("Push = %v, %v", msg, err)
	}
	if msg.MTI != openlcb.MTISimpleNodeIdentInfoRequest || msg.Dest.Alias != 0x123 || msg.Dest.ID != testNode || msg.Source.ID != 0 {
		t.Errorf("addressed message = %v", msg)
	}
	if len(msg.Payload) != 0 {
		t.Errorf("payload = %X, want empty", msg.Payload)
	}

	for _, s := range []string{":X10700123N;", ":X17050123N;", ":X1F123456N00;", ":S123N;"} {
		msg, err := a.Push(mustParse(t, s), now)
		if msg != nil || err != nil {
			t.Errorf("%s: Push = %v, %v; want nothing", s, msg, err)
		}
	}
}

func TestAssemblerAddressedMultiFrame(t *testing.T) {
	a := NewAssembler(nil)
	now := time.Now()
	snip := openlcb.SNIP{Manufacturer: "mikesmitty", Model: "CAN Bus Outpost", HardwareVersion: "Rev A", SoftwareVersion: "0.2.1", UserName: "Yard", UserDescription: "West"}
	frames := AddressedFrames(openlcb.MTISimpleNodeIdentInfoReply, 0x456, 0x123, snip.Encode())
	if len(frames) < 3 {
		t.Fatalf("expected a multi-frame reply, got %d frames", len(frames))
	}
	// Interleave an unrelated single-frame message from the same source to
	// make sure the partial is keyed by MTI as well.
	for i, f := range frames {
		if i == 1 {
			if msg, err := a.Push(mustParse(t, ":X19668456N0123D41820000000;"), now); err != nil || msg == nil || msg.MTI != openlcb.MTIProtocolSupportReply {
				t.Fatalf("interleaved message: %v, %v", msg, err)
			}
		}
		msg, err := a.Push(f, now)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if i < len(frames)-1 {
			if msg != nil {
				t.Fatalf("frame %d completed early: %v", i, msg)
			}
			continue
		}
		if msg == nil {
			t.Fatal("last frame did not complete the message")
		}
		got, err := openlcb.ParseSNIP(msg.Payload)
		if err != nil || got != snip {
			t.Fatalf("assembled SNIP = %+v, %v", got, err)
		}
	}
	if a.Pending() != 0 {
		t.Errorf("Pending = %d after completion", a.Pending())
	}
}

func TestAssemblerDatagrams(t *testing.T) {
	a := NewAssembler(nil)
	now := time.Now()
	payload := make([]byte, 72)
	for i := range payload {
		payload[i] = byte(i)
	}
	frames := DatagramFrames(0x456, 0x123, payload)
	if len(frames) != 9 {
		t.Fatalf("72-byte datagram should be 9 frames, got %d", len(frames))
	}
	for i, f := range frames[:len(frames)-1] {
		if msg, err := a.Push(f, now); msg != nil || err != nil {
			t.Fatalf("frame %d: %v, %v", i, msg, err)
		}
	}
	msg, err := a.Push(frames[len(frames)-1], now)
	if err != nil || msg == nil {
		t.Fatalf("last frame: %v, %v", msg, err)
	}
	if msg.MTI != openlcb.MTIDatagram || msg.Source.Alias != 0x456 || msg.Dest.Alias != 0x123 || !bytes.Equal(msg.Payload, payload) {
		t.Errorf("datagram = %v", msg)
	}

	one, err := a.Push(mustParse(t, ":X1A123456N2043;"), now)
	if err != nil || one == nil || !bytes.Equal(one.Payload, []byte{0x20, 0x43}) {
		t.Errorf("single-frame datagram = %v, %v", one, err)
	}
}

func TestAssemblerErrors(t *testing.T) {
	a := NewAssembler(nil)
	now := time.Now()
	tests := []struct {
		name     string
		frames   []string
		wantCode openlcb.ErrorCode
	}{
		{name: "datagram middle without start", frames: []string{":X1C123456N00;"}, wantCode: openlcb.ErrCodeOutOfOrderMiddleWithStart},
		{name: "datagram last without start", frames: []string{":X1D123456N00;"}, wantCode: openlcb.ErrCodeOutOfOrderMiddleWithStart},
		{name: "datagram start twice", frames: []string{":X1B123456N00;", ":X1B123456N00;"}, wantCode: openlcb.ErrCodeOutOfOrderStartBeforeEnd},
		{name: "addressed continuation without start", frames: []string{":X19A08456N2123AA;"}, wantCode: openlcb.ErrCodeOutOfOrderMiddleWithStart},
		{name: "addressed frame without destination", frames: []string{":X19DE8456N01;"}, wantCode: openlcb.ErrCodeInvalidArguments},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lastErr error
			for _, s := range tt.frames {
				_, lastErr = a.Push(mustParse(t, s), now)
			}
			var ae *AssemblyError
			if !errors.As(lastErr, &ae) || ae.Code != tt.wantCode {
				t.Fatalf("err = %v, want AssemblyError with %v", lastErr, tt.wantCode)
			}
		})
	}

	// Over-long datagram.
	a = NewAssembler(nil)
	frames := DatagramFrames(0x456, 0x123, make([]byte, 72))
	for _, f := range frames[:len(frames)-1] {
		if _, err := a.Push(f, now); err != nil {
			t.Fatal(err)
		}
	}
	extra := New(messageID(FrameTypeDatagramMiddle, 0x123, 0x456), make([]byte, 8))
	if _, err := a.Push(extra, now); err != nil {
		t.Fatalf("72nd byte rejected: %v", err)
	}
	if _, err := a.Push(extra, now); err == nil {
		t.Fatal("73+ byte datagram was accepted")
	}
	if a.Pending() != 0 {
		t.Error("over-long datagram left a partial")
	}
}

func TestAssemblerExpire(t *testing.T) {
	a := NewAssembler(nil)
	start := time.Now()
	if _, err := a.Push(mustParse(t, ":X1B123456N00;"), start); err != nil {
		t.Fatal(err)
	}
	if a.Expire(start.Add(time.Second)) != 0 || a.Pending() != 1 {
		t.Error("partial expired too soon")
	}
	if a.Expire(start.Add(10*time.Second)) != 1 || a.Pending() != 0 {
		t.Error("partial did not expire")
	}
	if _, err := a.Push(mustParse(t, ":X1D123456N00;"), start.Add(10*time.Second)); err == nil {
		t.Error("last frame after expiry should be an error")
	}
}

func TestFrames(t *testing.T) {
	tests := []struct {
		name    string
		msg     openlcb.Message
		want    []string
		wantErr bool
	}{
		{
			name: "global",
			msg:  openlcb.Message{MTI: openlcb.MTIVerifyNodeIDGlobal, Source: openlcb.NodeHandle{Alias: 0x123}},
			want: []string{":X19490123N;"},
		},
		{
			name: "addressed",
			msg:  openlcb.Message{MTI: openlcb.MTIProtocolSupportInquiry, Source: openlcb.NodeHandle{Alias: 0x123}, Dest: openlcb.NodeHandle{Alias: 0x456}},
			want: []string{":X19828123N0456;"},
		},
		{
			name: "datagram",
			msg:  openlcb.Message{MTI: openlcb.MTIDatagram, Source: openlcb.NodeHandle{Alias: 0x123}, Dest: openlcb.NodeHandle{Alias: 0x456}, Payload: []byte{0x20, 0x43, 0, 0, 0, 0, 0xFF, 64}},
			want: []string{":X1A456123N204300000000FF40;"},
		},
		{name: "no source alias", msg: openlcb.Message{MTI: openlcb.MTIVerifyNodeIDGlobal}, wantErr: true},
		{name: "addressed without dest", msg: openlcb.Message{MTI: openlcb.MTIProtocolSupportInquiry, Source: openlcb.NodeHandle{Alias: 0x123}}, wantErr: true},
		{name: "datagram without dest", msg: openlcb.Message{MTI: openlcb.MTIDatagram, Source: openlcb.NodeHandle{Alias: 0x123}}, wantErr: true},
		{name: "datagram too long", msg: openlcb.Message{MTI: openlcb.MTIDatagram, Source: openlcb.NodeHandle{Alias: 0x123}, Dest: openlcb.NodeHandle{Alias: 0x456}, Payload: make([]byte, 73)}, wantErr: true},
		{name: "global too long", msg: openlcb.Message{MTI: openlcb.MTIVerifyNodeIDGlobal, Source: openlcb.NodeHandle{Alias: 0x123}, Payload: make([]byte, 9)}, wantErr: true},
		{name: "stream unsupported", msg: openlcb.Message{MTI: openlcb.MTIStreamData, Source: openlcb.NodeHandle{Alias: 0x123}, Dest: openlcb.NodeHandle{Alias: 0x456}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frames, err := Frames(&tt.msg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Frames = %v, want error", frames)
				}
				return
			}
			if err != nil {
				t.Fatalf("Frames: %v", err)
			}
			if len(frames) != len(tt.want) {
				t.Fatalf("got %d frames, want %d", len(frames), len(tt.want))
			}
			for i, f := range frames {
				if f.String() != tt.want[i] {
					t.Errorf("frame %d = %s, want %s", i, f, tt.want[i])
				}
			}
		})
	}
}

func TestFramesRoundTripThroughAssembler(t *testing.T) {
	a := NewAssembler(nil)
	now := time.Now()
	long := bytes.Repeat([]byte("abcdefgh"), 9) // 72 bytes
	msgs := []openlcb.Message{
		{MTI: openlcb.MTIEventReport, Source: openlcb.NodeHandle{Alias: 0x123}, Payload: openlcb.EventID(0x0501010122000001).AsSlice()},
		{MTI: openlcb.MTISimpleNodeIdentInfoReply, Source: openlcb.NodeHandle{Alias: 0x123}, Dest: openlcb.NodeHandle{Alias: 0x456}, Payload: long},
		{MTI: openlcb.MTIDatagram, Source: openlcb.NodeHandle{Alias: 0x123}, Dest: openlcb.NodeHandle{Alias: 0x456}, Payload: long[:72]},
		{MTI: openlcb.MTIDatagramOK, Source: openlcb.NodeHandle{Alias: 0x456}, Dest: openlcb.NodeHandle{Alias: 0x123}, Payload: nil},
	}
	for _, want := range msgs {
		frames, err := Frames(&want)
		if err != nil {
			t.Fatal(err)
		}
		var got *openlcb.Message
		for _, f := range frames {
			got, err = a.Push(f, now)
			if err != nil {
				t.Fatal(err)
			}
		}
		if got == nil {
			t.Fatalf("%v: not reassembled", want.MTI)
		}
		if got.MTI != want.MTI || got.Source.Alias != want.Source.Alias || got.Dest.Alias != want.Dest.Alias || !bytes.Equal(got.Payload, want.Payload) {
			t.Errorf("%v: got %v, want %v", want.MTI, got, &want)
		}
	}
}
