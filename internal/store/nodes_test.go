package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
)

func TestNodesAndCDI(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id := openlcb.NodeID(0x050101011800)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := s.SaveNode(ctx, NodeRecord{ID: id, FirstSeen: t0, LastSeen: t0}); err != nil {
		t.Fatal(err)
	}
	// Identification arrives later; first_seen must not move forward.
	snip := &openlcb.SNIP{Manufacturer: "mikesmitty", Model: "CAN Bus Outpost", SoftwareVersion: "0.2.1", UserName: "Yard"}
	t1 := t0.Add(time.Hour)
	if err := s.SaveNode(ctx, NodeRecord{ID: id, FirstSeen: t1, LastSeen: t1, SNIP: snip, Protocols: openlcb.ProtocolCDI}); err != nil {
		t.Fatal(err)
	}
	// A later save without SNIP keeps the stored one.
	if err := s.SaveNode(ctx, NodeRecord{ID: id, FirstSeen: t1, LastSeen: t1.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.Nodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes = %d", len(nodes))
	}
	n := nodes[0]
	if n.ID != id || !n.FirstSeen.Equal(t0) || !n.LastSeen.Equal(t1.Add(time.Minute)) || n.Protocols != openlcb.ProtocolCDI {
		t.Errorf("node = %+v", n)
	}
	if n.SNIP == nil || n.SNIP.UserName != "Yard" {
		t.Errorf("snip = %+v", n.SNIP)
	}

	if rec, err := s.CDI(ctx, id); err != nil || rec != nil {
		t.Errorf("CDI before save = %v, %v", rec, err)
	}
	if err := s.SaveCDI(ctx, CDIRecord{NodeID: id, SoftwareVersion: "0.2.1", XML: []byte("<cdi/>"), FetchedAt: t1}); err != nil {
		t.Fatal(err)
	}
	rec, err := s.CDI(ctx, id)
	if err != nil || rec == nil || string(rec.XML) != "<cdi/>" || rec.SoftwareVersion != "0.2.1" || !rec.FetchedAt.Equal(t1) {
		t.Errorf("CDI = %+v, %v", rec, err)
	}
	if err := s.SaveCDI(ctx, CDIRecord{NodeID: id, SoftwareVersion: "0.3.0", XML: []byte("<cdi></cdi>")}); err != nil {
		t.Fatal(err)
	}
	if rec, _ := s.CDI(ctx, id); rec.SoftwareVersion != "0.3.0" {
		t.Errorf("CDI not replaced: %+v", rec)
	}
	if err := s.SaveCDI(ctx, CDIRecord{NodeID: 0x0A0000000099, XML: []byte("x")}); err == nil {
		t.Error("CDI for an unknown node should fail the foreign key")
	}
	if err := s.DeleteNode(ctx, id); err != nil {
		t.Fatal(err)
	}
	if rec, _ := s.CDI(ctx, id); rec != nil {
		t.Error("CDI survived node deletion")
	}
	if nodes, _ := s.Nodes(ctx); len(nodes) != 0 {
		t.Error("node not deleted")
	}
	if v, _ := s.SchemaVersion(ctx); v != 3 {
		t.Errorf("schema version = %d, want 3", v)
	}
}
