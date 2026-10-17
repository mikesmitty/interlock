package decoder

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

// CVBus reads and writes single CVs on one decoder, on the programming
// track or on the main.
type CVBus interface {
	ReadCV(ctx context.Context, cv int) (int, error)
	WriteCV(ctx context.Context, cv, value int) error
}

// indexWindow is how long index writes are remembered: a read or write
// that needs the same index values within it skips writing them again.
const indexWindow = time.Second

// tcsPause separates the steps of the TCS four-CV protocol.
const tcsPause = 10 * time.Millisecond

// Session runs planned accesses against one decoder. It remembers the
// index CVs it last wrote, so a page of indexed CVs costs one index write
// instead of one per CV.
type Session struct {
	bus   CVBus
	sleep func(context.Context, time.Duration) error

	mu     sync.Mutex
	last   []IndexWrite
	lastAt time.Time
}

// NewSession starts a session on a bus.
func NewSession(bus CVBus) *Session {
	return &Session{bus: bus, sleep: sleepCtx}
}

// Forget drops what the session knows about the index CVs, after
// something else wrote them (an identify, a factory reset).
func (s *Session) Forget() {
	s.mu.Lock()
	s.last = nil
	s.mu.Unlock()
}

// Read reads the value a CV name holds.
func (s *Session) Read(ctx context.Context, a Access, cache bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch a.Kind {
	case AccessTCS16:
		steps := []IndexWrite{{tcsIndex1, a.TCS[0] + 100}, {tcsIndex2, a.TCS[1]}}
		if err := s.writes(ctx, steps); err != nil {
			return 0, err
		}
		// The strobe write may be refused; TCS decoders answer anyway.
		_ = s.bus.WriteCV(ctx, tcsLSB, 0)
		if err := s.sleep(ctx, tcsPause); err != nil {
			return 0, err
		}
		msb, err := s.bus.ReadCV(ctx, tcsMSB)
		if err != nil {
			return 0, err
		}
		lsb, err := s.bus.ReadCV(ctx, tcsLSB)
		if err != nil {
			return 0, err
		}
		s.last = nil
		return msb<<8 | lsb, nil
	case AccessTCS8:
		steps := []IndexWrite{{tcsIndex1, a.TCS[0] + 100}, {tcsMSB, a.TCS[1]}, {tcsLSB, a.TCS[2]}}
		if err := s.writes(ctx, steps); err != nil {
			return 0, err
		}
		s.last = nil
		return s.bus.ReadCV(ctx, tcsIndex2)
	}
	if err := s.index(ctx, a, cache); err != nil {
		return 0, err
	}
	v, err := s.bus.ReadCV(ctx, a.CV)
	if err != nil {
		s.last = nil
		return 0, err
	}
	s.touched(a)
	return v, nil
}

// Write writes a value to a CV name.
func (s *Session) Write(ctx context.Context, a Access, value int, cache bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch a.Kind {
	case AccessTCS16:
		if value < 0 || value > 0xFFFF {
			return fmt.Errorf("CV %s: %d does not fit in 16 bits", a.Name, value)
		}
		s.last = nil
		return s.writes(ctx, []IndexWrite{{tcsIndex1, a.TCS[0]}, {tcsIndex2, a.TCS[1]}, {tcsMSB, value >> 8}, {tcsLSB, value & 0xFF}})
	case AccessTCS8:
		if value < 0 || value > 0xFF {
			return fmt.Errorf("CV %s: %d does not fit in a byte", a.Name, value)
		}
		s.last = nil
		return s.writes(ctx, []IndexWrite{{tcsIndex1, a.TCS[0]}, {tcsIndex2, value}, {tcsMSB, a.TCS[1]}, {tcsLSB, a.TCS[2]}})
	}
	if value < 0 || value > 0xFF {
		return fmt.Errorf("CV %s: %d does not fit in a byte", a.Name, value)
	}
	if err := s.index(ctx, a, cache); err != nil {
		return err
	}
	if err := s.bus.WriteCV(ctx, a.CV, value); err != nil {
		s.last = nil
		return err
	}
	s.touched(a)
	return nil
}

// index writes the index CVs an access needs, unless the same values
// went to the same CVs moments ago.
func (s *Session) index(ctx context.Context, a Access, cache bool) error {
	if len(a.Index) == 0 {
		return nil
	}
	if cache && slices.Equal(s.last, a.Index) && time.Since(s.lastAt) < indexWindow {
		return nil
	}
	s.last = nil
	if err := s.writes(ctx, a.Index); err != nil {
		return err
	}
	s.last = slices.Clone(a.Index)
	return nil
}

// touched records the end of an access: indexed ones restart the window;
// a plain access to an index CV changes what the decoder holds there.
func (s *Session) touched(a Access) {
	if len(a.Index) > 0 {
		s.lastAt = time.Now()
		return
	}
	for _, w := range s.last {
		if w.CV == a.CV {
			s.last = nil
			return
		}
	}
}

func (s *Session) writes(ctx context.Context, steps []IndexWrite) error {
	for i, w := range steps {
		if i > 0 && w.CV >= tcsIndex1 && w.CV <= tcsLSB {
			if err := s.sleep(ctx, tcsPause); err != nil {
				return err
			}
		}
		if err := s.bus.WriteCV(ctx, w.CV, w.Value); err != nil {
			s.last = nil
			return fmt.Errorf("write index CV %d: %w", w.CV, err)
		}
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
