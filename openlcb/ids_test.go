package openlcb

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseNodeID(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    NodeID
		wantErr bool
	}{
		{name: "dotted", in: "05.01.01.01.22.00", want: 0x050101012200},
		{name: "colons", in: "05:01:01:01:22:00", want: 0x050101012200},
		{name: "bare", in: "050101012200", want: 0x050101012200},
		{name: "lower case", in: "0a.ff.ee.dd.cc.bb", want: 0x0AFFEEDDCCBB},
		{name: "max", in: "FF.FF.FF.FF.FF.FF", want: NodeIDMax},
		{name: "zero", in: "00.00.00.00.00.00", wantErr: true},
		{name: "empty", in: "", wantErr: true},
		{name: "too short", in: "05.01.01.01.22", wantErr: true},
		{name: "too long", in: "05.01.01.01.22.00.00", wantErr: true},
		{name: "single digit byte", in: "5.1.1.1.22.0", wantErr: true},
		{name: "odd digits", in: "05010101220", wantErr: true},
		{name: "bad hex", in: "05.01.01.01.2G.00", wantErr: true},
		{name: "leading separator", in: ".05.01.01.01.22.00", wantErr: true},
		{name: "trailing separator", in: "05.01.01.01.22.00.", wantErr: true},
		{name: "double separator", in: "05..01.01.01.22.00", wantErr: true},
		{name: "spaces", in: "05 01 01 01 22 00", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNodeID(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseNodeID(%q) = %v, want error", tt.in, got)
				}
				if !errors.Is(err, ErrInvalidNodeID) {
					t.Fatalf("ParseNodeID(%q) error = %v, want ErrInvalidNodeID", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseNodeID(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseNodeID(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestNodeIDString(t *testing.T) {
	tests := []struct {
		id   NodeID
		want string
	}{
		{0x050101012200, "05.01.01.01.22.00"},
		{0x0A0000000001, "0A.00.00.00.00.01"},
		{NodeIDMax, "FF.FF.FF.FF.FF.FF"},
		{0, "00.00.00.00.00.00"},
	}
	for _, tt := range tests {
		if got := tt.id.String(); got != tt.want {
			t.Errorf("NodeID(%#x).String() = %q, want %q", uint64(tt.id), got, tt.want)
		}
	}
}

func TestNodeIDBytesRoundTrip(t *testing.T) {
	ids := []NodeID{0x050101012200, 0x0A0000000001, NodeIDMax, 1}
	for _, id := range ids {
		b := id.Bytes()
		if got := NodeIDFromBytes(b); got != id {
			t.Errorf("NodeIDFromBytes(%v.Bytes()) = %v", id, got)
		}
	}
	want := [6]byte{0x05, 0x01, 0x01, 0x01, 0x22, 0x00}
	if got := NodeID(0x050101012200).Bytes(); got != want {
		t.Errorf("Bytes() = %v, want %v (big-endian)", got, want)
	}
}

func TestNodeIDValid(t *testing.T) {
	tests := []struct {
		id   NodeID
		want bool
	}{
		{0, false},
		{1, true},
		{NodeIDMax, true},
		{NodeIDMax + 1, false},
	}
	for _, tt := range tests {
		if got := tt.id.Valid(); got != tt.want {
			t.Errorf("NodeID(%#x).Valid() = %v, want %v", uint64(tt.id), got, tt.want)
		}
	}
}

func TestNodeIDIsLocal(t *testing.T) {
	tests := []struct {
		id   NodeID
		want bool
	}{
		{0x0A0000000000, true},
		{0x0AFFFFFFFFFF, true},
		{0x050101012200, false},
		{0x0B0000000000, false},
	}
	for _, tt := range tests {
		if got := tt.id.IsLocal(); got != tt.want {
			t.Errorf("%v.IsLocal() = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestNodeIDEvent(t *testing.T) {
	got := NodeID(0x050101012200).Event(0x0001)
	if want := EventID(0x0501010122000001); got != want {
		t.Fatalf("Event(1) = %v, want %v", got, want)
	}
	if got.Node() != 0x050101012200 || got.Suffix() != 1 {
		t.Fatalf("Node()/Suffix() = %v/%d, want 05.01.01.01.22.00/1", got.Node(), got.Suffix())
	}
}

func TestRandomLocalNodeID(t *testing.T) {
	a, err := RandomLocalNodeID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := RandomLocalNodeID()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []NodeID{a, b} {
		if !id.Valid() {
			t.Errorf("%v is not valid", id)
		}
		if !id.IsLocal() {
			t.Errorf("%v is not in the locally assigned range", id)
		}
	}
	if a == b {
		t.Errorf("two random IDs were equal: %v", a)
	}
}

func TestNodeIDJSON(t *testing.T) {
	type doc struct {
		ID NodeID `json:"id"`
	}
	data, err := json.Marshal(doc{ID: 0x050101012200})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"id":"05.01.01.01.22.00"}`; string(data) != want {
		t.Fatalf("json = %s, want %s", data, want)
	}
	var back doc
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.ID != 0x050101012200 {
		t.Fatalf("round trip = %v", back.ID)
	}
	if err := json.Unmarshal([]byte(`{"id":"nope"}`), &back); err == nil {
		t.Fatal("expected an error for a bad node ID")
	}
}

func TestParseEventID(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    EventID
		wantErr bool
	}{
		{name: "dotted", in: "05.01.01.01.22.00.00.01", want: 0x0501010122000001},
		{name: "bare", in: "0501010122000001", want: 0x0501010122000001},
		{name: "zero allowed", in: "00.00.00.00.00.00.00.00", want: 0},
		{name: "well known emergency off", in: "01.00.00.00.00.00.FF.FF", want: 0x010000000000FFFF},
		{name: "too short", in: "05.01.01.01.22.00", wantErr: true},
		{name: "too long", in: "05.01.01.01.22.00.00.01.02", wantErr: true},
		{name: "bad hex", in: "05.01.01.01.22.00.00.ZZ", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEventID(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseEventID(%q) = %v, want error", tt.in, got)
				}
				if !errors.Is(err, ErrInvalidEventID) {
					t.Fatalf("ParseEventID(%q) error = %v, want ErrInvalidEventID", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEventID(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseEventID(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestEventIDStringAndBytes(t *testing.T) {
	id := EventID(0x0501010122000001)
	if got, want := id.String(), "05.01.01.01.22.00.00.01"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	want := [8]byte{0x05, 0x01, 0x01, 0x01, 0x22, 0x00, 0x00, 0x01}
	if got := id.Bytes(); got != want {
		t.Errorf("Bytes() = %v, want %v", got, want)
	}
	if got := EventIDFromBytes(want); got != id {
		t.Errorf("EventIDFromBytes = %v, want %v", got, id)
	}
	data, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `"05.01.01.01.22.00.00.01"` {
		t.Errorf("json = %s", data)
	}
	var back EventID
	if err := json.Unmarshal(data, &back); err != nil || back != id {
		t.Errorf("json round trip = %v, %v", back, err)
	}
}
