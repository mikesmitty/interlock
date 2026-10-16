package openlcb

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestMTIFlags(t *testing.T) {
	tests := []struct {
		mti       MTI
		addressed bool
		event     bool
		simple    bool
		special   bool
		priority  uint8
	}{
		{MTIInitializationComplete, false, false, false, false, 0},
		{MTIVerifyNodeIDAddressed, true, false, false, false, 1},
		{MTIVerifyNodeIDGlobal, false, false, true, false, 1},
		{MTIEventReport, false, true, true, false, 1},
		{MTIIdentifyConsumer, false, true, true, false, 2},
		{MTIProtocolSupportInquiry, true, false, false, false, 2},
		{MTISimpleNodeIdentInfoRequest, true, false, false, false, 3},
		{MTIDatagram, true, false, false, true, 3},
		{MTIDatagramOK, true, false, false, false, 2},
		{MTIStreamData, true, false, false, true, 3},
	}
	for _, tt := range tests {
		t.Run(tt.mti.String(), func(t *testing.T) {
			if got := tt.mti.IsAddressed(); got != tt.addressed {
				t.Errorf("IsAddressed = %v, want %v", got, tt.addressed)
			}
			if got := tt.mti.HasEvent(); got != tt.event {
				t.Errorf("HasEvent = %v, want %v", got, tt.event)
			}
			if got := tt.mti.IsSimple(); got != tt.simple {
				t.Errorf("IsSimple = %v, want %v", got, tt.simple)
			}
			if got := tt.mti.IsSpecial(); got != tt.special {
				t.Errorf("IsSpecial = %v, want %v", got, tt.special)
			}
			if got := tt.mti.Priority(); got != tt.priority {
				t.Errorf("Priority = %d, want %d", got, tt.priority)
			}
		})
	}
}

func TestMTIString(t *testing.T) {
	if got := MTIEventReport.String(); got != "Event Report" {
		t.Errorf("MTIEventReport.String() = %q", got)
	}
	if got := MTI(0x0ABC).String(); got != "MTI 0xABC" {
		t.Errorf("unknown MTI String() = %q", got)
	}
}

func TestMessageHelpers(t *testing.T) {
	event := Message{MTI: MTIEventReport, Payload: EventID(0x0501010122000001).AsSlice()}
	id, ok := event.EventID()
	if !ok || id != 0x0501010122000001 {
		t.Errorf("EventID() = %v, %v", id, ok)
	}
	short := Message{MTI: MTIEventReport, Payload: []byte{1, 2}}
	if _, ok := short.EventID(); ok {
		t.Error("EventID() succeeded on a short payload")
	}
	noEvent := Message{MTI: MTIVerifiedNodeID, Payload: NodeID(0x050101012200).AsSlice()}
	if _, ok := noEvent.EventID(); ok {
		t.Error("EventID() succeeded on a non-event MTI")
	}
	nid, ok := noEvent.PayloadNodeID()
	if !ok || nid != 0x050101012200 {
		t.Errorf("PayloadNodeID() = %v, %v", nid, ok)
	}
	if !noEvent.IsGlobal() {
		t.Error("Verified Node ID should be global")
	}
	addressed := Message{MTI: MTISimpleNodeIdentInfoRequest, Source: NodeHandle{Alias: 0x123}, Dest: NodeHandle{ID: 0x050101012200, Alias: 0x456}}
	if addressed.IsGlobal() {
		t.Error("SNIP request should be addressed")
	}
	if s := addressed.String(); !strings.Contains(s, "SNIP Request") || !strings.Contains(s, "0x456") {
		t.Errorf("String() = %q", s)
	}
}

func TestNodeHandleString(t *testing.T) {
	tests := []struct {
		h    NodeHandle
		want string
	}{
		{NodeHandle{}, "unknown"},
		{NodeHandle{Alias: 0x123}, "alias 0x123"},
		{NodeHandle{ID: 0x050101012200}, "05.01.01.01.22.00"},
		{NodeHandle{ID: 0x050101012200, Alias: 0x123}, "05.01.01.01.22.00 (0x123)"},
	}
	for _, tt := range tests {
		if got := tt.h.String(); got != tt.want {
			t.Errorf("%+v.String() = %q, want %q", tt.h, got, tt.want)
		}
	}
}

func TestErrorCode(t *testing.T) {
	if !ErrCodeOutOfOrderMiddleWithStart.Temporary() || ErrCodeOutOfOrderMiddleWithStart.Permanent() {
		t.Error("0x2041 should be temporary")
	}
	if !ErrCodeAddressSpaceUnknown.Permanent() || ErrCodeAddressSpaceUnknown.Temporary() {
		t.Error("0x1081 should be permanent")
	}
	if got := ErrCodeAddressSpaceUnknown.String(); got != "address space unknown (0x1081)" {
		t.Errorf("String() = %q", got)
	}
	if got := ErrorCode(0x2099).String(); got != "temporary error 0x2099" {
		t.Errorf("unknown temporary String() = %q", got)
	}
	err := error(&Error{Code: ErrCodeTimeout, Op: "datagram"})
	var olcbErr *Error
	if !errors.As(err, &olcbErr) || olcbErr.Code != ErrCodeTimeout || !olcbErr.Temporary() {
		t.Errorf("errors.As failed or wrong code: %v", err)
	}
	if err.Error() != "datagram: timeout (0x2010)" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestProtocols(t *testing.T) {
	// canbus-outpost advertises these (from its Protocol Support Reply).
	p := ProtocolSimple | ProtocolDatagram | ProtocolMemoryConfiguration | ProtocolEventExchange |
		ProtocolSimpleNodeInformation | ProtocolCDI | ProtocolFirmwareUpgrade
	wire := p.Bytes()
	want := []byte{0xD4, 0x18, 0x20, 0x00, 0x00, 0x00}
	if !bytes.Equal(wire, want) {
		t.Fatalf("Bytes() = % X, want % X", wire, want)
	}
	if back := ParseProtocols(wire); back != p {
		t.Fatalf("ParseProtocols round trip = %v, want %v", back, p)
	}
	if !p.Has(ProtocolCDI|ProtocolDatagram) || p.Has(ProtocolTractionControl) {
		t.Error("Has() wrong")
	}
	names := p.Names()
	if len(names) != 7 || names[0] != "Simple" || names[6] != "Firmware Upgrade" {
		t.Errorf("Names() = %v", names)
	}
	if ParseProtocols([]byte{0x80}) != ProtocolSimple {
		t.Error("short reply should pad with zeros")
	}
	if ParseProtocols(append(wire, 0xFF, 0xFF)) != p {
		t.Error("extra bytes should be ignored")
	}
	if Protocols(0).String() != "none" {
		t.Error("zero should print none")
	}
}

func TestSNIPRoundTrip(t *testing.T) {
	in := SNIP{
		Manufacturer:    "mikesmitty",
		Model:           "CAN Bus Outpost",
		HardwareVersion: "Rev A",
		SoftwareVersion: "0.2.1",
		UserName:        "Yard",
		UserDescription: "West yard throat",
	}
	wire := in.Encode()
	wantPrefix := append([]byte{4}, []byte("mikesmitty\x00CAN Bus Outpost\x00Rev A\x000.2.1\x00\x02Yard\x00West yard throat\x00")...)
	if !bytes.Equal(wire, wantPrefix) {
		t.Fatalf("Encode() = %q, want %q", wire, wantPrefix)
	}
	out, err := ParseSNIP(wire)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("ParseSNIP = %+v, want %+v", out, in)
	}
}

func TestSNIPEncodeLimits(t *testing.T) {
	long := strings.Repeat("x", 100)
	wire := SNIP{Manufacturer: long, Model: "m\x00hidden", UserName: long, UserDescription: long}.Encode()
	out, err := ParseSNIP(wire)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Manufacturer) != 40 || len(out.UserName) != 62 || len(out.UserDescription) != 63 {
		t.Errorf("lengths = %d %d %d, want 40 62 63", len(out.Manufacturer), len(out.UserName), len(out.UserDescription))
	}
	if out.Model != "m" {
		t.Errorf("embedded null not cut: %q", out.Model)
	}
}

func TestParseSNIP(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		want    SNIP
		wantErr bool
	}{
		{name: "version 1 sections", in: []byte("\x01A\x00B\x00C\x00D\x00\x01U\x00V\x00"), want: SNIP{"A", "B", "C", "D", "U", "V"}},
		{name: "manufacturer only", in: []byte("\x04A\x00B\x00C\x00D\x00"), want: SNIP{Manufacturer: "A", Model: "B", HardwareVersion: "C", SoftwareVersion: "D"}},
		{name: "truncated mid string", in: []byte("\x04A\x00Bee"), want: SNIP{Manufacturer: "A", Model: "Bee"}},
		{name: "empty strings", in: []byte("\x04\x00\x00\x00\x00\x02\x00\x00"), want: SNIP{}},
		{name: "empty", in: nil, wantErr: true},
		{name: "bad manufacturer version", in: []byte("\x07A\x00"), wantErr: true},
		{name: "bad user version", in: []byte("\x04A\x00B\x00C\x00D\x00\x09U\x00"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSNIP(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrSNIPVersion) {
					t.Fatalf("err = %v, want ErrSNIPVersion", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSNIP: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseSNIP = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestWellKnownName(t *testing.T) {
	if got := WellKnownName(EventEmergencyStop); got != "Emergency Stop" {
		t.Errorf("WellKnownName(EmergencyStop) = %q", got)
	}
	if got := WellKnownName(0x0501010122000001); got != "" {
		t.Errorf("WellKnownName(ordinary) = %q, want empty", got)
	}
}
