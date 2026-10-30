package main

import (
	"log"

	"github.com/nullentropy/clack/chat"
)

func (a *app) alert(store *chat.Store, al chat.Alert) {
	if s, err := loadSettings(a.settingsPath()); err != nil || s.Quiet {
		return
	}
	if al.Open && a.anyFocused() {
		return // they're looking at it
	}
	title, body := alertText(store, al)
	if err := notify(title, body); err != nil {
		log.Printf("notify: %v", err)
	}
}

func alertText(store *chat.Store, al chat.Alert) (title, body string) {
	user := func(id string) string {
		if u, ok := store.User(id); ok {
			return u.Display()
		}
		return id
	}
	channel := func(id string) string {
		if c, ok := store.Channel(id); ok {
			return c.Name
		}
		return id
	}
	title = al.Msg.Username
	if al.Msg.User != "" {
		title = user(al.Msg.User)
	}
	if c, ok := store.Channel(al.Channel); !ok || !c.Kind.IsDM() {
		where := " in #" + channel(al.Channel)
		if al.Msg.IsReply() {
			where = " in a thread in #" + channel(al.Channel)
		}
		title += where
	}
	return title, chat.Plain(al.Msg.Text, user, channel)
}
