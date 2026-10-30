package main

import (
	"cmp"
	"slices"
	"strings"

	"github.com/nullentropy/caution"
)

type sideRow struct {
	key     string // channel ID
	label   string
	unread  bool
	mention bool // an unread message names the user
	current bool
}

type sidebar struct {
	col   *caution.Node
	root  *caution.Node
	open  func(id string)
	shown []sideRow
	nodes []sideNodes
}

type sideNodes struct {
	row, label *caution.Node
}

func newSidebar(team string, open func(id string)) *sidebar {
	sb := &sidebar{open: open}
	sb.col = caution.VStack().Gap(0).Align("stretch").
		Anchor(caution.A{Left: caution.Px(0), Right: caution.Px(0), Top: caution.Px(4)})
	sb.root = caution.DockPanel().Kids(
		headerBar(
			caution.Label(team).FontSize(16).Weight(700).Anchor(caution.A{Left: caution.Px(20), CenterY: caution.Px(0)}),
		),
		caution.Scroll().InsetBottom(8).Kids(sb.col),
	)
	return sb
}

func (sb *sidebar) render(rows []sideRow) {
	sameKeys := slices.EqualFunc(rows, sb.shown, func(a, b sideRow) bool {
		return a.key == b.key && a.label == b.label
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
		sb.col.Add(caution.Panel().H(30).Kids(
			caution.Label(r.label).FontSize(12).Weight(600).Color("$inkFaint").
				Anchor(caution.A{Left: caution.Px(16), Bottom: caution.Px(4)}),
		))
		return sideNodes{}
	}
	n := sideNodes{
		label: caution.Label(r.label).Mono().FontSize(fontSize).
			Anchor(caution.A{Left: caution.Px(24), Right: caution.Px(8), CenterY: caution.Px(0)}),
	}
	key := r.key
	n.row = sb.col.Add(caution.Panel().H(24).Kids(
		n.label,
		caution.Glass().Anchor(fill).OnPick(func(float64, float64, *caution.Node) { sb.open(key) }),
	))
	style(n, r)
	return n
}

func style(n sideNodes, r sideRow) {
	if n.row == nil {
		return
	}
	weight, color, bg := 400, "$inkFaint", "#00000000"
	if r.unread {
		weight, color = 700, "$ink"
	}
	if r.mention {
		weight, color = 700, "$highlight"
	}
	if r.current {
		color, bg = "$ink", "$accent"
	}
	n.label.Weight(weight).Color(color)
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
		{label: "Channels"},
	}
	rows = append(rows, chans...)
	rows = append(rows, sideRow{label: "Direct messages"})
	return append(rows, dms...)
}
