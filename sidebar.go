package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/nullentropy/caution"
	"github.com/nullentropy/clack/chat"
)

type sideRow struct {
	key     string // channel ID
	num     int    // for alt+N, counting from 1
	label   string
	unread  bool
	mention bool // an unread message names the user
	current bool
	away    bool   // the person in a direct message is away
	tip     string // shown on hover
}

type sidebar struct {
	col   *caution.Node
	root  *caution.Node
	open  func(id string)
	shown []sideRow
	nodes []sideNodes
}

type sideNodes struct {
	row, num, label *caution.Node
}

func newSidebar(team string, open func(id string)) *sidebar {
	sb := &sidebar{open: open}
	sb.col = caution.VStack().Gap(0).Align("stretch").
		Anchor(caution.A{Left: caution.Px(0), Right: caution.Px(0), Top: caution.Px(4)})
	sb.root = caution.DockPanel().Kids(
		headerBar(
			caution.Label(team).Mono().FontSize(fontSize).Weight(700).Anchor(caution.A{Left: caution.Px(12), CenterY: caution.Px(0)}),
		),
		caution.Scroll().InsetBottom(8).Kids(sb.col),
	)
	return sb
}

func (sb *sidebar) render(rows []sideRow) {
	sameKeys := slices.EqualFunc(rows, sb.shown, func(a, b sideRow) bool {
		return a.key == b.key && a.num == b.num && a.label == b.label && a.tip == b.tip
	})
	if !sameKeys {
		sb.col.Clear()
		sb.nodes = sb.nodes[:0]
		for _, r := range rows {
			sb.nodes = append(sb.nodes, sb.add(r))
		}
		sb.shown = rows
		return
	}
	for i, r := range rows {
		if r != sb.shown[i] {
			style(sb.nodes[i], r)
		}
	}
	sb.shown = rows
}

func (sb *sidebar) add(r sideRow) sideNodes {
	if r.key == "" {
		sb.col.Add(caution.Panel().H(lineHeight + 10).Kids(
			caution.Label("── " + r.label).Mono().FontSize(fontSize).Color("$inkFaint").
				Anchor(caution.A{Left: caution.Px(12), Bottom: caution.Px(2)}),
		))
		return sideNodes{}
	}
	n := sideNodes{
		// right aligned in two columns, like weechat's buffer list
		num: caution.Label(fmt.Sprintf("%2d", r.num)).Mono().FontSize(fontSize).
			Anchor(caution.A{Left: caution.Px(12), CenterY: caution.Px(0)}),
		label: caution.Label(r.label).Mono().FontSize(fontSize).
			Anchor(caution.A{Left: caution.Px(12 + 3*monoAdvance), Right: caution.Px(8), CenterY: caution.Px(0)}),
	}
	key := r.key
	glass := caution.Glass().Anchor(fill).OnPick(func(float64, float64, *caution.Node) { sb.open(key) })
	if r.tip != "" {
		glass.Tip(r.tip)
	}
	n.row = sb.col.Add(caution.Panel().H(lineHeight+2).Kids(n.num, n.label, glass))
	style(n, r)
	return n
}

func style(n sideNodes, r sideRow) {
	if n.row == nil {
		return
	}
	weight, color, numColor, bg := 400, "$inkFaint", "$inkFaint", "#00000000"
	if r.unread {
		weight, color = 700, "$ink"
	}
	if r.mention {
		weight, color = 700, "$highlight"
	}
	if r.away {
		color = faded(color)
	}
	if r.current {
		color, numColor, bg = "$ink", "$ink", "$accent"
	}
	n.label.Weight(weight).Color(color)
	n.num.Color(numColor)
	n.row.Bg(bg)
}

func (w *window) sidebarRows() []sideRow {
	var chans, dms []sideRow
	for _, c := range w.store.Channels() {
		if !w.store.IsOpen(c.ID) {
			continue
		}
		r := sideRow{
			key:     c.ID,
			label:   w.channelLabel(c),
			unread:  w.store.Unread(c.ID) > 0 || w.store.NewReplies(c.ID) > 0,
			mention: w.store.UnreadMentions(c.ID) > 0,
			current: c.ID == w.active,
		}
		if c.Kind == chat.Direct {
			r.away = w.store.UserAway(c.Peer)
			r.tip = w.presenceLine(c.Peer)
		}
		if c.Kind.IsDM() {
			dms = append(dms, r)
		} else {
			chans = append(chans, r)
		}
	}
	byLabel := func(a, b sideRow) int {
		return cmp.Compare(strings.ToLower(strings.TrimPrefix(a.label, "#")), strings.ToLower(strings.TrimPrefix(b.label, "#")))
	}
	slices.SortFunc(chans, byLabel)
	slices.SortFunc(dms, byLabel)
	rows := []sideRow{
		{key: statusKey, label: "status", mention: w.store.UnseenMentions(), current: w.active == statusKey},
		{label: "channels"},
	}
	rows = append(rows, chans...)
	rows = append(rows, sideRow{label: "direct messages"})
	rows = append(rows, dms...)
	num := 0
	for i := range rows {
		if rows[i].key != "" {
			num++
			rows[i].num = num
		}
	}
	return rows
}
