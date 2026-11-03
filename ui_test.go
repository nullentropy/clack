package main

import (
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
	composer := mount.Root.find(func(n node) bool { return n.P["placeholder"] != nil })
	if composer == nil {
		t.Fatal("no composer in the mounted window")
	}
	go func() { // drain updates so the session never blocks on the socket
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}()

	sc := <-mounted
	onSession := func(fn func()) {
		done := make(chan struct{})
		sc.s.Update(func() { fn(); close(done) })
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("session never ran the update")
		}
	}
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

// node is a mounted node as the client receives it.
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
