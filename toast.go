package main

import (
	"log"
	"slices"
	"strings"
	"time"

	"github.com/nullentropy/caution"
)

var toastCorners = []string{"top-right", "top-left", "bottom-right", "bottom-left"}

const (
	toastFor   = 6 * time.Second
	toastMax   = 3
	toastW     = 340
	toastH     = 58
	toastInset = 16
)

func (a *app) toastCorner() string {
	s, err := loadSettings(a.settingsPath())
	if err != nil {
		log.Printf("settings: %v", err)
	}
	if !slices.Contains(toastCorners, s.ToastCorner) {
		return toastCorners[0]
	}
	return s.ToastCorner
}

func (a *app) toast(channel, title, body string) bool {
	screens := a.watching()
	corner := a.toastCorner()
	for _, sc := range screens {
		go sc.s.Update(func() {
			if sc.win != nil {
				sc.win.toast(corner, channel, title, body)
			}
		})
	}
	return len(screens) > 0
}

func toastAnchor(corner string) caution.A {
	var a caution.A
	if strings.HasPrefix(corner, "top") {
		a.Top = caution.Px(toastInset)
	} else {
		a.Bottom = caution.Px(60 + toastInset)
	}
	if strings.HasSuffix(corner, "left") {
		a.Left = caution.Px(toastInset)
	} else {
		a.Right = caution.Px(toastInset)
	}
	return a
}

func (w *window) toast(corner, channel, title, body string) {
	if channel == w.active {
		return
	}
	if w.toasts == nil || w.toastCorner != corner {
		if w.toasts != nil && w.toasts.Parent() != nil {
			w.toasts.Remove()
		}
		w.toasts = caution.VStack().Gap(8).Align("stretch").W(toastW).Anchor(toastAnchor(corner))
		w.toastCorner = corner
	}
	if kids := w.toasts.Children(); len(kids) >= toastMax {
		kids[0].Remove()
	}
	var card *caution.Node
	card = caution.Panel().H(toastH).Bg("$panelAlt").Border("$edge", 1).Radius(8).Shadow(18, 0, 4, "$scrim").Kids(
		caution.Label(title).FontSize(13).Weight(700).
			Anchor(caution.A{Left: caution.Px(12), Right: caution.Px(12), Top: caution.Px(10)}),
		caution.Label(strings.Join(strings.Fields(body), " ")).FontSize(12).Color("$inkDim").
			Anchor(caution.A{Left: caution.Px(12), Right: caution.Px(12), Top: caution.Px(31)}),
		caution.Glass().Anchor(fill).OnPick(func(float64, float64, *caution.Node) {
			w.dismissToast(card)
			w.store.SetOpen(channel, true)
			w.open(channel)
		}),
	)
	w.toasts.Add(card)
	if w.toasts.Parent() == nil {
		w.root.Add(w.toasts)
	}
	go func() {
		select {
		case <-w.s.Done():
		case <-time.After(toastFor):
			w.s.Update(func() { w.dismissToast(card) })
		}
	}()
}

func (w *window) dismissToast(card *caution.Node) {
	if card.Parent() == nil {
		return
	}
	card.Remove()
	if len(w.toasts.Children()) == 0 {
		w.toasts.Remove()
	}
}
