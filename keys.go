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
	w.fitOverlay = nil
	if w.overlay != nil {
		w.overlay.Remove()
		w.overlay = nil
		w.composer.Focus()
	}
}

func (w *window) showDialog(title string, width, height float64, content *caution.Node) *caution.Node {
	w.closeOverlay()
	dlg := caution.Dialog(title).CardSize(width, height)
	dlg.OnDismiss(w.closeOverlay)
	dlg.Kids(content)
	w.overlay = dlg
	w.root.Add(dlg)
	return dlg
}

func (w *window) closeBar() *caution.Node {
	close := caution.Button("Close").H(26).OnClick(w.closeOverlay).
		Anchor(caution.A{Right: caution.Px(12), CenterY: caution.Px(0)})
	close.Focus()
	return caution.Panel().Dock("bottom").H(closeBarH).Kids(close)
}

const closeBarH = 44

func (w *window) showHelp() {
	w.showDialog("Help", 620, 540, caution.DockPanel().Anchor(fill).Kids(
		w.closeBar(),
		caution.Scroll().Kids(
			caution.Label(helpText).Mono().FontSize(12).Wrap().Selectable().
				Anchor(caution.A{Left: caution.Px(16), Top: caution.Px(14), Right: caution.Px(16)}))))
}

// switchLimit is how many matches the quick switcher lists
const switchLimit = 8

func (w *window) showSwitcher() {
	sw := &switcher{matches: w.switchMatches, label: w.bufferLabel, list: caution.VStack().Gap(0).Align("stretch").NoFade(),
		pick: func(id string) {
			w.closeOverlay()
			if id != statusKey {
				w.store.SetOpen(id, true)
			}
			w.open(id)
		}}
	field := caution.TextField("").Placeholder("Jump to a conversation…").OnInput(sw.filter).
		OnNav(sw.move).OnSubmit(func(string) { sw.choose() })
	sw.filter("")
	// the title bar, padding and the field, then the rows
	w.showDialog("Jump to", 440, 110+switchLimit*26, caution.VStack().Gap(8).Align("stretch").Pad(12).Anchor(fill).Kids(field, sw.list))
	field.Focus()
}

type switcher struct {
	matches func(query string) []string
	label   func(id string) string
	pick    func(id string)
	list    *caution.Node

	shown []string
	sel   int
}

func (sw *switcher) filter(query string) {
	sw.shown, sw.sel = sw.matches(query), 0
	sw.render()
}

func (sw *switcher) move(dir int) {
	if n := len(sw.shown); n > 0 {
		sw.sel = ((sw.sel+dir)%n + n) % n
		sw.render()
	}
}

func (sw *switcher) choose() {
	if sw.sel < len(sw.shown) {
		sw.pick(sw.shown[sw.sel])
	}
}

func (sw *switcher) render() {
	sw.list.Clear()
	for i, id := range sw.shown {
		bg, color := "", "$inkDim"
		if i == sw.sel {
			bg, color = "$accent", "$ink"
		}
		sw.list.Add(caution.Panel().H(26).Bg(bg).Kids(
			caution.Label(sw.label(id)).FontSize(14).Color(color).Anchor(caution.A{Left: caution.Px(12), CenterY: caution.Px(0)}),
			caution.Glass().Anchor(fill).OnPick(func(float64, float64, *caution.Node) { sw.pick(id) }),
		))
	}
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
