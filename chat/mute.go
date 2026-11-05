package chat

import (
	"maps"
	"slices"
	"strings"
)

// Everywhere mutes someone in every channel but not in DMs
const Everywhere = "*"

// Mute hides user's messages in channel or everywhere
func (s *Store) Mute(user, channel string) {
	s.mu.Lock()
	s.mute(user, channel)
	s.saveLocked()
	s.mu.Unlock()
	s.notify(Change{})
}

// Unmute shows user's messages in again
func (s *Store) Unmute(user, channel string) {
	s.mu.Lock()
	if channel == Everywhere {
		delete(s.muted, user)
	} else if in := s.muted[user]; in != nil {
		delete(in, channel)
		if len(in) == 0 {
			delete(s.muted, user)
		}
	}
	s.saveLocked()
	s.mu.Unlock()
	s.notify(Change{})
}

// Muted reports if user's messages are hidden in channel
func (s *Store) Muted(channel, user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isMuted(channel, user)
}

// Mutes lists the channels each muted user is muted in
func (s *Store) Mutes() map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutesLocked()
}

func (s *Store) mute(user, channel string) {
	if s.muted[user] == nil {
		s.muted[user] = map[string]bool{}
	}
	s.muted[user][channel] = true
}

func (s *Store) mutesLocked() map[string][]string {
	if len(s.muted) == 0 {
		return nil
	}
	out := make(map[string][]string, len(s.muted))
	for user, in := range s.muted {
		out[user] = slices.Sorted(maps.Keys(in))
	}
	return out
}

func (s *Store) isMuted(channel, user string) bool {
	in := s.muted[user]
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
