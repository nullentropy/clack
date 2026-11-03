package slackapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/nullentropy/clack/chat"
	"github.com/slack-go/slack"
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

func TestAppMessageShowsItsCardsUnderItsName(t *testing.T) {
	payload := `{
		"type": "event_callback", "event": {
			"type": "message", "channel": "C1", "bot_id": "B1", "text": "", "ts": "1700000000.000100",
			"bot_profile": {"id": "B1", "name": "Google Calendar"},
			"attachments": [
				{"color": "3AA3E3", "pretext": "1 minute until this event:", "title": "standup", "title_link": "https://cal/1", "text": "*Where:* zoom", "fallback": "standup"},
				{"color": "danger", "fallback": "only a fallback"}
			]
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
	want := chat.Message{TS: "1700000000.000100", Username: "Google Calendar", Attachments: []chat.Attachment{
		{Color: "#3AA3E3", Pretext: "1 minute until this event:", Title: "<https://cal/1|standup>", Text: "*Where:* zoom"},
		{Color: "#e01e5a", Text: "only a fallback"},
	}}
	if len(got) != 1 {
		t.Fatalf("events = %+v, want one Posted", got)
	}
	if p, ok := got[0].(chat.Posted); !ok || !p.Msg.Equal(want) {
		t.Fatalf("event = %+v, want %+v", got[0], want)
	}
}

func TestWebhookMessagesAreNamedByOneLookup(t *testing.T) {
	lookups := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bots.info":
			lookups++
			fmt.Fprint(w, `{"ok": true, "bot": {"id": "B2", "name": "Zoom"}}`)
		case "/conversations.history":
			fmt.Fprint(w, `{"ok": true, "messages": [
				{"type": "message", "subtype": "bot_message", "bot_id": "B2", "text": "dana has started a meeting", "ts": "2.0"},
				{"type": "message", "subtype": "bot_message", "bot_id": "B2", "text": "the meeting ended", "ts": "1.0"}
			]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	b := &Backend{api: slack.New("xoxp-x", slack.OptionAPIURL(srv.URL+"/")), bots: map[string]string{}}
	msgs, err := b.History(context.Background(), "C1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Username != "Zoom" || msgs[1].Username != "Zoom" {
		t.Fatalf("messages = %+v, want both from Zoom", msgs)
	}
	if lookups != 1 {
		t.Fatalf("bots.info called %d times, want once", lookups)
	}
}

func TestZoomMeetingsLinkToJoin(t *testing.T) {
	call := func(ended bool) string {
		return fmt.Sprintf(`{"type": "message", "subtype": "bot_message", "bot_id": "B3", "username": "Zoom",
			"text": "dana has started a meeting", "ts": "1.0", "blocks": [
				{"type": "call", "block_id": "k", "call_id": "R1", "call": {"v1": {"id": "R1",
					"name": "Zoom meeting started by dana", "display_id": "829-5567-2450",
					"join_url": "https://us02web.zoom.us/j/82955672450?pwd=abc", "has_ended": %t}}},
				{"type": "section", "block_id": "s", "text": {"type": "mrkdwn", "text": "Meeting passcode: abc"}}
			]}`, ended)
	}
	cases := map[bool]chat.Attachment{
		false: {Title: "<https://us02web.zoom.us/j/82955672450?pwd=abc|Zoom meeting started by dana>", Text: "Meeting ID: 829-5567-2450"},
		true:  {Title: "Zoom meeting started by dana", Text: "ended"},
	}
	for ended, want := range cases {
		var m slack.Msg
		if err := json.Unmarshal([]byte(call(ended)), &m); err != nil {
			t.Fatal(err)
		}
		got := (&Backend{}).message(context.Background(), m)
		if got.Text != "dana has started a meeting" || len(got.Attachments) != 1 || got.Attachments[0] != want {
			t.Errorf("ended=%t: message = %+v, want the text and a card %+v", ended, got, want)
		}
	}
}

func TestUsersPostingThroughAnAppKeepTheirOwnName(t *testing.T) {
	var m slack.Msg
	raw := `{"type": "message", "user": "U1", "bot_id": "B1", "bot_profile": {"id": "B1", "name": "clack"}, "text": "hi", "ts": "1.0"}`
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if got := (&Backend{}).message(context.Background(), m); got.User != "U1" || got.Username != "" {
		t.Fatalf("message = %+v, want U1's, the same as one sent from slack itself", got)
	}
}

func TestHuddlesShowWithTheirThread(t *testing.T) {
	payload := `{
		"type": "event_callback", "event": {
			"type": "message", "subtype": "huddle_thread", "channel": "C1", "user": "USLACKBOT", "text": "",
			"ts": "1700000000.000100", "reply_count": 3,
			"room": {"id": "R1", "created_by": "U1", "date_start": 1700000000, "has_ended": false}
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
	if len(got) != 1 {
		t.Fatalf("events = %+v, want the huddle posted", got)
	}
	p, ok := got[0].(chat.Posted)
	if !ok || !p.Msg.System || p.Msg.Text != "A huddle started" || p.Msg.Replies != 3 {
		t.Fatalf("event = %+v, want a system line for the huddle with its 3 replies", got[0])
	}
}

func TestGiphyPostsShowTheirGIF(t *testing.T) {
	payload := `{
		"type": "event_callback", "event": {
			"type": "message", "channel": "C1", "bot_id": "B2", "user": "U1", "text": "", "ts": "1700000000.000200",
			"blocks": [
				{"type": "image", "image_url": "https://media2.giphy.com/media/abc/giphy.gif?cid=1", "alt_text": "dont panic", "title": {"type": "plain_text", "text": "dont panic"}},
				{"type": "context", "elements": [{"type": "mrkdwn", "text": "Posted using /giphy"}]}
			]
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
	if len(got) != 1 {
		t.Fatalf("events = %+v, want one Posted", got)
	}
	files := got[0].(chat.Posted).Msg.Files
	if len(files) != 1 || !files[0].IsImage() || !files[0].Animated() || files[0].Anim != "https://media2.giphy.com/media/abc/giphy.gif?cid=1" || files[0].Name != "dont panic" {
		t.Fatalf("files = %+v, want the GIF", files)
	}
}

func TestOnlySlackGetsTheToken(t *testing.T) {
	for u, want := range map[string]bool{
		"https://files.slack.com/files-pri/T1-F1/a.png": true,
		"https://media2.giphy.com/media/abc/giphy.gif":  false,
		"https://slack.com.evil.example/a.png":          false,
		"http://files.slack.com/a.png":                  false,
	} {
		if slackHosted(u) != want {
			t.Errorf("slackHosted(%q) = %v, want %v", u, !want, want)
		}
	}
}
