package store

import (
	"context"
	"testing"
	"time"
)

func TestTraces(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t)
	base := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for i := range 5 {
		rec := TraceRecord{
			ID: "t" + string(rune('a'+i)), AutomationID: "close_turnout",
			StartedAt: base.Add(time.Duration(i) * time.Minute), Result: "running", JSON: []byte(`{"n":` + string(rune('0'+i)) + `}`),
		}
		if err := s.SaveTrace(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveTrace(ctx, TraceRecord{ID: "other", AutomationID: "other", StartedAt: base, Result: "finished", JSON: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}

	// Finishing a trace replaces it.
	done := TraceRecord{ID: "te", AutomationID: "close_turnout", StartedAt: base.Add(4 * time.Minute), FinishedAt: base.Add(5 * time.Minute), Result: "finished", JSON: []byte(`{"n":4,"done":true}`)}
	if err := s.SaveTrace(ctx, done); err != nil {
		t.Fatal(err)
	}
	got, err := s.Trace(ctx, "te")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Result != "finished" || !got.FinishedAt.Equal(done.FinishedAt) || string(got.JSON) != string(done.JSON) {
		t.Fatalf("Trace(te) = %+v", got)
	}
	if missing, err := s.Trace(ctx, "nope"); err != nil || missing != nil {
		t.Fatalf("Trace(nope) = %v, %v", missing, err)
	}

	list, err := s.Traces(ctx, "close_turnout", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "te" || list[1].ID != "td" {
		t.Fatalf("Traces(limit 2) = %v", ids(list))
	}
	all, err := s.Traces(ctx, "close_turnout", 0)
	if err != nil || len(all) != 5 {
		t.Fatalf("Traces(all) = %v, %v", ids(all), err)
	}

	latest, err := s.LatestTraces(ctx)
	if err != nil || len(latest) != 2 {
		t.Fatalf("LatestTraces = %v, %v", ids(latest), err)
	}
	for _, rec := range latest {
		if rec.AutomationID == "close_turnout" && rec.ID != "te" {
			t.Errorf("latest of close_turnout = %s", rec.ID)
		}
	}

	if err := s.PruneTraces(ctx, "close_turnout", 3); err != nil {
		t.Fatal(err)
	}
	kept, _ := s.Traces(ctx, "close_turnout", 0)
	if len(kept) != 3 || kept[2].ID != "tc" {
		t.Fatalf("after prune: %v", ids(kept))
	}
	if other, _ := s.Traces(ctx, "other", 0); len(other) != 1 {
		t.Fatalf("prune touched another automation: %v", ids(other))
	}

	if err := s.DeleteTraces(ctx, "close_turnout"); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.Traces(ctx, "close_turnout", 0); len(left) != 0 {
		t.Fatalf("after delete: %v", ids(left))
	}
}

func ids(recs []TraceRecord) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	return out
}
