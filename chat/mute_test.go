package chat

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestMutedPeopleStayQuiet(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(&Demo{}, dir)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var alerts []Alert
	s.OnAlert(func(a Alert) { alerts = append(alerts, a) })
	post := func(channel, text string) Message {
		m := Message{TS: TSFromTime(time.Now().Add(time.Second)), User: "U3", Text: text}
		s.Apply(Posted{Channel: channel, Msg: m})
		time.Sleep(time.Millisecond) // a later TS for the next post
		return m
	}
	shows := func(channel string, m Message) bool {
		msgs, _ := s.Timeline(channel)
		return slices.ContainsFunc(msgs, func(x Message) bool { return x.TS == m.TS })
	}

	s.Mute("U3", "C1")
	general := post("C1", "<@U0> the Vogons are here")
	engineering := post("C2", "Nutri-Matic is down again")
	if shows("C1", general) || s.Unread("C1") != 0 || s.UnreadMentions("C1") != 0 || len(alerts) != 0 {
		t.Errorf("Ford muted in #general still shows there: shown %v, unread %d, mentions %d, alerts %d",
			shows("C1", general), s.Unread("C1"), s.UnreadMentions("C1"), len(alerts))
	}
	if !shows("C2", engineering) || s.Unread("C2") != 1 {
		t.Error("muting Ford in #general hid him in #engineering")
	}

	s.Mute("U3", Everywhere)
	dm := post("D3", "towel?")
	if shows("C2", engineering) || s.Unread("C2") != 0 {
		t.Error("Ford muted everywhere still shows in #engineering")
	}
	if !shows("D3", dm) || len(alerts) != 1 {
		t.Errorf("muting everywhere hid Ford's DM: shown %v, alerts %d", shows("D3", dm), len(alerts))
	}

	again := NewStore(&Demo{}, dir)
	if err := again.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !again.Muted("C1", "U3") || !again.Muted("C5", "U3") || again.Muted("D3", "U3") {
		t.Errorf("mutes after relaunch = %v", again.Mutes())
	}

	s.Unmute("U3", "C1")
	if !s.Muted("C1", "U3") {
		t.Error("unmuting Ford in #general undid his mute everywhere")
	}
	s.Unmute("U3", Everywhere)
	if !shows("C1", general) || !shows("C2", engineering) || len(s.Mutes()) != 0 {
		t.Errorf("after /unmute -a Ford is still hidden: %v", s.Mutes())
	}
}
