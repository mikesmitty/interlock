package api

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/mikesmitty/interlock/internal/clock"
)

type fakeClock struct {
	mu sync.Mutex
	st clock.State
}

func (c *fakeClock) State() clock.State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st
}

func (c *fakeClock) Apply(ch clock.Change) (clock.State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch.Time != nil {
		if len(*ch.Time) != 5 {
			return clock.State{}, &clock.InvalidError{Reason: "bad time"}
		}
		c.st.Time = *ch.Time
	}
	if ch.Running != nil {
		c.st.Running = *ch.Running
	}
	if ch.Rate != nil {
		c.st.Rate = *ch.Rate
	}
	return c.st, nil
}

func (c *fakeClock) Subscribe(func(clock.State)) func() { return func() {} }

func TestClockEndpoints(t *testing.T) {
	fc := &fakeClock{st: clock.State{Time: "06:00", Date: "2026-10-17", Rate: 4, Updated: time.Now()}}
	a := New(Deps{Node: fakeNode{}, Registry: newFakeRegistry(), Bus: &fakeBus{}, Clock: fc})
	mux := http.NewServeMux()
	a.Register(mux)
	ta := &testAPI{api: a, mux: mux}

	rec, body := ta.do(t, http.MethodGet, "/api/v1/clock", "")
	if rec.Code != http.StatusOK || body["time"] != "06:00" || body["rate"] != float64(4) || body["running"] != false {
		t.Fatalf("GET clock: %d %v", rec.Code, body)
	}
	rec, body = ta.do(t, http.MethodPost, "/api/v1/clock", `{"running":true,"rate":8,"time":"07:30"}`)
	if rec.Code != http.StatusOK || body["time"] != "07:30" || body["rate"] != float64(8) || body["running"] != true {
		t.Fatalf("POST clock: %d %v", rec.Code, body)
	}
	if rec, body := ta.do(t, http.MethodPost, "/api/v1/clock", `{"time":"7"}`); rec.Code != http.StatusBadRequest || body["error"] != "bad time" {
		t.Errorf("bad time: %d %v", rec.Code, body)
	}
	if rec, _ := ta.do(t, http.MethodPost, "/api/v1/clock", `nonsense`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad JSON: %d", rec.Code)
	}
}
