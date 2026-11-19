package main

import (
	"context"
	"testing"
	"time"

	"github.com/nullentropy/caution"
	"github.com/nullentropy/clack/chat"
)

type twoArthurs struct{ *chat.Demo }

func (d twoArthurs) Bootstrap(ctx context.Context) (chat.Snapshot, error) {
	snap, err := d.Demo.Bootstrap(ctx)
	snap.Users = append(snap.Users, chat.User{ID: "U9", Name: "agrajag", RealName: "Agrajag"})
	return snap, err
}

func TestTabCompletesNames(t *testing.T) {
	store := chat.NewStore(twoArthurs{&chat.Demo{}}, t.TempDir())
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := &window{store: store}
	field := caution.TextField("")
	tab := w.onTab(field, func() string { return "C1" })
	value := func() string { s, _ := field.Prop("value").(string); return s }

	press := func(shift bool, text string, caret int) (bool, string) {
		kept := tab(shift, text, caret)
		return kept, value()
	}
	if kept, _ := press(false, "", 0); kept {
		t.Fatal("Tab in an empty box should move focus")
	}
	if kept, _ := press(false, "hello", 2); kept {
		t.Fatal("Tab with the caret inside a word should move focus")
	}
	if kept, got := press(false, "hi a", 4); !kept || got != "hi @Agrajag " {
		t.Fatalf("a<Tab> gave %q (kept %v), want \"hi @Agrajag \"", got, kept)
	}
	if _, got := press(false, value(), 12); got != "hi @Arthur Dent " {
		t.Fatalf("Tab again gave %q, want the next match", got)
	}
	if _, got := press(true, value(), 16); got != "hi @Agrajag " {
		t.Fatalf("Shift+Tab gave %q, want the previous match", got)
	}
	if _, got := press(false, "@fo, look", 3); got != "@Ford Prefect, look" {
		t.Fatalf("completing before text gave %q", got)
	}
	field.SetValueNow("zz")
	if kept, got := press(false, "zz", 2); !kept || got != "zz" {
		t.Fatalf("no match: kept %v, value %q; want the key kept and the text alone", kept, got)
	}
}

func TestTypedMentionsLink(t *testing.T) {
	store := chat.NewStore(twoArthurs{&chat.Demo{}}, t.TempDir())
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := &window{store: store}
	ms := w.mentionables("C1")
	cases := map[string]string{
		"@Arthur Dent hi @Arthur Dentish @Ford Prefect": "<@U2> hi <@U2> Dentish <@U3>",
		"@arthur look":                         "<@U2> look",
		"@zaphod beeblebrox, and @zaphod":      "<@U1>, and <@U1>",
		"@here @channel @everyone":             "<!here> <!channel> <!everyone>",
		"@crew standup":                        "<!subteam^S1> standup",
		"mail ford@example.com":                "mail ford@example.com",
		"`@ford` and ```@ford\n@here``` @ford": "`@ford` and ```@ford\n@here``` <@U3>",
		"@nobody here":                         "@nobody here",
	}
	for in, want := range cases {
		if got := linkMentions(in, ms); got != want {
			t.Errorf("linkMentions(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLinkMentionsPrefersTheLongestThenTheFirst(t *testing.T) {
	ms := []mentionable{
		{keys: []string{"Arthur"}, ref: "<@U1>"},
		{keys: []string{"Zaphod Beeblebrox"}, ref: "<@U2>"},
		{keys: []string{"Arthur"}, ref: "<@U5>"},
		{keys: []string{"arthur"}, ref: "<!subteam^S1>"},
	}
	if got := linkMentions("@Zaphod Beeblebrox and @Arthur", ms); got != "<@U2> and <@U1>" {
		t.Fatalf("got %q", got)
	}
}

type dentTwins struct{ *chat.Demo }

func (d dentTwins) Bootstrap(ctx context.Context) (chat.Snapshot, error) {
	snap, err := d.Demo.Bootstrap(ctx)
	snap.Users = append(snap.Users, chat.User{ID: "U7", Name: "arthur2", RealName: "Arthur Dent"})
	return snap, err
}

func TestTypedNameGoesToTheLatestSpeaker(t *testing.T) {
	store := chat.NewStore(dentTwins{&chat.Demo{}}, t.TempDir())
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := &window{store: store}
	for _, u := range []string{"U7", "U2", "U7"} {
		store.Apply(chat.Posted{Channel: "C1", Msg: chat.Message{TS: chat.TSFromTime(time.Now()), User: u, Text: "hi"}})
		time.Sleep(time.Millisecond)
	}
	if got := linkMentions("@Arthur Dent", w.mentionables("C1")); got != "<@U7>" {
		t.Fatalf("got %q, want the Arthur Dent who spoke last", got)
	}
}
