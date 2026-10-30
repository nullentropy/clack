package main

import (
	"cmp"
	"slices"
	"time"

	"github.com/nullentropy/caution"
	"github.com/nullentropy/clack/chat"
)

type statusEntry struct {
	at      chat.TS
	msg     chat.Message
	channel string
}

const noteNick = "-!-"

func isNote(m chat.Message) bool { return m.User == "" && m.Username == noteNick }

func (w *window) note(text string) {
	now := chat.TSFromTime(time.Now())
	w.addStatus(statusEntry{at: now, msg: chat.Message{TS: now, Username: noteNick, Text: text}})
}

func (w *window) addStatus(entries ...statusEntry) {
	w.notes = append(w.notes, entries...)
	if w.active == statusKey {
		w.renderTimeline()
	} else {
		w.open(statusKey)
	}
}

func (w *window) renderStatusBuffer() *caution.Node {
	mentions := w.store.Mentions()
	var entries []statusEntry
	for _, m := range w.afterClearMentions(mentions) {
		msg := m.Msg
		if c, ok := w.store.Channel(m.Channel); ok {
			msg.Text = w.channelLabel(c) + ": " + msg.Text
		}
		entries = append(entries, statusEntry{at: m.Msg.TS, msg: msg, channel: m.Channel})
	}
	entries = append(entries, w.notes...)
	slices.SortStableFunc(entries, func(a, b statusEntry) int { return cmp.Compare(a.at, b.at) })
	msgs := make([]chat.Message, len(entries))
	w.statusTo = map[chat.TS]string{}
	for i, e := range entries {
		msgs[i] = e.msg
		if e.channel != "" {
			w.statusTo[e.msg.TS] = e.channel
		}
	}
	w.timeline.newSince = ""
	return w.timeline.sync(statusKey, msgs, "This window will show mentions from conversations you haven't opened")
}

func (w *window) afterClearMentions(mentions []chat.Mention) []chat.Mention {
	mark, ok := w.cleared[statusKey]
	if !ok {
		return mentions
	}
	return slices.DeleteFunc(mentions, func(m chat.Mention) bool { return m.Msg.TS <= mark })
}

func (w *window) openLine(channel string) *caution.Node {
	label := channel
	if c, ok := w.store.Channel(channel); ok {
		label = w.channelLabel(c)
	}
	return caution.Panel().H(lineHeight).Kids(
		caution.Label("→ open "+label).Mono().FontSize(fontSize).Color("$accent").Anchor(caution.A{Left: caution.Px(0), Top: caution.Px(0)}),
		caution.Glass().Anchor(fill).OnPick(func(float64, float64, *caution.Node) {
			w.store.SetOpen(channel, true)
			w.open(channel)
		}),
	)
}
