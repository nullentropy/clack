package main

import (
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/nullentropy/caution"
	"github.com/nullentropy/clack/chat"
)

type completion struct {
	head, tail string   // the text before the completed word, and after the caret
	matches    []string // display names, best first
	i          int
	shown      string // the box's text after the last completion
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

// wordAt splits text around the word that ends at the caret
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

func (w *window) nickMatches(channel, prefix string) []string {
	prefix = strings.ToLower(prefix)
	self := w.store.Self()
	matches := map[string]chat.User{}
	for _, u := range w.store.Users() {
		if strings.HasPrefix(strings.ToLower(u.Display()), prefix) || strings.HasPrefix(strings.ToLower(u.Name), prefix) {
			matches[u.ID] = u
		}
	}
	var out []string
	add := func(u chat.User) {
		w.mentions[u.Display()] = u.ID
		out = append(out, u.Display())
		delete(matches, u.ID)
	}
	msgs, _ := w.store.Timeline(channel)
	if w.thread.channel == channel && w.thread.thread != "" {
		msgs = append(msgs, w.store.Thread(channel, w.thread.thread)...)
		sort.Slice(msgs, func(i, j int) bool { return msgs[i].TS < msgs[j].TS })
	}
	for _, m := range slices.Backward(msgs) {
		if u, ok := matches[m.User]; ok && m.User != self {
			add(u)
		}
	}
	rest := make([]chat.User, 0, len(matches))
	for _, u := range matches {
		rest = append(rest, u)
	}
	slices.SortFunc(rest, func(a, b chat.User) int {
		if (a.ID == self) != (b.ID == self) {
			if a.ID == self {
				return 1
			}
			return -1
		}
		return strings.Compare(strings.ToLower(a.Display()), strings.ToLower(b.Display()))
	})
	for _, u := range rest {
		add(u)
	}
	return out
}

func linkMentions(text string, mentions map[string]string) string {
	names := make([]string, 0, len(mentions))
	for name := range mentions {
		if strings.Contains(text, "@"+name) {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for _, name := range names {
		var b strings.Builder
		rest := text
		for {
			i := strings.Index(rest, "@"+name)
			if i < 0 {
				break
			}
			end := i + 1 + len(name)
			b.WriteString(rest[:i])
			if end < len(rest) && isNameRune(rest[end:]) {
				b.WriteString(rest[i:end]) // "@Arthurm" isn't a mention of Arthur
			} else {
				b.WriteString("<@" + mentions[name] + ">")
			}
			rest = rest[end:]
		}
		b.WriteString(rest)
		text = b.String()
	}
	return text
}

func isNameRune(s string) bool {
	r := []rune(s)[0]
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
