package chat

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Channel is empty when the change isn't about one channel. User is set when
// a user's presence or status changed.
type Change struct {
	Channel string
	User    string
}

type Store struct {
	backend  Backend
	stateDir string

	mu            sync.Mutex
	snap          Snapshot
	users         map[string]User
	channels      map[string]Channel
	timeline      map[string][]Message // top-level only, ordered by TS
	loaded        map[string]bool
	threads       map[threadKey][]Message
	lookingUp     map[string]bool
	lastRead      map[string]TS
	seen          map[string]TS
	marking       map[string]bool
	threadSeen    map[threadKey]TS
	postedIn      map[threadKey]bool
	unreadReplies map[threadKey]int
	open          map[string]bool
	mutedIn       map[string]map[string]bool // user, then channel
	last          string
	mentions      []Mention
	seenMentions  int
	files         map[string]File
	link          Link
	markErr       string
	away          bool
	presence      map[string]presence
	autoAway      time.Duration // zero is never
	autoBack      bool
	idle          *time.Timer
	stopped       bool
	subs          map[int]func(Change)
	nextSub       int
	alert         func(Alert)
	awayStatus    string
	onStatus      func(string)
	posted        []msgKey
	started       TS
}

type msgKey struct {
	channel string
	ts      TS
}

const maxPosted = 1000

type Alert struct {
	Channel string
	Msg     Message
	// Open is set when the message is in the buffer the user is looking at.
	Open bool
}

func (s *Store) OnAlert(fn func(Alert)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alert = fn
}

type Mention struct {
	Channel string
	Msg     Message
}

const maxMentions = 500

const (
	maxTimeline = 1000
	trimBy      = 250
)

func trim(msgs []Message) []Message {
	if len(msgs) <= maxTimeline {
		return msgs
	}
	cut := len(msgs) - (maxTimeline - trimBy)
	kept := slices.DeleteFunc(slices.Clone(msgs[:cut]), func(m Message) bool { return m.NewReplies == 0 })
	return append(kept, msgs[cut:]...)
}

// An empty stateDir means the open list is not saved.
func NewStore(b Backend, stateDir string) *Store {
	return &Store{
		backend:       b,
		stateDir:      stateDir,
		open:          map[string]bool{},
		mutedIn:       map[string]map[string]bool{},
		files:         map[string]File{},
		timeline:      map[string][]Message{},
		loaded:        map[string]bool{},
		threads:       map[threadKey][]Message{},
		lookingUp:     map[string]bool{},
		lastRead:      map[string]TS{},
		seen:          map[string]TS{},
		marking:       map[string]bool{},
		threadSeen:    map[threadKey]TS{},
		postedIn:      map[threadKey]bool{},
		unreadReplies: map[threadKey]int{},
		subs:          map[int]func(Change){},
		presence:      map[string]presence{},
	}
}

func (s *Store) Start(ctx context.Context) error {
	snap, err := s.backend.Bootstrap(ctx)
	if err != nil {
		return err
	}
	// Nothing sent before launch counts as unread until slack's read cursor arrives.
	now := TSFromTime(time.Now())
	s.mu.Lock()
	s.started = now
	s.snap = snap
	s.away = snap.Away
	s.users = make(map[string]User, len(snap.Users))
	for _, u := range snap.Users {
		s.users[u.ID] = u
	}
	s.channels = make(map[string]Channel, len(snap.Channels))
	for _, c := range snap.Channels {
		s.channels[c.ID] = c
		s.lastRead[c.ID] = now
	}
	s.mu.Unlock()
	if err := s.loadState(); err != nil {
		log.Printf("open list: %v", err)
	}
	s.mu.Lock()
	s.resetIdleLocked()
	s.mu.Unlock()
	go s.readCursors(ctx)
	go s.pollPresence(ctx)
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		s.stopped = true
		s.resetIdleLocked()
		s.mu.Unlock()
	}()

	go func() {
		if err := s.backend.Listen(ctx, s.Apply); err != nil && ctx.Err() == nil {
			log.Printf("live events stopped: %v", err)
			s.Apply(Link{State: Offline, Err: err})
		}
	}()
	return nil
}

func (s *Store) readCursors(ctx context.Context) {
	s.mu.Lock()
	var ids []string
	for id := range s.open {
		if _, ok := s.channels[id]; ok {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	slices.Sort(ids)
	for _, id := range ids {
		ts, err := s.backend.ReadCursor(ctx, id)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("read cursor %s: %v", id, err)
			continue
		}
		s.setReadCursor(id, ts)
		s.Load(ctx, id)
	}
}

func (s *Store) setReadCursor(channel string, ts TS) {
	s.mu.Lock()
	apply := ts != "" && ts > s.seen[channel]
	if apply {
		s.lastRead[channel] = ts
		s.seen[channel] = ts
	}
	subs := s.subscribers()
	s.mu.Unlock()
	if apply {
		for _, fn := range subs {
			fn(Change{Channel: channel})
		}
	}
}

type savedState struct {
	Open  []string            `json:"open"`
	Last  string              `json:"last,omitempty"`
	Muted map[string][]string `json:"muted,omitempty"` // user to channels
}

func (s *Store) statePath() string {
	if s.stateDir == "" {
		return ""
	}
	return filepath.Join(s.stateDir, s.snap.TeamID+".json")
}

func (s *Store) loadState() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.statePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var st savedState
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	for _, id := range st.Open {
		s.open[id] = true
	}
	s.last = st.Last
	for user, channels := range st.Muted {
		for _, c := range channels {
			s.mute(user, c)
		}
	}
	return nil
}

func (s *Store) saveLocked() {
	path := s.statePath()
	if path == "" {
		return
	}
	st := savedState{Open: slices.Sorted(maps.Keys(s.open)), Last: s.last, Muted: s.mutesLocked()}
	data, err := json.MarshalIndent(st, "", "  ")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o700)
	}
	if err == nil {
		err = os.WriteFile(path+".tmp", data, 0o600)
	}
	if err == nil {
		err = os.Rename(path+".tmp", path)
	}
	if err != nil {
		log.Printf("save open list: %v", err)
	}
}

func (s *Store) IsOpen(channel string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open[channel]
}

func (s *Store) SetOpen(channel string, open bool) {
	s.mu.Lock()
	changed := s.open[channel] != open
	if open {
		s.open[channel] = true
	} else {
		delete(s.open, channel)
	}
	if changed {
		s.saveLocked()
	}
	subs := s.subscribers()
	s.mu.Unlock()
	if changed {
		for _, fn := range subs {
			fn(Change{Channel: channel})
		}
	}
}

func (s *Store) Last() string { s.mu.Lock(); defer s.mu.Unlock(); return s.last }

func (s *Store) SetLast(buffer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last != buffer {
		s.last = buffer
		s.saveLocked()
	}
}

func (s *Store) Mentions() []Mention {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.mentions)
}

func (s *Store) SeeMentions() {
	s.mu.Lock()
	changed := s.seenMentions != len(s.mentions)
	s.seenMentions = len(s.mentions)
	s.mu.Unlock()
	if changed {
		s.notify(Change{})
	}
}

func (s *Store) Pings() int {
	s.mu.Lock()
	n := len(s.mentions) - s.seenMentions
	dm := map[string]bool{}
	for id := range s.open {
		dm[id] = s.channels[id].Kind.IsDM()
	}
	s.mu.Unlock()
	for id, isDM := range dm {
		if isDM {
			n += s.Unread(id)
		} else {
			n += s.UnreadMentions(id)
		}
		n += s.NewReplies(id)
	}
	return n
}

func (s *Store) UnseenMentions() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.mentions) > s.seenMentions
}

func (s *Store) names(text string) bool {
	refs := []string{"@" + s.snap.Self, "!here", "!channel", "!everyone"}
	for _, g := range s.snap.Groups {
		if slices.Contains(g.Members, s.snap.Self) {
			refs = append(refs, "!subteam^"+g.ID)
		}
	}
	for _, r := range refs {
		if strings.Contains(text, "<"+r+">") || strings.Contains(text, "<"+r+"|") {
			return true
		}
	}
	return false
}

func (s *Store) Groups() []UserGroup {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.snap.Groups)
}

func (s *Store) GroupsUnavailable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap.GroupsUnavailable
}

func (s *Store) Subscribe(fn func(Change)) (cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextSub
	s.nextSub++
	s.subs[id] = fn
	return func() {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
	}
}

func (s *Store) Apply(ev Event) {
	s.mu.Lock()
	var ch Change
	var alert *Alert
	switch e := ev.(type) {
	case Posted:
		ch.Channel = e.Channel
		key := msgKey{e.Channel, e.Msg.TS}
		if slices.Contains(s.posted, key) || s.has(e.Channel, e.Msg.ThreadTS, e.Msg.TS) || e.Msg.Broadcast && s.has(e.Channel, "", e.Msg.TS) {
			break
		}
		if s.posted = append(s.posted, key); len(s.posted) > maxPosted {
			s.posted = s.posted[len(s.posted)-maxPosted:]
		}
		s.index(e.Msg)
		fromOther := e.Msg.User != s.snap.Self && !e.Msg.System && !s.isMuted(e.Channel, e.Msg.User)
		late := e.Msg.TS < s.started
		c, known := s.channels[e.Channel]
		if !known && !s.lookingUp[e.Channel] {
			s.lookingUp[e.Channel] = true
			go s.discover(e.Channel, fromOther)
		}
		if known && c.Kind.IsDM() && fromOther && !s.open[e.Channel] {
			s.open[e.Channel] = true
			s.saveLocked()
		}
		if fromOther && !late && !s.open[e.Channel] && s.names(e.Msg.Text) {
			s.mentions = append(s.mentions, Mention{e.Channel, e.Msg})
			if n := len(s.mentions); n > maxMentions {
				s.mentions = slices.Delete(s.mentions, 0, n-maxMentions)
				s.seenMentions = max(0, s.seenMentions-(n-maxMentions))
			}
		}
		if fromOther && !late &&
			(c.Kind.IsDM() || !known && strings.HasPrefix(e.Channel, "D") || s.names(e.Msg.Text) || e.Msg.IsReply() && s.isMine(threadKey{e.Channel, e.Msg.ThreadTS})) {
			alert = &Alert{e.Channel, e.Msg, e.Channel == s.last}
		}
		if e.Msg.User == s.snap.Self {
			s.dropPending(e.Channel, e.Msg.ThreadTS)
		}
		if e.Msg.IsReply() && !late {
			s.bumpReplies(e.Channel, e.Msg.ThreadTS)
			if !s.isMuted(e.Channel, e.Msg.User) {
				s.countReply(e.Channel, e.Msg)
			}
		}
		s.put(e.Channel, e.Msg)
	case Edited:
		ch.Channel = e.Channel
		s.index(e.Msg)
		s.update(e.Channel, func(msgs []Message) []Message {
			if i, ok := find(msgs, e.Msg.TS); ok {
				if e.Msg.Reactions == nil {
					e.Msg.Reactions = msgs[i].Reactions // nil means the edit didn't say, not that they were removed
				}
				if e.Msg.Root == "" {
					e.Msg.Root = msgs[i].Root // slack sends the parent with a broadcast but not with its edits
				}
				e.Msg.NewReplies = msgs[i].NewReplies
				msgs[i] = e.Msg
			}
			return msgs
		})
	case Reacted:
		ch.Channel = e.Channel
		s.update(e.Channel, func(msgs []Message) []Message {
			if i, ok := find(msgs, e.TS); ok {
				msgs[i].Reactions = react(msgs[i].Reactions, e)
			}
			return msgs
		})
	case Deleted:
		ch.Channel = e.Channel
		s.update(e.Channel, func(msgs []Message) []Message {
			if i, ok := find(msgs, e.TS); ok {
				return slices.Delete(msgs, i, i+1)
			}
			return msgs
		})
	case ChannelChanged:
		ch.Channel = e.Channel.ID
		if _, ok := s.channels[e.Channel.ID]; ok {
			s.channels[e.Channel.ID] = e.Channel
		}
	case UserChanged:
		ch.User = e.User.ID
		s.users[e.User.ID] = e.User
	case PresenceChanged:
		ch.User = e.User
		p := s.presence[e.User]
		p.away = e.Away
		s.presence[e.User] = p
	case Link:
		s.link = e
	}
	subs := s.subscribers()
	alertFn := s.alert
	s.mu.Unlock()
	if alert != nil && alertFn != nil {
		alertFn(*alert)
	}
	for _, fn := range subs {
		fn(ch)
	}
}

func react(rs []Reaction, e Reacted) []Reaction {
	out := make([]Reaction, 0, len(rs)+1)
	found := false
	for _, r := range rs {
		if r.Name == e.Name {
			found = true
			users := slices.DeleteFunc(slices.Clone(r.Users), func(u string) bool { return u == e.User })
			if !e.Removed {
				users = append(users, e.User)
			}
			if len(users) == 0 {
				continue
			}
			r.Users = users
		}
		out = append(out, r)
	}
	if !found && !e.Removed {
		out = append(out, Reaction{Name: e.Name, Users: []string{e.User}})
	}
	return out
}

func (s *Store) bumpReplies(channel string, parent TS) {
	s.update(channel, func(msgs []Message) []Message {
		if i, ok := find(msgs, parent); ok {
			msgs[i].Replies++
		}
		return msgs
	})
}

func (s *Store) update(channel string, fn func([]Message) []Message) {
	if msgs, ok := s.timeline[channel]; ok {
		s.timeline[channel] = fn(msgs)
	}
	for k, msgs := range s.threads {
		if k.channel == channel {
			s.threads[k] = fn(msgs)
		}
	}
}

func (s *Store) subscribers() []func(Change) {
	out := make([]func(Change), 0, len(s.subs))
	for _, fn := range s.subs {
		out = append(out, fn)
	}
	return out
}

func (s *Store) notify(c Change) {
	s.mu.Lock()
	subs := s.subscribers()
	s.mu.Unlock()
	for _, fn := range subs {
		fn(c)
	}
}

// Load fetches a channel's history only once. Live messages that arrived
// before the fetch are merged in by timestamp.
func (s *Store) Load(ctx context.Context, channel string) {
	s.mu.Lock()
	done := s.loaded[channel]
	s.loaded[channel] = true
	s.mu.Unlock()
	if done {
		return
	}
	go func() {
		msgs, err := s.backend.History(ctx, channel)
		if err != nil {
			log.Printf("history %s: %v", channel, err)
			s.mu.Lock()
			s.loaded[channel] = false
			s.mu.Unlock()
			return
		}
		top := slices.DeleteFunc(msgs, func(m Message) bool { return m.IsReply() && !m.Broadcast })
		s.mu.Lock()
		s.index(top...)
		s.timeline[channel] = trim(merge(s.timeline[channel], top))
		s.showNewReplies(channel)
		subs := s.subscribers()
		s.mu.Unlock()
		for _, fn := range subs {
			fn(Change{Channel: channel})
		}
	}()
}

func (s *Store) discover(channel string, fromOther bool) {
	c, err := s.backend.Info(context.Background(), channel)
	if err != nil {
		log.Printf("info %s: %v", channel, err)
		s.mu.Lock()
		delete(s.lookingUp, channel)
		s.mu.Unlock()
		return
	}
	s.add(c, "", fromOther && c.Kind.IsDM()) // no cursor, so the message that revealed it is unread
}

func (s *Store) add(c Channel, unreadAfter TS, open bool) {
	s.mu.Lock()
	s.channels[c.ID] = c
	if open && !s.open[c.ID] {
		s.open[c.ID] = true
		s.saveLocked()
	}
	if _, ok := s.lastRead[c.ID]; !ok {
		s.lastRead[c.ID] = unreadAfter
	}
	delete(s.lookingUp, c.ID)
	subs := s.subscribers()
	s.mu.Unlock()
	for _, fn := range subs {
		fn(Change{Channel: c.ID})
	}
}

func (s *Store) Join(ctx context.Context, name string) (Channel, error) {
	c, err := s.backend.Join(ctx, name)
	if err != nil {
		return Channel{}, err
	}
	s.add(c, TSFromTime(time.Now()), true)
	return c, nil
}

func (s *Store) OpenDM(ctx context.Context, user string) (Channel, error) {
	c, err := s.backend.OpenDM(ctx, user)
	if err != nil {
		return Channel{}, err
	}
	s.add(c, TSFromTime(time.Now()), true)
	return c, nil
}

type threadKey struct {
	channel string
	ts      TS
}

// LoadThread fetches a thread only once. Live replies keep it current after that.
func (s *Store) LoadThread(ctx context.Context, channel string, thread TS) {
	k := threadKey{channel, thread}
	s.mu.Lock()
	_, done := s.threads[k]
	if !done {
		s.threads[k] = nil
	}
	s.mu.Unlock()
	if done {
		return
	}
	go func() {
		msgs, err := s.backend.Replies(ctx, channel, thread)
		s.mu.Lock()
		if err != nil {
			log.Printf("replies %s/%s: %v", channel, thread, err)
			delete(s.threads, k)
		} else {
			s.index(msgs...)
			s.threads[k] = merge(s.threads[k], msgs)
			if slices.ContainsFunc(msgs, func(m Message) bool { return m.User == s.snap.Self }) {
				s.postedIn[k] = true
			}
			if s.unreadReplies[k] > 0 {
				s.showParent(k)
			}
		}
		subs := s.subscribers()
		s.mu.Unlock()
		for _, fn := range subs {
			fn(Change{Channel: channel})
		}
	}()
}

// Thread returns a copy.
func (s *Store) Thread(channel string, thread TS) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.visible(channel, s.threads[threadKey{channel, thread}])
}

func (s *Store) index(msgs ...Message) {
	for _, m := range msgs {
		for _, f := range m.Files {
			s.files[f.ID] = f
		}
	}
}

func (s *Store) File(id string) (File, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.files[id]
	return f, ok
}

func (s *Store) FileFailed(id, reason string) {
	s.mu.Lock()
	f, ok := s.files[id]
	if !ok || f.Err == reason {
		s.mu.Unlock()
		return
	}
	f.Err = reason
	s.files[id] = f
	touched := map[string]bool{}
	mark := func(channel string, msgs []Message) {
		for i := range msgs {
			for j := range msgs[i].Files {
				if msgs[i].Files[j].ID == id {
					msgs[i].Files = slices.Clone(msgs[i].Files)
					msgs[i].Files[j].Err = reason
					touched[channel] = true
				}
			}
		}
	}
	for channel, msgs := range s.timeline {
		mark(channel, msgs)
	}
	for k, msgs := range s.threads {
		mark(k.channel, msgs)
	}
	subs := s.subscribers()
	s.mu.Unlock()
	for channel := range touched {
		for _, fn := range subs {
			fn(Change{Channel: channel})
		}
	}
}

func (s *Store) Download(ctx context.Context, id string, full bool, w io.Writer) error {
	f, ok := s.File(id)
	if !ok {
		return fmt.Errorf("no file %s", id)
	}
	url := f.Thumb
	if full || url == "" {
		url = f.URL
	}
	return s.backend.Download(ctx, url, w)
}

func (s *Store) DownloadAnim(ctx context.Context, id string, full bool, w io.Writer) error {
	f, ok := s.File(id)
	if !ok {
		return fmt.Errorf("no file %s", id)
	}
	url := f.Anim
	if full || url == "" {
		url = f.URL
	}
	return s.backend.Download(ctx, url, w)
}

func (s *Store) Send(ctx context.Context, channel string, thread TS, text string) error {
	s.mu.Lock()
	m := Message{TS: TSFromTime(time.Now()), User: s.snap.Self, Text: text, ThreadTS: thread, Pending: true}
	s.put(channel, m)
	s.mu.Unlock()
	s.notify(Change{Channel: channel})

	ts, err := s.backend.Send(ctx, channel, thread, text)
	s.mu.Lock()
	s.update(channel, func(msgs []Message) []Message {
		if i, ok := find(msgs, m.TS); ok && msgs[i].Pending {
			return slices.Delete(msgs, i, i+1)
		}
		return msgs
	})
	if err == nil && !s.has(channel, thread, ts) {
		// Slack's echo replaces it and bumps the reply count then.
		m.TS, m.Pending = ts, false
		s.put(channel, m)
	}
	s.mu.Unlock()
	s.notify(Change{Channel: channel})
	return err
}

func (s *Store) put(channel string, m Message) {
	if !m.IsReply() || m.Broadcast {
		s.timeline[channel] = trim(merge(s.timeline[channel], []Message{m}))
	}
	if !m.IsReply() {
		return
	}
	k := threadKey{channel, m.ThreadTS}
	if t, ok := s.threads[k]; ok {
		s.threads[k] = merge(t, []Message{m})
	}
}

func (s *Store) has(channel string, thread, ts TS) bool {
	msgs := s.timeline[channel]
	if thread != "" {
		msgs = s.threads[threadKey{channel, thread}]
	}
	_, ok := find(msgs, ts)
	return ok
}

func (s *Store) dropPending(channel string, thread TS) {
	s.update(channel, func(msgs []Message) []Message {
		if i := slices.IndexFunc(msgs, func(m Message) bool { return m.Pending && m.ThreadTS == thread }); i >= 0 {
			return slices.Delete(msgs, i, i+1)
		}
		return msgs
	})
}

func (s *Store) Upload(ctx context.Context, channel string, thread TS, name string, data []byte) error {
	return s.backend.Upload(ctx, channel, thread, name, data)
}

func (s *Store) Edit(ctx context.Context, channel string, ts TS, text string) error {
	return s.backend.Edit(ctx, channel, ts, text)
}

func (s *Store) React(ctx context.Context, channel string, ts TS, name string) error {
	return s.backend.React(ctx, channel, ts, name)
}

func (s *Store) Newest(channel string, thread TS) (Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.timeline[channel]
	if thread != "" {
		msgs = s.threads[threadKey{channel, thread}]
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if !msgs[i].Pending {
			return msgs[i], true
		}
	}
	return Message{}, false
}

func (s *Store) Members(ctx context.Context, channel string) ([]string, error) {
	return s.backend.Members(ctx, channel)
}

func (s *Store) Search(ctx context.Context, query string) ([]Hit, error) {
	return s.backend.Search(ctx, query)
}

func (s *Store) SetTopic(ctx context.Context, channel, topic string) error {
	return s.backend.SetTopic(ctx, channel, topic)
}

func (s *Store) LastOwn(channel string, thread TS) (Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.timeline[channel]
	if thread != "" {
		msgs = s.threads[threadKey{channel, thread}]
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].User == s.snap.Self && !msgs[i].Pending {
			return msgs[i], true
		}
	}
	return Message{}, false
}

func (s *Store) LastRead(channel string) TS {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRead[channel]
}

func (s *Store) MarkRead(channel string) {
	s.mu.Lock()
	var newest TS // a message still sending has no slack timestamp to mark
	for _, m := range slices.Backward(s.timeline[channel]) {
		if !m.Pending {
			newest = m.TS
			break
		}
	}
	if newest == "" {
		s.mu.Unlock()
		return
	}
	moved := newest > s.lastRead[channel]
	if moved {
		s.lastRead[channel] = newest
	}
	push := newest > s.seen[channel]
	if push {
		s.seen[channel] = newest
	}
	subs := s.subscribers()
	s.mu.Unlock()
	if push {
		s.pushRead(channel)
	}
	if moved {
		for _, fn := range subs {
			fn(Change{Channel: channel})
		}
	}
}

func (s *Store) pushRead(channel string) {
	s.mu.Lock()
	if s.marking[channel] {
		s.mu.Unlock()
		return
	}
	s.marking[channel] = true
	s.mu.Unlock()
	go func() {
		var sent TS
		for {
			s.mu.Lock()
			ts := s.seen[channel]
			if ts == sent {
				delete(s.marking, channel)
				s.mu.Unlock()
				return
			}
			s.mu.Unlock()
			if err := s.backend.Mark(context.Background(), channel, ts); err != nil {
				log.Printf("mark %s read: %v", channel, err)
				s.mu.Lock()
				delete(s.marking, channel)
				s.markErr = s.markProblem(channel, err)
				s.mu.Unlock()
				s.notify(Change{})
				return
			}
			s.mu.Lock()
			cleared := s.markErr != ""
			s.markErr = ""
			s.mu.Unlock()
			if cleared {
				s.notify(Change{})
			}
			sent = ts
		}
	}()
}

func (s *Store) Team() string { s.mu.Lock(); defer s.mu.Unlock(); return s.snap.Team }
func (s *Store) Self() string { s.mu.Lock(); defer s.mu.Unlock(); return s.snap.Self }
func (s *Store) Link() Link   { s.mu.Lock(); defer s.mu.Unlock(); return s.link }

// MarkProblem is why slack last refused to mark something read, or "" once a mark succeeds.
func (s *Store) MarkProblem() string { s.mu.Lock(); defer s.mu.Unlock(); return s.markErr }

var markScope = map[ChannelKind]string{
	Public: "channels:write", Private: "groups:write", Group: "mpim:write", Direct: "im:write",
}

func (s *Store) markProblem(channel string, err error) string {
	kind := s.channels[channel].Kind
	if strings.Contains(err.Error(), "missing_scope") {
		return fmt.Sprintf("Slack won't let clack mark %s read: add the %s scope to your Slack app and reinstall it",
			kindNames[kind], markScope[kind])
	}
	return "couldn't mark " + kindNames[kind] + " read: " + err.Error()
}

var kindNames = map[ChannelKind]string{
	Public: "channels", Private: "private channels", Group: "group messages", Direct: "direct messages",
}

func (s *Store) User(id string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	return u, ok
}

func (s *Store) Channel(id string) (Channel, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.channels[id]
	return c, ok
}

// Channels are sorted by ID.
func (s *Store) Channels() []Channel {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Collect(maps.Values(s.channels))
	slices.SortFunc(out, func(a, b Channel) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func (s *Store) Users() []User {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.snap.Users)
}

// Timeline returns a copy, and whether the history has been requested.
func (s *Store) Timeline(channel string) ([]Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.visible(channel, s.timeline[channel]), s.loaded[channel]
}

// Unread counts other people's messages after the read cursor
func (s *Store) Unread(channel string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.timeline[channel] {
		if m.TS > s.lastRead[channel] && m.User != s.snap.Self && !m.System && !s.isMuted(channel, m.User) {
			n++
		}
	}
	return n
}

// MentionsMe is false for the user's own messages.
func (s *Store) MentionsMe(m Message) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return m.User != s.snap.Self && s.names(m.Text)
}

func (s *Store) UnreadMentions(channel string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.timeline[channel] {
		if m.TS > s.lastRead[channel] && m.User != s.snap.Self && s.names(m.Text) && !s.isMuted(channel, m.User) {
			n++
		}
	}
	return n
}

func merge(base, add []Message) []Message {
	out := slices.Clone(base)
	for _, m := range add {
		if i, ok := find(out, m.TS); ok {
			out[i] = m
		} else {
			out = slices.Insert(out, i, m)
		}
	}
	return out
}

func find(msgs []Message, ts TS) (int, bool) {
	return slices.BinarySearchFunc(msgs, ts, func(m Message, t TS) int {
		switch {
		case m.TS < t:
			return -1
		case m.TS > t:
			return 1
		}
		return 0
	})
}
