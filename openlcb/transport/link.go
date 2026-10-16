// Package transport connects Interlock to CAN segments: GridConnect text
// over serial ports and TCP, with reconnection, and a TCP listener that
// lets other tools attach to the hub.
package transport

import (
	"context"
	"sync"
	"time"

	"github.com/mikesmitty/interlock/openlcb/can"
)

// Link is one connection carrying CAN frames.
type Link interface {
	// Name identifies the link in logs, the monitor and the API.
	Name() string
	// Send queues a frame for transmission. It returns quickly; a link
	// that cannot keep up drops frames and counts them.
	Send(f can.Frame) error
	// Run operates the link until ctx ends, handing every received frame
	// to recv. recv must not block. Run returns nil when ctx ends and an
	// error when the link cannot continue.
	Run(ctx context.Context, recv func(can.Frame)) error
	// Status reports the link's state for the UI.
	Status() Status
}

// Status is a snapshot of a link.
type Status struct {
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	Target      string    `json:"target"`
	Connected   bool      `json:"connected"`
	Since       time.Time `json:"since,omitzero"`
	LastError   string    `json:"last_error,omitempty"`
	FramesIn    uint64    `json:"frames_in"`
	FramesOut   uint64    `json:"frames_out"`
	Dropped     uint64    `json:"dropped"`
	ParseErrors uint64    `json:"parse_errors"`
	Reconnects  uint64    `json:"reconnects"`
}

// stats is the mutable part of a Status.
type stats struct {
	mu          sync.Mutex
	connected   bool
	since       time.Time
	lastError   string
	framesIn    uint64
	framesOut   uint64
	dropped     uint64
	parseErrors uint64
	reconnects  uint64
}

func (s *stats) setConnected(connected bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if connected && !s.connected {
		s.since = time.Now()
	}
	s.connected = connected
	if err != nil {
		s.lastError = err.Error()
	}
}

func (s *stats) fill(st *Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st.Connected = s.connected
	st.Since = s.since
	st.LastError = s.lastError
	st.FramesIn = s.framesIn
	st.FramesOut = s.framesOut
	st.Dropped = s.dropped
	st.ParseErrors = s.parseErrors
	st.Reconnects = s.reconnects
}
