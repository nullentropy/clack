package chat

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPlain(t *testing.T) {
	users := map[string]string{"U1": "Arthur"}
	chans := map[string]string{"C1": "general"}
	user := func(id string) string { return users[id] }
	channel := func(id string) string { return chans[id] }

	cases := map[string]string{
		"hi <@U1>":                   "hi @Arthur",
		"hi <@U9|trillian>":          "hi @trillian",
		"see <#C1>":                  "see #general",
		"see <#C2|random>":           "see #random",
		"<!here> deploy":             "@here deploy",
		"ping <!subteam^S1|@oncall>": "ping @oncall",
		"<https://x.dev|docs> and <https://y.dev>": "docs and https://y.dev",
		"<mailto:a@b.c|a@b.c>":                     "a@b.c",
		"a &lt;b&gt; &amp; c":                      "a <b> & c",
		"unclosed < bracket":                       "unclosed < bracket",
	}
	for in, want := range cases {
		if got := Plain(in, user, channel); got != want {
			t.Errorf("Plain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTSRoundTrip(t *testing.T) {
	at := time.Unix(1700000000, 123456000)
	ts := TSFromTime(at)
	if ts != "1700000000.123456" {
		t.Fatalf("TSFromTime = %q", ts)
	}
	if !ts.Time().Equal(at) {
		t.Fatalf("Time() = %v, want %v", ts.Time(), at)
	}
}

type fakeBackend struct {
	history []Message
	replies []Message
	info    map[string]Channel // channels Info, Join and OpenDM can find
	cursors map[string]TS      // slack's read cursor by channel

	mu       sync.Mutex
	marked   []TS
	presence chan bool       // each SetAway
	away     map[string]bool // other users' presence
	awayErr  error
	send     func(text string) (TS, error) // nil posts straight away
}

func (f *fakeBackend) Send(_ context.Context, _ string, _ TS, text string) (TS, error) {
	if f.send != nil {
		return f.send(text)
	}
	return TSFromTime(time.Now()), nil
}

func (f *fakeBackend) Bootstrap(context.Context) (Snapshot, error) {
	return Snapshot{TeamID: "T1", Self: "ME", Channels: []Channel{
		{ID: "C1", Name: "general"},
		{ID: "C2", Name: "random"},
		{ID: "D1", Kind: Direct, Peer: "U1"},
	}}, nil
}
func (f *fakeBackend) History(context.Context, string) ([]Message, error) { return f.history, nil }
func (f *fakeBackend) Replies(context.Context, string, TS) ([]Message, error) {
	return f.replies, nil
}
func (f *fakeBackend) Edit(context.Context, string, TS, string) error    { return nil }
func (f *fakeBackend) React(context.Context, string, TS, string) error   { return nil }
func (f *fakeBackend) Members(context.Context, string) ([]string, error) { return nil, nil }
func (f *fakeBackend) Search(context.Context, string) ([]Hit, error)     { return nil, nil }
func (f *fakeBackend) SetTopic(context.Context, string, string) error    { return nil }

func (f *fakeBackend) SetAway(_ context.Context, away bool) error {
	if f.presence != nil {
		f.presence <- away
	}
	return f.awayErr
}

func (f *fakeBackend) Presence(_ context.Context, user string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.away[user], nil
}

func (f *fakeBackend) Mark(_ context.Context, _ string, ts TS) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marked = append(f.marked, ts)
	return nil
}

func (f *fakeBackend) Download(_ context.Context, url string, w io.Writer) error {
	_, err := io.WriteString(w, "contents of "+url)
	return err
}
func (f *fakeBackend) Upload(context.Context, string, TS, string, []byte) error { return nil }
func (f *fakeBackend) ReadCursor(_ context.Context, channel string) (TS, error) {
	return f.cursors[channel], nil
}
func (f *fakeBackend) Info(_ context.Context, channel string) (Channel, error) {
	return f.find(channel)
}
func (f *fakeBackend) Join(_ context.Context, name string) (Channel, error) {
	return f.find(name)
}
func (f *fakeBackend) OpenDM(_ context.Context, user string) (Channel, error) {
	return f.find(user)
}

func (f *fakeBackend) find(key string) (Channel, error) {
	if c, ok := f.info[key]; ok {
		return c, nil
	}
	return Channel{}, errors.New("not found: " + key)
}
func (f *fakeBackend) Listen(ctx context.Context, _ func(Event)) error { <-ctx.Done(); return nil }

func startStore(t *testing.T, b Backend) (*Store, chan Change) {
	return startStoreIn(t, b, t.TempDir())
}

func startStoreIn(t *testing.T, b Backend, dir string) (*Store, chan Change) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := NewStore(b, dir)
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	changes := make(chan Change, 16)
	s.Subscribe(func(c Change) { changes <- c })
	return s, changes
}

func tsList(msgs []Message) []TS {
	out := make([]TS, len(msgs))
	for i, m := range msgs {
		out[i] = m.TS
	}
	return out
}

func TestLiveMessagesSurviveHistoryLoad(t *testing.T) {
	future := func(sec int64) TS { return TSFromTime(time.Now().Add(time.Duration(sec) * time.Second)) }
	a, b, c := future(10), future(20), future(30)
	s, changes := startStore(t, &fakeBackend{history: []Message{
		{TS: a, User: "U1", Text: "old"},
		{TS: b, User: "U1", Text: "overlap"},
		{TS: b + "9", ThreadTS: a, User: "U1", Text: "a reply"},
	}})

	s.Apply(Posted{Channel: "C1", Msg: Message{TS: c, User: "U1", Text: "live"}})
	s.Apply(Posted{Channel: "C1", Msg: Message{TS: b, User: "U1", Text: "overlap"}})
	<-changes
	<-changes
	s.Load(context.Background(), "C1")
	<-changes

	msgs, loaded := s.Timeline("C1")
	if !loaded {
		t.Fatal("timeline not marked loaded")
	}
	if got := tsList(msgs); len(got) != 3 || got[0] != a || got[1] != b || got[2] != c {
		t.Fatalf("timeline = %v, want [%s %s %s] with the reply left out", got, a, b, c)
	}
	if n := s.Unread("C1"); n != 3 {
		t.Fatalf("unread = %d, want 3", n)
	}
	s.MarkRead("C1")
	<-changes
	if n := s.Unread("C1"); n != 0 {
		t.Fatalf("unread after MarkRead = %d, want 0", n)
	}
}

func TestRepliesCountOnParent(t *testing.T) {
	s, changes := startStore(t, &fakeBackend{})
	parent := TSFromTime(time.Now())
	s.Apply(Posted{Channel: "C1", Msg: Message{TS: parent, User: "U1"}})
	s.Apply(Posted{Channel: "C1", Msg: Message{TS: parent + "1", ThreadTS: parent, User: "U2"}})
	<-changes
	<-changes
	msgs, _ := s.Timeline("C1")
	if len(msgs) != 1 || msgs[0].Replies != 1 {
		t.Fatalf("timeline = %+v, want one parent with Replies=1", msgs)
	}
}

func TestEditAndDelete(t *testing.T) {
	s, changes := startStore(t, &fakeBackend{})
	ts := TSFromTime(time.Now())
	s.Apply(Posted{Channel: "C1", Msg: Message{TS: ts, Text: "typo"}})
	s.Apply(Edited{Channel: "C1", Msg: Message{TS: ts, Text: "fixed"}})
	<-changes
	<-changes
	if msgs, _ := s.Timeline("C1"); msgs[0].Text != "fixed" {
		t.Fatalf("text after edit = %q", msgs[0].Text)
	}
	s.Apply(Deleted{Channel: "C1", TS: ts})
	<-changes
	if msgs, _ := s.Timeline("C1"); len(msgs) != 0 {
		t.Fatalf("timeline after delete = %+v", msgs)
	}
}

func TestLoadedThreadFollowsLiveReplies(t *testing.T) {
	parent := TSFromTime(time.Now().Add(time.Second))
	first, live := parent+"1", parent+"2"
	s, changes := startStore(t, &fakeBackend{replies: []Message{
		{TS: parent, ThreadTS: parent, Replies: 1, User: "U1"},
		{TS: first, ThreadTS: parent, User: "U2", Text: "first"},
	}})
	s.Apply(Posted{Channel: "C1", Msg: Message{TS: parent, ThreadTS: parent, Replies: 1, User: "U1"}})
	<-changes
	s.LoadThread(context.Background(), "C1", parent)
	<-changes
	if got := tsList(s.Thread("C1", parent)); len(got) != 2 || got[0] != parent || got[1] != first {
		t.Fatalf("thread = %v, want [%s %s]", got, parent, first)
	}

	s.Apply(Posted{Channel: "C1", Msg: Message{TS: live, ThreadTS: parent, User: "U3", Text: "live"}})
	<-changes
	thread := s.Thread("C1", parent)
	if got := tsList(thread); len(got) != 3 || got[2] != live {
		t.Fatalf("thread after live reply = %v, want it to end with %s", got, live)
	}
	if thread[0].Replies != 2 {
		t.Fatalf("thread parent Replies = %d, want 2", thread[0].Replies)
	}
	if msgs, _ := s.Timeline("C1"); len(msgs) != 1 || msgs[0].Replies != 2 {
		t.Fatalf("timeline = %+v, want the parent alone with Replies=2", msgs)
	}

	s.Apply(Edited{Channel: "C1", Msg: Message{TS: first, ThreadTS: parent, User: "U2", Text: "edited"}})
	s.Apply(Deleted{Channel: "C1", TS: live})
	<-changes
	<-changes
	thread = s.Thread("C1", parent)
	if len(thread) != 2 || thread[1].Text != "edited" {
		t.Fatalf("thread after edit and delete = %+v", thread)
	}
}

func channelByID(s *Store, id string) (Channel, bool) {
	for _, c := range s.Channels() {
		if c.ID == id {
			return c, true
		}
	}
	return Channel{}, false
}

func TestOpenListSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, changes := startStoreIn(t, &fakeBackend{}, dir)
	s.SetOpen("C1", true)
	s.SetOpen("C2", true)
	s.SetOpen("C2", false)
	s.SetLast("C1")
	<-changes
	<-changes
	<-changes

	again, _ := startStoreIn(t, &fakeBackend{}, dir)
	if !again.IsOpen("C1") || again.IsOpen("C2") || again.Last() != "C1" {
		t.Fatalf("after restart: C1 open %v, C2 open %v, last %q; want C1 open and last", again.IsOpen("C1"), again.IsOpen("C2"), again.Last())
	}
}

func TestDirectMessageFromSomeoneReopens(t *testing.T) {
	s, changes := startStore(t, &fakeBackend{})
	s.Apply(Posted{Channel: "D1", Msg: Message{TS: TSFromTime(time.Now()), User: "ME", Text: "sent from my phone"}})
	<-changes
	if s.IsOpen("D1") {
		t.Fatal("my own message reopened the DM")
	}
	s.Apply(Posted{Channel: "D1", Msg: Message{TS: TSFromTime(time.Now()), User: "U1", Text: "hi"}})
	<-changes
	if !s.IsOpen("D1") {
		t.Fatal("a message from U1 left the DM closed")
	}
}

func TestMentionsOnlyFromClosedChannels(t *testing.T) {
	s, changes := startStore(t, &fakeBackend{})
	s.SetOpen("C1", true)
	<-changes
	ts := TSFromTime(time.Now())
	s.Apply(Posted{Channel: "C1", Msg: Message{TS: ts, User: "U1", Text: "<@ME> in an open channel"}})
	s.Apply(Posted{Channel: "C2", Msg: Message{TS: ts + "1", User: "U1", Text: "no mention here"}})
	s.Apply(Posted{Channel: "C2", Msg: Message{TS: ts + "2", User: "U1", Text: "hey <@ME>, look"}})
	s.Apply(Posted{Channel: "C2", Msg: Message{TS: ts + "3", User: "ME", Text: "<@ME> talking to myself"}})
	for range 4 {
		<-changes
	}
	got := s.Mentions()
	if len(got) != 1 || got[0].Channel != "C2" || got[0].Msg.TS != ts+"2" {
		t.Fatalf("mentions = %+v, want only the one from U1 in C2", got)
	}
}

func TestMessageInUnknownChannelAddsIt(t *testing.T) {
	s, changes := startStore(t, &fakeBackend{info: map[string]Channel{
		"D9": {ID: "D9", Kind: Direct, Peer: "U9"},
	}})
	s.Apply(Posted{Channel: "D9", Msg: Message{TS: TSFromTime(time.Now()), User: "U9", Text: "hi"}})
	<-changes
	<-changes
	c, ok := channelByID(s, "D9")
	if !ok || c.Peer != "U9" || !s.IsOpen("D9") {
		t.Fatalf("D9 = %+v (found %v, open %v), want an open DM with U9", c, ok, s.IsOpen("D9"))
	}
	if n := s.Unread("D9"); n != 1 {
		t.Fatalf("unread in D9 = %d, want the message that revealed it", n)
	}
}

func TestJoinOpens(t *testing.T) {
	s, changes := startStore(t, &fakeBackend{info: map[string]Channel{"lunch": {ID: "C7", Name: "lunch"}}})
	c, err := s.Join(context.Background(), "lunch")
	if err != nil || c.ID != "C7" {
		t.Fatalf("Join = %+v, %v", c, err)
	}
	<-changes
	if _, ok := channelByID(s, "C7"); !ok || !s.IsOpen("C7") {
		t.Fatal("joined channel is not listed and open")
	}
	if _, err := s.Join(context.Background(), "nowhere"); err == nil {
		t.Fatal("joining a missing channel succeeded")
	}
}

func TestChannelChangedUpdatesTopic(t *testing.T) {
	s, changes := startStore(t, &fakeBackend{})
	s.Apply(ChannelChanged{Channel: Channel{ID: "C1", Name: "general", Topic: "new topic"}})
	<-changes
	if c, _ := s.Channel("C1"); c.Topic != "new topic" {
		t.Fatalf("topic = %q, want the new one", c.Topic)
	}
}

func TestDownloadPicksPreviewOrOriginal(t *testing.T) {
	s, changes := startStore(t, &fakeBackend{})
	f := File{ID: "F1", Name: "chart.png", Mime: "image/png", URL: "orig", Thumb: "thumb", ThumbW: 720, ThumbH: 400}
	s.Apply(Posted{Channel: "C1", Msg: Message{TS: TSFromTime(time.Now()), User: "U1", Files: []File{f}}})
	<-changes
	var got strings.Builder
	if err := s.Download(context.Background(), "F1", false, &got); err != nil || got.String() != "contents of thumb" {
		t.Fatalf("preview download = %q, %v", got.String(), err)
	}
	got.Reset()
	if err := s.Download(context.Background(), "F1", true, &got); err != nil || got.String() != "contents of orig" {
		t.Fatalf("full download = %q, %v", got.String(), err)
	}
	if err := s.Download(context.Background(), "F9", false, &got); err == nil {
		t.Fatal("downloading an unseen file succeeded")
	}
}

func TestMarkReadReachesSlackCoalesced(t *testing.T) {
	b := &fakeBackend{}
	s, changes := startStore(t, b)
	base := time.Now().Add(time.Second)
	var last TS
	for i := range 20 {
		last = TSFromTime(base.Add(time.Duration(i) * time.Millisecond))
		s.Apply(Posted{Channel: "C1", Msg: Message{TS: last, User: "U1", Text: "hi"}})
		<-changes
		s.MarkRead("C1")
		<-changes
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		b.mu.Lock()
		marked := slices.Clone(b.marked)
		b.mu.Unlock()
		if n := len(marked); n > 0 && marked[n-1] == last {
			if n > 20 {
				t.Fatalf("%d Mark calls for 20 reads", n)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Slack's cursor = %v, want it to end at %s", marked, last)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUnreadMentions(t *testing.T) {
	s, changes := startStore(t, &fakeBackend{})
	base := time.Now().Add(time.Second)
	post := func(i int, user, text string) Message {
		m := Message{TS: TSFromTime(base.Add(time.Duration(i) * time.Millisecond)), User: user, Text: text}
		s.Apply(Posted{Channel: "C1", Msg: m})
		<-changes
		return m
	}
	ping := post(1, "U1", "hey <@ME>, look")
	post(2, "U1", "no mention")
	mine := post(3, "ME", "<@ME> note to self")
	if !s.MentionsMe(ping) || s.MentionsMe(mine) {
		t.Fatal("MentionsMe should count only other people naming me")
	}
	if n := s.UnreadMentions("C1"); n != 1 {
		t.Fatalf("unread mentions = %d, want 1", n)
	}
	s.MarkRead("C1")
	<-changes
	if n := s.UnreadMentions("C1"); n != 0 {
		t.Fatalf("unread mentions after reading = %d, want 0", n)
	}
}

func TestHiddenURLs(t *testing.T) {
	cases := map[string][]string{
		"see <https://example.com/a/long/path|example.com/a/lo…>":    {"https://example.com/a/long/path"},
		"<https://x.dev/q?a=1&amp;b=2|the docs> and <https://y.dev>": {"https://x.dev/q?a=1&b=2"},
		"<https://z.dev|z.dev> <https://z.dev|https://z.dev>":        nil,
		"<@U1> in <#C1|general>, mail <mailto:a@b.c|a@b.c>":          nil,
		"no links": nil,
	}
	for in, want := range cases {
		if got := HiddenURLs(in); !slices.Equal(got, want) {
			t.Errorf("HiddenURLs(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpeningAChannelMarksOlderHistoryReadInSlack(t *testing.T) {
	old := TSFromTime(time.Now().Add(-time.Hour))
	b := &fakeBackend{history: []Message{{TS: old, User: "U1", Text: "sent before launch"}}}
	s, changes := startStore(t, b)
	s.Load(context.Background(), "C1")
	<-changes
	s.MarkRead("C1")
	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		marked := slices.Clone(b.marked)
		b.mu.Unlock()
		if slices.Contains(marked, old) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Slack's cursor = %v, want %s: Slack still shows the channel unread", marked, old)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s.Unread("C1") != 0 {
		t.Fatal("older history counts as unread in clack")
	}
}

func TestDemoSearchAndMembers(t *testing.T) {
	d := &Demo{}
	if _, err := d.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	hits, err := d.Search(context.Background(), "BENCHMARKS")
	if err != nil || len(hits) == 0 {
		t.Fatalf("search: %v, %v; want the drive message", hits, err)
	}
	for _, h := range hits {
		if !strings.Contains(strings.ToLower(h.Msg.Text), "benchmarks") || h.Channel == "" {
			t.Errorf("hit %+v doesn't match or has no channel", h)
		}
	}
	if ids, _ := d.Members(context.Background(), "D2"); !slices.Equal(ids, []string{"U0", "U2"}) {
		t.Errorf("D2 members = %v, want you and arthur", ids)
	}
}

func TestReactionsAddRemoveAndSurviveEdits(t *testing.T) {
	ts := TSFromTime(time.Now().Add(time.Second))
	s, changes := startStore(t, &fakeBackend{})
	s.Apply(Posted{Channel: "C1", Msg: Message{TS: ts, User: "U1", Text: "ship it"}})
	<-changes
	s.Apply(Reacted{Channel: "C1", TS: ts, Name: "+1", User: "U1"})
	s.Apply(Reacted{Channel: "C1", TS: ts, Name: "+1", User: "ME"})
	s.Apply(Reacted{Channel: "C1", TS: ts, Name: "tada", User: "U1"})
	s.Apply(Edited{Channel: "C1", Msg: Message{TS: ts, User: "U1", Text: "ship it!"}})
	s.Apply(Reacted{Channel: "C1", TS: ts, Name: "tada", User: "U1", Removed: true})
	m, _ := s.Newest("C1", "")
	want := []Reaction{{Name: "+1", Users: []string{"U1", "ME"}}}
	if m.Text != "ship it!" || !slices.EqualFunc(m.Reactions, want, func(a, b Reaction) bool {
		return a.Name == b.Name && slices.Equal(a.Users, b.Users)
	}) {
		t.Fatalf("message = %+v, want the edit with only +1 from U1 and ME", m)
	}
}

func TestNewRepliesInTheUsersThreads(t *testing.T) {
	at := func(sec int) TS { return TSFromTime(time.Now().Add(time.Duration(sec) * time.Second)) }
	s, changes := startStore(t, &fakeBackend{})
	post := func(m Message) { s.Apply(Posted{Channel: "C1", Msg: m}); <-changes }
	mine, theirs := at(1), at(2)
	post(Message{TS: mine, User: "ME", Text: "my question"})
	post(Message{TS: theirs, User: "U1", Text: "their question"})

	post(Message{TS: at(3), User: "U1", ThreadTS: mine, Text: "an answer"})
	post(Message{TS: at(4), User: "U2", ThreadTS: theirs, Text: "not my thread"})
	parent := func(ts TS) Message {
		msgs, _ := s.Timeline("C1")
		i, _ := find(msgs, ts)
		return msgs[i]
	}
	if parent(mine).NewReplies != 1 || parent(theirs).NewReplies != 0 || s.NewReplies("C1") != 1 {
		t.Fatalf("new replies: mine %d, theirs %d, channel %d; want 1, 0, 1",
			parent(mine).NewReplies, parent(theirs).NewReplies, s.NewReplies("C1"))
	}

	post(Message{TS: at(5), User: "ME", ThreadTS: theirs, Text: "joining in"})
	post(Message{TS: at(6), User: "U2", ThreadTS: theirs, Text: "welcome"})
	if parent(theirs).NewReplies != 1 {
		t.Fatalf("after replying in their thread, a new reply counts %d, want 1", parent(theirs).NewReplies)
	}

	s.MarkThreadRead("C1", mine)
	<-changes
	if parent(mine).NewReplies != 0 || s.NewReplies("C1") != 1 {
		t.Fatalf("after reading mine: %d on it, %d in the channel; want 0, 1", parent(mine).NewReplies, s.NewReplies("C1"))
	}
}

func TestAlertsForDMsMentionsAndThreadReplies(t *testing.T) {
	at := func(sec int) TS { return TSFromTime(time.Now().Add(time.Duration(sec) * time.Second)) }
	s, changes := startStore(t, &fakeBackend{})
	var alerts []string
	s.OnAlert(func(a Alert) {
		if a.Open {
			alerts = append(alerts, a.Msg.Text+" (open)")
		} else {
			alerts = append(alerts, a.Msg.Text)
		}
	})
	post := func(channel string, m Message) { s.Apply(Posted{Channel: channel, Msg: m}); <-changes }
	mine := at(1)
	post("C1", Message{TS: mine, User: "ME", Text: "mine"})
	post("D1", Message{TS: at(2), User: "U1", Text: "dm"})
	post("C2", Message{TS: at(3), User: "U1", Text: "chatter"})
	post("C2", Message{TS: at(4), User: "U1", Text: "hey <@ME>"})
	post("C1", Message{TS: at(5), User: "U1", ThreadTS: mine, Text: "reply"})
	post("D1", Message{TS: at(6), User: "ME", Text: "my own dm"})
	s.SetLast("D1")
	post("D1", Message{TS: at(7), User: "U1", Text: "dm while open"})
	want := []string{"dm", "hey <@ME>", "reply", "dm while open (open)"}
	if !slices.Equal(alerts, want) {
		t.Fatalf("alerts = %q, want %q", alerts, want)
	}
}

func TestDemoHistoryIsInOrder(t *testing.T) {
	d := &Demo{}
	if _, err := d.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	for channel := range demoHistory {
		msgs, _ := d.History(context.Background(), channel)
		if !slices.IsSortedFunc(msgs, func(a, b Message) int { return strings.Compare(string(a.TS), string(b.TS)) }) {
			t.Errorf("%s history is out of order", channel)
		}
	}
}

func TestSystemLinesAreNeitherUnreadNorAlerts(t *testing.T) {
	s, changes := startStore(t, &fakeBackend{})
	alerted := false
	s.OnAlert(func(Alert) { alerted = true })
	s.Apply(Posted{Channel: "D1", Msg: Message{TS: TSFromTime(time.Now().Add(time.Second)), User: "U1", Text: "<@U1> has joined the channel", System: true}})
	<-changes
	if s.Unread("D1") != 0 || alerted {
		t.Fatalf("a join line: %d unread, alerted %v; want neither", s.Unread("D1"), alerted)
	}
}

func TestPingsCountUnseenMentionsAndUnreadOnesInOpenChannels(t *testing.T) {
	at := func(sec int) TS { return TSFromTime(time.Now().Add(time.Duration(sec) * time.Second)) }
	s, changes := startStore(t, &fakeBackend{})
	s.SetOpen("C1", true)
	<-changes
	s.Apply(Posted{Channel: "C1", Msg: Message{TS: at(1), User: "U1", Text: "hi <@ME>"}})
	<-changes
	s.Apply(Posted{Channel: "C2", Msg: Message{TS: at(2), User: "U1", Text: "also <@ME>"}})
	<-changes
	if n := s.Pings(); n != 2 {
		t.Fatalf("pings = %d, want one in open C1 and one in the log from C2", n)
	}
	s.SeeMentions()
	s.MarkRead("C1")
	if n := s.Pings(); n != 0 {
		t.Fatalf("after reading both, pings = %d", n)
	}
}

func TestARedeliveredMessageDoesNotAlertOrCountAgain(t *testing.T) {
	at := func(sec int) TS { return TSFromTime(time.Now().Add(time.Duration(sec) * time.Second)) }
	s, changes := startStore(t, &fakeBackend{})
	alerts := 0
	s.OnAlert(func(Alert) { alerts++ })
	post := func(channel string, m Message) { s.Apply(Posted{Channel: channel, Msg: m}); <-changes }
	mine := at(1)
	post("C1", Message{TS: mine, User: "ME", Text: "lunch?"})
	reply := Message{TS: at(2), User: "U1", Text: "sure", ThreadTS: mine}
	dm := Message{TS: at(3), User: "U1", Text: "hey"}
	for range 2 { // Slack sends an event again when the ack was late
		post("C1", reply)
		post("D1", dm)
	}
	if alerts != 2 {
		t.Fatalf("%d alerts, want one for the reply and one for the DM", alerts)
	}
	if n := s.Unread("D1"); n != 1 {
		t.Fatalf("D1 unread = %d, want 1", n)
	}
	if m, _ := s.Newest("C1", ""); m.Replies != 1 {
		t.Fatalf("the thread counts %d replies, want 1", m.Replies)
	}
}

func TestAMessageFromBeforeLaunchDoesNotAlert(t *testing.T) {
	s, changes := startStore(t, &fakeBackend{})
	alerts := 0
	s.OnAlert(func(Alert) { alerts++ })
	// Slack re-sends what the previous run never acked, minutes later
	s.Apply(Posted{Channel: "D1", Msg: Message{TS: TSFromTime(time.Now().Add(-6 * time.Minute)), User: "U1", Text: "hey"}})
	<-changes
	if alerts != 0 {
		t.Fatal("a DM sent before launch alerted")
	}
}

func TestPingsCountWhatWouldNotify(t *testing.T) {
	at := func(sec int) TS { return TSFromTime(time.Now().Add(time.Duration(sec) * time.Second)) }
	parent := at(1)
	s, changes := startStore(t, &fakeBackend{})
	s.SetOpen("C1", true)
	<-changes
	s.Apply(Posted{Channel: "C1", Msg: Message{TS: parent, User: "ME", Text: "lunch?"}})
	<-changes
	s.Apply(Posted{Channel: "C1", Msg: Message{TS: at(2), User: "U1", Text: "nobody named"}})
	<-changes
	if n := s.Pings(); n != 0 {
		t.Fatalf("pings for a plain channel message = %d, want 0", n)
	}
	s.Apply(Posted{Channel: "D1", Msg: Message{TS: at(3), User: "U1", Text: "hey"}})
	<-changes
	s.Apply(Posted{Channel: "C1", Msg: Message{TS: at(4), User: "U1", Text: "sure", ThreadTS: parent}})
	<-changes
	if n := s.Pings(); n != 2 {
		t.Fatalf("pings = %d, want the DM and the reply in my thread", n)
	}
	s.MarkRead("D1")
	s.MarkThreadRead("C1", parent)
	if n := s.Pings(); n != 0 {
		t.Fatalf("after reading both, pings = %d", n)
	}
}

func TestTimelineIsBounded(t *testing.T) {
	s, changes := startStore(t, &fakeBackend{})
	base := time.Now().Add(time.Second)
	for i := range maxTimeline + 1 {
		s.Apply(Posted{Channel: "C2", Msg: Message{TS: TSFromTime(base.Add(time.Duration(i) * time.Millisecond)), User: "U1", Text: "x"}})
		<-changes
	}
	msgs, _ := s.Timeline("C2")
	if len(msgs) != maxTimeline-trimBy {
		t.Fatalf("after %d messages, %d kept; want %d", maxTimeline+1, len(msgs), maxTimeline-trimBy)
	}
	if last := msgs[len(msgs)-1].TS; last != TSFromTime(base.Add(maxTimeline*time.Millisecond)) {
		t.Fatalf("newest kept is %s, want the last one sent", last)
	}
}

type refusingMarks struct{ fakeBackend }

func (*refusingMarks) Mark(context.Context, string, TS) error { return errors.New("missing_scope") }

func TestRefusedMarkSaysWhichScope(t *testing.T) {
	s, changes := startStore(t, &refusingMarks{})
	s.Apply(Posted{Channel: "D1", Msg: Message{TS: TSFromTime(time.Now().Add(time.Second)), User: "U1", Text: "hi"}})
	<-changes
	s.MarkRead("D1")
	deadline := time.Now().Add(time.Second)
	for s.MarkProblem() == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if p := s.MarkProblem(); !strings.Contains(p, "im:write") || !strings.Contains(p, "direct messages") {
		t.Fatalf("problem = %q, want it to name im:write for direct messages", p)
	}
}

func TestSlacksReadCursorAtLaunch(t *testing.T) {
	dir := t.TempDir()
	s, changes := startStoreIn(t, &fakeBackend{}, dir)
	s.SetOpen("C1", true)
	s.SetOpen("C2", true)
	<-changes
	<-changes

	msgs := []Message{
		{TS: "1700000001.000000", User: "U1", Text: "one"},
		{TS: "1700000002.000000", User: "U1", Text: "two"},
		{TS: "1700000003.000000", User: "U1", Text: "three"},
		{TS: "1700000004.000000", User: "U1", Text: "four"},
	}
	b := &fakeBackend{history: msgs, cursors: map[string]TS{"C1": "1700000002.000000", "C2": "1700000004.000000"}}
	again, _ := startStoreIn(t, b, dir)
	deadline := time.Now().Add(2 * time.Second)
	for again.Unread("C1") != 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := again.Unread("C1"); n != 2 {
		t.Fatalf("C1 has %d unread, want the 2 after Slack's cursor", n)
	}
	if n := again.Unread("C2"); n != 0 {
		t.Fatalf("C2 has %d unread, want 0: Slack has it read to the end", n)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.marked) != 0 {
		t.Fatalf("launch marked %v in Slack; it should only read the cursor", b.marked)
	}
}

func TestReadCursorDoesNotUndoViewing(t *testing.T) {
	msgs := []Message{
		{TS: "1700000001.000000", User: "U1", Text: "one"},
		{TS: "1700000002.000000", User: "U1", Text: "two"},
	}
	s, changes := startStore(t, &fakeBackend{history: msgs})
	s.Load(context.Background(), "C1")
	<-changes
	s.MarkRead("C1")
	s.setReadCursor("C1", "1700000001.000000")
	if n := s.Unread("C1"); n != 0 {
		t.Fatalf("C1 has %d unread after viewing it and a stale cursor arriving, want 0", n)
	}
}

func TestSentMessageShowsBeforeSlackAnswers(t *testing.T) {
	answer := make(chan error)
	real := TSFromTime(time.Now().Add(time.Second))
	b := &fakeBackend{send: func(string) (TS, error) { return real, <-answer }}
	s, changes := startStore(t, b)

	done := make(chan error)
	go func() { done <- s.Send(context.Background(), "C1", "", "hello") }()
	<-changes
	msgs, _ := s.Timeline("C1")
	if len(msgs) != 1 || !msgs[0].Pending || msgs[0].Text != "hello" || msgs[0].User != "ME" {
		t.Fatalf("while sending, timeline = %+v, want one pending hello", msgs)
	}
	if _, ok := s.LastOwn("C1", ""); ok {
		t.Fatal("LastOwn offered a message slack doesn't have yet")
	}

	answer <- nil
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	msgs, _ = s.Timeline("C1")
	if len(msgs) != 1 || msgs[0].Pending || msgs[0].TS != real {
		t.Fatalf("after sending, timeline = %+v, want hello at %s", msgs, real)
	}
}

func TestEchoBeforeAnswerIsNotDoubled(t *testing.T) {
	real := TSFromTime(time.Now().Add(time.Second))
	var s *Store
	b := &fakeBackend{send: func(text string) (TS, error) {
		s.Apply(Posted{Channel: "C1", Msg: Message{TS: real, User: "ME", Text: text}})
		return real, nil
	}}
	s, _ = startStore(t, b)
	if err := s.Send(context.Background(), "C1", "", "hi"); err != nil {
		t.Fatal(err)
	}
	if msgs, _ := s.Timeline("C1"); len(msgs) != 1 || msgs[0].TS != real || msgs[0].Pending {
		t.Fatalf("timeline = %+v, want just slack's copy", msgs)
	}
}

func TestRefusedSendDisappears(t *testing.T) {
	s, _ := startStore(t, &fakeBackend{send: func(string) (TS, error) { return "", errors.New("not_in_channel") }})
	if err := s.Send(context.Background(), "C1", "", "hi"); err == nil {
		t.Fatal("want the refusal back")
	}
	if msgs, _ := s.Timeline("C1"); len(msgs) != 0 {
		t.Fatalf("timeline = %+v, want empty", msgs)
	}
}
