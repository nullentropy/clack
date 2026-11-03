package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/png"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/nullentropy/caution"
	"github.com/nullentropy/clack/chat"
)

// caution decodes a GIF to its first frame, so clack decodes the frames
// itself, serves each as a PNG at /files/<id>?frame=N, and steps the image
// through them

// gifPixels bounds the pixels across one GIF's frames, which the client
// keeps as textures: 16M is 64MB of RGBA. Longer GIFs drop frames evenly
const gifPixels = 16 << 20

type animation struct {
	frames [][]byte // PNGs
	delays []time.Duration
}

// decodeGIF composites each frame over the ones before it, as the GIF's
// disposal methods say, since later frames are often only the part that changed
func decodeGIF(data []byte) (*animation, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := image.Rect(0, 0, g.Config.Width, g.Config.Height)
	if bounds.Empty() && len(g.Image) > 0 {
		bounds = g.Image[0].Bounds()
	}
	step := 1
	if px := bounds.Dx() * bounds.Dy(); px > 0 {
		step = max(1, (len(g.Image)*px+gifPixels-1)/gifPixels)
	}
	canvas := image.NewRGBA(bounds)
	a := &animation{}
	for i, frame := range g.Image {
		disposal := byte(gif.DisposalNone)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		var before *image.RGBA
		if disposal == gif.DisposalPrevious {
			before = image.NewRGBA(bounds)
			copy(before.Pix, canvas.Pix)
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		delay := 100 * time.Millisecond // what browsers play for 0 or 1
		if i < len(g.Delay) && g.Delay[i] > 1 {
			delay = time.Duration(g.Delay[i]) * 10 * time.Millisecond
		}
		if i%step == 0 {
			var out bytes.Buffer
			if err := png.Encode(&out, canvas); err != nil {
				return nil, err
			}
			a.frames = append(a.frames, out.Bytes())
			a.delays = append(a.delays, delay)
		} else {
			a.delays[len(a.delays)-1] += delay // a dropped frame's time goes to the one shown
		}
		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, frame.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			canvas = before
		}
	}
	return a, nil
}

// gifCache keeps the last few decoded GIFs, so every window and every frame
// request shares one download and decode
type gifCache struct {
	mu    sync.Mutex
	byKey map[string]*gifEntry
	order []string // oldest first
}

type gifEntry struct {
	done chan struct{}
	anim *animation
	err  error
}

const gifsKept = 12

func (c *gifCache) get(ctx context.Context, store *chat.Store, id string, full bool) (*animation, error) {
	key := fmt.Sprintf("%p/%s/%v", store, id, full)
	c.mu.Lock()
	if c.byKey == nil {
		c.byKey = map[string]*gifEntry{}
	}
	e, ok := c.byKey[key]
	if !ok {
		e = &gifEntry{done: make(chan struct{})}
		c.byKey[key] = e
		c.order = append(c.order, key)
		if len(c.order) > gifsKept {
			delete(c.byKey, c.order[0])
			c.order = c.order[1:]
		}
	}
	c.mu.Unlock()
	if !ok {
		var buf bytes.Buffer
		e.err = store.DownloadAnim(context.WithoutCancel(ctx), id, full, &buf) // the first asker may leave, others still wait
		if e.err == nil {
			e.anim, e.err = decodeGIF(buf.Bytes())
		}
		if e.err != nil {
			c.mu.Lock()
			if c.byKey[key] == e {
				delete(c.byKey, key) // a later look tries again
			}
			c.mu.Unlock()
		}
		close(e.done)
	}
	select {
	case <-e.done:
		return e.anim, e.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (a *app) serveFrame(ctx context.Context, store *chat.Store, id string, full bool, frame string) ([]byte, error) {
	n, err := strconv.Atoi(frame)
	if err != nil {
		return nil, fmt.Errorf("frame %q", frame)
	}
	anim, err := a.gifs.get(ctx, store, id, full)
	if err != nil {
		return nil, err
	}
	if n < 0 || n >= len(anim.frames) {
		return nil, fmt.Errorf("frame %d of %d", n, len(anim.frames))
	}
	return anim.frames[n], nil
}

// animate steps img through a GIF's frames until img leaves the window
func (w *window) animate(img *caution.Node, f chat.File, full bool) {
	if w.s == nil || w.app == nil || !f.Animated() {
		return
	}
	store := w.store
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { <-w.s.Done(); cancel() }()
		anim, err := w.app.gifs.get(ctx, store, f.ID, full)
		if err != nil || len(anim.frames) < 2 {
			return // the still preview stays
		}
		base := "/files/" + url.PathEscape(f.ID) + "?"
		if full {
			base += "full&"
		}
		srcs := make([]string, len(anim.frames))
		for i := range srcs {
			srcs[i] = base + "frame=" + strconv.Itoa(i)
		}
		w.s.Update(func() { w.s.Preload(srcs...) })
		for i := 0; ; i = (i + 1) % len(srcs) {
			shown := make(chan bool, 1)
			w.s.Update(func() {
				on := w.shows(img)
				if on {
					img.SetSrc(srcs[i])
				}
				shown <- on
			})
			select {
			case on := <-shown:
				if !on {
					return
				}
			case <-ctx.Done():
				return
			}
			select {
			case <-time.After(anim.delays[i]):
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (w *window) shows(n *caution.Node) bool {
	for ; n != nil; n = n.Parent() {
		if n == w.root {
			return true
		}
	}
	return false
}
