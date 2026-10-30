package chat

import "slices"

func (s *Store) countReply(channel string, m Message) {
	k := threadKey{channel, m.ThreadTS}
	switch {
	case m.User == s.snap.Self:
		s.mine[k] = true
		delete(s.newIn, k)
	case s.isMine(k):
		s.newIn[k]++
	default:
		return
	}
	s.setNewReplies(k)
}

func (s *Store) isMine(k threadKey) bool {
	if s.mine[k] {
		return true
	}
	i, ok := find(s.timeline[k.channel], k.ts)
	if !ok {
		return false
	}
	parent := s.timeline[k.channel][i]
	return parent.User == s.snap.Self || slices.Contains(parent.ReplyUsers, s.snap.Self)
}

func (s *Store) setNewReplies(k threadKey) {
	s.update(k.channel, func(msgs []Message) []Message {
		if i, ok := find(msgs, k.ts); ok {
			msgs[i].NewReplies = s.newIn[k]
		}
		return msgs
	})
}

func (s *Store) showNewReplies(channel string) {
	for k := range s.newIn {
		if k.channel == channel {
			s.setNewReplies(k)
		}
	}
}

func (s *Store) NewReplies(channel string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, c := range s.newIn {
		if k.channel == channel {
			n += c
		}
	}
	return n
}

func (s *Store) MarkThreadRead(channel string, thread TS) {
	k := threadKey{channel, thread}
	s.mu.Lock()
	if s.newIn[k] == 0 {
		s.mu.Unlock()
		return
	}
	delete(s.newIn, k)
	s.setNewReplies(k)
	s.mu.Unlock()
	s.notify(Change{Channel: channel})
}
