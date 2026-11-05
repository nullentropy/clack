package main

import (
	"log"
	"sync"
	"time"
)

// windowSettle is a window size debounce before we save the size
const windowSettle = 500 * time.Millisecond

// windowSaver saves the native window's size so we can reopen the same way
type windowSaver struct {
	a *app

	mu        sync.Mutex
	timer     *time.Timer
	pending   bool
	w, h      int
	maximized bool
}

func (ws *windowSaver) note(w, h int, maximized bool) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.w, ws.h, ws.maximized, ws.pending = w, h, maximized, true
	if ws.timer != nil {
		ws.timer.Stop()
	}
	ws.timer = time.AfterFunc(windowSettle, ws.flush)
}

// flush forces a save without debounce for when we quit
func (ws *windowSaver) flush() {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if !ws.pending {
		return
	}
	ws.pending = false
	err := ws.a.updateSettings(func(s *settings) {
		s.Maximized = ws.maximized
		if !ws.maximized && ws.w > 0 && ws.h > 0 {
			s.WindowW, s.WindowH = ws.w, ws.h
		}
	})
	if err != nil {
		log.Printf("settings: window: %v", err)
	}
}

// windowSize is the size to open at
func windowSize(s settings) (w, h int) {
	if s.WindowW > 0 && s.WindowH > 0 {
		return s.WindowW, s.WindowH
	}
	return 1180, 780
}
