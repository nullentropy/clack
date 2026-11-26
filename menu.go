package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nullentropy/caution"
)

func (sc *screen) setMenu() {
	if !sc.a.appMenu {
		return
	}
	menus := sc.menus()
	if sig := menuSignature(menus); !sc.menuSet || sig != sc.menuSig {
		sc.menuSet, sc.menuSig = true, sig
		sc.s.SetMenu(menus...)
	}
}

func (sc *screen) menus() []caution.Menu {
	app := []caution.MenuItem{
		{Title: "Settings…", Key: "cmd+,", OnPick: sc.askTokens},
		{Sep: true},
		{Title: "Check for Updates…", OnPick: sc.checkForUpdates},
	}
	if tag := sc.a.UpdateReady(); tag != "" {
		app = append(app, caution.MenuItem{Title: "Restart to Update to " + strings.TrimPrefix(tag, "v"), OnPick: sc.restartToUpdate})
	}
	return []caution.Menu{
		{Title: "clack", App: true, Items: app},
		{Title: "Go", Items: sc.goItems()},
		{Title: "View", Items: sc.viewItems()},
		{Title: "Status", Items: sc.statusItems()},
		{Title: "Help", Items: []caution.MenuItem{{Title: "clack Help", OnPick: sc.onChat((*window).showHelp)}}},
	}
}

func (sc *screen) onChat(fn func(w *window)) func() {
	return func() {
		if sc.win != nil && sc.root == sc.win.root {
			fn(sc.win)
		}
	}
}

func (sc *screen) goItems() []caution.MenuItem {
	items := []caution.MenuItem{
		{Title: "Jump to…", Key: "cmd+k", OnPick: sc.onChat((*window).showSwitcher)},
		{Title: "Previous Channel", OnPick: sc.onChat(func(w *window) { w.step(-1) })},
		{Title: "Next Channel", OnPick: sc.onChat(func(w *window) { w.step(+1) })},
		{Sep: true},
	}
	if sc.win != nil {
		for i, label := range sc.win.menuBufferLabels() {
			n := i + 1
			items = append(items, caution.MenuItem{Title: label, Key: fmt.Sprintf("cmd+%d", n), OnPick: sc.onChat(func(w *window) { w.openNth(n) })})
		}
		items = append(items, caution.MenuItem{Sep: true})
	}
	return append(items,
		caution.MenuItem{Title: "Close Thread", OnPick: sc.onChat((*window).closeThread)},
		caution.MenuItem{Title: "Leave Channel", OnPick: sc.onChat(func(w *window) { w.part(w.active, "") })},
		caution.MenuItem{Title: "Clear Scrollback", OnPick: sc.onChat((*window).clear)},
		caution.MenuItem{Title: "Members", OnPick: sc.onChat(func(w *window) { w.who(w.active) })},
	)
}

func (sc *screen) viewItems() []caution.MenuItem {
	var themeItems []caution.MenuItem
	for _, name := range themeNames() {
		themeItems = append(themeItems, caution.MenuItem{Title: menuName(name), OnPick: sc.onChat(func(w *window) { w.theme(name) })})
	}
	effectItems := []caution.MenuItem{{Title: "Off", OnPick: sc.onChat(func(w *window) { w.fx("off") })}}
	for _, name := range effectNames() {
		effectItems = append(effectItems, caution.MenuItem{Title: menuName(name), OnPick: sc.onChat(func(w *window) { w.fx(name) })})
	}
	cfg := sc.a.fx()
	toggle := func(title, extra string, on bool) caution.MenuItem {
		if on {
			return caution.MenuItem{Title: "Turn Off " + title, OnPick: sc.onChat(func(w *window) { w.fx(extra + " off") })}
		}
		return caution.MenuItem{Title: "Turn On " + title, OnPick: sc.onChat(func(w *window) { w.fx(extra + " on") })}
	}
	return []caution.MenuItem{
		{Title: "Theme", Items: themeItems},
		{Title: "Effect", Items: effectItems},
		{Sep: true},
		toggle("Ping Glitch", "ping", cfg.ping),
		toggle("Shimmer", "shimmer", cfg.shimmer),
		toggle("Animated Sidebar", "sidebar", cfg.sidebar),
	}
}

func (sc *screen) statusItems() []caution.MenuItem {
	notifications := caution.MenuItem{Title: "Turn Off Notifications", OnPick: sc.onChat(func(w *window) { w.notify("off") })}
	if s, _ := loadSettings(sc.a.settingsPath()); s.Quiet {
		notifications = caution.MenuItem{Title: "Turn On Notifications", OnPick: sc.onChat(func(w *window) { w.notify("on") })}
	}
	var corners []caution.MenuItem
	for _, corner := range toastCorners {
		corners = append(corners, caution.MenuItem{Title: menuName(corner), OnPick: sc.onChat(func(w *window) { w.notify("corner " + corner) })})
	}
	return []caution.MenuItem{
		{Title: "Set Away", OnPick: sc.onChat(func(w *window) { w.setAway(true, "") })},
		{Title: "Set Back", OnPick: sc.onChat(func(w *window) { w.setAway(false, "") })},
		{Sep: true},
		notifications,
		{Title: "Notification Corner", Items: corners},
	}
}

func menuName(name string) string {
	if len(name) <= 3 {
		return strings.ToUpper(name)
	}
	words := strings.Split(name, "-")
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func menuSignature(menus []caution.Menu) string {
	var b strings.Builder
	var items func([]caution.MenuItem)
	items = func(its []caution.MenuItem) {
		for _, it := range its {
			fmt.Fprintf(&b, "%q %q %v [", it.Title, it.Key, it.Sep)
			items(it.Items)
			b.WriteString("]")
		}
	}
	for _, m := range menus {
		fmt.Fprintf(&b, "%q %v {", m.Title, m.App)
		items(m.Items)
		b.WriteString("}")
	}
	return b.String()
}

func (w *window) menuBufferLabels() []string {
	keys := w.buffers()
	labels := make([]string, 0, min(len(keys), 9))
	for _, id := range keys[:min(len(keys), 9)] {
		labels = append(labels, w.bufferLabel(id))
	}
	return labels
}

func (w *window) renderSidebar() {
	w.sidebar.render(w.sidebarRows())
	if labels := w.menuBufferLabels(); !slices.Equal(labels, w.menuBuffers) {
		w.menuBuffers = labels
		w.menu()
	}
}
