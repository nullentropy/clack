package chat

import (
	"context"
	"log"
	"slices"
)

func (s *Store) countReply(channel string, m Message) {
	k := threadKey{channel, m.ThreadTS}
	switch {
	case m.User == s.snap.Self:
		s.postedIn[k] = true
		delete(s.unreadReplies, k)
	case s.isMine(k):
		s.unreadReplies[k]++
		s.showParent(k)
	default:
		return
	}
	s.setNewReplies(k)
}

func (s *Store) showParent(k threadKey) {
	if _, ok := find(s.timeline[k.channel], k.ts); ok {
		return
	}
	t, loaded := s.threads[k]
	if !loaded {
		go s.LoadThread(context.Background(), k.channel, k.ts)
		return
	}
	if i, ok := find(t, k.ts); ok {
		s.timeline[k.channel] = merge(s.timeline[k.channel], t[i:i+1])
		s.setNewReplies(k)
	}
}

func (s *Store) isMine(k threadKey) bool {
	if s.postedIn[k] {
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
			msgs[i].NewReplies = s.unreadReplies[k]
		}
		return msgs
	})
}

func (s *Store) showNewReplies(channel string) {
	for k := range s.unreadReplies {
		if k.channel == channel {
			s.setNewReplies(k)
		}
	}
}

func (s *Store) NewReplies(channel string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, c := range s.unreadReplies {
		if k.channel == channel {
			n += c
		}
	}
	return n
}

func (s *Store) MarkThreadRead(channel string, thread TS) {
	k := threadKey{channel, thread}
	s.mu.Lock()
	var newest TS
	for _, m := range slices.Backward(s.threads[k]) {
		if !m.Pending {
			newest = m.TS
			break
		}
	}
	push := newest > thread && newest > s.threadSeen[k]
	if push {
		s.threadSeen[k] = newest
	}
	cleared := s.unreadReplies[k] != 0
	if cleared {
		delete(s.unreadReplies, k)
		s.setNewReplies(k)
	}
	s.mu.Unlock()
	if push {
		go func() {
			if err := s.backend.MarkThread(context.Background(), channel, thread, newest); err != nil {
				log.Printf("mark thread %s/%s read: %v", channel, thread, err)
			}
		}()
	}
	if cleared {
		s.notify(Change{Channel: channel})
	}
}
