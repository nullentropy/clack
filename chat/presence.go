package chat

import (
	"cmp"
	"context"
	"log"
	"maps"
	"slices"
	"time"
)

// Away returns true if the user is set away in slack
func (s *Store) Away() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.away }

func (s *Store) SetAway(ctx context.Context, away bool) error {
	s.mu.Lock()
	was := s.away
	s.away = away
	self := Change{User: s.snap.Self}
	s.mu.Unlock()
	s.notify(self)
	if err := s.backend.SetAway(ctx, away); err != nil {
		s.mu.Lock()
		s.away = was
		s.mu.Unlock()
		s.notify(self)
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

type presence struct {
	away    bool
	checked time.Time // zero until polled
}

// UserAway reports whether a user is away, as of the last poll
func (s *Store) UserAway(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == s.snap.Self {
		return s.away
	}
	return s.presence[id].away
}

// slack's Events API never sends presence so we poll
// they allow about 50 calls a minute, we make 40
const (
	presenceEvery = 15 * time.Second
	presenceBatch = 10
)

func (s *Store) pollPresence(ctx context.Context) {
	tick := time.NewTicker(presenceEvery)
	defer tick.Stop()
	for {
		s.checkPresence(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// checkPresence asks slack about the watched users it checked longest ago
func (s *Store) checkPresence(ctx context.Context) {
	for _, id := range s.stalest(presenceBatch) {
		away, err := s.backend.Presence(ctx, id)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("users.getPresence %s: %v", id, err)
			continue
		}
		s.mu.Lock()
		p := s.presence[id]
		was, known := p.away, !p.checked.IsZero()
		p.checked = time.Now()
		s.presence[id] = p
		s.mu.Unlock()
		if !known || was != away {
			s.Apply(PresenceChanged{User: id, Away: away})
		}
	}
}

func (s *Store) stalest(n int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := slices.Collect(maps.Keys(s.watchedLocked()))
	slices.SortFunc(ids, func(a, b string) int {
		return cmp.Or(s.presence[a].checked.Compare(s.presence[b].checked), cmp.Compare(a, b))
	})
	return ids[:min(n, len(ids))]
}

// watchedLocked is who the user can see: the people in their open direct
// messages and the authors in the buffer they have open
func (s *Store) watchedLocked() map[string]bool {
	out := map[string]bool{}
	for id, c := range s.channels {
		if !s.open[id] {
			continue
		}
		if c.Peer != "" {
			out[c.Peer] = true
		}
		for _, m := range c.Members {
			out[m] = true
		}
	}
	for _, m := range s.timeline[s.last] {
		if m.User != "" && !m.System {
			out[m.User] = true
		}
	}
	delete(out, s.snap.Self)
	return out
}
