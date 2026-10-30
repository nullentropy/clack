package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nullentropy/caution"
)

// bindKeys registers the session's shortcuts once
func (sc *screen) bindKeys() {
	on := func(fn func(w *window), combos ...string) {
		for _, c := range combos {
			sc.s.OnKey(c, func() {
				if sc.win != nil && sc.root == sc.win.root {
					fn(sc.win)
				}
			})
		}
	}
	for n := 1; n <= 9; n++ {
		on(func(w *window) { w.openNth(n) }, fmt.Sprintf("alt+%d", n), fmt.Sprintf("cmd+%d", n))
	}
	on(func(w *window) { w.step(-1) }, "alt+up")
	on(func(w *window) { w.step(+1) }, "alt+down")
	on((*window).showSwitcher, "cmd+k", "ctrl+k")
	on((*window).closeOverlay, "escape")
}

func (w *window) buffers() []string {
	var keys []string
	for _, r := range w.sidebarRows() {
		if r.key != "" {
			keys = append(keys, r.key)
		}
	}
	return keys
}

// counting from 1
func (w *window) openNth(n int) {
	if keys := w.buffers(); n <= len(keys) {
		w.open(keys[n-1])
	}
}

func (w *window) step(by int) {
	keys := w.buffers()
	i := slices.Index(keys, w.active)
	w.open(keys[((i+by)%len(keys)+len(keys))%len(keys)])
}

func (w *window) closeOverlay() {
	if w.overlay != nil {
		w.overlay.Remove()
		w.overlay = nil
		w.composer.Focus()
	}
}

func (w *window) showOverlay(card *caution.Node) {
	w.closeOverlay()
	w.overlay = caution.Panel().Anchor(fill).Kids(
		caution.Panel().Anchor(fill).Bg("$scrim"),
		caution.Glass().Anchor(fill).OnPick(func(float64, float64, *caution.Node) { w.closeOverlay() }),
		card,
	)
	w.root.Add(w.overlay)
}

func (w *window) showHelp() {
	w.closeOverlay()
	dlg := caution.Dialog("Help").CardSize(620, 540)
	dlg.OnDismiss(w.closeOverlay)
	dlg.Kids(caution.Scroll().Anchor(fill).Kids(
		caution.Label(helpText).Mono().FontSize(12).Wrap().Selectable().
			Anchor(caution.A{Left: caution.Px(16), Top: caution.Px(14), Right: caution.Px(16)})))
	w.overlay = dlg
	w.root.Add(dlg)
}

// switchLimit is how many matches the quick switcher lists
const switchLimit = 8

func (w *window) showSwitcher() {
	list := caution.VStack().Gap(0).Align("stretch").NoFade() // redrawn on every keystroke
	var matches []string
	show := func(query string) {
		matches = w.switchMatches(query)
		list.Clear()
		for i, id := range matches {
			bg, color := "", "$inkDim"
			if i == 0 {
				bg, color = "$accent", "$ink"
			}
			list.Add(caution.Panel().H(26).Bg(bg).Kids(caution.Label(w.bufferLabel(id)).FontSize(14).Color(color).
				Anchor(caution.A{Left: caution.Px(12), CenterY: caution.Px(0)})))
		}
	}
	field := caution.TextField("").Placeholder("Jump to a conversation…").OnInput(show).OnSubmit(func(text string) {
		if text == "" || len(matches) == 0 {
			w.closeOverlay()
			return
		}
		id := matches[0]
		w.closeOverlay()
		if id != statusKey {
			w.store.SetOpen(id, true)
		}
		w.open(id)
	})
	show("")
	w.showOverlay(caution.VStack().Gap(8).Align("stretch").W(440).Pad(12).Bg("$panel").
		Border("$edge", 1).Shadow(24, 0, 8, "#00000080").
		Anchor(caution.A{CenterX: caution.Px(0), Top: caution.Px(96)}).Kids(field, list))
	field.Focus()
}

func (w *window) switchMatches(query string) []string {
	query = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(query), "#"))
	open := w.buffers()
	ids := slices.Clone(open)
	for _, c := range w.store.Channels() {
		if !slices.Contains(open, c.ID) {
			ids = append(ids, c.ID)
		}
	}
	rank := func(id string) int {
		name := strings.ToLower(strings.TrimPrefix(w.bufferLabel(id), "#"))
		r := 0
		switch {
		case strings.HasPrefix(name, query):
		case strings.Contains(name, query):
			r = 2
		default:
			return -1
		}
		if !slices.Contains(open, id) {
			r++
		}
		return r
	}
	var out []string
	for r := 0; r < 4; r++ {
		for _, id := range ids {
			if rank(id) == r {
				out = append(out, id)
			}
		}
	}
	return out[:min(len(out), switchLimit)]
}

func (w *window) bufferLabel(id string) string {
	if c, ok := w.store.Channel(id); ok {
		return w.channelLabel(c)
	}
	return id // the status buffer
}
