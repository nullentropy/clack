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
	Group  // multi-person direct message
	Direct // one-to-one direct message
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
	ID       string
	Name     string
	RealName string
	Avatar   string
}

// Display is the name shown on messages
func (u User) Display() string {
	if u.RealName != "" {
		return u.RealName
	}
	return u.Name
}

// TS is a Slack message timestamp ("1700000000.000100")
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
	TS        TS
	User      string // user ID (empty for bots, they have Username instead)
	Username  string
	Text      string // Slack mrkdwn
	ThreadTS  TS
	Replies   int
	Files     []File
	Reactions []Reaction

	// not a real message (joins, parts, etc)
	System bool

	ReplyUsers []string
	NewReplies int
}

// Reaction is one emoji on a message by the slack name
type Reaction struct {
	Name  string
	Users []string
}

// Equal reports if two messages would render the same
func (m Message) Equal(o Message) bool {
	return m.TS == o.TS && m.System == o.System && m.User == o.User && m.Username == o.Username && m.Text == o.Text &&
		m.ThreadTS == o.ThreadTS && m.Replies == o.Replies && m.NewReplies == o.NewReplies && slices.Equal(m.Files, o.Files) &&
		slices.EqualFunc(m.Reactions, o.Reactions, func(a, b Reaction) bool {
			return a.Name == b.Name && slices.Equal(a.Users, b.Users)
		})
}

// File is an attachment
type File struct {
	ID     string
	Name   string
	Mime   string
	Size   int
	URL    string
	Thumb  string
	ThumbW int
	ThumbH int
	Err    string // why the preview couldn't be fetched
}

// IsImage reports if the file has a preview to show inline
func (f File) IsImage() bool { return strings.HasPrefix(f.Mime, "image/") && f.Thumb != "" }

// IsReply reports if the message is inside a thread
func (m Message) IsReply() bool { return m.ThreadTS != "" && m.ThreadTS != m.TS }

// Hit is a message a search found
type Hit struct {
	Channel string
	Msg     Message
}

// Snapshot is what a backend knows at startup
type Snapshot struct {
	Team     string
	TeamID   string
	Self     string
	Users    []User
	Channels []Channel
	Away     bool // the user set themselves away
}

// Event is a change a backend observed after startup
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

func (Posted) event()         {}
func (Edited) event()         {}
func (Deleted) event()        {}
func (ChannelChanged) event() {}
func (Reacted) event()        {}
func (Link) event()           {}

type Backend interface {
	Bootstrap(ctx context.Context) (Snapshot, error)
	// History returns the channel's most recent messages, oldest first
	History(ctx context.Context, channel string) ([]Message, error)
	// Replies returns a thread: its parent, then the replies oldest first
	Replies(ctx context.Context, channel string, thread TS) ([]Message, error)
	// Send posts to the channel, or into a thread when thread is set
	Send(ctx context.Context, channel string, thread TS, text string) error
	// Edit replaces the text of one of the user's own messages
	Edit(ctx context.Context, channel string, ts TS, text string) error
	// React adds the user's reaction, by emoji name, to a message
	React(ctx context.Context, channel string, ts TS, name string) error
	// Members lists the users in a conversation
	Members(ctx context.Context, channel string) ([]string, error)
	// Search finds messages matching a slack search query, best first
	Search(ctx context.Context, query string) ([]Hit, error)
	// SetTopic changes a channel's topic
	SetTopic(ctx context.Context, channel, topic string) error
	// SetAway sets the user away, or back to automatic presence
	SetAway(ctx context.Context, away bool) error
	// Upload posts a file to the channel, or into a thread when thread is set
	Upload(ctx context.Context, channel string, thread TS, name string, data []byte) error
	// ReadCursor is where the user has read up to in the conversation in
	// slack, or "" when it doesn't say
	ReadCursor(ctx context.Context, channel string) (TS, error)
	// Info describes a channel the user is in
	Info(ctx context.Context, channel string) (Channel, error)
	// Join joins the public channel called name
	Join(ctx context.Context, name string) (Channel, error)
	// OpenDM starts or finds the direct message with a user
	OpenDM(ctx context.Context, user string) (Channel, error)
	// Download writes a file's content, from one of its URLs, to w
	Download(ctx context.Context, url string, w io.Writer) error
	// Mark moves the user's read cursor in the channel to ts
	Mark(ctx context.Context, channel string, ts TS) error
	// Listen blocks, reporting live events until ctx ends
	Listen(ctx context.Context, emit func(Event)) error
}
