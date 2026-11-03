package slackapi

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nullentropy/clack/chat"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type Backend struct {
	api     *slack.Client
	Debug   bool
	self    string
	handles map[string]string // user handle -> ID, to read group DM names

	mu   sync.Mutex
	bots map[string]string // bot ID -> name, for apps whose messages don't say
}

func New(userToken, appToken string) *Backend {
	return &Backend{api: slack.New(userToken, slack.OptionAppLevelToken(appToken)), bots: map[string]string{}}
}

func (b *Backend) Bootstrap(ctx context.Context) (chat.Snapshot, error) {
	auth, err := b.api.AuthTestContext(ctx)
	if err != nil {
		return chat.Snapshot{}, fmt.Errorf("auth.test: %w", err)
	}
	// socket mode will give up when slack refuses the token so do it now while we can
	// report if it doesn't work
	if _, _, err := b.api.StartSocketModeContext(ctx); err != nil {
		return chat.Snapshot{}, fmt.Errorf("the app-level token can't open Socket Mode (%w): check Socket Mode is on, and make a new token with connections:write under Basic Information", err)
	}
	members, err := b.api.GetUsersContext(ctx)
	if err != nil {
		return chat.Snapshot{}, fmt.Errorf("users.list: %w", err)
	}
	b.self = auth.UserID
	b.handles = make(map[string]string, len(members))
	users := make([]chat.User, 0, len(members))
	for _, u := range members {
		b.handles[u.Name] = u.ID
		if u.Deleted {
			continue
		}
		display := u.Profile.DisplayName
		if display == "" {
			display = u.RealName
		}
		users = append(users, chat.User{ID: u.ID, Name: u.Name, RealName: display, Avatar: u.Profile.Image72})
	}

	var channels []chat.Channel
	params := &slack.GetConversationsForUserParameters{
		Types:           []string{"public_channel", "private_channel", "mpim", "im"},
		Limit:           200,
		ExcludeArchived: true,
	}
	for {
		page, cursor, err := b.api.GetConversationsForUserContext(ctx, params)
		if err != nil {
			return chat.Snapshot{}, fmt.Errorf("users.conversations: %w", err)
		}
		for _, c := range page {
			channels = append(channels, b.channel(c))
		}
		if cursor == "" {
			break
		}
		params.Cursor = cursor
	}
	snap := chat.Snapshot{Team: auth.Team, TeamID: auth.TeamID, Self: auth.UserID, Users: users, Channels: channels}
	if p, err := b.api.GetUserPresenceContext(ctx, auth.UserID); err == nil {
		snap.Away = p.ManualAway
	} else {
		log.Printf("users.getPresence: %v", err)
	}
	return snap, nil
}

func (b *Backend) channel(c slack.Channel) chat.Channel {
	out := chat.Channel{ID: c.ID, Name: c.Name, Topic: c.Topic.Value}
	switch {
	case c.IsIM:
		out.Kind, out.Peer = chat.Direct, c.User
	case c.IsMpIM:
		out.Kind = chat.Group
		out.Members = groupMembers(c.Name, b.handles, b.self)
	case c.IsPrivate:
		out.Kind = chat.Private
	}
	return out
}

var groupName = regexp.MustCompile(`^mpdm-(.+)-\d+$`)

func groupMembers(name string, handles map[string]string, self string) []string {
	m := groupName.FindStringSubmatch(name)
	if m == nil {
		return nil
	}
	var out []string
	for _, h := range strings.Split(m[1], "--") {
		if id, ok := handles[h]; ok && id != self {
			out = append(out, id)
		}
	}
	return out
}
func retry(ctx context.Context, call func() error) error {
	for {
		err := call()
		var limited *slack.RateLimitedError
		if !errors.As(err, &limited) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(limited.RetryAfter):
		}
	}
}

func (b *Backend) Info(ctx context.Context, channel string) (chat.Channel, error) {
	var c *slack.Channel
	err := retry(ctx, func() (err error) {
		c, err = b.api.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: channel})
		return err
	})
	if err != nil {
		return chat.Channel{}, fmt.Errorf("conversations.info: %w", err)
	}
	return b.channel(*c), nil
}

func (b *Backend) Upload(ctx context.Context, channel string, thread chat.TS, name string, data []byte) error {
	_, err := b.api.UploadFileContext(ctx, slack.UploadFileParameters{
		Reader: bytes.NewReader(data), FileSize: len(data), Filename: name, Title: name,
		Channel: channel, ThreadTimestamp: string(thread),
	})
	if err != nil && strings.Contains(err.Error(), "missing_scope") {
		return errors.New("Slack refused: the user token needs the files:write scope")
	}
	if err != nil {
		return fmt.Errorf("files upload: %w", err)
	}
	return nil
}

func (b *Backend) ReadCursor(ctx context.Context, channel string) (chat.TS, error) {
	var c *slack.Channel
	err := retry(ctx, func() (err error) {
		c, err = b.api.GetConversationInfoContext(ctx, &slack.GetConversationInfoInput{ChannelID: channel})
		return err
	})
	if err != nil {
		return "", fmt.Errorf("conversations.info: %w", err)
	}
	return chat.TS(c.LastRead), nil
}

func (b *Backend) Join(ctx context.Context, name string) (chat.Channel, error) {
	params := &slack.GetConversationsParameters{Types: []string{"public_channel"}, ExcludeArchived: true, Limit: 1000}
	for {
		var page []slack.Channel
		var cursor string
		err := retry(ctx, func() (err error) {
			page, cursor, err = b.api.GetConversationsContext(ctx, params)
			return err
		})
		if err != nil {
			return chat.Channel{}, fmt.Errorf("conversations.list: %w", err)
		}
		for _, c := range page {
			if c.Name != name {
				continue
			}
			joined, _, _, err := b.api.JoinConversationContext(ctx, c.ID)
			if err != nil {
				return chat.Channel{}, fmt.Errorf("conversations.join: %w", err)
			}
			return b.channel(*joined), nil
		}
		if cursor == "" {
			return chat.Channel{}, fmt.Errorf("no public channel called #%s", name)
		}
		params.Cursor = cursor
	}
}

func (b *Backend) OpenDM(ctx context.Context, user string) (chat.Channel, error) {
	c, _, _, err := b.api.OpenConversationContext(ctx, &slack.OpenConversationParameters{Users: []string{user}, ReturnIM: true})
	if err != nil {
		return chat.Channel{}, fmt.Errorf("conversations.open: %w", err)
	}
	return chat.Channel{ID: c.ID, Kind: chat.Direct, Peer: user}, nil
}

func (b *Backend) History(ctx context.Context, channel string) ([]chat.Message, error) {
	resp, err := b.api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{ChannelID: channel, Limit: 100})
	if err != nil {
		return nil, fmt.Errorf("conversations.history: %w", err)
	}
	// slack returns newest first
	out := make([]chat.Message, len(resp.Messages))
	for i, m := range resp.Messages {
		out[len(out)-1-i] = b.message(ctx, m.Msg)
	}
	return out, nil
}

func (b *Backend) Members(ctx context.Context, channel string) ([]string, error) {
	var out []string
	params := &slack.GetUsersInConversationParameters{ChannelID: channel, Limit: 1000}
	for {
		page, cursor, err := b.api.GetUsersInConversationContext(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("conversations.members: %w", err)
		}
		out = append(out, page...)
		if cursor == "" {
			return out, nil
		}
		params.Cursor = cursor
	}
}

func (b *Backend) Search(ctx context.Context, query string) ([]chat.Hit, error) {
	params := slack.NewSearchParameters()
	params.Count = searchHits
	resp, err := b.api.SearchMessagesContext(ctx, query, params)
	if err != nil {
		return nil, fmt.Errorf("search.messages: %w", err)
	}
	out := make([]chat.Hit, len(resp.Matches))
	for i, m := range resp.Matches {
		out[i] = chat.Hit{Channel: m.Channel.ID, Msg: chat.Message{
			TS: chat.TS(m.Timestamp), User: m.User, Username: m.Username, Text: m.Text,
		}}
	}
	return out, nil
}

const searchHits = 20

var systemSubtypes = map[string]bool{
	"channel_topic": true, "group_topic": true,
	"channel_join": true, "group_join": true,
	"channel_leave": true, "group_leave": true,
	"huddle_thread": true,
}

func (b *Backend) message(ctx context.Context, m slack.Msg) chat.Message {
	username := m.Username
	switch {
	// a user posting through an app, clack included, is still that user
	case username != "" || m.BotID == "" || m.User != "":
	case m.BotProfile != nil:
		username = m.BotProfile.Name
	default: // incoming webhooks name nobody
		username = b.botName(ctx, m.BotID)
	}
	text := m.Text
	if m.SubType == "huddle_thread" && text == "" {
		text = "A huddle started" // as slack writes it, for the huddle's thread to hang off
	}
	return chat.Message{
		System:      systemSubtypes[m.SubType],
		TS:          chat.TS(m.Timestamp),
		User:        m.User,
		Username:    username,
		Text:        text,
		ThreadTS:    chat.TS(m.ThreadTimestamp),
		Replies:     m.ReplyCount,
		Files:       append(files(m.Files), linkedImages(m.Blocks, m.Attachments)...),
		Attachments: append(calls(m.Blocks), attachments(m.Attachments)...),
		Reactions:   reactions(m.Reactions),
		ReplyUsers:  m.ReplyUsers,
	}
}

// calls are the cards for zoom meetings and others: what to click to join until the call ends
func calls(blocks slack.Blocks) []chat.Attachment {
	var out []chat.Attachment
	for _, b := range blocks.BlockSet {
		c, ok := b.(*slack.CallBlock)
		if !ok || c.Call == nil || c.Call.V1 == nil {
			continue
		}
		v := c.Call.V1
		a := chat.Attachment{Title: cmp.Or(v.Name, "Call")}
		switch {
		case v.HasEnded:
			a.Text = "ended"
		case v.JoinURL != "":
			a.Title = "<" + v.JoinURL + "|" + a.Title + ">"
		}
		if v.DisplayID != "" && !v.HasEnded {
			a.Text = "Meeting ID: " + v.DisplayID
		}
		out = append(out, a)
	}
	return out
}

// slack's names for attachment colors
var attachmentColors = map[string]string{"good": "#2eb67d", "warning": "#ecb22e", "danger": "#e01e5a"}

// botName is an app's name, looked up once per app
func (b *Backend) botName(ctx context.Context, id string) string {
	b.mu.Lock()
	name, ok := b.bots[id]
	b.mu.Unlock()
	if ok {
		return name
	}
	bot, err := b.api.GetBotInfoContext(ctx, slack.GetBotInfoParameters{Bot: id})
	if err != nil {
		log.Printf("bots.info %s: %v", id, err) // and stays nameless rather than asking for every message
	} else {
		name = bot.Name
	}
	b.mu.Lock()
	b.bots[id] = name
	b.mu.Unlock()
	return name
}

// linkedImages are images a message links to instead of an upload
func linkedImages(blocks slack.Blocks, atts []slack.Attachment) []chat.File {
	var out []chat.File
	add := func(u, name string, w, h int) {
		if u == "" {
			return
		}
		if w <= 0 || h <= 0 {
			w, h = 320, 240 // image blocks don't say, and the image is fit inside
		}
		sum := sha1.Sum([]byte(u))
		f := chat.File{ID: "link-" + hex.EncodeToString(sum[:8]), Name: cmp.Or(name, "image"), URL: u, Thumb: u, ThumbW: w, ThumbH: h, Mime: "image/jpeg"}
		if p, err := url.Parse(u); err == nil {
			f.Mime = cmp.Or(mime.TypeByExtension(path.Ext(p.Path)), f.Mime)
		}
		if f.Animated() {
			f.Anim = u
		}
		out = append(out, f)
	}
	for _, b := range blocks.BlockSet {
		if img, ok := b.(*slack.ImageBlock); ok {
			name := img.AltText
			if img.Title != nil {
				name = cmp.Or(img.Title.Text, name)
			}
			add(img.ImageURL, name, 0, 0)
		}
	}
	for _, a := range atts {
		add(a.ImageURL, a.Title, a.ImageWidth, a.ImageHeight)
	}
	return out
}

func attachments(in []slack.Attachment) []chat.Attachment {
	var out []chat.Attachment
	for _, a := range in {
		c := chat.Attachment{Pretext: a.Pretext, Title: a.Title, Text: a.Text}
		if a.TitleLink != "" && c.Title != "" {
			c.Title = "<" + a.TitleLink + "|" + c.Title + ">"
		}
		if c == (chat.Attachment{}) && a.ImageURL == "" {
			c.Text = a.Fallback
		}
		if c == (chat.Attachment{}) {
			continue
		}
		c.Color = attachmentColors[a.Color]
		if c.Color == "" && a.Color != "" {
			c.Color = "#" + strings.TrimPrefix(a.Color, "#")
		}
		out = append(out, c)
	}
	return out
}

func reactions(in []slack.ItemReaction) []chat.Reaction {
	if len(in) == 0 {
		return nil
	}
	out := make([]chat.Reaction, len(in))
	for i, r := range in {
		out[i] = chat.Reaction{Name: r.Name, Users: r.Users}
	}
	return out
}

func (b *Backend) React(ctx context.Context, channel string, ts chat.TS, name string) error {
	return b.api.AddReactionContext(ctx, name, slack.NewRefToMessage(channel, string(ts)))
}

func files(in []slack.File) []chat.File {
	var out []chat.File
	for _, f := range in {
		c := chat.File{ID: f.ID, Name: f.Name, Mime: f.Mimetype, Size: f.Size, URL: f.URLPrivate}
		for _, t := range []struct {
			url  string
			w, h int
		}{{f.Thumb720, f.Thumb720W, f.Thumb720H}, {f.Thumb480, f.Thumb480W, f.Thumb480H}, {f.Thumb360, f.Thumb360W, f.Thumb360H}} {
			if t.url != "" {
				c.Thumb, c.ThumbW, c.ThumbH = t.url, t.w, t.h
				break
			}
		}
		c.Anim = cmp.Or(f.Thumb480Gif, f.Thumb360Gif) // the thumbs above are stills, even for a GIF
		out = append(out, c)
	}
	return out
}

func (b *Backend) Edit(ctx context.Context, channel string, ts chat.TS, text string) error {
	_, _, _, err := b.api.UpdateMessageContext(ctx, channel, string(ts), slack.MsgOptionText(text, false))
	return err
}

func (b *Backend) SetTopic(ctx context.Context, channel, topic string) error {
	_, err := b.api.SetTopicOfConversationContext(ctx, channel, topic)
	return err
}

func (b *Backend) SetAway(ctx context.Context, away bool) error {
	presence := "auto"
	if away {
		presence = "away"
	}
	return b.api.SetUserPresenceContext(ctx, presence)
}

func (b *Backend) Mark(ctx context.Context, channel string, ts chat.TS) error {
	return retry(ctx, func() error { return b.api.MarkConversationContext(ctx, channel, string(ts)) })
}

func (b *Backend) Download(ctx context.Context, url string, w io.Writer) error {
	if !slackHosted(url) {
		return fetch(ctx, url, w) // without the token, which is for Slack alone
	}
	err := b.api.GetFileContext(ctx, url, w)
	// if we don't have files:read slack will send us to the sign in page
	if err != nil && strings.Contains(err.Error(), "sign-in page") {
		return errors.New("the token can't read files: add the files:read scope and reinstall the Slack app")
	}
	return err
}

func slackHosted(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && (u.Host == "slack.com" || strings.HasSuffix(u.Host, ".slack.com"))
}

// maxFetch bounds an image fetched from outside slack
const maxFetch = 32 << 20

func fetch(ctx context.Context, url string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", req.URL.Host, res.Status)
	}
	_, err = io.Copy(w, io.LimitReader(res.Body, maxFetch))
	return err
}

func (b *Backend) Replies(ctx context.Context, channel string, thread chat.TS) ([]chat.Message, error) {
	params := &slack.GetConversationRepliesParameters{ChannelID: channel, Timestamp: string(thread), Limit: 200}
	var out []chat.Message
	for {
		page, more, cursor, err := b.api.GetConversationRepliesContext(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("conversations.replies: %w", err)
		}
		for _, m := range page {
			out = append(out, b.message(ctx, m.Msg))
		}
		if !more || cursor == "" {
			return out, nil
		}
		params.Cursor = cursor
	}
}

func (b *Backend) Send(ctx context.Context, channel string, thread chat.TS, text string) (chat.TS, error) {
	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if thread != "" {
		opts = append(opts, slack.MsgOptionTS(string(thread)))
	}
	_, ts, err := b.api.PostMessageContext(ctx, channel, opts...)
	return chat.TS(ts), err
}

func (b *Backend) Listen(ctx context.Context, emit func(chat.Event)) error {
	client := socketmode.New(b.api)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-client.Events:
				b.handle(client, ev, emit)
			}
		}
	}()
	return client.RunContext(ctx)
}

func (b *Backend) handle(client *socketmode.Client, ev socketmode.Event, emit func(chat.Event)) {
	if b.Debug {
		log.Printf("socket mode: %s", describe(ev))
	}
	switch ev.Type {
	case socketmode.EventTypeConnecting:
		emit(chat.Link{State: chat.Connecting})
	case socketmode.EventTypeConnected:
		log.Printf("socket mode: connected")
		emit(chat.Link{State: chat.Connected})
	case socketmode.EventTypeConnectionError, socketmode.EventTypeInvalidAuth:
		log.Printf("socket mode: %s: %v", ev.Type, ev.Data)
		emit(chat.Link{State: chat.Offline, Err: fmt.Errorf("socket mode: %s", ev.Type)})
	case socketmode.EventTypeEventsAPI:
		if ev.Request != nil {
			client.Ack(*ev.Request)
		}
		api, ok := ev.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}
		switch r := api.InnerEvent.Data.(type) {
		case *slackevents.ReactionAddedEvent:
			emit(chat.Reacted{Channel: r.Item.Channel, TS: chat.TS(r.Item.Timestamp), Name: r.Reaction, User: r.User})
		case *slackevents.ReactionRemovedEvent:
			emit(chat.Reacted{Channel: r.Item.Channel, TS: chat.TS(r.Item.Timestamp), Name: r.Reaction, User: r.User, Removed: true})
		}
		if m, ok := api.InnerEvent.Data.(*slackevents.MessageEvent); ok {
			if e := b.messageEvent(m); e != nil {
				emit(e)
			}
			// the event carries no topic, so the channel is fetched again
			if m.SubType == "channel_topic" || m.SubType == "group_topic" {
				go b.refresh(m.Channel, emit)
			}
		}
	}
}

// for debug
func describe(ev socketmode.Event) string {
	api, ok := ev.Data.(slackevents.EventsAPIEvent)
	if !ok {
		return string(ev.Type)
	}
	s := fmt.Sprintf("%s %s", ev.Type, api.InnerEvent.Type)
	if m, ok := api.InnerEvent.Data.(*slackevents.MessageEvent); ok {
		s += fmt.Sprintf(" channel=%s subtype=%q user=%s ts=%s", m.Channel, m.SubType, m.User, m.TimeStamp)
	}
	if ev.Request != nil && ev.Request.RetryAttempt > 0 {
		s += fmt.Sprintf(" retry=%d (%s)", ev.Request.RetryAttempt, ev.Request.RetryReason)
	}
	return s
}

func (b *Backend) refresh(channel string, emit func(chat.Event)) {
	c, err := b.Info(context.Background(), channel)
	if err != nil {
		log.Printf("refresh %s: %v", channel, err)
		return
	}
	emit(chat.ChannelChanged{Channel: c})
}

func (b *Backend) messageEvent(m *slackevents.MessageEvent) chat.Event {
	switch m.SubType {
	case "message_changed":
		if m.Message == nil {
			return nil
		}
		return chat.Edited{Channel: m.Channel, Msg: b.message(context.Background(), *m.Message)}
	case "message_deleted":
		return chat.Deleted{Channel: m.Channel, TS: chat.TS(m.DeletedTimeStamp)}
	case "", "bot_message", "thread_broadcast", "me_message", "file_share",
		"channel_topic", "group_topic", "channel_join", "group_join", "channel_leave", "group_leave", "huddle_thread":
		if m.Message == nil {
			return nil
		}
		msg := b.message(context.Background(), *m.Message) // the whole event, as slackevents copies it here
		if m.SubType == "thread_broadcast" {
			msg.ThreadTS = ""
		}
		return chat.Posted{Channel: m.Channel, Msg: msg}
	}
	return nil
}
