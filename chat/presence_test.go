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

func TestPresencePollMarksOpenDirectMessagePeersAway(t *testing.T) {
	b := &fakeBackend{away: map[string]bool{"U1": true}}
	s, changes := startStore(t, b)
	s.SetOpen("D1", true)
	s.checkPresence(context.Background())
	if !s.UserAway("U1") {
		t.Fatal("Zaphod's DM is open and he's away, but the store says present")
	}
	for {
		select {
		case c := <-changes:
			if c.User == "U1" {
				return
			}
		case <-time.After(time.Second):
			t.Fatal("no change for Zaphod's presence")
		}
	}
}

func TestUserChangedUpdatesStatusUntilItExpires(t *testing.T) {
	s, _ := startStore(t, &fakeBackend{})
	now := time.Now()
	s.Apply(UserChanged{User: User{ID: "U3", RealName: "Ford Prefect", StatusEmoji: ":thumbsup:", Status: "Hitching a ride", StatusUntil: now.Add(time.Hour)}})
	u, _ := s.User("U3")
	if got := u.StatusLine(now); got != ":thumbsup: Hitching a ride" {
		t.Fatalf("status line %q", got)
	}
	if got := u.StatusLine(now.Add(2 * time.Hour)); got != "" {
		t.Fatalf("expired status still shows %q", got)
	}
}
