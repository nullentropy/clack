package main

import (
	"cmp"
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"net/url"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nullentropy/caution"
	"github.com/nullentropy/clack/chat"
)

const (
	// consecutive messages from one author within this window share a nick
	groupWindow = 5 * time.Minute

	fontSize    = 13
	lineHeight  = 18
	threadWidth = 480

	// the nick column fits the longest name in the buffer
	nickWidth   = 140
	threadNick  = 120
	monoAdvance = 0.6 * fontSize

	// image previews fit within these
	imageW       = 360
	imageH       = 270
	threadImageW = 240

	// statusKey names the status buffer
	statusKey = "status"
)

// target is where a composer posts
type target struct {
	channel string
	thread  chat.TS
}

// window is one session's view of the store
type window struct {
	s           *caution.Session
	store       *chat.Store
	quit        func()
	tokens      func()      // opens the settings screen
	app         *app        // for settings
	focused     func() bool // whether the window has the user's attention
	unsubscribe func()
	changesMu   sync.Mutex
	changes     []chat.Change // waiting for the session goroutine
	changesKick chan struct{}

	backdrop *caution.Node
	backFrag string // what backdrop shows, "" for nothing
	shimmer  bool

	active     string                    // a channel ID, or statusKey
	newSince   chat.TS                   // the read cursor when active opened with unread messages
	notes      []statusEntry             // lines commands added to the status buffer
	local      map[string][]chat.Message // command output in other buffers, shown only here
	statusTo   map[chat.TS]string        // status buffer messages, by TS, to the channel each opens
	cleared    map[string]chat.TS        // per buffer, the last message /clear hid
	subtitle   string
	shownTitle string
	drafts     map[target]string // unsent composer text, per channel or thread
	sidebar    *sidebar
	title      *caution.Node
	status     *caution.Node
	timeline   *logView
	composer   *caution.Node
	root       *caution.Node

	thread         target        // zero while no thread is open
	content        *caution.Node // the timeline, and the thread beside it while one is open
	threadView     *caution.Node
	threadChannel  *caution.Node
	threadLog      *logView
	threadComposer *caution.Node

	overlay     *caution.Node
	fitOverlay  func(width, height float64) // resizes the overlay with the window, when it should
	mentions    map[string]string
	dropHint    *caution.Node // shown while files are dragged over the window
	toasts      *caution.Node
	toastCorner string
	threadW     float64 // the thread pane's width
}

// newWindow makes sc's view of store
func newWindow(sc *screen, store *chat.Store) *window {
	s := sc.s
	w := &window{s: s, store: store, changesKick: make(chan struct{}, 1), app: sc.a, quit: sc.a.quit, tokens: sc.askTokens, focused: sc.isFocused,
		mentions: map[string]string{}, drafts: map[target]string{}}
	w.renderTitle()

	w.sidebar = newSidebar(store.Team(), w.open)

	w.title = caution.Label("").FontSize(16).Weight(700)
	w.status = caution.Label("").FontSize(12).Color("$inkFaint")
	w.timeline = newLogView(nickWidth, w.nickName, func(m chat.Message, prev *chat.Message, nickW float64) *caution.Node {
		return w.messageRow(m, prev, nickW, true)
	})
	w.timeline.self = store.Self()
	w.composer = composerField("Message", w.drafted(w.composerKey), func(text string) { w.submit(w.composer, w.composerTarget(), text) })
	w.composer.OnTab(w.onTab(w.composer, func() string { return w.active }))

	header := headerBar(
		caution.VStack().Gap(2).Anchor(caution.A{Left: caution.Px(20), Right: caution.Px(20), CenterY: caution.Px(0)}).
			Kids(w.title, w.status),
	)
	footer := caution.Panel().Dock("bottom").H(60).Kids(w.composer)
	body := caution.Scroll().InsetBottom(8).StickBottom().Kids(w.timeline.col)

	w.threadChannel = caution.Label("").FontSize(12).Color("$inkFaint")
	w.threadLog = newLogView(threadNick, w.nickName, func(m chat.Message, prev *chat.Message, nickW float64) *caution.Node {
		return w.messageRow(m, prev, nickW, false)
	})
	w.threadComposer = composerField("Reply", w.drafted(func() target { return w.thread }), func(text string) { w.submit(w.threadComposer, w.thread, text) })
	w.threadComposer.OnTab(w.onTab(w.threadComposer, func() string { return w.thread.channel }))
	w.threadView = caution.Panel().Kids(caution.DockPanel().Anchor(fill).Kids(
		headerBar(
			caution.VStack().Gap(2).Anchor(caution.A{Left: caution.Px(16), CenterY: caution.Px(0)}).Kids(
				caution.Label("Thread").FontSize(16).Weight(700), w.threadChannel),
			caution.Button("Close").H(26).Anchor(caution.A{Right: caution.Px(12), CenterY: caution.Px(0)}).
				OnClick(w.closeThread),
		),
		caution.Panel().Dock("bottom").H(60).Kids(w.threadComposer),
		caution.Scroll().InsetBottom(8).StickBottom().Kids(w.threadLog.col),
	), caution.Panel().W(1).Bg("$edge").Anchor(caution.A{Left: caution.Px(0), Top: caution.Px(0), Bottom: caution.Px(0)}))

	w.content = caution.HSplit().SplitPinEnd().SplitPos(threadWidth).SplitMin(360, 300).
		Kids(caution.DockPanel().Kids(header, footer, body))
	w.threadW = threadWidth
	w.content.OnSplitResize(func(pos float64) { w.threadW = pos; w.content.SplitPos(pos) })

	w.backdrop = caution.Panel()
	w.root = caution.Panel().Kids(caution.HSplit().SplitPos(260).SplitMin(180, 360).Anchor(fill).Kids(
		caution.Panel().Bg("$panelAlt").Kids(w.backdrop.Anchor(fill), w.sidebar.root.Anchor(fill)),
		w.content,
	))

	s.OnResize(func(width, height float64) {
		if w.fitOverlay != nil {
			w.fitOverlay(width, height)
		}
	})
	w.unsubscribe = store.Subscribe(w.queueChange)
	go w.deliverChanges()

	if last := store.Last(); store.IsOpen(last) {
		w.open(last)
	} else {
		w.open(statusKey)
	}
	return w
}

func (w *window) renderTitle() {
	title := w.store.Team()
	if n := w.store.Pings(); n > 0 {
		title = fmt.Sprintf("%s (%d)", title, n)
	}
	if title != w.shownTitle {
		w.shownTitle = title
		w.s.SetTitle(title)
	}
}

func (w *window) close() { w.unsubscribe() }

var fill = caution.A{Left: caution.Px(0), Right: caution.Px(0), Top: caution.Px(0), Bottom: caution.Px(0)}

func headerBar(kids ...*caution.Node) *caution.Node {
	return caution.Panel().Dock("top").H(56).Kids(kids...).Kids(
		caution.Panel().H(1).Bg("$edge").Anchor(caution.A{Left: caution.Px(0), Right: caution.Px(0), Bottom: caution.Px(0)}),
	)
}

// queueChange runs on the goroutine reading slack events, which can't wait
// for any ui stuff so we don't hold up acks (slack sends again when there's no ack)
func (w *window) queueChange(c chat.Change) {
	w.changesMu.Lock()
	if !slices.Contains(w.changes, c) {
		w.changes = append(w.changes, c)
	}
	w.changesMu.Unlock()
	select {
	case w.changesKick <- struct{}{}:
	default:
	}
}

func (w *window) deliverChanges() {
	defer w.unsubscribe()
	for {
		select {
		case <-w.s.Done():
			return
		case <-w.changesKick:
		}
		w.changesMu.Lock()
		cs := w.changes
		w.changes = nil
		w.changesMu.Unlock()
		w.s.Update(func() {
			for _, c := range cs {
				w.changed(c)
			}
		})
	}
}

func (w *window) changed(c chat.Change) {
	w.renderTitle()
	if c.User != "" {
		w.timeline.restyle(c.User)
		w.threadLog.restyle(c.User)
		w.renderHeader()
	}
	if c.Channel == "" {
		w.renderStatus()
	}
	if w.active == statusKey || c.Channel != "" && c.Channel == w.active {
		w.markRead()
		w.renderHeader()
		w.renderTimeline()
	}
	if c.Channel != "" && c.Channel == w.thread.channel {
		w.renderThread()
	}
	w.sidebar.render(w.sidebarRows())
}

func (w *window) open(id string) {
	if id != w.thread.channel {
		w.closeThread()
	}
	if id != w.active {
		w.composer.SetValueNow(w.drafts[target{channel: id}])
	}
	w.store.SetLast(id)
	w.store.Activity()
	if id == statusKey {
		w.active = id
		w.renderHeader()
		w.composer.Placeholder("/join a channel or person, or /help")
		w.markRead()
		w.renderTimeline()
		w.sidebar.render(w.sidebarRows())
		w.composer.Focus()
		return
	}
	c, ok := w.store.Channel(id)
	if !ok {
		return
	}
	w.active = id
	w.renderHeader()
	w.composer.Placeholder("Message " + w.channelLabel(c))
	w.store.Load(context.Background(), id)
	w.newSince = ""
	if w.store.Unread(id) > 0 {
		w.newSince = w.store.LastRead(id)
	}
	w.markRead()
	if last := w.renderTimeline(); last != nil {
		last.Reveal()
	}
	w.sidebar.render(w.sidebarRows())
	w.composer.Focus()
}

// markRead clears what the user can see, once the window has their attention
func (w *window) markRead() {
	switch {
	case !w.focused():
	case w.active == statusKey:
		w.store.SeeMentions()
	default:
		w.store.MarkRead(w.active)
	}
}

func (w *window) composerTarget() target {
	if w.active == statusKey {
		return target{}
	}
	return target{channel: w.active}
}

func (w *window) composerKey() target { return target{channel: w.active} }

func (w *window) drafted(key func() target) func(string) {
	return func(text string) {
		w.typed(text)
		if text == "" {
			delete(w.drafts, key())
		} else {
			w.drafts[key()] = text
		}
	}
}

func composerField(placeholder string, input, submit func(string)) *caution.Node {
	return caution.TextField("").Placeholder(placeholder).
		Anchor(caution.A{Left: caution.Px(16), Right: caution.Px(16), Top: caution.Px(8)}).
		OnInput(input).OnSubmit(submit)
}

func (w *window) reactionLine(rs []chat.Reaction) *caution.Node {
	row := caution.HStack().Gap(12)
	for _, r := range rs {
		color := "$inkDim"
		if slices.Contains(r.Users, w.store.Self()) {
			color = "$accent"
		}
		row.Add(caution.Label(fmt.Sprintf("%s ×%d", r.Name, len(r.Users))).Mono().FontSize(fontSize).Color(color).
			Tip(strings.Join(w.userNames(r.Users), ", ")))
	}
	return row
}

func (w *window) userNames(ids []string) []string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = w.userName(id)
	}
	return names
}

func (w *window) body(runs []chat.Run, color string) []*caution.Node {
	var nodes []*caution.Node
	var spans []caution.Span
	flush := func() {
		if len(spans) > 0 {
			nodes = append(nodes, w.richText(spans, color))
			spans = nil
		}
	}
	for _, r := range runs {
		if r.Pre {
			flush()
			nodes = append(nodes, caution.HStack().Gap(8).Align("stretch").Kids(
				caution.Panel().W(3).Bg("$code"),
				caution.Label(r.Text).Mono().FontSize(fontSize).Color("$ink").Wrap().Selectable().Fill(1)))
			continue
		}
		spans = append(spans, w.span(r))
	}
	flush()
	if len(nodes) == 0 {
		nodes = append(nodes, w.richText([]caution.Span{{Text: "", Mono: true}}, color))
	}
	return nodes
}

func (w *window) richText(spans []caution.Span, color string) *caution.Node {
	return caution.RichText(spans...).FontSize(fontSize).Color(color).Selectable().OnSpanClick(w.spanClick)
}

func (w *window) span(r chat.Run) caution.Span {
	sp := caution.Span{Text: r.Text, Mono: true, Bold: r.Bold, Italic: r.Italic}
	switch {
	case r.User == w.store.Self():
		sp.Color, sp.Bold = "$highlight", true
	case r.User != "":
		sp.Color = nickColor(r.User)
	case r.Channel != "":
		sp.Color, sp.Action = "$link", "channel:"+r.Channel
	case r.Link != "":
		sp.Color, sp.Action = "$link", "url:"+r.Link
	}
	if r.Code {
		sp.Bg = "$code"
	}
	if r.Strike {
		sp.Color = "$inkFaint"
	}
	return sp
}

func (w *window) spanClick(action string) {
	kind, ref, _ := strings.Cut(action, ":")
	switch kind {
	case "url":
		if err := openURL(ref); err != nil {
			w.notice("couldn't open link: " + err.Error())
		}
	case "channel":
		if _, ok := w.store.Channel(ref); !ok {
			w.notice("you're not in that channel · /join it first")
			return
		}
		w.store.SetOpen(ref, true)
		w.open(ref)
	case "file":
		w.download(ref)
	}
}

func (w *window) download(id string) {
	f, ok := w.store.File(id)
	if !ok {
		return
	}
	dir, err := downloadsDir()
	if err != nil {
		w.notice("couldn't find your Downloads folder: " + err.Error())
		return
	}
	w.notice(fmt.Sprintf("downloading %s (%s)…", f.Name, byteSize(f.Size)))
	go func() {
		path, err := saveFile(context.Background(), w.store, id, dir)
		w.s.Update(func() {
			if err != nil {
				w.notice(fmt.Sprintf("couldn't download %s: %v", f.Name, err))
				return
			}
			w.notice("saved " + path)
		})
	}()
}

func (w *window) linkLine(u string) *caution.Node {
	return w.richText([]caution.Span{
		{Text: "↗ ", Mono: true, Color: "$link", Action: "url:" + u},
		{Text: u, Mono: true, Color: "$inkFaint", Action: "url:" + u},
	}, "$inkFaint")
}

func openURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("clack only opens web links, not %s:", u.Scheme)
	}
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "/usr/bin/open"
	}
	cmd := exec.Command(opener, u.String())
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// for auto-away
func (w *window) typed(text string) {
	if text != "" {
		w.store.Typed()
	}
}

func (w *window) submit(field *caution.Node, to target, text string) {
	if field == w.composer {
		delete(w.drafts, w.composerKey())
	} else {
		delete(w.drafts, w.thread)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	field.ClearValue()
	if sb, ok := parseSubst(text); ok {
		w.substitute(to, sb)
		return
	}
	if w.command(to, text) || to.channel == "" {
		return
	}
	w.post(to, strings.TrimPrefix(text, "/")) // "//x" sends "/x"
}

func (w *window) post(to target, text string) {
	text = linkMentions(text, w.mentions)
	go func() {
		if err := w.store.Send(context.Background(), to.channel, to.thread, text); err != nil {
			w.s.Update(func() { w.notice("send failed: " + err.Error()) })
		}
	}()
}

const maxLocal = 50

// notice adds a line just for the user
func (w *window) notice(text string) {
	if w.active == statusKey || w.active == "" {
		w.note(text)
		return
	}
	if w.local == nil {
		w.local = map[string][]chat.Message{}
	}
	lines := append(w.local[w.active], chat.Message{TS: chat.TSFromTime(time.Now()), Username: noteNick, Text: text})
	w.local[w.active] = lines[max(0, len(lines)-maxLocal):]
	w.renderTimeline()
}

func (w *window) openThread(channel string, ts chat.TS) {
	if t := (target{channel, ts}); t != w.thread {
		w.threadComposer.SetValueNow(w.drafts[t])
		w.thread = t
	}
	if c, ok := w.store.Channel(channel); ok {
		w.threadChannel.SetText(w.channelLabel(c))
	}
	if w.threadView.Parent() == nil {
		w.content.Add(w.threadView)
	}
	w.store.LoadThread(context.Background(), channel, ts)
	if last := w.renderThread(); last != nil {
		last.Reveal()
	}
	w.threadComposer.Focus()
}

func (w *window) closeThread() {
	if w.thread == (target{}) {
		return
	}
	w.thread = target{}
	w.threadView.Remove()
	w.composer.Focus()
}

func (w *window) setFX(cfg fxSettings, theme map[string]string) {
	var back caution.FX
	if cfg.sidebar {
		back = backdrop(theme)
	}
	// the theme's colors are in the frag's uniforms, so compare those too
	if key := back.Frag + fmt.Sprint(back.Uniforms); key != w.backFrag {
		w.backFrag = key
		w.backdrop.Clear()
		if back.Frag != "" {
			w.backdrop.Add(caution.Shader(back).Anchor(fill))
		}
	}
	if cfg.shimmer != w.shimmer {
		w.shimmer = cfg.shimmer
		w.timeline.key, w.threadLog.key = "", "" // rows are cached, so build them again
		w.renderTimeline()
		if w.thread.channel != "" {
			w.renderThread()
		}
	}
}

func (w *window) renderThread() *caution.Node {
	t := w.thread
	w.store.MarkThreadRead(t.channel, t.thread)
	return w.threadLog.sync(t.channel+"/"+string(t.thread), w.store.Thread(t.channel, t.thread), "Loading…")
}

func (w *window) channelLabel(c chat.Channel) string {
	switch c.Kind {
	case chat.Direct:
		return w.userName(c.Peer)
	case chat.Group:
		if len(c.Members) == 0 {
			return c.Name
		}
		names := make([]string, len(c.Members))
		for i, id := range c.Members {
			names[i] = w.userName(id)
		}
		slices.Sort(names)
		return strings.Join(names, ", ")
	}
	return "#" + c.Name
}

// presenceLine says whether a user is away and what status they set, "" for neither
func (w *window) presenceLine(id string) string {
	if id == "" {
		return ""
	}
	var parts []string
	if w.store.UserAway(id) {
		parts = append(parts, "away")
	}
	if u, ok := w.store.User(id); ok {
		if s := u.StatusLine(time.Now()); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " · ")
}

func (w *window) userName(id string) string {
	if u, ok := w.store.User(id); ok {
		return u.Display()
	}
	return id
}

func (w *window) channelName(id string) string {
	if c, ok := w.store.Channel(id); ok {
		return c.Name
	}
	return ""
}

var kindLabel = map[chat.ChannelKind]string{
	chat.Public:  "public channel",
	chat.Private: "private channel",
	chat.Group:   "group message",
	chat.Direct:  "direct message",
}

func (w *window) renderHeader() {
	if w.active == statusKey {
		w.title.SetText("status")
		w.subtitle = "mentions from conversations you haven't opened"
	} else if c, ok := w.store.Channel(w.active); ok {
		w.title.SetText(w.channelLabel(c))
		w.subtitle = kindLabel[c.Kind]
		if c.Topic != "" {
			w.subtitle = chat.Plain(c.Topic, w.userName, w.channelName)
		}
		if p := w.presenceLine(c.Peer); c.Kind == chat.Direct && p != "" {
			w.subtitle = p
		}
	}
	w.renderStatus()
}

func (w *window) renderStatus() {
	line := w.subtitle
	if w.store.Away() {
		line = "away · " + line
	}
	if p := w.store.MarkProblem(); p != "" {
		line += " · " + p
	}
	switch l := w.store.Link(); l.State {
	case chat.Connecting:
		line += " · connecting…"
	case chat.Offline:
		line += " · offline: " + fmt.Sprint(l.Err) + " · /tokens to change them"
	}
	w.status.SetText(line)
}

func (w *window) renderTimeline() *caution.Node {
	if w.active == statusKey {
		return w.renderStatusBuffer()
	}
	msgs, _ := w.store.Timeline(w.active)
	if lines := w.local[w.active]; len(lines) > 0 {
		msgs = slices.SortedStableFunc(slices.Values(append(msgs, lines...)), func(a, b chat.Message) int { return cmp.Compare(a.TS, b.TS) })
	}
	msgs = w.afterClear(w.active, msgs)
	w.timeline.newSince = w.newSince
	return w.timeline.sync(w.active+"|"+string(w.newSince), msgs, "No messages yet.")
}

func (w *window) afterClear(buffer string, msgs []chat.Message) []chat.Message {
	mark, ok := w.cleared[buffer]
	if !ok {
		return msgs
	}
	return slices.DeleteFunc(slices.Clone(msgs), func(m chat.Message) bool { return m.TS <= mark })
}

func (w *window) clear() {
	var msgs []chat.Message
	if w.active == statusKey {
		w.notes = nil
		for _, m := range w.store.Mentions() {
			msgs = append(msgs, m.Msg)
		}
	} else {
		msgs, _ = w.store.Timeline(w.active)
	}
	if len(msgs) == 0 {
		w.renderTimeline()
		return
	}
	if w.cleared == nil {
		w.cleared = map[string]chat.TS{}
	}
	w.cleared[w.active] = slices.MaxFunc(msgs, func(a, b chat.Message) int { return strings.Compare(string(a.TS), string(b.TS)) }).TS
	w.renderTimeline()
}

type logView struct {
	col            *caution.Node
	row            func(m chat.Message, prev *chat.Message, nickW float64) *caution.Node
	nick           func(m chat.Message) string
	nickW, maxNick float64
	key            string
	empty          string
	shown          []chat.Message
	slots          []*caution.Node
	newSince       chat.TS
	self           string
	markAt         int // index of the message under the rule, -1 before it's placed, or markDropped
}

const markDropped = -2

func newLogView(maxNick float64, nick func(chat.Message) string, row func(m chat.Message, prev *chat.Message, nickW float64) *caution.Node) *logView {
	col := caution.VStack().Gap(0).Align("stretch").NoFade().
		Anchor(caution.A{Left: caution.Px(0), Right: caution.Px(0), Top: caution.Px(8)})
	return &logView{col: col, row: row, nick: nick, maxNick: maxNick, markAt: -1}
}

func (v *logView) sync(key string, msgs []chat.Message, empty string) *caution.Node {
	nickW := v.nickWidth(msgs)
	if v.key == key && nickW <= v.nickW && v.dropFront(msgs) {
		n := len(v.shown)
		for i := range n {
			if !msgs[i].Equal(v.shown[i]) {
				v.slots[i].Clear().Kids(v.fill(msgs, i)...)
			}
		}
		v.add(msgs, n)
		v.shown = slices.Clone(msgs)
		return nil
	}
	if v.key == key && len(v.shown) == 0 && len(msgs) == 0 && v.empty == empty {
		return nil
	}
	v.key, v.empty, v.shown, v.slots, v.markAt, v.nickW = key, empty, slices.Clone(msgs), nil, -1, nickW
	v.col.Clear()
	if len(msgs) == 0 {
		v.col.Add(caution.Panel().H(40).Kids(caution.Label(empty).Mono().FontSize(fontSize).Color("$inkFaint").
			Anchor(caution.A{Left: caution.Px(20), Right: caution.Px(20), CenterY: caution.Px(0)})))
		return nil
	}
	return v.add(msgs, 0)
}

// restyle redraws the rows a user wrote, after their presence or status changed
func (v *logView) restyle(user string) {
	for i, m := range v.shown {
		if m.User == user {
			v.slots[i].Clear().Kids(v.fill(v.shown, i)...)
		}
	}
}

func (v *logView) nickWidth(msgs []chat.Message) float64 {
	longest := 0
	for _, m := range msgs {
		longest = max(longest, utf8.RuneCountInString(v.nick(m)))
	}
	return min(math.Ceil(float64(longest)*monoAdvance), v.maxNick)
}

func (v *logView) dropFront(msgs []chat.Message) bool {
	if len(v.shown) == 0 || len(msgs) == 0 {
		return false
	}
	k := slices.IndexFunc(v.shown, func(m chat.Message) bool { return m.TS == msgs[0].TS })
	if k < 0 || len(msgs) < len(v.shown)-k ||
		!slices.EqualFunc(msgs[:len(v.shown)-k], v.shown[k:], func(a, b chat.Message) bool { return a.TS == b.TS }) {
		return false
	}
	if k == 0 {
		return true
	}
	for _, s := range v.slots[:k] {
		s.Remove()
	}
	v.slots, v.shown = v.slots[k:], v.shown[k:]
	if v.markAt >= 0 {
		if v.markAt -= k; v.markAt < 0 {
			v.markAt = markDropped
		}
	}
	v.slots[0].Clear().Kids(v.fill(msgs, 0)...)
	return true
}

func (v *logView) add(msgs []chat.Message, from int) *caution.Node {
	var last *caution.Node
	for i := from; i < len(msgs); i++ {
		if m := msgs[i]; v.newSince != "" && v.markAt == -1 && m.TS > v.newSince && m.User != v.self {
			v.markAt = i
		}
		last = v.col.Add(caution.VStack().Gap(0).Align("stretch").Kids(v.fill(msgs, i)...))
		v.slots = append(v.slots, last)
	}
	return last
}

func (v *logView) fill(msgs []chat.Message, i int) []*caution.Node {
	var kids []*caution.Node
	if prevOnScreen(msgs, i) == nil {
		kids = append(kids, dayDivider(msgs[i].TS.Time()))
	}
	if i == v.markAt {
		kids = append(kids, newMarker())
	}
	return append(kids, v.row(msgs[i], v.prev(msgs, i), v.nickW))
}

func (v *logView) prev(msgs []chat.Message, i int) *chat.Message {
	if i == v.markAt {
		return nil
	}
	return prevOnScreen(msgs, i)
}

func prevOnScreen(msgs []chat.Message, i int) *chat.Message {
	if i == 0 || !sameDay(msgs[i-1].TS.Time(), msgs[i].TS.Time()) {
		return nil
	}
	return &msgs[i-1]
}

func (w *window) nickName(m chat.Message) string {
	if m.System {
		return noteNick
	}
	if u, ok := w.store.User(m.User); ok && u.Display() != "" {
		return u.Display()
	}
	return m.Username
}

func (w *window) messageRow(m chat.Message, prev *chat.Message, nickW float64, replies bool) *caution.Node {
	grouped := prev != nil && !prev.System && !m.System && prev.User == m.User && prev.Username == m.Username &&
		m.TS.Time().Sub(prev.TS.Time()) < groupWindow

	name := w.nickName(m)
	if fit := int(nickW / monoAdvance); utf8.RuneCountInString(name) > fit {
		name = string([]rune(name)[:fit-1]) + "…"
	}
	nick := caution.Panel().W(nickW).H(lineHeight)
	if !grouped {
		color := nickColor(m.User + m.Username)
		if m.User != "" && w.store.UserAway(m.User) {
			color = faded(color)
		}
		label := caution.Label(name).Mono().FontSize(fontSize).Weight(700).Color(color).
			Anchor(caution.A{Right: caution.Px(0), Top: caution.Px(0)})
		if tip := w.presenceLine(m.User); tip != "" {
			label.Tip(w.nickName(m) + " · " + tip)
		}
		nick.Kids(label)
	}

	var lines []*caution.Node
	color := "$ink"
	if isNote(m) || m.System || m.Pending {
		color = "$inkDim"
	}
	runs := chat.Format(m.Text, w.userName, w.channelName)
	if m.System && len(runs) > 0 {
		runs[0].Text = strings.TrimPrefix(runs[0].Text, "@") // "@Arthur has joined the channel" reads as IRC's "arthur has joined"
	}
	if len(runs) > 0 || len(m.Files) == 0 && len(m.Attachments) == 0 {
		lines = append(lines, w.body(runs, color)...)
	}
	for _, u := range chat.HiddenURLs(m.Text) {
		lines = append(lines, w.linkLine(u))
	}
	maxW := float64(imageW)
	if !replies {
		maxW = threadImageW
	}
	for _, f := range m.Files {
		lines = append(lines, w.attachment(f, maxW))
	}
	for _, a := range m.Attachments {
		lines = append(lines, w.card(a))
	}
	if len(m.Reactions) > 0 {
		lines = append(lines, w.reactionLine(m.Reactions))
	}
	if to := w.statusTo[m.TS]; replies && w.active == statusKey && to != "" {
		lines = append(lines, w.openLine(to))
	}
	if replies && m.Replies > 0 {
		channel, ts := w.active, m.TS
		lines = append(lines, caution.Panel().H(lineHeight).Kids(
			caution.Label(replyLine(m)).
				Mono().FontSize(fontSize).Color(replyColor(m)).Anchor(caution.A{Left: caution.Px(0), Top: caution.Px(0)}),
			caution.Glass().Anchor(fill).OnPick(func(float64, float64, *caution.Node) { w.openThread(channel, ts) }),
		))
	}

	return caution.HStack().Gap(8).Pad(1).Kids(
		caution.Panel().W(8).H(1),
		caution.Label(m.TS.Time().Format("15:04")).Mono().FontSize(fontSize).Color("$inkFaint"),
		nick,
		caution.Label("│").Mono().FontSize(fontSize).Color("$inkFaint"),
		w.textColumn(m, lines),
		caution.Panel().W(8).H(1),
	).Tip(m.TS.Time().Format("Monday, January 2 at 15:04"))
}

func (w *window) card(a chat.Attachment) *caution.Node {
	var runs []chat.Run
	for _, part := range []struct {
		text string
		bold bool
	}{{a.Title, true}, {clip(a.Text), false}} {
		if part.text == "" {
			continue
		}
		if len(runs) > 0 {
			runs = append(runs, chat.Run{Text: "\n"})
		}
		for _, r := range chat.Format(part.text, w.userName, w.channelName) {
			r.Bold = r.Bold || part.bold
			runs = append(runs, r)
		}
	}
	bar := a.Color
	if bar == "" {
		bar = "$inkFaint"
	}
	card := caution.HStack().Gap(8).Align("stretch").Kids(
		caution.Panel().W(3).Bg(bar),
		caution.VStack().Gap(2).Align("stretch").Fill(1).Kids(w.body(runs, "$ink")...))
	if a.Pretext == "" {
		return card
	}
	return caution.VStack().Gap(2).Align("stretch").Kids(append(w.body(chat.Format(a.Pretext, w.userName, w.channelName), "$ink"), card)...)
}

// cardLines and cardChars bound a card's text, as slack folds long ones
const (
	cardLines = 6
	cardChars = 600
)

func clip(text string) string {
	cut := len(text)
	if lines := strings.SplitAfterN(text, "\n", cardLines+1); len(lines) > cardLines {
		cut = len(text) - len(lines[cardLines]) - 1
	}
	if cut > cardChars {
		cut = cardChars
		for !utf8.RuneStart(text[cut]) {
			cut--
		}
	}
	if cut == len(text) {
		return text
	}
	text = text[:cut]
	if open := strings.LastIndexByte(text, '<'); open > strings.LastIndexByte(text, '>') {
		text = text[:open] // don't leave half a link
	}
	return strings.TrimRight(text, " \n") + " …"
}

func (w *window) textColumn(m chat.Message, lines []*caution.Node) *caution.Node {
	col := caution.VStack().Gap(2).Align("stretch").Fill(1).Kids(lines...)
	if w.shimmer && w.store.MentionsMe(m) {
		col.Effect(shimmer)
	}
	return col
}

func replyLine(m chat.Message) string {
	s := fmt.Sprintf("└─ %d %s", m.Replies, plural(m.Replies, "reply", "replies"))
	if m.NewReplies > 0 {
		s += fmt.Sprintf(" · %d new", m.NewReplies)
	}
	return s
}

func replyColor(m chat.Message) string {
	if m.NewReplies > 0 {
		return "$highlight"
	}
	return "$accent"
}

func (w *window) attachment(f chat.File, maxW float64) *caution.Node {
	if f.Err != "" {
		return caution.Label(fmt.Sprintf("[%s · couldn't load: %s]", f.Name, f.Err)).
			Mono().FontSize(fontSize).Color("$inkDim").Wrap().Selectable()
	}
	if !f.IsImage() || f.ThumbW <= 0 || f.ThumbH <= 0 {
		return w.richText([]caution.Span{
			{Text: "[", Mono: true},
			{Text: "↓ " + f.Name, Mono: true, Color: "$link", Action: "file:" + f.ID},
			{Text: " · " + byteSize(f.Size) + "]", Mono: true},
		}, "$inkDim")
	}
	scale := min(1, maxW/float64(f.ThumbW), imageH/float64(f.ThumbH))
	pw, ph := float64(f.ThumbW)*scale, float64(f.ThumbH)*scale
	img := caution.Image("/files/" + url.PathEscape(f.ID)).Anchor(fill).Fit("contain").Radius(4).Alt(f.Name)
	w.animate(img, f, false)
	return caution.HStack().Kids(caution.Panel().W(pw).H(ph).Tip(f.Name).Kids(
		img,
		caution.Glass().Anchor(fill).OnPick(func(float64, float64, *caution.Node) { w.showImage(f) }),
	))
}

func (w *window) showImage(f chat.File) {
	img := caution.Image("/files/" + url.PathEscape(f.ID) + "?full").Fit("contain").Alt(f.Name).
		Anchor(caution.A{Left: caution.Px(12), Right: caution.Px(12), Top: caution.Px(12), Bottom: caution.Px(12)})
	dlg := w.showDialog(f.Name, 0, 0, caution.DockPanel().Anchor(fill).Kids(
		w.closeBar(),
		caution.Panel().Kids(
			img,
			caution.Glass().Anchor(fill).OnPick(func(float64, float64, *caution.Node) { w.closeOverlay() }),
		),
	))
	w.fitOverlay = func(width, height float64) {
		dlg.CardSize(max(320, width-80), max(240, height-80))
	}
	w.fitOverlay(w.s.Viewport())
	w.animate(img, f, true)
}

func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

func nickColor(key string) string {
	h := fnv.New32a()
	h.Write([]byte(key))
	return fmt.Sprintf("$nick%d", h.Sum32()%nickColors)
}

func newMarker() *caution.Node {
	return caution.Panel().H(20).Kids(
		caution.Panel().H(1).Bg("$danger").Anchor(caution.A{Left: caution.Px(16), Right: caution.Px(56), CenterY: caution.Px(0)}),
		caution.Label("new").Mono().FontSize(12).Weight(700).Color("$danger").
			Anchor(caution.A{Right: caution.Px(16), CenterY: caution.Px(0)}),
	)
}

func dayDivider(t time.Time) *caution.Node {
	label := t.Format("Monday, January 2")
	if sameDay(t, time.Now()) {
		label = "Today"
	} else if sameDay(t, time.Now().AddDate(0, 0, -1)) {
		label = "Yesterday"
	}
	return caution.Panel().H(28).Kids(
		caution.Label("── " + label + " ──").Mono().FontSize(fontSize).Color("$inkDim").
			Anchor(caution.A{CenterX: caution.Px(0), CenterY: caution.Px(0)}),
	)
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
