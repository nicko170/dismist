package controller

import "time"

// locateState runs the locate expiry timer (design §5.6 "Locate"). Locate
// itself is config: APRecord.LocateUntil makes the compiler emit
// LED.Locator=true, which is pushed like any change. When it expires the
// controller calls Options.OnLocateExpired, which clears LocateUntil and
// pushes LED.Locator=false; "Stop" clears it early the same way.
type locateState struct {
	until time.Time
	timer timerHandle
}

// maxLocate bounds a locate request (the UI offers up to 60 minutes).
const maxLocate = 24 * time.Hour

// SetLocateUntil arms (or, with a zero time, disarms) the locate expiry
// timer. A time already past fires at once.
func (s *Server) SetLocateUntil(until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if until.Equal(s.locate.until) {
		return
	}
	s.locate.until = until
	if until.IsZero() {
		s.locate.timer.stop()
		return
	}
	delay := time.Until(until)
	if delay > maxLocate {
		delay = maxLocate
	}
	if delay < 0 {
		delay = 0
	}
	s.locate.timer.set(delay, func() { s.locateExpired(until) })
}

// LocateUntil is when the AP's locate state expires (zero: not locating).
func (s *Server) LocateUntil() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.locate.until
}

func (s *Server) locateExpired(until time.Time) {
	s.mu.Lock()
	if !s.locate.until.Equal(until) {
		s.mu.Unlock()
		return
	}
	s.locate.until = time.Time{}
	s.locate.timer.timer = nil
	s.emitLocked(Event{Kind: EventAction, Severity: SeverityInfo, Title: "Locate expired"})
	s.mu.Unlock()
	s.flush()
	if s.onLocateExpired != nil {
		s.onLocateExpired(s.auth.ID)
	}
}
