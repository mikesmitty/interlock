package train

import "time"

// SetCVStoreClock makes a store read the time from now, for tests.
func SetCVStoreClock(s *CVStore, now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}
