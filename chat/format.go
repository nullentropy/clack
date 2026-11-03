package chat

import (
	"html"
	"strconv"
	"strings"
	"time"
)

func Plain(text string, user func(id string) string, channel func(id string) string) string {
	var b strings.Builder
	for {
		open := strings.IndexByte(text, '<')
		if open < 0 {
			break
		}
		end := strings.IndexByte(text[open:], '>')
		if end < 0 {
			break
		}
		b.WriteString(html.UnescapeString(text[:open]))
		b.WriteString(entity(text[open+1:open+end], user, channel))
		text = text[open+end+1:]
	}
	b.WriteString(html.UnescapeString(text))
	return b.String()
}

func HiddenURLs(text string) []string {
	var urls []string
	for {
		open := strings.IndexByte(text, '<')
		if open < 0 {
			return urls
		}
		end := strings.IndexByte(text[open:], '>')
		if end < 0 {
			return urls
		}
		ref, label, hasLabel := strings.Cut(text[open+1:open+end], "|")
		text = text[open+end+1:]
		ref, label = html.UnescapeString(ref), html.UnescapeString(label)
		if !hasLabel || !strings.Contains(ref, "://") {
			continue
		}
		if bare := ref[strings.Index(ref, "://")+3:]; label != ref && label != bare {
			urls = append(urls, ref)
		}
	}
}

func entity(body string, user func(string) string, channel func(string) string) string {
	ref, label, hasLabel := strings.Cut(body, "|")
	label = html.UnescapeString(label)
	switch {
	case strings.HasPrefix(ref, "@"):
		if name := user(ref[1:]); name != "" {
			return "@" + name
		}
		if hasLabel {
			return "@" + label
		}
		return ref
	case strings.HasPrefix(ref, "#"):
		if hasLabel && label != "" {
			return "#" + label
		}
		if name := channel(ref[1:]); name != "" {
			return "#" + name
		}
		return ref
	case strings.HasPrefix(ref, "!date^"):
		if s, ok := date(ref, time.Now()); ok {
			return s
		}
		return label
	case strings.HasPrefix(ref, "!subteam^"):
		return label
	case strings.HasPrefix(ref, "!"):
		return "@" + ref[1:]
	case hasLabel:
		return label
	}
	return strings.TrimPrefix(html.UnescapeString(ref), "mailto:")
}

// date renders a "!date^1700000000^{date_short} at {time}" reference in
// local time, with clack's 24 hour clock
func date(ref string, now time.Time) (string, bool) {
	parts := strings.Split(ref, "^")
	if len(parts) < 3 {
		return "", false
	}
	sec, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", false
	}
	t := time.Unix(sec, 0).In(now.Location())
	pretty := func(layout string) string {
		y, m, d := t.Date()
		switch days := int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Sub(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)).Hours() / 24); days {
		case 0:
			return "today"
		case -1:
			return "yesterday"
		case 1:
			return "tomorrow"
		}
		return t.Format(layout)
	}
	return strings.NewReplacer(
		"{date_num}", t.Format("2006-01-02"),
		"{date}", t.Format("January 2, 2006"),
		"{date_short}", t.Format("Jan 2, 2006"),
		"{date_long}", t.Format("Monday, January 2, 2006"),
		"{date_pretty}", pretty("January 2, 2006"),
		"{date_short_pretty}", pretty("Jan 2, 2006"),
		"{date_long_pretty}", pretty("Monday, January 2, 2006"),
		"{time}", t.Format("15:04"),
		"{time_secs}", t.Format("15:04:05"),
	).Replace(parts[2]), true
}

// Run is a stretch of message text in one style. a Pre run is a code block
type Run struct {
	Text                       string
	Bold, Italic, Strike, Code bool
	Pre                        bool
	Link                       string // a web address the run opens
	User                       string // the ID of a mentioned user
	Channel                    string // the ID of a mentioned channel
}

// Format parses Slack mrkdwn into styled runs
func Format(text string, user func(id string) string, channel func(id string) string) []Run {
	f := formatter{user: user, channel: channel}
	for {
		open := strings.Index(text, "```")
		if open < 0 {
			break
		}
		end := strings.Index(text[open+3:], "```")
		if end < 0 {
			break
		}
		f.inline(strings.TrimSuffix(text[:open], "\n"), Run{})
		if code := strings.Trim(text[open+3:open+3+end], "\n"); code != "" {
			f.out = append(f.out, Run{Text: html.UnescapeString(code), Pre: true})
		}
		text = strings.TrimPrefix(text[open+3+end+3:], "\n")
	}
	f.inline(text, Run{})
	return f.out
}

type formatter struct {
	user, channel func(string) string
	out           []Run
}

// add appends text in style, joining it to the previous run when they match
func (f *formatter) add(text string, style Run) {
	if text == "" {
		return
	}
	if n := len(f.out); n > 0 {
		last := f.out[n-1]
		last.Text, style.Text = "", ""
		if last == style {
			f.out[n-1].Text += text
			return
		}
	}
	style.Text = text
	f.out = append(f.out, style)
}

func (f *formatter) inline(s string, style Run) {
	plain := 0 // start of text not yet added
	flush := func(end int) { f.add(html.UnescapeString(s[plain:end]), style) }
	for i := 0; i < len(s); {
		switch c := s[i]; c {
		case '<':
			if end := strings.IndexByte(s[i:], '>'); end > 0 {
				flush(i)
				f.entity(s[i+1:i+end], style)
				i += end + 1
				plain = i
				continue
			}
		case '`':
			if end := strings.IndexByte(s[i+1:], '`'); end > 0 && !strings.Contains(s[i+1:i+1+end], "\n") {
				flush(i)
				code := style
				code.Code = true
				f.add(html.UnescapeString(s[i+1:i+1+end]), code)
				i += end + 2
				plain = i
				continue
			}
		case '*', '_', '~':
			if end := closer(s, i); end > 0 {
				flush(i)
				inner := style
				switch c {
				case '*':
					inner.Bold = true
				case '_':
					inner.Italic = true
				case '~':
					inner.Strike = true
				}
				f.inline(s[i+1:end], inner)
				i = end + 1
				plain = i
				continue
			}
		}
		i++
	}
	flush(len(s))
}

// closer finds the mark that closes the one at s[i]
func closer(s string, i int) int {
	c := s[i]
	if i > 0 && (!edge(s[i-1]) || s[i-1] == '\\') {
		return -1
	}
	if i+1 >= len(s) || s[i+1] == ' ' || s[i+1] == '\n' || s[i+1] == c {
		return -1
	}
	for j := i + 2; j < len(s); j++ {
		switch {
		case s[j] == '\n':
			return -1
		case s[j] == c && s[j-1] != ' ' && (j+1 == len(s) || edge(s[j+1])):
			return j
		}
	}
	return -1
}

// edge reports whether b can sit beside a formatting mark
func edge(b byte) bool {
	return b < 0x80 && !('a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9')
}

func (f *formatter) entity(body string, style Run) {
	text := entity(body, f.user, f.channel)
	ref, _, _ := strings.Cut(body, "|")
	ref = html.UnescapeString(ref)
	switch {
	case strings.HasPrefix(ref, "@"):
		style.User = ref[1:]
	case strings.HasPrefix(ref, "#"):
		style.Channel = ref[1:]
	case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"):
		style.Link = ref
	}
	f.add(text, style)
}
