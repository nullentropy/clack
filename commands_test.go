package main

import (
	"context"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nullentropy/caution"
	"github.com/nullentropy/clack/chat"
)

func TestFindChannel(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := &window{store: store}
	cases := map[string]string{
		"#general":    "C1",
		"Engineering": "C2",
		"zaphod":      "D1",
		"Arthur Dent": "D2",
		"#nowhere":    "",
		"":            "",
	}
	for arg, want := range cases {
		if got, _ := w.findChannel(arg); got != want {
			t.Errorf("findChannel(%q) = %q, want %q", arg, got, want)
		}
	}
}

func TestDoubleSlashIsAMessage(t *testing.T) {
	w := &window{}
	if w.command(target{channel: "C1"}, "//shrug") {
		t.Fatal("//shrug ran as a command")
	}
}

func TestParseSubst(t *testing.T) {
	cases := map[string]*subst{
		"s/teh/the":         {pattern: "teh", repl: "the"},
		"s/teh/the/":        {pattern: "teh", repl: "the"},
		"s/a/b/g":           {pattern: "a", repl: "b", global: true},
		"/s/teh/the":        {pattern: "teh", repl: "the"},
		"/s /teh/the":       {pattern: "teh", repl: "the"},
		`s/a\/b/c`:          {pattern: "a/b", repl: "c"},
		"s/typo/":           {pattern: "typo", repl: ""},
		"s/he said so":      nil,
		"s//x":              nil,
		"s/a/b/x":           nil,
		"s/a/b c/d":         nil,
		"sure/thing/really": nil,
		"/summon":           nil,
	}
	for in, want := range cases {
		got, ok := parseSubst(in)
		if want == nil {
			if ok {
				t.Errorf("parseSubst(%q) = %+v, want not a substitution", in, got)
			}
			continue
		}
		if !ok || got != *want {
			t.Errorf("parseSubst(%q) = %+v, %v; want %+v", in, got, ok, *want)
		}
	}
}

func TestSubstApply(t *testing.T) {
	cases := []struct {
		sb         subst
		in, want   string
		wantChange bool
	}{
		{subst{pattern: "o", repl: "0"}, "foo boo", "f0o boo", true},
		{subst{pattern: "o", repl: "0", global: true}, "foo boo", "f00 b00", true},
		{subst{pattern: "b[a-z]+", repl: "BAR"}, "foo bar", "foo BAR", true},
		{subst{pattern: "(", repl: "["}, "f(x)", "f[x)", true},
		{subst{pattern: "zzz", repl: "y"}, "foo", "foo", false},
	}
	for _, c := range cases {
		got, changed := c.sb.apply(c.in)
		if got != c.want || changed != c.wantChange {
			t.Errorf("%+v.apply(%q) = %q, %v; want %q, %v", c.sb, c.in, got, changed, c.want, c.wantChange)
		}
	}
}

func TestSubstituteEditsLastOwnMessage(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	changed := make(chan struct{}, 8)
	store.Subscribe(func(chat.Change) { changed <- struct{}{} })
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-changed // the demo is listening, so sends echo back
	if err := store.Send(context.Background(), "C1", "", "teh build is green"); err != nil {
		t.Fatal(err)
	}
	for len(changed) > 0 { // Send finished telling subscribers
		<-changed
	}
	w := &window{store: store}
	w.substitute(target{channel: "C1"}, subst{pattern: "teh", repl: "the"})
	<-changed
	m, ok := store.LastOwn("C1", "")
	if !ok || m.Text != "the build is green" {
		t.Fatalf("last own message = %+v, want the edited text", m)
	}
}

func TestClearHidesScrollbackUntilNewMessages(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	changed := make(chan struct{}, 8)
	store.Subscribe(func(chat.Change) { changed <- struct{}{} })
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-changed
	store.Load(context.Background(), "C1")
	<-changed
	w := &window{store: store, active: "C1", timeline: newLogView(nickWidth, func(chat.Message) string { return "" }, func(chat.Message, *chat.Message, float64) *caution.Node { return caution.Panel() })}
	w.renderTimeline()
	if len(w.timeline.shown) == 0 {
		t.Fatal("demo #general has no scrollback to clear")
	}
	w.command(target{channel: "C1"}, "/clear")
	if len(w.timeline.shown) != 0 {
		t.Fatalf("after /clear, %d messages still shown", len(w.timeline.shown))
	}
	if err := store.Send(context.Background(), "C1", "", "fresh"); err != nil {
		t.Fatal(err)
	}
	<-changed
	w.renderTimeline()
	if len(w.timeline.shown) != 1 || w.timeline.shown[0].Text != "fresh" {
		t.Fatalf("after a new message, shown = %+v, want just it", w.timeline.shown)
	}
}

func TestMsgSendsWithoutSwitching(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	changed := make(chan chat.Change, 8)
	store.Subscribe(func(c chat.Change) { changed <- c })
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-changed
	w := &window{store: store, active: "C1", timeline: testLog()}
	w.command(target{channel: "C1"}, "/msg arthur tea?")
	// the pending copy announces itself first, then the sent message
	for range 2 {
		if c := <-changed; c.Channel != "D2" {
			t.Fatalf("message landed in %q, want D2", c.Channel)
		}
		if _, ok := store.LastOwn("D2", ""); ok {
			break
		}
	}
	if m, ok := store.LastOwn("D2", ""); !ok || m.Text != "tea?" {
		t.Fatalf("last own message in D2 = %+v, want tea?", m)
	}
	if w.active != "C1" {
		t.Fatalf("active buffer = %q, want C1", w.active)
	}
}

func TestTableflipSendsTheFlip(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	changed := make(chan struct{}, 8)
	store.Subscribe(func(chat.Change) { changed <- struct{}{} })
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-changed
	w := &window{store: store, active: "C1", timeline: testLog()}
	cases := map[string]string{
		"/shrug":                 `¯\_(ツ)_/¯`,
		"/shrug dunno":           `dunno ¯\_(ツ)_/¯`,
		"/tableflip":             "(╯°□°)╯︵ ┻━┻",
		"/tableflip mondays":     "mondays (╯°□°)╯︵ ┻━┻",
		"/tableunflip":           "┬─┬ノ( º _ ºノ)",
		"/tableunflip calm down": "calm down ┬─┬ノ( º _ ºノ)",
	}
	for in, want := range cases {
		w.command(target{channel: "C1"}, in)
		for m, _ := store.LastOwn("C1", ""); m.Text != want; m, _ = store.LastOwn("C1", "") {
			<-changed
		}
	}
}

func TestTopicSetsChannelTopic(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	changed := make(chan struct{}, 8)
	store.Subscribe(func(chat.Change) { changed <- struct{}{} })
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-changed
	w := &window{store: store, timeline: testLog()}
	w.command(target{channel: "C1"}, "/topic Don't panic")
	<-changed
	if c, _ := store.Channel("C1"); c.Topic != "Don't panic" {
		t.Fatalf("topic = %q, want the new one", c.Topic)
	}
}

func TestShortDuration(t *testing.T) {
	cases := map[time.Duration]string{
		10 * time.Minute:           "10m",
		90 * time.Second:           "1m30s",
		30 * time.Second:           "30s",
		time.Hour:                  "1h",
		time.Hour + 30*time.Minute: "1h30m",
		time.Hour + 30*time.Second: "1h0m30s",
	}
	for d, want := range cases {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestOpenURLRefusesNonWebLinks(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "javascript:alert(1)", "slack://open", "-a Calculator"} {
		if err := openURL(u); err == nil {
			t.Errorf("openURL(%q) went through", u)
		}
	}
}

func TestNickColumnFitsLongestName(t *testing.T) {
	names := map[string]string{"U1": "ford", "U2": "Arthur Dent", "U3": "Someone With A Very Long Display Name"}
	v := newLogView(nickWidth, func(m chat.Message) string { return names[m.User] }, nil)
	short := []chat.Message{{User: "U1"}, {User: "U1"}}
	if got := v.nickWidth(short); got != math.Ceil(4*monoAdvance) {
		t.Errorf("ford alone: %v, want four characters wide", got)
	}
	if got := v.nickWidth(append(short, chat.Message{User: "U2"})); got != math.Ceil(11*monoAdvance) {
		t.Errorf("with Arthur Dent: %v, want eleven characters wide", got)
	}
	if got := v.nickWidth([]chat.Message{{User: "U3"}}); got != nickWidth {
		t.Errorf("a very long name: %v, want the %v cap", got, nickWidth)
	}
}

func TestStatusBufferAppendsCommandOutputAfterOlderLines(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := &window{store: store, active: statusKey, timeline: newLogView(nickWidth, func(chat.Message) string { return "" },
		func(chat.Message, *chat.Message, float64) *caution.Node { return caution.Panel() })}
	w.note("first")
	old := chat.TSFromTime(time.Now().Add(-48 * time.Hour))
	w.addStatus(statusEntry{at: chat.TSFromTime(time.Now()), msg: chat.Message{TS: old, User: "U1", Text: "an old hit"}, channel: "C1"})
	shown := w.timeline.shown
	if len(shown) != 2 || shown[0].Text != "first" || shown[1].Text != "an old hit" {
		t.Fatalf("status buffer = %+v, want the note then the hit", shown)
	}
	if w.statusTo[old] != "C1" {
		t.Fatalf("the hit opens %q, want C1", w.statusTo[old])
	}
	w.clear()
	if len(w.timeline.shown) != 0 {
		t.Fatalf("after /clear, %d lines remain", len(w.timeline.shown))
	}
}

func TestSwitcherRanksPrefixThenSubstringOpenFirst(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	w := &window{store: store}
	got := w.switchMatches("#eng")
	if len(got) == 0 || got[0] != "C2" {
		t.Fatalf("#eng → %v, want engineering first", got)
	}
	for _, id := range w.switchMatches("an") {
		if !strings.Contains(strings.ToLower(w.bufferLabel(id)), "an") {
			t.Errorf("%s doesn't contain \"an\"", w.bufferLabel(id))
		}
	}
	if n := len(w.switchMatches("")); n == 0 || n > switchLimit {
		t.Fatalf("empty query lists %d, want 1..%d", n, switchLimit)
	}
}

func TestReactAddsToNewestMessage(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	changed := make(chan struct{}, 8)
	store.Subscribe(func(chat.Change) { changed <- struct{}{} })
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-changed
	store.Load(context.Background(), "C2")
	<-changed
	w := &window{store: store, timeline: testLog()}
	w.command(target{channel: "C2"}, "/react :eyes:")
	<-changed
	m, _ := store.Newest("C2", "")
	if len(m.Reactions) != 1 || m.Reactions[0].Name != "eyes" || m.Reactions[0].Users[0] != "U0" {
		t.Fatalf("newest message reactions = %+v, want eyes from you", m.Reactions)
	}
}

func TestLogViewTrimsFrontWithoutRebuilding(t *testing.T) {
	drawn := 0
	v := newLogView(nickWidth, func(chat.Message) string { return "ford" },
		func(chat.Message, *chat.Message, float64) *caution.Node { drawn++; return caution.Panel() })
	base := time.Now()
	var msgs []chat.Message
	for i := range 12 {
		msgs = append(msgs, chat.Message{TS: chat.TSFromTime(base.Add(time.Duration(i) * time.Second)), User: "U1"})
	}
	v.sync("C1", msgs[:10], "")
	kept := slices.Clone(v.slots[3:])
	drawn = 0
	if v.sync("C1", msgs[3:], "") != nil {
		t.Fatal("trimming the front rebuilt the view")
	}
	if drawn != 3 { // the new first row, for its divider, and two new ones
		t.Fatalf("drew %d rows, want 3", drawn)
	}
	if len(v.slots) != 9 || v.slots[1] != kept[1] || v.slots[6] != kept[6] {
		t.Fatal("kept rows were replaced")
	}
}

func TestStatusBufferKeepsMentionsUnseenUntilFocused(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	changed := make(chan struct{}, 8)
	store.Subscribe(func(chat.Change) { changed <- struct{}{} })
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-changed
	focused := false
	w := &window{store: store, active: statusKey, focused: func() bool { return focused },
		timeline: newLogView(nickWidth, func(chat.Message) string { return "" },
			func(chat.Message, *chat.Message, float64) *caution.Node { return caution.Panel() })}
	store.Apply(chat.Posted{Channel: "C4", Msg: chat.Message{TS: chat.TSFromTime(time.Now().Add(time.Second)), User: "U3", Text: "<@U0> can we archive this channel now?"}})
	<-changed
	w.markRead()
	w.renderStatusBuffer()
	if n := store.Pings(); n != 1 {
		t.Fatalf("pings with clack in the background = %d, want 1", n)
	}
	focused = true
	w.markRead()
	if n := store.Pings(); n != 0 {
		t.Fatalf("pings after focusing the status buffer = %d, want 0", n)
	}
}

func TestCommandOutputIsALineInTheBufferNotTheHeader(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	changed := make(chan struct{}, 8)
	store.Subscribe(func(chat.Change) { changed <- struct{}{} })
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-changed
	store.Load(context.Background(), "C1")
	<-changed
	w := &window{store: store, active: "C1", app: &app{dir: t.TempDir()},
		status: caution.Label("Ship-wide news"), timeline: testLog()}
	w.renderTimeline()
	before := len(w.timeline.shown)
	w.command(target{channel: "C1"}, "/fx")
	shown := w.timeline.shown
	if len(shown) <= before || !isNote(shown[len(shown)-1]) {
		t.Fatalf("after /fx the buffer ends with %+v, want a note", shown[len(shown)-1])
	}
	if got := w.status.Prop("text"); got != "Ship-wide news" {
		t.Fatalf("the header says %q, want the topic left alone", got)
	}
	if store.Unread("C1") != 0 {
		t.Fatal("a note counts as unread")
	}
	w.active = "C2"
	w.renderTimeline()
	w.active = "C1"
	w.renderTimeline()
	if !slices.ContainsFunc(w.timeline.shown, isNote) {
		t.Fatal("the note was lost after switching buffers")
	}
}

func testLog() *logView {
	return newLogView(nickWidth, func(chat.Message) string { return "" },
		func(chat.Message, *chat.Message, float64) *caution.Node { return caution.Panel() })
}

func TestSwitcherArrowsMoveTheHighlightAndEnterOpensIt(t *testing.T) {
	var opened string
	sw := &switcher{
		matches: func(q string) []string {
			if q == "" {
				return []string{"status", "C1", "C2"}
			}
			return []string{"C2"}
		},
		label: func(id string) string { return id },
		pick:  func(id string) { opened = id },
		list:  caution.VStack(),
	}
	sw.filter("")
	sw.move(1)
	sw.move(1)
	sw.choose()
	if opened != "C2" {
		t.Fatalf("Down twice then Enter opened %q, want C2", opened)
	}
	sw.move(1) // past the end wraps to the top
	sw.move(-1)
	sw.move(-1)
	sw.choose()
	if opened != "C1" {
		t.Fatalf("wrapping around opened %q, want C1", opened)
	}
	sw.move(1)
	sw.filter("c2") // typing starts the highlight over
	sw.choose()
	if opened != "C2" {
		t.Fatalf("after typing, Enter opened %q, want the first match", opened)
	}
}

func TestMuteHidesSomeoneInTheOpenChannel(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	changed := make(chan struct{}, 8)
	store.Subscribe(func(chat.Change) { changed <- struct{}{} })
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-changed
	store.Load(context.Background(), "C1")
	<-changed
	w := &window{store: store, active: "C1", app: &app{dir: t.TempDir()},
		status: caution.Label(""), timeline: testLog()}
	w.renderTimeline()
	fromFord := func(m chat.Message) bool { return m.User == "U3" }
	if !slices.ContainsFunc(w.timeline.shown, fromFord) {
		t.Fatal("#general has nothing from Ford to mute")
	}

	w.command(target{channel: "C1"}, "/mute @Ford Prefect")
	if slices.ContainsFunc(w.timeline.shown, fromFord) {
		t.Error("Ford still shows in #general after /mute")
	}
	if store.Muted("C2", "U3") {
		t.Error("/mute without -a muted Ford in #engineering too")
	}

	w.command(target{channel: "D2"}, "/mute ford")
	if store.Muted("D2", "U3") {
		t.Error("/mute in a DM muted Ford there")
	}

	w.command(target{channel: "C1"}, "/unmute ford")
	if !slices.ContainsFunc(w.timeline.shown, fromFord) {
		t.Error("Ford is still hidden in #general after /unmute")
	}
}
