package main

import (
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nullentropy/caution"
	"github.com/nullentropy/clack/chat"
)

type completion struct {
	head, tail string
	matches    []string
	i          int
	shown      string
}

func (w *window) onTab(field *caution.Node, channel func() string) func(shift bool, value string, caret int) bool {
	var c completion
	return func(shift bool, value string, caret int) bool {
		if value != c.shown || len(c.matches) == 0 {
			head, word, tail := wordAt(value, caret)
			if word == "" {
				return false
			}
			c = completion{head: head, tail: tail, matches: w.nickMatches(channel(), strings.TrimPrefix(word, "@"))}
			if len(c.matches) == 0 {
				return true
			}
		} else if shift {
			c.i = (c.i - 1 + len(c.matches)) % len(c.matches)
		} else {
			c.i = (c.i + 1) % len(c.matches)
		}
		name := c.matches[c.i]
		c.shown = c.head + "@" + name
		if c.tail == "" {
			c.shown += " "
		}
		c.shown += c.tail
		field.SetValueNow(c.shown)
		return true
	}
}

func wordAt(text string, caret int) (head, word, tail string) {
	runes := []rune(text)
	caret = min(max(caret, 0), len(runes))
	if caret < len(runes) && isNameRune(string(runes[caret:])) {
		return text, "", "" // the caret is inside a word
	}
	start := caret
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	return string(runes[:start]), string(runes[start:caret]), string(runes[caret:])
}

type mentionable struct {
	label string
	keys  []string
	ref   string
}

var broadcasts = []mentionable{
	{label: "here", keys: []string{"here"}, ref: "<!here>"},
	{label: "channel", keys: []string{"channel"}, ref: "<!channel>"},
	{label: "everyone", keys: []string{"everyone"}, ref: "<!everyone>"},
}

func (w *window) mentionables(channel string) []mentionable {
	var out []mentionable
	for _, u := range w.rankedUsers(channel) {
		out = append(out, mentionable{label: u.Display(), keys: []string{u.Display(), u.Name}, ref: "<@" + u.ID + ">"})
	}
	out = append(out, broadcasts...)
	groups := w.store.Groups()
	slices.SortFunc(groups, func(a, b chat.UserGroup) int {
		return strings.Compare(strings.ToLower(a.Handle), strings.ToLower(b.Handle))
	})
	for _, g := range groups {
		out = append(out, mentionable{label: g.Handle, keys: []string{g.Handle}, ref: "<!subteam^" + g.ID + ">"})
	}
	return out
}

func (w *window) nickMatches(channel, prefix string) []string {
	prefix = strings.ToLower(prefix)
	var out []string
	for _, m := range w.mentionables(channel) {
		for _, k := range m.keys {
			if strings.HasPrefix(strings.ToLower(k), prefix) {
				out = append(out, m.label)
				break
			}
		}
	}
	return out
}

func (w *window) rankedUsers(channel string) []chat.User {
	self := w.store.Self()
	users := w.store.Users()
	slices.SortFunc(users, func(a, b chat.User) int {
		if (a.ID == self) != (b.ID == self) {
			if a.ID == self {
				return 1
			}
			return -1
		}
		return strings.Compare(strings.ToLower(a.Display()), strings.ToLower(b.Display()))
	})
	msgs, _ := w.store.Timeline(channel)
	if w.thread.channel == channel && w.thread.thread != "" {
		msgs = append(msgs, w.store.Thread(channel, w.thread.thread)...)
		sort.Slice(msgs, func(i, j int) bool { return msgs[i].TS < msgs[j].TS })
	}
	recent := map[string]int{}
	for _, m := range slices.Backward(msgs) {
		if _, ok := recent[m.User]; !ok && m.User != self {
			recent[m.User] = len(recent)
		}
	}
	slices.SortStableFunc(users, func(a, b chat.User) int {
		ra, oka := recent[a.ID]
		rb, okb := recent[b.ID]
		switch {
		case oka && okb:
			return ra - rb
		case oka:
			return -1
		case okb:
			return 1
		}
		return 0
	})
	return users
}

func linkMentions(text string, ms []mentionable) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], "`") {
			fence := "`"
			if strings.HasPrefix(text[i:], "```") {
				fence = "```"
			}
			if end := strings.Index(text[i+len(fence):], fence); end >= 0 {
				n := i + len(fence) + end + len(fence)
				b.WriteString(text[i:n])
				i = n
				continue
			}
		}
		if text[i] != '@' || i > 0 && isNameRune(text[lastRune(text[:i]):]) {
			b.WriteByte(text[i])
			i++
			continue
		}
		rest := text[i+1:]
		best, bestLen := "", 0
		for _, m := range ms {
			for _, k := range m.keys {
				if k == "" || len(k) > len(rest) || len(k) <= bestLen || !strings.EqualFold(rest[:len(k)], k) {
					continue
				}
				if len(k) < len(rest) && isNameRune(rest[len(k):]) {
					continue // "@Arthurm" isn't a mention of Arthur
				}
				best, bestLen = m.ref, len(k)
			}
		}
		if best == "" {
			b.WriteByte('@')
			i++
			continue
		}
		b.WriteString(best)
		i += 1 + bestLen
	}
	return b.String()
}

func lastRune(s string) int {
	_, n := utf8.DecodeLastRuneInString(s)
	return len(s) - n
}

func isNameRune(s string) bool {
	r := []rune(s)[0]
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
