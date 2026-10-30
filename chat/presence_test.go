package chat

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIdleGoesAwayAndTypingComesBack(t *testing.T) {
	b := &fakeBackend{presence: make(chan bool, 4)}
	s, _ := startStore(t, b)
	s.SetAutoBack(true)
	s.SetAutoAway(20 * time.Millisecond)
	if away := next(t, b.presence); !away || !s.Away() {
		t.Fatal("idle didn't set the user away")
	}
	s.Typed()
	if away := next(t, b.presence); away {
		t.Fatal("typing didn't bring the user back")
	}
	if s.Away() {
		t.Fatal("store still says away after typing")
	}
}

func TestActivityPostponesAutoAway(t *testing.T) {
	b := &fakeBackend{presence: make(chan bool, 4)}
	s, _ := startStore(t, b)
	s.SetAutoAway(60 * time.Millisecond)
	for range 4 {
		time.Sleep(30 * time.Millisecond)
		s.Activity()
	}
	select {
	case <-b.presence:
		t.Fatal("went away while active")
	default:
	}
}

func TestTypingWithoutAutoBackStaysAway(t *testing.T) {
	b := &fakeBackend{presence: make(chan bool, 4)}
	s, _ := startStore(t, b)
	if err := s.SetAway(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	next(t, b.presence)
	s.Typed()
	select {
	case <-b.presence:
		t.Fatal("typing changed presence with auto-back off")
	case <-time.After(20 * time.Millisecond):
	}
	if !s.Away() {
		t.Fatal("no longer away")
	}
}

func TestSetAwayRevertsWhenSlackRefuses(t *testing.T) {
	s, _ := startStore(t, &fakeBackend{awayErr: errors.New("missing_scope")})
	if err := s.SetAway(context.Background(), true); err == nil {
		t.Fatal("no error from a refused SetAway")
	}
	if s.Away() {
		t.Fatal("still away after Slack refused")
	}
}

// next is the next presence the store set, failing after a second.
func next(t *testing.T, presence chan bool) bool {
	t.Helper()
	select {
	case away := <-presence:
		return away
	case <-time.After(time.Second):
		t.Fatal("presence never changed")
		return false
	}
}
