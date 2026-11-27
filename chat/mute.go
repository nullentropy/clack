package chat

import (
	"maps"
	"slices"
	"strings"
)

// Everywhere mutes someone in every channel except DMs.
const Everywhere = "*"

func (s *Store) Mute(user, channel string) {
	s.mu.Lock()
	s.mute(user, channel)
	s.saveLocked()
	s.mu.Unlock()
	s.notify(Change{})
}

func (s *Store) Unmute(user, channel string) {
	s.mu.Lock()
	if channel == Everywhere {
		delete(s.mutedIn, user)
	} else if in := s.mutedIn[user]; in != nil {
		delete(in, channel)
		if len(in) == 0 {
			delete(s.mutedIn, user)
		}
	}
	s.saveLocked()
	s.mu.Unlock()
	s.notify(Change{})
}

func (s *Store) Muted(channel, user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isMuted(channel, user)
}

// Mutes maps each muted user to the channels they are muted in.
func (s *Store) Mutes() map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutesLocked()
}

func (s *Store) mute(user, channel string) {
	if s.mutedIn[user] == nil {
		s.mutedIn[user] = map[string]bool{}
	}
	s.mutedIn[user][channel] = true
}

func (s *Store) mutesLocked() map[string][]string {
	if len(s.mutedIn) == 0 {
		return nil
	}
	out := make(map[string][]string, len(s.mutedIn))
	for user, in := range s.mutedIn {
		out[user] = slices.Sorted(maps.Keys(in))
	}
	return out
}

func (s *Store) isMuted(channel, user string) bool {
	in := s.mutedIn[user]
	if in[channel] {
		return true
	}
	if !in[Everywhere] {
		return false
	}
	c, known := s.channels[channel]
	return !c.Kind.IsDM() && (known || !strings.HasPrefix(channel, "D"))
}

func (s *Store) visible(channel string, msgs []Message) []Message {
	return slices.DeleteFunc(slices.Clone(msgs), func(m Message) bool { return s.isMuted(channel, m.User) })
}
