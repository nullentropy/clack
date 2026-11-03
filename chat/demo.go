package chat

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"io"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"
)

type Demo struct {
	Chatter time.Duration

	mu    sync.Mutex
	emit  func(Event)
	start time.Time
}

var demoUsers = []User{
	{ID: "U0", Name: "you", RealName: "You"},
	{ID: "U1", Name: "zaphod", RealName: "Zaphod Beeblebrox"},
	{ID: "U2", Name: "arthur", RealName: "Arthur Dent"},
	{ID: "U3", Name: "ford", RealName: "Ford Prefect"},
}

var demoChannels = []Channel{
	{ID: "C1", Name: "general", Kind: Public, Topic: "Ship-wide news. Bridge briefing is at 10:30."},
	{ID: "C2", Name: "engineering", Kind: Public, Topic: "Heart of Gold builds, reviews and the Improbability Drive. Benchmarks: <https://example.com/bench|the usual doc>"},
	{ID: "C3", Name: "launch-plans", Kind: Private, Topic: "Launch is Friday"},
	{ID: "C4", Name: "old-project", Kind: Public},
	{ID: "D1", Kind: Direct, Peer: "U1"},
	{ID: "D2", Kind: Direct, Peer: "U2"},
	{ID: "D3", Kind: Direct, Peer: "U3"},
	{ID: "G1", Name: "mpdm-zaphod--arthur--you-1", Kind: Group, Members: []string{"U1", "U2"}},
}

var demoDirectory = []Channel{
	{ID: "C5", Name: "random", Kind: Public},
	{ID: "C6", Name: "watercooler", Kind: Public},
}

type demoLine struct {
	ago  time.Duration
	user string
	text string
}

var demoHistory = map[string][]demoLine{
	"C1": {
		{26 * time.Hour, "U1", "Morning all. Eddie the computer is unbearably cheerful today."},
		{25*time.Hour + 58*time.Minute, "U1", "Tea is in the usual place. Ignore whatever the Nutri-Matic offers you."},
		{3 * time.Hour, "U2", "Reminder: <!here> the bridge briefing moves to 10:30 today."},
		{2*time.Hour + 59*time.Minute, "U3", "Works for me. I'll post notes in <#C2>."},
		{2*time.Hour + 58*time.Minute, "U3", "Also, for anyone joining late: we go round the bridge left to right, keep updates to a minute each, and take anything that needs more than that to a thread afterwards so the rest of the crew can get back to not panicking. If you can't make it, drop your update in the channel before we start."},
		{40 * time.Minute, "U1", "Has anyone seen <@U2>'s towel? I want to borrow it."},
		{39*time.Minute + 30*time.Second, "U3", "<@U3> set the channel topic: Ship-wide news. Bridge briefing is at 10:30."},
		{38 * time.Minute, "U2", "Top drawer of my desk. Please return it &amp; don't lose it."},
	},
	"C2": {
		{5 * time.Hour, "U3", "Merged the Improbability Drive patch. Benchmarks are in <https://example.com/bench|the usual doc>."},
		{4 * time.Hour, "U1", "Nice. Did the odds move?"},
		{4*time.Hour - time.Minute, "U3", "Up to 2^276,709 to 1 against. Long tail is still the Nutri-Matic."},
		{4*time.Hour - 2*time.Minute, "U2", "<@U2> has joined the channel"},
		{3*time.Hour + 30*time.Minute, "U1", "Try `IMPROBABILITY=finite` and count the *whales*, not the _petunias_:\n```\ngo test -run=NONE -bench=. -benchmem ./drive\n```"},
		{3 * time.Hour, "U2", "<@U0> want to pair on the Nutri-Matic? ~Thursday~ Friday works too, I never could get the hang of Thursdays. Notes from last time: <https://example.com/notes/nutri-matic-2026-10-12|Nutri-Matic notes>"},
	},
	"C3": {
		{50 * time.Minute, "U2", "Draft launch checklist is up. Comments welcome before Friday."},
	},
	"C4": {
		{30 * 24 * time.Hour, "U3", "Archiving the Deep Thought scripts here for reference. The answer was 42."},
	},
	"C5": {
		{2 * 24 * time.Hour, "U2", "Anyone up for chess at lunch?"},
	},
	"D3": {
		{21 * 24 * time.Hour, "U3", "Thanks for the review last month."},
	},
	"G1": {
		{90 * time.Minute, "U1", "Arthur, you, me: Magrathea trip tomorrow?"},
		{85 * time.Minute, "U2", "Fine, as long as it isn't a Thursday."},
	},
	"D1": {
		{2 * time.Hour, "U1", "Can you review my Improbability Drive patch when you get a chance?"},
	},
	"D2": {},
}

var demoReplies = map[string]map[time.Duration][]demoLine{
	"C1": {
		2*time.Hour + 58*time.Minute: {
			{2*time.Hour + 50*time.Minute, "U1", "Does the minute include questions?"},
			{2*time.Hour + 47*time.Minute, "U3", "Questions go here, in the thread."},
			{2*time.Hour + 40*time.Minute, "U2", "Works for me. I'll keep a timer going."},
		},
	},
	"C2": {
		5 * time.Hour: {
			{4*time.Hour + 50*time.Minute, "U2", "Is the Nutri-Matic fix in this one too?"},
			{4*time.Hour + 45*time.Minute, "U3", "No, that's next week. This is just the drive."},
		},
	},
}

var demoFiles = map[string]map[time.Duration][]File{
	"C2": {
		5 * time.Hour: {
			{ID: "F1", Name: "improbability-bench.png", Mime: "image/png", Size: 48213, URL: "demo:bench", Thumb: "demo:bench", ThumbW: 720, ThumbH: 405},
			{ID: "F2", Name: "nutri-matic-notes.pdf", Mime: "application/pdf", Size: 1284113, URL: "demo:notes"},
		},
		4 * time.Hour: {
			{ID: "F3", Name: "drive-spinning-up.gif", Mime: "image/gif", Size: 18000, URL: "demo:spin", Thumb: "demo:spin", ThumbW: 320, ThumbH: 120, Anim: "demo:spin"},
		},
	},
}

var demoReactions = map[string]map[time.Duration][]Reaction{
	"C1": {3 * time.Hour: {{Name: "+1", Users: []string{"U1", "U3"}}, {Name: "tea", Users: []string{"U1"}}}},
	"C2": {5 * time.Hour: {{Name: "tada", Users: []string{"U1", "U2", "U0"}}}},
}

var demoSystem = map[string]map[time.Duration]bool{
	"C1": {39*time.Minute + 30*time.Second: true},
	"C2": {4*time.Hour - 2*time.Minute: true},
}

var demoChatter = []struct{ channel, user, text string }{
	{"C1", "U1", "Table for four at Milliways, leaving in ten minutes."},
	{"C1", "U3", "Has anyone else's build turned into a bowl of petunias?"},
	{"C1", "U2", "<@U0> did you get the briefing notes?"},
	{"C2", "U2", "Found the bug. It was a Babel fish."},
	{"C2", "U3", "Recalibrating the drive now, expect a few improbable side effects."},
	{"D2", "U2", "Got a minute? I can't find a decent cup of tea anywhere."},
	{"C3", "U1", "Added towels to the checklist."},
	{"C4", "U3", "<@U0> can we archive this channel now?"},
	{"D3", "U3", "Got a minute to look at the improbability numbers?"},
}

func (d *Demo) Bootstrap(context.Context) (Snapshot, error) {
	d.mu.Lock()
	d.start = time.Now()
	d.mu.Unlock()
	return Snapshot{Team: "Clack Demo", TeamID: "T0DEMO", Self: "U0", Users: demoUsers, Channels: demoChannels}, nil
}

func (d *Demo) History(_ context.Context, channel string) ([]Message, error) {
	var out []Message
	for _, h := range demoHistory[channel] {
		m := d.message(h)
		m.Files = demoFiles[channel][h.ago]
		m.Reactions = demoReactions[channel][h.ago]
		m.System = demoSystem[channel][h.ago]
		if n := len(demoReplies[channel][h.ago]); n > 0 {
			m.ThreadTS, m.Replies = m.TS, n
		}
		out = append(out, m)
	}
	return out, nil
}

func (d *Demo) Replies(_ context.Context, channel string, thread TS) ([]Message, error) {
	for _, h := range demoHistory[channel] {
		parent := d.message(h)
		if parent.TS != thread {
			continue
		}
		replies := demoReplies[channel][h.ago]
		parent.ThreadTS, parent.Replies = thread, len(replies)
		out := []Message{parent}
		for _, r := range replies {
			m := d.message(r)
			m.ThreadTS = thread
			out = append(out, m)
		}
		return out, nil
	}
	return nil, nil
}

func (d *Demo) Info(_ context.Context, channel string) (Channel, error) {
	for _, c := range slices.Concat(demoChannels, demoDirectory) {
		if c.ID == channel {
			return c, nil
		}
	}
	return Channel{}, fmt.Errorf("no channel %s", channel)
}

func (d *Demo) Upload(_ context.Context, channel string, thread TS, name string, data []byte) error {
	d.mu.Lock()
	emit := d.emit
	d.mu.Unlock()
	if emit != nil {
		ts := TSFromTime(time.Now())
		emit(Posted{Channel: channel, Msg: Message{TS: ts, User: "U0", ThreadTS: thread,
			Files: []File{{ID: "up" + string(ts), Name: name, Size: len(data)}}}})
	}
	return nil
}

func (d *Demo) ReadCursor(context.Context, string) (TS, error) { return "", nil }

func (d *Demo) Join(_ context.Context, name string) (Channel, error) {
	for _, c := range slices.Concat(demoChannels, demoDirectory) {
		if c.Kind == Public && c.Name == name {
			return c, nil
		}
	}
	return Channel{}, fmt.Errorf("no channel called #%s", name)
}

func (d *Demo) OpenDM(_ context.Context, user string) (Channel, error) {
	for _, c := range demoChannels {
		if c.Kind == Direct && c.Peer == user {
			return c, nil
		}
	}
	return Channel{ID: "D" + user, Kind: Direct, Peer: user}, nil
}

func (d *Demo) Mark(context.Context, string, TS) error { return nil }

func (d *Demo) React(_ context.Context, channel string, ts TS, name string) error {
	d.mu.Lock()
	emit := d.emit
	d.mu.Unlock()
	if emit != nil {
		emit(Reacted{Channel: channel, TS: ts, Name: name, User: "U0"})
	}
	return nil
}

func (d *Demo) Members(ctx context.Context, channel string) ([]string, error) {
	c, err := d.Info(ctx, channel)
	if err != nil {
		return nil, err
	}
	switch c.Kind {
	case Direct:
		return []string{"U0", c.Peer}, nil
	case Group:
		return append([]string{"U0"}, c.Members...), nil
	}
	ids := make([]string, len(demoUsers))
	for i, u := range demoUsers {
		ids[i] = u.ID
	}
	return ids, nil
}

func (d *Demo) Search(_ context.Context, query string) ([]Hit, error) {
	var hits []Hit
	for channel, lines := range demoHistory {
		for _, l := range lines {
			if strings.Contains(strings.ToLower(l.text), strings.ToLower(query)) {
				hits = append(hits, Hit{Channel: channel, Msg: d.message(l)})
			}
		}
	}
	slices.SortFunc(hits, func(a, b Hit) int { return strings.Compare(string(b.Msg.TS), string(a.Msg.TS)) })
	return hits, nil
}

func (d *Demo) SetAway(context.Context, bool) error { return nil }

func (d *Demo) Edit(_ context.Context, channel string, ts TS, text string) error {
	d.mu.Lock()
	emit := d.emit
	d.mu.Unlock()
	if emit != nil {
		emit(Edited{Channel: channel, Msg: Message{TS: ts, User: "U0", Text: text}})
	}
	return nil
}

func (d *Demo) SetTopic(ctx context.Context, channel, topic string) error {
	c, err := d.Info(ctx, channel)
	if err != nil {
		return err
	}
	c.Topic = topic
	d.mu.Lock()
	emit := d.emit
	d.mu.Unlock()
	if emit != nil {
		emit(ChannelChanged{Channel: c})
	}
	return nil
}

func (d *Demo) Download(_ context.Context, url string, w io.Writer) error {
	if url == "demo:spin" {
		return gif.EncodeAll(w, spinGIF())
	}
	if url != "demo:bench" {
		return fmt.Errorf("%s isn't available offline", url)
	}
	img := image.NewRGBA(image.Rect(0, 0, 720, 405))
	fill := func(r image.Rectangle, c color.RGBA) { draw.Draw(img, r, image.NewUniform(c), image.Point{}, draw.Src) }
	fill(img.Bounds(), color.RGBA{0x1d, 0x20, 0x25, 0xff})
	fill(image.Rect(60, 350, 680, 353), color.RGBA{0x55, 0x5a, 0x63, 0xff})
	before := color.RGBA{0x55, 0x5a, 0x63, 0xff}
	after := color.RGBA{0xf6, 0xc2, 0x1c, 0xff}
	for i, h := range []int{260, 228, 300, 264, 190, 167, 240, 211} {
		c := before
		if i%2 == 1 {
			c = after
		}
		x := 80 + i*72
		fill(image.Rect(x, 350-h, x+56, 350), c)
	}
	return png.Encode(w, img)
}

// spinGIF is a yellow block running along a track
func spinGIF() *gif.GIF {
	pal := color.Palette{color.RGBA{0x1d, 0x20, 0x25, 0xff}, color.RGBA{0x55, 0x5a, 0x63, 0xff}, color.RGBA{0xf6, 0xc2, 0x1c, 0xff}}
	const w, h, steps = 320, 120, 12
	g := &gif.GIF{Config: image.Config{Width: w, Height: h, ColorModel: pal}}
	first := image.NewPaletted(image.Rect(0, 0, w, h), pal)
	for x := 20; x < w-20; x++ {
		first.SetColorIndex(x, 90, 1)
	}
	for i := range steps {
		x := 20 + i*(w-80)/(steps-1)
		frame := first
		if i > 0 {
			frame = image.NewPaletted(image.Rect(0, 30, w, 80), pal)
		}
		draw.Draw(frame, image.Rect(0, 30, w, 80), image.NewUniform(pal[0]), image.Point{}, draw.Src)
		draw.Draw(frame, image.Rect(x, 40, x+40, 80), image.NewUniform(pal[2]), image.Point{}, draw.Src)
		g.Image = append(g.Image, frame)
		g.Delay = append(g.Delay, 8)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	return g
}

func (d *Demo) message(l demoLine) Message {
	d.mu.Lock()
	defer d.mu.Unlock()
	return Message{TS: TSFromTime(d.start.Add(-l.ago)), User: l.user, Text: l.text}
}

func (d *Demo) Send(_ context.Context, channel string, thread TS, text string) (TS, error) {
	d.mu.Lock()
	emit := d.emit
	d.mu.Unlock()
	ts := TSFromTime(time.Now())
	if emit != nil {
		emit(Posted{Channel: channel, Msg: Message{TS: ts, User: "U0", Text: text, ThreadTS: thread}})
	}
	return ts, nil
}

func (d *Demo) Listen(ctx context.Context, emit func(Event)) error {
	d.mu.Lock()
	d.emit = emit
	d.mu.Unlock()
	emit(Link{State: Connected})
	if d.Chatter <= 0 {
		<-ctx.Done()
		return nil
	}
	tick := time.NewTicker(d.Chatter)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			c := demoChatter[rand.IntN(len(demoChatter))]
			emit(Posted{Channel: c.channel, Msg: Message{TS: TSFromTime(time.Now()), User: c.user, Text: c.text}})
		}
	}
}
