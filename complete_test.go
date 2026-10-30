package main

import (
	"context"
	"testing"

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
	w := &window{store: store, mentions: map[string]string{}}
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
	if got := linkMentions("@Arthur Dent hi @Arthur Dentish @Ford Prefect", w.mentions); got != "<@U2> hi @Arthur Dentish <@U3>" {
		t.Fatalf("linkMentions gave %q", got)
	}
}

func TestLinkMentionsPrefersTheLongestName(t *testing.T) {
	got := linkMentions("@Zaphod Beeblebrox and @Arthur", map[string]string{"Arthur": "U1", "Zaphod Beeblebrox": "U2"})
	if got != "<@U2> and <@U1>" {
		t.Fatalf("got %q", got)
	}
}
