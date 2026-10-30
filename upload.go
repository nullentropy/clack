package main

import (
	"context"
	"fmt"

	"github.com/nullentropy/caution"
)

// bindDrops posts files dropped to the active conversation
func (sc *screen) bindDrops() {
	onWindow := func(fn func(w *window)) {
		if sc.win != nil && sc.root == sc.win.root {
			fn(sc.win)
		}
	}
	sc.s.OnFileDrag(func(over bool) {
		onWindow(func(w *window) { w.dragHint(over) })
	})
	sc.s.OnFileDrop(func(files []caution.File, x, _ float64) {
		onWindow(func(w *window) {
			w.dragHint(false)
			w.upload(w.dropTarget(x), files)
		})
	})
}

// dropTarget is the conversation under x
func (w *window) dropTarget(x float64) target {
	if w.thread.channel != "" && w.threadView.Parent() != nil {
		if width, _ := w.s.Viewport(); x >= width-w.threadW {
			return w.thread
		}
	}
	return w.composerTarget()
}

func (w *window) upload(to target, files []caution.File) {
	if to.channel == "" {
		w.notice("open a conversation to upload to it")
		return
	}
	for _, f := range files {
		w.notice(fmt.Sprintf("uploading %s (%s)…", f.Name, byteSize(len(f.Data))))
		go func() {
			err := w.store.Upload(context.Background(), to.channel, to.thread, f.Name, f.Data)
			w.s.Update(func() {
				if err != nil {
					w.notice(fmt.Sprintf("couldn't upload %s: %v", f.Name, err))
					return
				}
				w.notice("uploaded " + f.Name)
			})
		}()
	}
}

// dragHint dims the window while files are dragged over it
func (w *window) dragHint(over bool) {
	if w.dropHint != nil {
		w.dropHint.Remove()
		w.dropHint = nil
	}
	if !over {
		return
	}
	where := "the conversation under the pointer"
	if w.active == statusKey && w.thread.channel == "" {
		where = "a conversation · open one first"
	} else if w.thread.channel == "" {
		if c, ok := w.store.Channel(w.active); ok {
			where = w.channelLabel(c)
		}
	}
	w.dropHint = caution.Panel().Anchor(fill).Kids(
		caution.Panel().Anchor(fill).Bg("$scrim"),
		caution.Label("Drop to upload to "+where).FontSize(18).Weight(600).Color("$ink").
			Anchor(caution.A{CenterX: caution.Px(0), CenterY: caution.Px(0)}),
	)
	w.root.Add(w.dropHint)
}
