package monitor

import (
	"strings"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
	"github.com/mikesmitty/interlock/openlcb/can"
	"github.com/mikesmitty/interlock/openlcb/hub"
	"github.com/mikesmitty/interlock/openlcb/node"
)

func frame(t *testing.T, s string) can.Frame {
	t.Helper()
	f, err := can.ParseGridConnect(s)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestDecodeFrame(t *testing.T) {
	cache := can.NewAliasCache()
	cache.Add(0x123, 0x050101012200)
	lookup := func(a openlcb.Alias) openlcb.NodeID { id, _ := cache.Lookup(a); return id }
	tests := []struct {
		frame   string
		kind    string
		typ     string
		summary string
		srcID   openlcb.NodeID
	}{
		{":X17050123N;", "control", "CID1", "Check ID 1 of 4", 0x050101012200},
		{":X10700123N;", "control", "RID", "Reserve ID", 0x050101012200},
		{":X10701123N050101012200;", "control", "AMD", "alias is 05.01.01.01.22.00", 0x050101012200},
		{":X10702456N;", "control", "AME", "everyone announce", 0},
		{":X10702456N050101012200;", "control", "AME", "who is 05.01.01.01.22.00", 0},
		{":X10703123N050101012200;", "control", "AMR", "gave up its alias", 0x050101012200},
		{":X19100123N050101012200;", "message", "Initialization Complete", "05.01.01.01.22.00 is up", 0x050101012200},
		{":X19170123N050101012200;", "message", "Verified Node ID", "I am 05.01.01.01.22.00", 0x050101012200},
		{":X19490456N;", "message", "Verify Node ID Global", "everyone identify", 0},
		{":X19490456N050101012200;", "message", "Verify Node ID Global", "is 05.01.01.01.22.00 here?", 0},
		{":X195B4123N010000000000FFFD;", "message", "Event Report", "Emergency Stop", 0x050101012200},
		{":X195B4123N0501010122000001;", "message", "Event Report", "05.01.01.01.22.00.00.01", 0x050101012200},
		{":X19828456N0123;", "message", "Protocol Support Inquiry", "which protocols", 0},
		{":X19668123N0456D41820000000;", "message", "Protocol Support Reply", "Memory Configuration", 0x050101012200},
		{":X19DE8456N0123;", "message", "SNIP Request", "identify yourself", 0},
		{":X19A08123N1456046D696B6573;", "fragment", "SNIP Reply", "first frame", 0x050101012200},
		{":X19A08123N3456046D696B6573;", "fragment", "SNIP Reply", "middle frame", 0x050101012200},
		{":X19A08123N2456046D696B6573;", "fragment", "SNIP Reply", "last frame", 0x050101012200},
		{":X19A28456N012380;", "message", "Datagram OK", "reply pending", 0},
		{":X19A28456N0123;", "message", "Datagram OK", "received", 0},
		{":X19A48456N01231081;", "message", "Datagram Rejected", "address space unknown", 0},
		{":X19068456N012310430DE8;", "message", "Optional Interaction Rejected", "for SNIP Request", 0},
		{":X1A123456N204000000000FF40;", "datagram", "Datagram", "read 64 bytes from CDI at 0x0", 0},
		{":X1A123456N20430000000040;", "datagram", "Datagram", "read 64 bytes from CDI at 0x0", 0},
		{":X1A123456N2041000000800A;", "datagram", "Datagram", "read 10 bytes from configuration at 0x80", 0},
		{":X1A123456N2001000000000102;", "datagram", "Datagram", "write 2 bytes to configuration at 0x0", 0},
		{":X1A123456N2051000000800102;", "datagram", "Datagram", "2 bytes from configuration at 0x80", 0},
		{":X1A123456N20A8;", "datagram", "Datagram", "update complete", 0},
		{":X1A123456N20A9;", "datagram", "Datagram", "reboot", 0},
		{":X1A123456N2084FD;", "datagram", "Datagram", "information about configuration", 0},
		{":X1B123456N2000000000000000;", "fragment", "Datagram", "first frame: write", 0},
		{":X1C123456N0000;", "fragment", "Datagram", "middle frame", 0},
		{":X1D123456N0000;", "fragment", "Datagram", "last frame", 0},
		{":X195EB456N0123000100;", "message", "Traction Control Command", "set speed and direction", 0},
		{":X195EB456N01232001;", "message", "Traction Control Command", "assign controller", 0},
		{":X191E9123N045620010000;", "message", "Traction Control Reply", "assign controller reply", 0x050101012200},
		{":X1F123456N00;", "other", "Stream Data", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.frame, func(t *testing.T) {
			kind, d := DecodeFrame(frame(t, tt.frame), lookup)
			if kind != tt.kind {
				t.Errorf("kind = %q, want %q", kind, tt.kind)
			}
			if d == nil {
				t.Fatal("no decode")
			}
			if d.Type != tt.typ {
				t.Errorf("Type = %q, want %q", d.Type, tt.typ)
			}
			if !strings.Contains(d.Summary, tt.summary) {
				t.Errorf("Summary = %q, want it to contain %q", d.Summary, tt.summary)
			}
			if d.Source == nil {
				t.Fatal("no source")
			}
			if d.Source.ID != tt.srcID {
				t.Errorf("Source.ID = %v, want %v", d.Source.ID, tt.srcID)
			}
		})
	}

	if kind, d := DecodeFrame(can.Frame{ID: 0x123, Len: 1}, lookup); kind != "other" || d != nil {
		t.Errorf("standard frame = %q, %v", kind, d)
	}
}

func TestDecodeMessage(t *testing.T) {
	snip := openlcb.SNIP{Manufacturer: "mikesmitty", Model: "CAN Bus Outpost", UserName: "Yard"}
	msg := &openlcb.Message{MTI: openlcb.MTISimpleNodeIdentInfoReply, Source: openlcb.NodeHandle{ID: 0x050101012201, Alias: 0x456}, Dest: openlcb.NodeHandle{Alias: 0x123}, Payload: snip.Encode()}
	d := DecodeMessage(msg)
	if d.Type != "SNIP Reply" || d.Summary != `mikesmitty CAN Bus Outpost "Yard"` || d.Source.ID != 0x050101012201 || d.Dest.Alias != 0x123 {
		t.Errorf("DecodeMessage = %+v", d)
	}
}

func TestMonitorRingAndSubscribers(t *testing.T) {
	m := New(3, nil)
	var got []Entry
	unsubscribe := m.Subscribe(func(e Entry) { got = append(got, e) })

	for i := 1; i <= 5; i++ {
		m.HandleHubEvent(hub.Event{Time: time.Now(), Frame: can.RIDFrame(openlcb.Alias(i)), Direction: hub.In, Link: "a"})
	}
	if len(got) != 5 || got[4].Seq != 5 {
		t.Fatalf("subscriber got %d entries, last seq %d", len(got), got[len(got)-1].Seq)
	}
	recent := m.Recent(0)
	if len(recent) != 3 || recent[0].Seq != 3 || recent[2].Seq != 5 {
		t.Fatalf("Recent = %+v", recent)
	}
	if since := m.Since(3, 0); len(since) != 2 || since[0].Seq != 4 {
		t.Fatalf("Since(3) = %+v", since)
	}
	if since := m.Since(0, 1); len(since) != 1 || since[0].Seq != 3 {
		t.Fatalf("Since(0, 1) = %+v", since)
	}
	if m.LastSeq() != 5 {
		t.Errorf("LastSeq = %d", m.LastSeq())
	}
	if recent[0].Kind != "control" || recent[0].Decoded.Type != "RID" || recent[0].Link != "a" || recent[0].Direction != "in" {
		t.Errorf("entry = %+v", recent[0])
	}

	unsubscribe()
	m.HandleHubEvent(hub.Event{Frame: can.RIDFrame(9), Direction: hub.Out})
	if len(got) != 5 {
		t.Error("unsubscribed subscriber still called")
	}
	if e := m.Recent(1)[0]; e.Direction != "out" || e.Link != "" {
		t.Errorf("out entry = %+v", e)
	}
}

func TestMonitorAssembledMessages(t *testing.T) {
	m := New(10, nil)
	short := &openlcb.Message{MTI: openlcb.MTIVerifiedNodeID, Source: openlcb.NodeHandle{Alias: 1}, Payload: make([]byte, 6)}
	m.HandleNodeEvent(node.Event{Kind: node.EventMessage, Message: short})
	if m.LastSeq() != 0 {
		t.Fatal("single-frame message was added as assembled")
	}
	long := &openlcb.Message{MTI: openlcb.MTISimpleNodeIdentInfoReply, Source: openlcb.NodeHandle{Alias: 1}, Dest: openlcb.NodeHandle{Alias: 2}, Payload: openlcb.SNIP{Manufacturer: "a", Model: "b"}.Encode()}
	m.HandleNodeEvent(node.Event{Kind: node.EventMessage, Message: long})
	dg := &openlcb.Message{MTI: openlcb.MTIDatagram, Source: openlcb.NodeHandle{Alias: 1}, Dest: openlcb.NodeHandle{Alias: 2}, Payload: append([]byte{0x20, 0x51, 0, 0, 0, 0}, make([]byte, 20)...)}
	m.HandleNodeEvent(node.Event{Kind: node.EventMessage, Message: dg})
	m.HandleNodeEvent(node.Event{Kind: node.EventAliasMapped})
	entries := m.Recent(0)
	if len(entries) != 2 || entries[0].Kind != "assembled" || entries[0].Decoded.Type != "SNIP Reply" || entries[1].Decoded.Summary != "20 bytes from configuration at 0x0" {
		t.Fatalf("entries = %+v", entries)
	}
}
