package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nullentropy/clack/chat"
)

const helpText = `COMMANDS
  /join #channel, /j     open a channel or DM, joining in Slack if needed
  /part                  close the buffer in clack (you stay in Slack)
  /msg name text, /m     DM someone without switching buffers
  /me action             send an action
  s/old/new/             edit your last message
  /topic text, /t        set the topic; bare shows it
  /who, /names           list members in the status buffer
  /search terms, /s      Slack search, hits in the status buffer
  /react +1, /r          react to the newest message
  /close                 close the open thread
  /clear                 hide scrollback until relaunch
  //text                 send a message starting with /

PRESENCE AND NOTIFICATIONS
  /away, /back           set yourself away or back
  /autoaway 10m          go away after idling; off stops it
  /autoback on           typing while away brings you back
  /notify off            stop OS notifications

CLACK
  /theme name            dark, light or terminal
  /fx name               crt, amber, green, vhs, dither, glow, film; off stops it
  /fx ping on            glitch the window when a DM or mention arrives
  /fx shimmer on         color sweeps through messages that mention you
  /fx sidebar on         animated sidebar background
  /settings, /tokens     tokens, presence and notifications
  /help                  this
  /quit, /exit           exit clack

KEYS
  Tab                    complete a name: gr → @Arthur Dent
  Alt+1…9, Cmd+1…9       open the Nth buffer
  Alt+↑, Alt+↓           previous or next buffer
  Cmd+K, Ctrl+K          quick switcher; ↑ ↓ to pick, Enter to open
  Esc                    close a popup

MOUSE
  reply count            open the thread
  image                  full size
  link, #channel         open it
  drop a file            upload a file`

func (w *window) command(to target, text string) bool {
	if !strings.HasPrefix(text, "/") || strings.HasPrefix(text, "//") {
		return false
	}
	name, arg, _ := strings.Cut(text[1:], " ")
	arg = strings.TrimSpace(arg)
	switch strings.ToLower(name) {
	case "quit", "exit":
		w.quit()
	case "join", "j":
		w.join(arg)
	case "part", "leave":
		w.part(to.channel, arg)
	case "msg", "m":
		w.msg(arg)
	case "me":
		if arg != "" {
			w.post(to, "_"+arg+"_")
		}
	case "clear":
		w.clear()
	case "topic", "t":
		w.topic(to.channel, arg)
	case "who", "names":
		w.who(to.channel)
	case "search", "s":
		w.search(arg)
	case "react", "r":
		w.react(to, arg)
	case "theme":
		w.theme(arg)
	case "fx":
		w.fx(arg)
	case "notify":
		w.notify(arg)
	case "away":
		w.setAway(true)
	case "back":
		w.setAway(false)
	case "autoaway":
		w.autoAway(arg)
	case "autoback":
		w.autoBack(arg)
	case "close":
		w.closeThread()
	case "settings", "tokens":
		w.tokens()
	case "help":
		w.showHelp()
	default:
		w.notice("unknown command /" + name + " · try /help")
	}
	return true
}

func (w *window) join(arg string) {
	name := strings.TrimPrefix(arg, "#")
	if name == "" {
		w.notice("usage: /join #channel, or /join name for a direct message")
		return
	}
	if id, ok := w.findChannel(arg); ok {
		w.store.SetOpen(id, true)
		w.open(id)
		return
	}
	do := func(ctx context.Context) (chat.Channel, error) { return w.store.Join(ctx, name) }
	if u, ok := w.findUser(name); ok && !strings.HasPrefix(arg, "#") {
		do = func(ctx context.Context) (chat.Channel, error) { return w.store.OpenDM(ctx, u) }
	}
	w.notice("joining " + arg + "…")
	go func() {
		c, err := do(context.Background())
		w.s.Update(func() {
			if err != nil {
				w.notice(err.Error())
				return
			}
			w.open(c.ID)
		})
	}()
}

func (w *window) msg(arg string) {
	name, text, _ := strings.Cut(arg, " ")
	text = strings.TrimSpace(text)
	if name == "" || text == "" {
		w.notice("usage: /msg name text, or /msg #channel text")
		return
	}
	if id, ok := w.findChannel(name); ok {
		w.post(target{channel: id}, text)
		w.notice("→ " + name + ": " + text)
		return
	}
	u, ok := w.findUser(name)
	if !ok || strings.HasPrefix(name, "#") {
		w.notice("no channel or person called " + name)
		return
	}
	text = linkMentions(text, w.mentions)
	go func() {
		c, err := w.store.OpenDM(context.Background(), u)
		if err == nil {
			err = w.store.Send(context.Background(), c.ID, "", text)
		}
		w.s.Update(func() {
			if err != nil {
				w.notice("send failed: " + err.Error())
				return
			}
			w.notice("→ " + name + ": " + text)
		})
	}()
}

func (w *window) topic(channel, text string) {
	c, ok := w.store.Channel(channel)
	if !ok || c.Kind.IsDM() {
		w.notice("/topic works in a channel")
		return
	}
	if text == "" {
		if c.Topic == "" {
			w.notice(w.channelLabel(c) + " has no topic · /topic text sets one")
		} else {
			w.notice(w.channelLabel(c) + ": " + chat.Plain(c.Topic, w.userName, w.channelName))
		}
		return
	}
	go func() {
		if err := w.store.SetTopic(context.Background(), channel, text); err != nil {
			w.s.Update(func() { w.notice("topic failed: " + err.Error()) })
		}
	}()
}

func (w *window) setAway(away bool) {
	go func() {
		if err := w.store.SetAway(context.Background(), away); err != nil {
			w.s.Update(func() { w.notice("presence failed: " + err.Error()) })
		}
	}()
}

func (w *window) autoAway(arg string) {
	if arg == "" {
		if d := w.store.AutoAway(); d > 0 {
			w.notice("auto-away after " + shortDuration(d) + " idle · /autoaway off")
		} else {
			w.notice("auto-away is off · /autoaway 10m turns it on")
		}
		return
	}
	d, err := parseAutoAway(arg)
	if err != nil {
		w.notice("usage: /autoaway 10m, or /autoaway off")
		return
	}
	if w.presence(d, w.store.AutoBack()) {
		w.autoAway("")
	}
}

func parseAutoAway(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "off" {
		return 0, nil
	}
	if _, err := strconv.Atoi(s); err == nil {
		s += "m"
	}
	d, err := time.ParseDuration(s)
	if err == nil && d < 0 {
		err = errors.New("negative")
	}
	return d, err
}

func (w *window) presence(autoAway time.Duration, autoBack bool) bool {
	if err := w.app.setPresence(autoAway, autoBack); err != nil {
		w.notice("couldn't save settings: " + err.Error())
		return false
	}
	return true
}

func (w *window) autoBack(arg string) {
	switch arg {
	case "on", "off":
		if !w.presence(w.store.AutoAway(), arg == "on") {
			return
		}
	case "":
	default:
		w.notice("usage: /autoback on, or /autoback off")
		return
	}
	if w.store.AutoBack() {
		w.notice("typing while away brings you back · /autoback off")
	} else {
		w.notice("you stay away until /back · /autoback on")
	}
}

func (w *window) notify(arg string) {
	switch arg {
	case "on", "off":
		if err := w.app.updateSettings(func(s *settings) { s.Quiet = arg == "off" }); err != nil {
			w.notice("couldn't save settings: " + err.Error())
			return
		}
	case "":
	default:
		w.notice("usage: /notify on, or /notify off")
		return
	}
	if s, _ := loadSettings(w.app.settingsPath()); s.Quiet {
		w.notice("notifications are off · /notify on")
	} else {
		w.notice("DMs, mentions and replies in your threads notify you · /notify off")
	}
}

func (w *window) theme(name string) {
	if name == "" {
		w.notice("themes: " + strings.Join(themeNames(), ", ") + " · /theme name switches")
		return
	}
	if err := w.app.setTheme(strings.ToLower(name)); err != nil {
		w.notice(err.Error())
	}
}

func (w *window) fx(arg string) {
	name, state, _ := strings.Cut(strings.ToLower(arg), " ")
	switch {
	case name == "":
		cfg := w.app.fx()
		on := func(b bool) string { return map[bool]string{true: "on", false: "off"}[b] }
		current := cmp.Or(cfg.effect, "none")
		w.notice("window effect: " + current + " · choose from " + strings.Join(effectNames(), ", ") + ", or off")
		w.notice("ping " + on(cfg.ping) + ": glitch the window when a DM or mention arrives")
		w.notice("shimmer " + on(cfg.shimmer) + ": color sweeps through messages that mention you")
		w.notice("sidebar " + on(cfg.sidebar) + ": animated sidebar background")
		w.notice("/fx crt sets the window effect, /fx shimmer on or off toggles one of these")
	case slices.Contains(extras, name):
		if state != "on" && state != "off" {
			w.notice("/fx " + name + " on, or off")
			return
		}
		if err := w.app.setExtra(name, state == "on"); err != nil {
			w.notice(err.Error())
		}
	default:
		if err := w.app.setEffect(name); err != nil {
			w.notice(err.Error())
		}
	}
}

func shortDuration(d time.Duration) string {
	s := d.Round(time.Second).String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

func (w *window) who(channel string) {
	c, ok := w.store.Channel(channel)
	if !ok {
		w.notice("/who works in a conversation")
		return
	}
	label := w.channelLabel(c)
	go func() {
		ids, err := w.store.Members(context.Background(), channel)
		w.s.Update(func() {
			if err != nil {
				w.notice("/who failed: " + err.Error())
				return
			}
			names := w.userNames(ids)
			slices.SortFunc(names, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
			w.note(fmt.Sprintf("%s has %d %s: %s", label, len(names), plural(len(names), "member", "members"), strings.Join(names, ", ")))
		})
	}()
}

func (w *window) react(to target, name string) {
	name = strings.Trim(name, ": ")
	if name == "" || strings.ContainsAny(name, " ") {
		w.notice("usage: /react +1, or any emoji name such as /react tada")
		return
	}
	m, ok := w.store.Newest(to.channel, to.thread)
	if !ok {
		w.notice("no message here to react to")
		return
	}
	go func() {
		if err := w.store.React(context.Background(), to.channel, m.TS, name); err != nil {
			w.s.Update(func() { w.notice("react failed: " + err.Error()) })
		}
	}()
}

func (w *window) search(query string) {
	if query == "" {
		w.notice("usage: /search terms, with Slack's modifiers such as in:#general or from:@Arthur")
		return
	}
	w.notice("searching for " + query + "…")
	go func() {
		hits, err := w.store.Search(context.Background(), query)
		w.s.Update(func() {
			if err != nil {
				w.notice("search failed: " + err.Error())
				return
			}
			w.renderStatus()
			now := chat.TSFromTime(time.Now())
			entries := []statusEntry{{at: now, msg: chat.Message{TS: now, Username: noteNick,
				Text: fmt.Sprintf("search %q: %d %s", query, len(hits), plural(len(hits), "hit", "hits"))}}}
			for _, h := range hits {
				msg := h.Msg
				if c, ok := w.store.Channel(h.Channel); ok {
					msg.Text = w.channelLabel(c) + ": " + msg.Text
				}
				to := h.Channel
				if _, ok := w.store.Channel(to); !ok {
					to = "" // not a conversation the user is in
				}
				entries = append(entries, statusEntry{at: now, msg: msg, channel: to})
			}
			w.addStatus(entries...)
		})
	}()
}

func (w *window) part(current, arg string) {
	id := current
	if arg != "" {
		var ok bool
		if id, ok = w.findChannel(arg); !ok {
			w.notice("no channel or person called " + arg)
			return
		}
	}
	if id == "" {
		w.notice("usage: /part, or /part #channel")
		return
	}
	w.store.SetOpen(id, false)
	if w.active == id {
		w.open(statusKey)
	}
}

func (w *window) findUser(name string) (string, bool) {
	name = strings.ToLower(name)
	for _, u := range w.store.Users() {
		if strings.ToLower(u.Name) == name || strings.ToLower(u.Display()) == name {
			return u.ID, true
		}
	}
	return "", false
}

func (w *window) findChannel(arg string) (string, bool) {
	name := strings.ToLower(strings.TrimPrefix(arg, "#"))
	if name == "" {
		return "", false
	}
	for _, c := range w.store.Channels() {
		if c.Kind != chat.Direct && strings.ToLower(c.Name) == name {
			return c.ID, true
		}
		if u, ok := w.store.User(c.Peer); ok && c.Kind == chat.Direct &&
			(strings.ToLower(u.Name) == name || strings.ToLower(u.Display()) == name) {
			return c.ID, true
		}
	}
	return "", false
}

// let's you do s/typo/nottypo to your last message
type subst struct {
	pattern, repl string
	global        bool
}

func parseSubst(text string) (subst, bool) {
	rest, ok := strings.CutPrefix(strings.TrimPrefix(text, "/"), "s")
	if !ok {
		return subst{}, false
	}
	rest, ok = strings.CutPrefix(strings.TrimPrefix(rest, " "), "/")
	if !ok {
		return subst{}, false
	}
	parts := splitSlashes(rest)
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" {
		return subst{}, false
	}
	sb := subst{pattern: parts[0], repl: parts[1]}
	if len(parts) == 3 {
		switch parts[2] {
		case "":
		case "g":
			sb.global = true
		default:
			return subst{}, false
		}
	}
	return sb, true
}

func splitSlashes(s string) []string {
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '/':
			cur.WriteByte('/')
			i++
		case s[i] == '/':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(s[i])
		}
	}
	return append(parts, cur.String())
}

func (sb subst) apply(text string) (string, bool) {
	re, err := regexp.Compile(sb.pattern)
	if err != nil {
		re = regexp.MustCompile(regexp.QuoteMeta(sb.pattern))
	}
	loc := re.FindStringIndex(text)
	if loc == nil {
		return text, false
	}
	if sb.global {
		return re.ReplaceAllLiteralString(text, sb.repl), true
	}
	return text[:loc[0]] + sb.repl + text[loc[1]:], true
}

func (w *window) substitute(to target, sb subst) {
	if to.channel == "" {
		w.notice("s/old/new/ edits your last message in a channel or thread")
		return
	}
	m, ok := w.store.LastOwn(to.channel, to.thread)
	if !ok {
		w.notice("no message of yours here to edit")
		return
	}
	text, ok := sb.apply(m.Text)
	if !ok {
		w.notice("s/" + sb.pattern + "/: no match in your last message")
		return
	}
	go func() {
		if err := w.store.Edit(context.Background(), to.channel, m.TS, text); err != nil {
			w.s.Update(func() { w.notice("edit failed: " + err.Error()) })
		}
	}()
}
