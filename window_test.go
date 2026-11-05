package main

import "testing"

func TestWindowKeepsRestoredSizeWhileMaximized(t *testing.T) {
	a := &app{dir: t.TempDir()}
	if err := saveSettings(a.settingsPath(), settings{UserToken: "xoxp-1"}); err != nil {
		t.Fatal(err)
	}
	ws := &windowSaver{a: a}
	ws.note(1400, 900, false)
	ws.note(1300, 850, false)
	ws.flush()
	ws.note(1728, 1080, true)
	ws.flush()

	got, err := loadSettings(a.settingsPath())
	want := settings{UserToken: "xoxp-1", WindowW: 1300, WindowH: 850, Maximized: true}
	if err != nil || got != want {
		t.Fatalf("settings = %+v, %v; want %+v", got, err, want)
	}
	if w, h := windowSize(got); w != 1300 || h != 850 {
		t.Fatalf("windowSize = %dx%d; want 1300x850", w, h)
	}

	ws.note(1300, 850, false)
	ws.flush()
	if got, _ := loadSettings(a.settingsPath()); got.Maximized {
		t.Fatal("still maximized after restore")
	}
}

func TestWindowSizeDefaultsBeforeAnySave(t *testing.T) {
	if w, h := windowSize(settings{}); w != 1180 || h != 780 {
		t.Fatalf("windowSize = %dx%d; want 1180x780", w, h)
	}
}
