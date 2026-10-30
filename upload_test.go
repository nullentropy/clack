package main

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/nullentropy/clack/chat"
)

func TestDroppedFileUploadsToTheOpenChannel(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	if err := store.Start(ctx); err != nil {
		t.Fatal(err)
	}
	store.SetOpen("C1", true)
	store.SetLast("C1")
	a := &app{dir: t.TempDir()}
	a.use(store, stop)

	c := dial(t, a.mount)
	var mount map[string]any
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

	seq := 0
	send := func(value map[string]any) {
		seq++
		if err := c.WriteJSON(map[string]any{"t": "ev", "id": 0, "ev": "filedrop", "value": value, "seq": seq}); err != nil {
			t.Fatal(err)
		}
	}
	send(map[string]any{"p": "start", "x": 400.0, "y": 300.0, "files": []any{map[string]any{"name": "notes.txt", "type": "text/plain", "size": 5}}})
	send(map[string]any{"p": "data", "f": 0, "b": base64.StdEncoding.EncodeToString([]byte("hello"))})
	send(map[string]any{"p": "end"})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msgs, _ := store.Timeline("C1")
		for _, m := range msgs {
			if len(m.Files) == 1 && m.Files[0].Name == "notes.txt" && m.Files[0].Size == 5 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the dropped file never reached #general")
}
