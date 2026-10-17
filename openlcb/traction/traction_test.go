package traction

import (
	"bytes"
	"testing"

	"github.com/mikesmitty/interlock/openlcb"
)

func TestSpeedRoundTrip(t *testing.T) {
	cases := []struct {
		s    Speed
		wire [2]byte
	}{
		{Speed{Value: 0}, [2]byte{0x00, 0x00}},
		{Speed{Value: 0, Reverse: true}, [2]byte{0x80, 0x00}},
		{Speed{Value: 1}, [2]byte{0x3C, 0x00}},
		{Speed{Value: 30}, [2]byte{0x4F, 0x80}},
		{Speed{Value: 126, Reverse: true}, [2]byte{0xD7, 0xE0}},
		{Speed{Value: 0.5}, [2]byte{0x38, 0x00}},
	}
	for _, c := range cases {
		if got := c.s.Encode(); got != c.wire {
			t.Errorf("%v encodes to %X, want %X", c.s, got, c.wire)
		}
		back, estop := DecodeSpeed(c.wire[:])
		if estop || back != c.s {
			t.Errorf("%X decodes to %v (estop %v), want %v", c.wire, back, estop, c.s)
		}
	}
	if s, estop := DecodeSpeed([]byte{0x7E, 0x00}); !estop || s.Value != 0 {
		t.Errorf("NaN decodes to %v estop=%v", s, estop)
	}
	if got := (Speed{Value: -5}).Encode(); got != (Speed{Value: 5}).Encode() {
		t.Errorf("negative magnitude should encode as its absolute value")
	}
	if got := (Speed{Value: 30, Reverse: true}).String(); got != "30 reverse" {
		t.Errorf("String = %q", got)
	}
}

func TestCommands(t *testing.T) {
	ctrl := openlcb.NodeID(0x0A0000000001)
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"set speed", SetSpeed(Speed{Value: 30}), []byte{0x00, 0x4F, 0x80}},
		{"set function", SetFunction(2, 1), []byte{0x01, 0, 0, 2, 0, 1}},
		{"estop", EmergencyStop(), []byte{0x02}},
		{"query speeds", QuerySpeeds(), []byte{0x10}},
		{"query function", QueryFunction(0x12345), []byte{0x11, 0x01, 0x23, 0x45}},
		{"assign", AssignController(ctrl), []byte{0x20, 0x01, 0, 0x0A, 0, 0, 0, 0, 1}},
		{"release", ReleaseController(ctrl), []byte{0x20, 0x02, 0, 0x0A, 0, 0, 0, 0, 1}},
		{"query controller", QueryController(), []byte{0x20, 0x03}},
		{"changed accept", ControllerChangedReply(true), []byte{0x20, 0x04, 0}},
		{"changed refuse", ControllerChangedReply(false), []byte{0x20, 0x04, 1}},
		{"attach", AttachConsist(ctrl, ConsistFlagHide|ConsistFlagLinkFn), []byte{0x30, 0x01, 0x88, 0x0A, 0, 0, 0, 0, 1}},
		{"detach", DetachConsist(ctrl), []byte{0x30, 0x02, 0, 0x0A, 0, 0, 0, 0, 1}},
		{"query consist", QueryConsist(-1), []byte{0x30, 0x03}},
		{"query consist 2", QueryConsist(2), []byte{0x30, 0x03, 2}},
		{"reserve", Reserve(), []byte{0x40, 0x01}},
	}
	for _, c := range cases {
		if !bytes.Equal(c.got, c.want) {
			t.Errorf("%s = %X, want %X", c.name, c.got, c.want)
		}
	}
}

func TestReplies(t *testing.T) {
	sp, err := ParseSpeeds([]byte{0x10, 0x4F, 0x80, 0x00, 0x4F, 0x80, 0x4F, 0x80})
	if err != nil || sp.Set.Value != 30 || sp.EStop || sp.Actual.Value != 30 {
		t.Errorf("ParseSpeeds = %+v, %v", sp, err)
	}
	// simple-dcc flags an emergency stop in bit 7, OpenMRN in bit 0.
	for _, status := range []byte{0x80, 0x01} {
		sp, err = ParseSpeeds([]byte{0x10, 0x00, 0x00, status})
		if err != nil || !sp.EStop {
			t.Errorf("status %02X: ParseSpeeds = %+v, %v", status, sp, err)
		}
	}
	if _, err := ParseSpeeds([]byte{0x10, 0x00}); err == nil {
		t.Error("short speeds reply should fail")
	}
	fn, v, err := ParseFunction([]byte{0x11, 0, 0, 5, 0, 1})
	if err != nil || fn != 5 || v != 1 {
		t.Errorf("ParseFunction = %d %d %v", fn, v, err)
	}
	if r, err := ParseAssignReply([]byte{0x20, 0x01, 0x02}); err != nil || r != AssignRefusedByTrain {
		t.Errorf("ParseAssignReply = %d %v", r, err)
	}
	id, err := ParseControllerReply([]byte{0x20, 0x03, 0, 0x0A, 0, 0, 0, 0, 1})
	if err != nil || id != 0x0A0000000001 {
		t.Errorf("ParseControllerReply = %v %v", id, err)
	}
	res, err := ParseConsistResult([]byte{0x30, 0x01, 0x0A, 0, 0, 0, 0, 1, 0x10, 0x32})
	if err != nil || res.Member != 0x0A0000000001 || res.Code != 0x1032 {
		t.Errorf("ParseConsistResult = %+v %v", res, err)
	}
	count, entry, err := ParseConsistQuery([]byte{0x30, 0x03, 2, 1, 0x02, 0x06, 0x01, 0, 0, 0xC4, 0xD2})
	if err != nil || count != 2 || entry == nil || entry.Index != 1 || entry.Flags != 2 || entry.Member != DCCNodeID(1234, true) {
		t.Errorf("ParseConsistQuery = %d %+v %v", count, entry, err)
	}
}

func TestParseCommand(t *testing.T) {
	c, err := ParseCommand([]byte{0x80, 0xCF, 0x80})
	if err != nil || c.Kind != CmdSetSpeed || !c.Listener || c.Speed != (Speed{Value: 30, Reverse: true}) {
		t.Errorf("listener set speed = %+v, %v", c, err)
	}
	c, err = ParseCommand(AssignController(0x0A0000000001))
	if err != nil || c.Kind != CmdController || c.Sub != ControllerAssign || c.Node != 0x0A0000000001 {
		t.Errorf("assign = %+v, %v", c, err)
	}
	c, err = ParseCommand(QueryConsist(3))
	if err != nil || c.Index != 3 {
		t.Errorf("query consist = %+v, %v", c, err)
	}
	c, err = ParseCommand([]byte{0x20, 0x04, 0, 0x0A, 0, 0, 0, 0, 2})
	if err != nil || c.Sub != ControllerChanged || c.Node != 0x0A0000000002 {
		t.Errorf("changed = %+v, %v", c, err)
	}
	if _, err := ParseCommand([]byte{0x01, 0}); err == nil {
		t.Error("short set function should fail")
	}
	if _, err := ParseCommand([]byte{0x55}); err == nil {
		t.Error("unknown command should fail")
	}
}

func TestSearchEvent(t *testing.T) {
	ev, err := SearchEvent("1234", SearchAllocate|SearchExact|SearchAddressOnly|SearchDCC|SearchLong)
	if err != nil || ev != 0x090099FFFF1234EC {
		t.Fatalf("SearchEvent = %v, %v", ev, err)
	}
	if got := AddressSearch(1234, true, true); got != ev {
		t.Errorf("AddressSearch = %v", got)
	}
	if got := AddressSearch(3, false, false); got != 0x090099FFFFFFF368 {
		t.Errorf("AddressSearch(3, short) = %v", got)
	}
	query, flags, ok := ParseSearch(ev)
	if !ok || query != "1234" || flags != SearchAllocate|SearchExact|SearchAddressOnly|SearchDCC|SearchLong {
		t.Errorf("ParseSearch = %q %02X %v", query, flags, ok)
	}
	if _, _, ok := ParseSearch(0x090099FFFFFA34EC); ok {
		t.Error("a nibble above 9 should not parse")
	}
	if _, _, ok := ParseSearch(0x090099FFFF1234FC); ok {
		t.Error("the reserved flag should not parse")
	}
	if !IsSearchEvent(ev) || IsSearchEvent(EventIsTrain) {
		t.Error("IsSearchEvent")
	}
	for _, bad := range []string{"", "1234567", "12a"} {
		if _, err := SearchEvent(bad, 0); err == nil {
			t.Errorf("SearchEvent(%q) should fail", bad)
		}
	}
}

func TestSearchMatches(t *testing.T) {
	cases := []struct {
		query   string
		flags   SearchFlags
		address uint16
		long    bool
		want    bool
	}{
		{"1234", SearchExact | SearchDCC | SearchLong, 1234, true, true},
		{"12", SearchDCC | SearchLong, 1234, true, true},
		{"12", SearchExact | SearchDCC | SearchLong, 1234, true, false},
		{"1234", SearchExact | SearchDCC | SearchLong, 1234, false, false},
		{"3", SearchExact | SearchDCC, 3, false, true},
		{"3", SearchExact | SearchDCC, 3, true, false},
		{"3", SearchExact | SearchDCC | SearchAllocate, 3, true, true},
		{"300", SearchExact | SearchDCC, 300, true, true},
		{"5", 0, 55, false, true},
		{"", SearchExact, 0, false, false},
	}
	for _, c := range cases {
		if got := SearchMatches(c.query, c.flags, c.address, c.long); got != c.want {
			t.Errorf("SearchMatches(%q, %02X, %d, %v) = %v", c.query, c.flags, c.address, c.long, got)
		}
	}
}

func TestDCCNodeIDs(t *testing.T) {
	if id := DCCNodeID(1234, true); id != 0x0601000004D2|0xC000 {
		t.Errorf("DCCNodeID(1234, long) = %v", id)
	}
	if id := DCCNodeID(3, false); id != 0x060100000003 {
		t.Errorf("DCCNodeID(3, short) = %v", id)
	}
	addr, long, ok := DCCAddress(0x06010000C4D2)
	if !ok || addr != 1234 || !long {
		t.Errorf("DCCAddress(long 1234) = %d %v %v", addr, long, ok)
	}
	addr, long, ok = DCCAddress(0x060100000003)
	if !ok || addr != 3 || long {
		t.Errorf("DCCAddress(short 3) = %d %v %v", addr, long, ok)
	}
	if _, long, ok := DCCAddress(0x0601000000FF); !ok || !long {
		t.Error("255 without the selector is still a long address")
	}
	if _, _, ok := DCCAddress(0x0A0000000001); ok {
		t.Error("a non-DCC node ID should not decode")
	}
	for _, c := range []struct {
		addr uint16
		long bool
		want string
	}{{1234, true, "1234"}, {3, false, "3 (short)"}, {3, true, "3 (long)"}} {
		if got := AddressName(c.addr, c.long); got != c.want {
			t.Errorf("AddressName(%d, %v) = %q", c.addr, c.long, got)
		}
	}
}
