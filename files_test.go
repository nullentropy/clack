package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nullentropy/clack/chat"
)

func TestSavingAnAttachmentKeepsEarlierDownloads(t *testing.T) {
	store := chat.NewStore(&chat.Demo{}, t.TempDir())
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	if err := store.Start(ctx); err != nil {
		t.Fatal(err)
	}
	store.Load(ctx, "C2")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, ok := store.File("F1"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("#engineering never showed improbability-bench.png")
		}
		time.Sleep(10 * time.Millisecond)
	}

	dir := t.TempDir()
	first, err := saveFile(ctx, store, "F1", dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := saveFile(ctx, store, "F1", dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "improbability-bench.png"); first != want {
		t.Errorf("first save went to %s, want %s", first, want)
	}
	if want := filepath.Join(dir, "improbability-bench (1).png"); second != want {
		t.Errorf("second save went to %s, want %s", second, want)
	}
	for _, p := range []string{first, second} {
		if data, err := os.ReadFile(p); err != nil || len(data) == 0 {
			t.Errorf("%s: %d bytes, %v", p, len(data), err)
		}
	}
}
