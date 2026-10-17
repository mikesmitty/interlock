package olcbtcp_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/transport/olcbtcp"
)

const (
	gateway = openlcb.NodeID(0x0A0000000001)
	nodeA   = openlcb.NodeID(0x050101012200)
	nodeB   = openlcb.NodeID(0x050101012201)
)

func TestEncodeDecode(t *testing.T) {
	tests := []struct {
		name string
		msg  openlcb.Message
		want []byte
	}{
		{"global", openlcb.Message{MTI: openlcb.MTIEventReport, Source: openlcb.NodeHandle{ID: nodeA}, Payload: []byte{1, 2, 3, 4, 5, 6, 7, 8}},
			[]byte{0x80, 0x00, 0x00, 0x00, 0x1C, 0x0A, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0x12, 0x34,
				0x05, 0xB4, 0x05, 0x01, 0x01, 0x01, 0x22, 0x00, 1, 2, 3, 4, 5, 6, 7, 8}},
		{"addressed", openlcb.Message{MTI: openlcb.MTIProtocolSupportInquiry, Source: openlcb.NodeHandle{ID: nodeA}, Dest: openlcb.NodeHandle{ID: nodeB}},
			[]byte{0x80, 0x00, 0x00, 0x00, 0x1A, 0x0A, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0x12, 0x34,
				0x08, 0x28, 0x05, 0x01, 0x01, 0x01, 0x22, 0x00, 0x05, 0x01, 0x01, 0x01, 0x22, 0x01}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := olcbtcp.Encode(nil, &tc.msg, gateway, 0x1234)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("Encode = % X\nwant     % X", got, tc.want)
			}
			msg, hdr, n, err := olcbtcp.Decode(got)
			if err != nil || n != len(got) {
				t.Fatalf("Decode: n=%d err=%v", n, err)
			}
			if hdr.Gateway != gateway || hdr.Capture != 0x1234 || hdr.Flags != olcbtcp.FlagMessage {
				t.Errorf("header = %+v", hdr)
			}
			if msg.MTI != tc.msg.MTI || msg.Source.ID != tc.msg.Source.ID || msg.Dest.ID != tc.msg.Dest.ID || !bytes.Equal(msg.Payload, tc.msg.Payload) {
				t.Errorf("message = %+v, want %+v", msg, tc.msg)
			}
		})
	}
	if _, err := olcbtcp.Encode(nil, &openlcb.Message{MTI: openlcb.MTIEventReport}, gateway, 0); err == nil {
		t.Error("a message without a source should be refused")
	}
	if _, err := olcbtcp.Encode(nil, &openlcb.Message{MTI: openlcb.MTIDatagram, Source: openlcb.NodeHandle{ID: nodeA}}, gateway, 0); err == nil {
		t.Error("an addressed message without a destination should be refused")
	}
}

func TestReader(t *testing.T) {
	var stream []byte
	msgs := []openlcb.Message{
		{MTI: openlcb.MTIInitializationComplete, Source: openlcb.NodeHandle{ID: nodeA}, Payload: nodeA.AsSlice()},
		{MTI: openlcb.MTIDatagram, Source: openlcb.NodeHandle{ID: nodeA}, Dest: openlcb.NodeHandle{ID: nodeB}, Payload: bytes.Repeat([]byte{0x20}, 72)},
		{MTI: openlcb.MTISimpleNodeIdentInfoReply, Source: openlcb.NodeHandle{ID: nodeB}, Dest: openlcb.NodeHandle{ID: nodeA}, Payload: []byte("\x04Interlock\x00")},
	}
	for i := range msgs {
		var err error
		stream, err = olcbtcp.Encode(stream, &msgs[i], gateway, uint64(i))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			// A link control frame between messages is skipped.
			stream = append(stream, 0x00, 0x00, 0x00, 0x00, 0x0C, 0x0A, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0)
		}
	}
	// Feed it one byte at a time to exercise partial reads.
	r := olcbtcp.NewReader(iotest(stream))
	for i, want := range msgs {
		got, hdr, err := r.Next()
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if got.MTI != want.MTI || !bytes.Equal(got.Payload, want.Payload) || hdr.Capture != uint64(i) {
			t.Errorf("message %d = %v (capture %d), want %v", i, got, hdr.Capture, &want)
		}
	}
	if _, _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("after the stream: %v", err)
	}
	if r.Errors != 0 {
		t.Errorf("Errors = %d", r.Errors)
	}
}

// iotest returns a reader that hands out one byte per Read.
func iotest(b []byte) io.Reader { return &oneByte{b: b} }

type oneByte struct{ b []byte }

func (o *oneByte) Read(p []byte) (int, error) {
	if len(o.b) == 0 {
		return 0, io.EOF
	}
	p[0] = o.b[0]
	o.b = o.b[1:]
	return 1, nil
}
