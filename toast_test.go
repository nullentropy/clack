package main

import (
	"testing"
	"time"
)

func TestAlertsShowInTheFocusedWindow(t *testing.T) {
	sc, _, _ := mountTestWindow(t)
	a := sc.a
	if err := a.updateSettings(func(s *settings) { s.ToastCorner = "bottom-left" }); err != nil {
		t.Fatal(err)
	}
	cards := func() (n int, anchor map[string]any) {
		runOnSession(t, sc, func() {
			if w := sc.win; w.toasts != nil && w.toasts.Parent() != nil {
				n = len(w.toasts.Children())
				anchor, _ = w.toasts.Prop("anchors").(map[string]any)
			}
		})
		return n, anchor
	}
	waitCards := func(want int) map[string]any {
		t.Helper()
		for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			n, anchor := cards()
			if n == want {
				return anchor
			}
			if time.Now().After(deadline) {
				t.Fatalf("window shows %d toasts, want %d", n, want)
			}
		}
	}

	if !a.toast("C2", "Zaphod Beeblebrox in #engineering", "hello") {
		t.Fatal("toast reported no focused window")
	}
	anchor := waitCards(1)
	if _, ok := anchor["left"]; !ok {
		t.Fatalf("toasts anchor = %v, want the bottom-left corner", anchor)
	}
	if _, ok := anchor["bottom"]; !ok {
		t.Fatalf("toasts anchor = %v, want the bottom-left corner", anchor)
	}

	show := func(channel, title, body string) {
		runOnSession(t, sc, func() { sc.win.toast("bottom-left", channel, title, body) })
	}
	for range toastMax + 2 {
		show("C2", "Zaphod Beeblebrox in #engineering", "again")
	}
	waitCards(toastMax)

	show("C1", "Arthur Dent in #general", "already open here")
	waitCards(toastMax)

	runOnSession(t, sc, func() { sc.win.dismissToast(sc.win.toasts.Children()[0]) })
	waitCards(toastMax - 1)

	runOnSession(t, sc, sc.askTokens)
	if a.toast("C2", "Zaphod Beeblebrox in #engineering", "on settings") {
		t.Fatal("toast went to a window on the settings screen, want the OS notification")
	}
	runOnSession(t, sc, sc.cancelTokens)
	if !a.toast("C2", "Zaphod Beeblebrox in #engineering", "back") {
		t.Fatal("toast reported no watching window after leaving settings")
	}

	a.setFocused(sc, false)
	if a.toast("C2", "Zaphod Beeblebrox in #engineering", "away") {
		t.Fatal("toast went to a window without focus, want the OS notification")
	}
}
