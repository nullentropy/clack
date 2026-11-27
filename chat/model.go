package chat

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

type ChannelKind int

const (
	Public ChannelKind = iota
	Private
	Group // multi-person direct message
	Direct
)

func (k ChannelKind) IsDM() bool { return k == Group || k == Direct }

type Channel struct {
	ID      string
	Name    string
	Kind    ChannelKind
	Peer    string
	Members []string // a Group's other members
	Topic   string   // Slack mrkdwn
}

type User struct {
	ID          string
	Name        string
	RealName    string
	Avatar      string
	Status      string
	StatusEmoji string    // slack name, such as ":palm_tree:"
	StatusUntil time.Time // when slack clears the status, zero for never
}

func (u User) StatusLine(now time.Time) string {
	if !u.StatusUntil.IsZero() && !now.Before(u.StatusUntil) {
		return ""
	}
	return strings.TrimSpace(u.StatusEmoji + " " + u.Status)
}

func (u User) Display() string {
	if u.RealName != "" {
		return u.RealName
	}
	return u.Name
}

// TS is a Slack message timestamp, such as "1700000000.000100".
type TS string

func (t TS) Time() time.Time {
	sec, frac, _ := strings.Cut(string(t), ".")
	s, _ := strconv.ParseInt(sec, 10, 64)
	us, _ := strconv.ParseInt(frac, 10, 64)
	return time.Unix(s, us*1000)
}

func TSFromTime(t time.Time) TS {
	return TS(fmt.Sprintf("%d.%06d", t.Unix(), t.Nanosecond()/1000))
}

type Message struct {
	TS       TS
	User     string // empty for bots, which have Username instead
	Username string
	Text     string // Slack mrkdwn
	ThreadTS TS
	Replies  int
	// Broadcast is set on a thread reply that was also sent to the channel.
	Broadcast bool
	// Root is the parent's text on a broadcast, or "" when slack didn't send it.
	Root        string
	Files       []File
	Attachments []Attachment
	Reactions   []Reaction

	System  bool // joins, leaves and other events that aren't messages
	Pending bool

	ReplyUsers []string
	NewReplies int
}

type Reaction struct {
	Name  string // the emoji's slack name
	Users []string
}

// Equal reports whether two messages render the same.
func (m Message) Equal(o Message) bool {
	return m.TS == o.TS && m.System == o.System && m.Pending == o.Pending && m.User == o.User && m.Username == o.Username && m.Text == o.Text &&
		m.ThreadTS == o.ThreadTS && m.Broadcast == o.Broadcast && m.Root == o.Root && m.Replies == o.Replies && m.NewReplies == o.NewReplies && slices.Equal(m.Files, o.Files) && slices.Equal(m.Attachments, o.Attachments) &&
		slices.EqualFunc(m.Reactions, o.Reactions, func(a, b Reaction) bool {
			return a.Name == b.Name && slices.Equal(a.Users, b.Users)
		})
}

// Attachment is an app's card under a message, or a link preview.
type Attachment struct {
	Color   string // "#rrggbb", or "" for the default
	Pretext string // Slack mrkdwn, as are Title and Text
	Title   string
	Text    string
}

type File struct {
	ID     string
	Name   string
	Mime   string
	Size   int
	URL    string
	Thumb  string
	ThumbW int
	ThumbH int
	Anim   string
	Err    string // why the preview couldn't be fetched
}

func (f File) Animated() bool { return f.Mime == "image/gif" }

func (f File) IsImage() bool { return strings.HasPrefix(f.Mime, "image/") && f.Thumb != "" }

func (m Message) IsReply() bool { return m.ThreadTS != "" && m.ThreadTS != m.TS }

type Hit struct {
	Channel string
	Msg     Message
}

type Snapshot struct {
	Team              string
	TeamID            string
	Self              string
	Users             []User
	Channels          []Channel
	Away              bool // set by the user, not automatic
	Groups            []UserGroup
	GroupsUnavailable bool
}

type UserGroup struct {
	ID      string
	Handle  string
	Name    string
	Members []string
}

type Event interface{ event() }

type Posted struct {
	Channel string
	Msg     Message
}

type Edited struct {
	Channel string
	Msg     Message
}

type Deleted struct {
	Channel string
	TS      TS
}

type Reacted struct {
	Channel string
	TS      TS
	Name    string
	User    string
	Removed bool
}

type ChannelChanged struct {
	Channel Channel
}

type UserChanged struct {
	User User
}

type PresenceChanged struct {
	User string
	Away bool
}

type LinkState int

const (
	Connecting LinkState = iota
	Connected
	Offline
)

type Link struct {
	State LinkState
	Err   error
}

func (Posted) event()          {}
func (Edited) event()          {}
func (Deleted) event()         {}
func (ChannelChanged) event()  {}
func (Reacted) event()         {}
func (Link) event()            {}
func (UserChanged) event()     {}
func (PresenceChanged) event() {}

type Backend interface {
	Bootstrap(ctx context.Context) (Snapshot, error)
	// History returns the most recent messages, oldest first.
	History(ctx context.Context, channel string) ([]Message, error)
	// Replies returns the parent, then the replies oldest first.
	Replies(ctx context.Context, channel string, thread TS) ([]Message, error)
	Send(ctx context.Context, channel string, thread TS, text string) (TS, error)
	Edit(ctx context.Context, channel string, ts TS, text string) error
	React(ctx context.Context, channel string, ts TS, name string) error
	Members(ctx context.Context, channel string) ([]string, error)
	// Search returns the best matches first.
	Search(ctx context.Context, query string) ([]Hit, error)
	SetTopic(ctx context.Context, channel, topic string) error
	// SetAway(false) returns the user to automatic presence.
	SetAway(ctx context.Context, away bool) error
	SetStatus(ctx context.Context, text string) error
	Presence(ctx context.Context, user string) (away bool, err error)
	Upload(ctx context.Context, channel string, thread TS, name string, data []byte) error
	// ReadCursor returns "" when slack doesn't say.
	ReadCursor(ctx context.Context, channel string) (TS, error)
	Info(ctx context.Context, channel string) (Channel, error)
	Join(ctx context.Context, name string) (Channel, error)
	OpenDM(ctx context.Context, user string) (Channel, error)
	Download(ctx context.Context, url string, w io.Writer) error
	Mark(ctx context.Context, channel string, ts TS) error
	MarkThread(ctx context.Context, channel string, thread, ts TS) error
	// Listen blocks until ctx ends.
	Listen(ctx context.Context, emit func(Event)) error
}
