package chat

import (
	"context"
	"log"
	"time"
)

// Away returns true if the user is set away in slack
func (s *Store) Away() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.away }

func (s *Store) SetAway(ctx context.Context, away bool) error {
	s.mu.Lock()
	was := s.away
	s.away = away
	s.mu.Unlock()
	s.notify(Change{})
	if err := s.backend.SetAway(ctx, away); err != nil {
		s.mu.Lock()
		s.away = was
		s.mu.Unlock()
		s.notify(Change{})
		return err
	}
	return nil
}

// 0 to turn off autoaway
func (s *Store) AutoAway() time.Duration { s.mu.Lock(); defer s.mu.Unlock(); return s.autoAway }
func (s *Store) AutoBack() bool          { s.mu.Lock(); defer s.mu.Unlock(); return s.autoBack }

func (s *Store) SetAutoAway(after time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoAway = after
	s.resetIdleLocked()
}

func (s *Store) SetAutoBack(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoBack = on
}

// Activity restarts the idle countdown
func (s *Store) Activity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetIdleLocked()
}

// Typed is when the user is typing which also counts towards activity
func (s *Store) Typed() {
	s.mu.Lock()
	s.resetIdleLocked()
	back := s.away && s.autoBack
	s.mu.Unlock()
	if back {
		go s.autoSet(false)
	}
}

func (s *Store) resetIdleLocked() {
	if s.idle != nil {
		s.idle.Stop()
	}
	if s.autoAway > 0 && !s.stopped {
		s.idle = time.AfterFunc(s.autoAway, func() {
			if !s.Away() {
				s.autoSet(true)
			}
		})
	}
}

func (s *Store) autoSet(away bool) {
	if err := s.SetAway(context.Background(), away); err != nil {
		log.Printf("presence: %v", err)
	}
}
