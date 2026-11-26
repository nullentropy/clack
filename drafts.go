package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/nullentropy/clack/chat"
)

type savedDraft struct {
	Channel string  `json:"channel"`
	Thread  chat.TS `json:"thread,omitempty"`
	Text    string  `json:"text"`
}

func (a *app) draftsPath() string { return filepath.Join(a.dir, "drafts.json") }

func saveDrafts(path string, drafts map[target]string) error {
	if len(drafts) == 0 {
		return nil
	}
	list := make([]savedDraft, 0, len(drafts))
	for t, text := range drafts {
		list = append(list, savedDraft{Channel: t.channel, Thread: t.thread, Text: text})
	}
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func takeDrafts(path string) map[target]string {
	drafts := map[target]string{}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return drafts
	}
	if err == nil {
		err = os.Remove(path)
	}
	var list []savedDraft
	if err == nil {
		err = json.Unmarshal(b, &list)
	}
	if err != nil {
		log.Printf("drafts: %v", err)
	}
	for _, d := range list {
		if d.Text != "" {
			drafts[target{channel: d.Channel, thread: d.Thread}] = d.Text
		}
	}
	return drafts
}

func (a *app) takeDrafts() map[target]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return takeDrafts(a.draftsPath())
}

func (a *app) collectDrafts() map[target]string {
	a.mu.Lock()
	screens := make([]*screen, 0, len(a.screens))
	for sc := range a.screens {
		screens = append(screens, sc)
	}
	a.mu.Unlock()
	all := map[target]string{}
	for _, sc := range screens {
		got := make(chan map[target]string, 1)
		go sc.s.Update(func() {
			m := map[target]string{}
			if sc.win != nil {
				maps.Copy(m, sc.win.drafts)
			}
			got <- m
		})
		select {
		case m := <-got:
			maps.Copy(all, m)
		case <-sc.s.Done():
		case <-time.After(2 * time.Second):
		}
	}
	return all
}
