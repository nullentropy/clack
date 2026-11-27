package main

import (
	"cmp"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nullentropy/caution"
	"github.com/nullentropy/clack/chat"
)

func TestDraftsStayWithTheirChannel(t *testing.T) {
	sc, c, mount := mountTestWindow(t)
	composer := mount.find(func(n node) bool { return n.P["placeholder"] != nil })
	if composer == nil {
		t.Fatal("no composer in the mounted window")
	}
	onSession := func(fn func()) { runOnSession(t, sc, fn) }
	shown := func() (v string) {
		onSession(func() { v, _ = sc.win.composer.Prop("value").(string) })
		return v
	}
	seq := 0
	typeText := func(text string) {
		seq++
		if err := c.WriteJSON(map[string]any{"t": "ev", "id": composer.ID, "ev": "input", "value": text, "seq": seq}); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(3 * time.Second); shown() != text; time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("the session never saw %q typed", text)
			}
		}
	}
	open := func(id string) { onSession(func() { sc.win.open(id) }) }

	open("C1")
	typeText("half a thought")
	open("C2")
	if v := shown(); v != "" {
		t.Fatalf("C2's composer shows %q, want it empty", v)
	}
	typeText("for engineering")
	open("C1")
	if v := shown(); v != "half a thought" {
		t.Fatalf("back in C1 the composer shows %q, want its draft", v)
	}
	open("C2")
	if v := shown(); v != "for engineering" {
		t.Fatalf("back in C2 the composer shows %q, want its draft", v)
	}
}

func mountTestWindow(t *testing.T) (*screen, *websocket.Conn, node) {
	t.Helper()
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	started := make(chan struct{}, 1)
	store.Subscribe(func(chat.Change) {
		select {
		case started <- struct{}{}:
		default:
		}
	})
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	if err := store.Start(ctx); err != nil {
		t.Fatal(err)
	}
	<-started
	store.SetOpen("C1", true)
	store.SetLast("C1")
	a := &app{dir: t.TempDir()}
	a.use(store, stop)

	mounted := make(chan *screen, 1)
	c := dial(t, func(s *caution.Session) *caution.Node {
		root := a.mount(s)
		a.mu.Lock()
		for sc := range a.focused {
			mounted <- sc
		}
		a.mu.Unlock()
		return root
	})
	var mount struct{ Root node }
	if err := c.ReadJSON(&mount); err != nil {
		t.Fatal(err)
	}
	go func() { // drain updates so the session never blocks on the socket
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}()
	return <-mounted, c, mount.Root
}

func runOnSession(t *testing.T, sc *screen, fn func()) {
	t.Helper()
	done := make(chan struct{})
	sc.s.Update(func() { fn(); close(done) })
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("session never ran the update")
	}
}

type node struct {
	ID   int            `json:"id"`
	P    map[string]any `json:"p"`
	Kids []node         `json:"kids"`
}

func (n node) find(match func(node) bool) *node {
	if match(n) {
		return &n
	}
	for _, k := range n.Kids {
		if f := k.find(match); f != nil {
			return f
		}
	}
	return nil
}

var served struct {
	sync.Mutex
	mount caution.MountFunc
}

func dial(t *testing.T, mount caution.MountFunc) *websocket.Conn {
	served.Lock()
	served.mount = mount
	served.Unlock()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go caution.ServeListener(l, func(s *caution.Session) *caution.Node {
		served.Lock()
		mount := served.mount
		served.Unlock()
		return mount(s)
	}, caution.Options{})
	c, _, err := websocket.DefaultDialer.Dial("ws://"+l.Addr().String()+"/ws?vw=1180&vh=780", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestLongCardsAreClipped(t *testing.T) {
	short := "*Where:* zoom\n*Guests:* you"
	if got := clip(short); got != short {
		t.Fatalf("clip(short) = %q", got)
	}
	tall := strings.Repeat("line\n", 20)
	if got := clip(tall); strings.Count(got, "\n") != cardLines-1 || !strings.HasSuffix(got, " …") {
		t.Fatalf("clip(tall) = %q, want %d lines ending in …", got, cardLines)
	}
	wide := strings.Repeat("x", cardChars-10) + " <https://github.com/pull/1|a pull request>"
	if got := clip(wide); strings.Contains(got, "<") {
		t.Fatalf("clip(wide) = %q, left half a link", got)
	}
}

func TestAStuckWindowDoesNotHoldUpSlackEvents(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	started := make(chan struct{}, 1)
	store.Subscribe(func(chat.Change) {
		select {
		case started <- struct{}{}:
		default:
		}
	})
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	if err := store.Start(ctx); err != nil {
		t.Fatal(err)
	}
	<-started
	a := &app{dir: t.TempDir()}
	a.use(store, stop)
	mounted := make(chan *caution.Session, 1)
	c := dial(t, func(s *caution.Session) *caution.Node {
		mounted <- s
		return a.mount(s)
	})
	var mount struct{ Root node }
	if err := c.ReadJSON(&mount); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	(<-mounted).Update(func() { <-release }) // the window stops taking updates, as when its client stops reading

	applied := make(chan struct{})
	go func() {
		base := time.Now()
		for i := range 500 {
			store.Apply(chat.Posted{Channel: "C1", Msg: chat.Message{TS: chat.TSFromTime(base.Add(time.Duration(i) * time.Millisecond)), User: "U1", Text: "hi"}})
		}
		close(applied)
	}()
	select {
	case <-applied:
	case <-time.After(3 * time.Second):
		t.Fatal("events stopped being applied while the window was stuck")
	}
}

func TestAwayNicksFadeAndShowStatusOnHover(t *testing.T) {
	sc, _, _ := mountTestWindow(t)
	sc.win.store.Apply(chat.PresenceChanged{User: "U2", Away: true})
	nick := func(name string) (color, tip any) {
		runOnSession(t, sc, func() {
			var walk func(n *caution.Node)
			walk = func(n *caution.Node) {
				if n.Prop("text") == name && n.Prop("weight") == 700 {
					color, tip = n.Prop("color"), n.Prop("tip")
				}
				for _, k := range n.Children() {
					walk(k)
				}
			}
			walk(sc.win.timeline.col)
		})
		return
	}
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if c, _ := nick("Arthur Dent"); c == faded(nickColor("U2")) {
			break
		}
		if time.Now().After(deadline) {
			c, _ := nick("Arthur Dent")
			t.Fatalf("Arthur is away but his nick is %v", c)
		}
	}
	if c, tip := nick("Ford Prefect"); c != nickColor("U3") || tip != "Ford Prefect · :thumbsup: Hitching a ride" {
		t.Fatalf("Ford's nick is %v with tip %v", c, tip)
	}
}

func TestAnimatedEffectsRunOnlyWhileFocused(t *testing.T) {
	sc, c, _ := mountTestWindow(t)
	for _, name := range []string{"ping", "shimmer", "sidebar"} {
		if err := sc.a.setExtra(name, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := sc.a.setEffect("vhs"); err != nil {
		t.Fatal(err)
	}
	animated := func() (n int) {
		runOnSession(t, sc, func() {
			var walk func(n *caution.Node)
			walk = func(nd *caution.Node) {
				if nd.Prop("animate") == true {
					n++
				}
				if fx, ok := nd.Prop("effect").(map[string]any); ok && fx["animate"] == true {
					n++
				}
				for _, k := range nd.Children() {
					walk(k)
				}
			}
			walk(sc.root)
		})
		return n
	}
	focus := func(on bool) {
		if err := c.WriteJSON(map[string]any{"t": "ev", "id": 0, "ev": "focus", "value": on, "seq": 1}); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			var f bool
			runOnSession(t, sc, func() { f = sc.focused })
			if f == on {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("the screen never saw focused=%v", on)
			}
		}
	}

	if n := animated(); n < 3 {
		t.Fatalf("focused with shimmer and the sidebar on, %d nodes animate, want the window, the backdrop and a mention", n)
	}
	focus(false)
	if n := animated(); n != 0 {
		t.Fatalf("unfocused, %d nodes still animate", n)
	}
	sc.a.ping()
	time.Sleep(50 * time.Millisecond)
	if n := animated(); n != 0 {
		t.Fatalf("unfocused, a ping left %d nodes animating", n)
	}
	focus(true)
	if n := animated(); n < 3 {
		t.Fatalf("focused again, only %d nodes animate", n)
	}
}

func TestThreadBroadcastsSayWhichThreadAndOpenIt(t *testing.T) {
	sc, _, _ := mountTestWindow(t)
	type line struct{ text, action string }
	lines := func(col func() *caution.Node) (out []line) {
		runOnSession(t, sc, func() {
			var walk func(n *caution.Node)
			walk = func(n *caution.Node) {
				if spans, ok := n.Prop("spans").([]caution.Span); ok {
					var l line
					for _, sp := range spans {
						l.text += sp.Text
						l.action = cmp.Or(l.action, sp.Action)
					}
					out = append(out, l)
				}
				for _, k := range n.Children() {
					walk(k)
				}
			}
			walk(col())
		})
		return out
	}
	timeline := func() *caution.Node { return sc.win.timeline.col }
	threadPane := func() *caution.Node { return sc.win.threadLog.col }
	waitFor := func(col func() *caution.Node, text string) line {
		t.Helper()
		for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			for _, l := range lines(col) {
				if l.text == text {
					return l
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("no line %q among %+v", text, lines(col))
			}
		}
	}
	count := func(col func() *caution.Node, match func(string) bool) (n int) {
		for _, l := range lines(col) {
			if match(l.text) {
				n++
			}
		}
		return n
	}

	fromHistory := waitFor(timeline, "replied to a thread: Also, for anyone joining late: we go round the bridge left to right, keep update…")
	waitFor(timeline, "Works for me. I'll keep a timer going.")
	msgs, _ := sc.win.store.Timeline("C1")
	var parent chat.TS
	var broadcast chat.Message
	for _, m := range msgs {
		if strings.HasPrefix(m.Text, "Also, for anyone joining late") {
			parent = m.TS
		}
		if m.Broadcast {
			broadcast = m
		}
	}
	sc.win.store.Apply(chat.Edited{Channel: "C1", Msg: chat.Message{TS: broadcast.TS, User: broadcast.User,
		Text: "Works for me. I'll keep a timer going, Arthur.", ThreadTS: parent, Broadcast: true}})
	waitFor(timeline, "Works for me. I'll keep a timer going, Arthur.")
	fromHistory = waitFor(timeline, fromHistory.text)
	runOnSession(t, sc, func() { sc.win.spanClick(fromHistory.action) })
	if sc.win.thread != (target{"C1", parent}) {
		t.Fatalf("clicking %q opened thread %+v, want C1/%s", fromHistory.text, sc.win.thread, parent)
	}
	waitFor(threadPane, "Works for me. I'll keep a timer going.")

	sc.win.store.Apply(chat.Posted{Channel: "C1", Msg: chat.Message{TS: chat.TSFromTime(time.Now()), User: "U1",
		Text: "Zaphod's update: the Heart of Gold is parked.", ThreadTS: parent, Broadcast: true}})
	fromLive := waitFor(timeline, "replied to a thread")
	waitFor(threadPane, "Zaphod's update: the Heart of Gold is parked.")
	if fromLive.action != fromHistory.action {
		t.Fatalf("the live broadcast's line opens %q, want %q", fromLive.action, fromHistory.action)
	}
	for _, text := range []string{"Works for me. I'll keep a timer going.", "Zaphod's update: the Heart of Gold is parked."} {
		if n := count(threadPane, func(s string) bool { return s == text }); n != 1 {
			t.Fatalf("the thread shows %q %d times, want once", text, n)
		}
	}
	if n := count(threadPane, func(s string) bool { return strings.HasPrefix(s, "replied to a thread") }); n != 0 {
		t.Fatalf("the thread labels %d of its replies as broadcasts", n)
	}
}
