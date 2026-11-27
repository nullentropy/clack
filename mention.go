package main

import (
	"strings"

	"github.com/nullentropy/caution"
)

const (
	popupMax  = 8
	popupRowH = 26
)

type mentionPopup struct {
	w       *window
	field   *caution.Node
	channel func() string
	host    *caution.Node
	popup   *caution.Node
	list    *caution.Node

	head    string
	matches []string
	sel     int
}

func (w *window) newMentionPopup(field *caution.Node, channel func() string, host *caution.Node) *mentionPopup {
	list := caution.VStack().Gap(0).Align("stretch").NoFade().
		Anchor(caution.A{Left: caution.Px(1), Right: caution.Px(1), Top: caution.Px(1), Bottom: caution.Px(1)})
	return &mentionPopup{w: w, field: field, channel: channel, host: host, list: list,
		popup: caution.Panel().W(320).Bg("$panelAlt").Border("$edge", 1).Radius(6).
			Shadow(18, 0, 4, "$scrim").Anchor(caution.A{Left: caution.Px(16), Bottom: caution.Px(4)}).Kids(list)}
}

func (p *mentionPopup) open() bool { return len(p.matches) > 0 }

func (p *mentionPopup) update(value string) {
	head, word := mentionWord(value)
	var matches []string
	if word != "" {
		matches = p.w.nickMatches(p.channel(), word)
	}
	p.head, p.matches, p.sel = head, matches[:min(len(matches), popupMax)], 0
	p.render()
}

// mentionWord's word is "" unless text ends in "@word".
func mentionWord(text string) (head, word string) {
	i := strings.LastIndexAny(text, " \t\n")
	last := text[i+1:]
	if len(last) < 2 || last[0] != '@' {
		return text, ""
	}
	return text[:i+1], last[1:]
}

func (p *mentionPopup) move(dir int) {
	if n := len(p.matches); n > 0 {
		p.sel = ((p.sel+dir)%n + n) % n
		p.paintSelection()
	}
}

func (p *mentionPopup) pick(i int) {
	if i < 0 || i >= len(p.matches) {
		return
	}
	text := p.head + "@" + p.matches[i] + " "
	p.close()
	p.field.SetValueNow(text)
}

func (p *mentionPopup) close() {
	p.matches = nil
	p.render()
}

func (p *mentionPopup) render() {
	p.list.Clear()
	if !p.open() {
		if p.popup.Parent() != nil {
			p.popup.Remove()
		}
		return
	}
	for i, label := range p.matches {
		p.list.Add(caution.Panel().H(popupRowH).Kids(
			caution.Label("@"+label).FontSize(14).Anchor(caution.A{Left: caution.Px(12), CenterY: caution.Px(0)}),
			caution.Glass().Anchor(fill).OnPick(func(float64, float64, *caution.Node) { p.pick(i) }),
		))
	}
	p.paintSelection()
	p.popup.H(float64(popupRowH*len(p.matches) + 2))
	if p.popup.Parent() == nil {
		p.host.Add(p.popup)
	}
}

func (p *mentionPopup) paintSelection() {
	for i, row := range p.list.Children() {
		bg, color := "", "$inkDim"
		if i == p.sel {
			bg, color = "$accent", "$ink"
		}
		row.Bg(bg)
		row.Children()[0].Color(color)
	}
}

func (p *mentionPopup) onSubmit(send func(string)) func(string) {
	return func(text string) {
		if p.open() {
			sel := p.sel
			p.update(text)
			if p.open() {
				p.pick(min(sel, len(p.matches)-1))
				return
			}
		}
		send(text)
	}
}

func (p *mentionPopup) onTab(complete func(shift bool, value string, caret int) bool) func(shift bool, value string, caret int) bool {
	return func(shift bool, value string, caret int) bool {
		if p.open() {
			p.pick(p.sel)
			return true
		}
		return complete(shift, value, caret)
	}
}
