package main

import (
	_ "embed"
	"sync"

	"github.com/nullentropy/clack/chat"
)

//go:embed icon.png
var icon []byte

//go:embed icon-alert.png
var alertIcon []byte

type dock struct {
	mu    sync.Mutex
	alert bool
}

func (d *dock) update(store *chat.Store) {
	alert := store.Pings() > 0
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.alert == alert {
		return
	}
	d.alert = alert
	if alert {
		setDockIcon(alertIcon)
	} else {
		setDockIcon(icon)
	}
}
