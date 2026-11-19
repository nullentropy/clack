package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/nullentropy/caution"
	"github.com/nullentropy/clack/chat"
)

func TestMentionPopup(t *testing.T) {
	store := chat.NewStore(twoArthurs{&chat.Demo{}}, t.TempDir())
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := &window{store: store}
	field := caution.TextField("")
	host := caution.Panel()
	p := w.newMentionPopup(field, func() string { return "C1" }, host)
	value := func() string { s, _ := field.Prop("value").(string); return s }
	var sent []string
	submit := p.onSubmit(func(text string) { sent = append(sent, text) })
	tab := p.onTab(func(bool, string, int) bool { return false })

	for _, text := range []string{"", "@", "hi", "hi @zzz", "@ar done", "mail a@b"} {
		if p.update(text); p.open() {
			t.Errorf("popup open for %q", text)
		}
	}
	p.update("hi @a")
	if !slices.Equal(p.matches, []string{"Agrajag", "Arthur Dent"}) || len(host.Children()) != 1 {
		t.Fatalf("matches = %q, shown %d", p.matches, len(host.Children()))
	}
	p.move(+1)
	submit("hi @a")
	if got := value(); got != "hi @Arthur Dent " || len(sent) != 0 || p.open() || len(host.Children()) != 0 {
		t.Fatalf("Enter in the popup gave %q, sent %q, open %v", got, sent, p.open())
	}
	submit("hi @Arthur Dent ")
	if len(sent) != 1 {
		t.Fatal("Enter with the popup closed should send")
	}

	p.update("@cr")
	if !slices.Equal(p.matches, []string{"crew"}) {
		t.Fatalf("matches = %q, want the crew group", p.matches)
	}
	tab(false, "@cr", 3)
	if got := value(); got != "@crew " {
		t.Fatalf("Tab in the popup gave %q", got)
	}
	p.update("ping @he")
	if !slices.Equal(p.matches, []string{"here"}) {
		t.Fatalf("matches = %q, want here", p.matches)
	}
	// Enter can beat the debounced input that would have closed the popup
	submit("ping @here now")
	if len(sent) != 2 || sent[1] != "ping @here now" {
		t.Fatalf("sent %q, want the text sent as typed", sent)
	}
}

func TestTypingAnAtShowsSuggestionsInTheWindow(t *testing.T) {
	sc, c, mount := mountTestWindow(t)
	composer := mount.find(func(n node) bool { return n.P["placeholder"] != nil })
	if composer == nil {
		t.Fatal("no composer in the mounted window")
	}
	var find func(n *caution.Node) bool
	find = func(n *caution.Node) bool {
		if n.Prop("text") == "@Arthur Dent" {
			return true
		}
		return slices.ContainsFunc(n.Children(), find)
	}
	var before bool
	runOnSession(t, sc, func() { before = find(sc.win.root) })
	if before {
		t.Fatal("@Arthur Dent shown before typing")
	}
	if err := c.WriteJSON(map[string]any{"t": "ev", "id": composer.ID, "ev": "input", "value": "hi @ar", "seq": 1}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		var shown bool
		runOnSession(t, sc, func() { shown = find(sc.win.root) })
		if shown {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("typing @ar never showed @Arthur Dent")
		}
	}
}

func TestMentionPopupIsPainted(t *testing.T) {
	store := chat.NewStore(twoArthurs{&chat.Demo{}}, t.TempDir())
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := &window{store: store}
	host := caution.Panel()
	p := w.newMentionPopup(caution.TextField(""), func() string { return "C1" }, host)
	p.update("hi @a")
	if len(host.Children()) != 1 {
		t.Fatalf("popup shown %d times", len(host.Children()))
	}
	popup := host.Children()[0]
	if popup.Type() != caution.Panel().Type() || popup.Prop("bg") != "$panelAlt" {
		t.Fatalf("popup is a %v with bg %v, want a panel with $panelAlt", popup.Type(), popup.Prop("bg"))
	}
}

// caution fades in every inserted subtree, so a rebuilt popup flashes transparent
func TestMentionPopupMoveKeepsNodes(t *testing.T) {
	store := chat.NewStore(twoArthurs{&chat.Demo{}}, t.TempDir())
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := &window{store: store}
	host := caution.Panel()
	p := w.newMentionPopup(caution.TextField(""), func() string { return "C1" }, host)
	p.update("hi @a")
	popup := host.Children()[0]
	rows := p.list.Children()
	p.move(+1)
	if len(host.Children()) != 1 || host.Children()[0] != popup || !slices.Equal(p.list.Children(), rows) {
		t.Fatal("moving the selection rebuilt the popup")
	}
	if rows[0].Prop("bg") == "$accent" || rows[1].Prop("bg") != "$accent" {
		t.Fatalf("row bgs %v %v, want the second selected", rows[0].Prop("bg"), rows[1].Prop("bg"))
	}
}
