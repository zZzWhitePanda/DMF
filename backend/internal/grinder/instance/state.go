package instance

import (
	"sync"
	"time"
)

// State tracks per-account runtime data: cooldowns, pause flags, balance guesses.
type State struct {
	mu          sync.Mutex
	cooldowns   map[string]time.Time
	paused      bool // user-triggered pause (q/esc on dashboard)
	breakPaused bool // humanized-break-triggered pause (scheduler)
	balance     int

	// handledRandomEvents tracks message IDs we've already responded to as
	// a random event. Prevents double-fire when the stuck-detector resync
	// replays recent DM messages (an edited event-result message often
	// still matches the detector by title + still has the original role
	// ping in mention_roles, so a one-shot responder would hit it twice).
	// Bounded size: cleared when it crosses 500 entries since memory is
	// the only concern — old events aren't needed once a few hundred
	// messages have gone by.
	handledRandomEvents map[string]bool
}

// NewState constructs an empty state.
func NewState() *State {
	return &State{cooldowns: make(map[string]time.Time)}
}

// SetCooldown records that a command cannot run again until `until`.
func (s *State) SetCooldown(cmd string, until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cooldowns[cmd] = until
}

// Ready reports whether a command is ready to run now.
func (s *State) Ready(cmd string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.cooldowns[cmd]
	if !ok {
		return true
	}
	return time.Now().After(t)
}

// NextReady returns the duration until the command is ready (0 if already ready).
func (s *State) NextReady(cmd string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.cooldowns[cmd]
	if !ok {
		return 0
	}
	d := time.Until(t)
	if d < 0 {
		return 0
	}
	return d
}

// Pause halts command execution until Resume is called.
func (s *State) Pause() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = true
}

// Resume clears a pause.
func (s *State) Resume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = false
}

// IsPaused reports the current pause state (either user or break).
func (s *State) IsPaused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paused || s.breakPaused
}

// PauseBreak marks the humanized-break-triggered pause.
func (s *State) PauseBreak() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.breakPaused = true
}

// ResumeBreak clears the humanized-break-triggered pause.
func (s *State) ResumeBreak() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.breakPaused = false
}

// AddBalance adds n to the tracked balance guess.
func (s *State) AddBalance(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.balance += n
}

// Balance returns the tracked balance guess.
func (s *State) Balance() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.balance
}

// MarkRandomEventHandled records that we responded to msgID as a random
// event. Subsequent calls to HasHandledRandomEvent for the same ID
// return true until the bounded map rolls over.
func (s *State) MarkRandomEventHandled(msgID string) {
	if msgID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handledRandomEvents == nil {
		s.handledRandomEvents = make(map[string]bool)
	}
	if len(s.handledRandomEvents) >= 500 {
		s.handledRandomEvents = make(map[string]bool)
	}
	s.handledRandomEvents[msgID] = true
}

// HasHandledRandomEvent reports whether msgID has already triggered a
// random-event auto-response in this session.
func (s *State) HasHandledRandomEvent(msgID string) bool {
	if msgID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handledRandomEvents[msgID]
}
