package slackapi

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/nullentropy/clack/chat"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func TestGroupMembers(t *testing.T) {
	handles := map[string]string{"zaphod": "U1", "arthur": "U2", "you": "U0", "mr.prosser": "U4", "vogon-jeltz": "U5"}
	cases := map[string][]string{
		"mpdm-zaphod--arthur--you-1":           {"U1", "U2"},
		"mpdm-mr.prosser--vogon-jeltz--you-12": {"U4", "U5"},
		"general":                              nil,
	}
	for name, want := range cases {
		if got := groupMembers(name, handles, "U0"); !slices.Equal(got, want) {
			t.Errorf("groupMembers(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDirectMessageEventPosts(t *testing.T) {
	payload := `{
		"token": "x", "team_id": "T1", "api_app_id": "A1", "type": "event_callback",
		"event_id": "Ev1", "event_time": 1700000000,
		"event": {
			"type": "message", "channel": "D1", "channel_type": "im",
			"user": "U2", "text": "hi there", "ts": "1700000000.000100",
			"event_ts": "1700000000.000100"
		}
	}`
	parsed, err := slackevents.ParseEvent(json.RawMessage(payload), slackevents.OptionNoVerifyToken())
	if err != nil {
		t.Fatal(err)
	}
	var got []chat.Event
	(&Backend{}).handle(nil, socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: parsed}, func(e chat.Event) {
		got = append(got, e)
	})
	want := chat.Posted{Channel: "D1", Msg: chat.Message{TS: "1700000000.000100", User: "U2", Text: "hi there"}}
	if len(got) != 1 {
		t.Fatalf("events = %+v, want one Posted", got)
	}
	if p, ok := got[0].(chat.Posted); !ok || p.Channel != want.Channel || !p.Msg.Equal(want.Msg) {
		t.Fatalf("event = %+v, want %+v", got[0], want)
	}
}
