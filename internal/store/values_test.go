package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/openlcb"
)

func TestNodeValues(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id := openlcb.NodeID(0x0A0000000042)
	t0 := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	if rec, err := s.NodeValues(ctx, id); err != nil || rec != nil {
		t.Errorf("values before save = %v, %v", rec, err)
	}
	if err := s.SaveNodeValues(ctx, ValuesRecord{NodeID: id, Values: map[string]string{"253:132": "x"}}); err == nil {
		t.Error("values for an unknown node should fail the foreign key")
	}
	if err := s.SaveNode(ctx, NodeRecord{ID: id, FirstSeen: t0, LastSeen: t0}); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"253:132": "0A.00.00.00.00.42.00.00", "253:540": "1"}
	if err := s.SaveNodeValues(ctx, ValuesRecord{NodeID: id, SoftwareVersion: "0.2.1", Values: values, ReadAt: t0}); err != nil {
		t.Fatal(err)
	}
	rec, err := s.NodeValues(ctx, id)
	if err != nil || rec == nil {
		t.Fatalf("values = %v, %v", rec, err)
	}
	if rec.SoftwareVersion != "0.2.1" || !rec.ReadAt.Equal(t0) || len(rec.Values) != 2 || rec.Values["253:540"] != "1" {
		t.Errorf("record = %+v", rec)
	}
	if err := s.SaveNodeValues(ctx, ValuesRecord{NodeID: id, SoftwareVersion: "0.3.0", Values: map[string]string{"253:132": "y"}}); err != nil {
		t.Fatal(err)
	}
	if rec, _ := s.NodeValues(ctx, id); rec.SoftwareVersion != "0.3.0" || len(rec.Values) != 1 {
		t.Errorf("values not replaced: %+v", rec)
	}
	if err := s.DeleteNodeValues(ctx, id); err != nil {
		t.Fatal(err)
	}
	if rec, _ := s.NodeValues(ctx, id); rec != nil {
		t.Error("values survived deletion")
	}
	if err := s.SaveNodeValues(ctx, ValuesRecord{NodeID: id, Values: values}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNode(ctx, id); err != nil {
		t.Fatal(err)
	}
	if rec, _ := s.NodeValues(ctx, id); rec != nil {
		t.Error("values survived node deletion")
	}
}
