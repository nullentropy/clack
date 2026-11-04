package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nullentropy/caution"
	"github.com/nullentropy/clack/chat"
	"github.com/nullentropy/clack/slackapi"
)

// configDir is where clack keeps its settings and open lists
// $XDG_CONFIG_HOME/clack, or ~/.config/clack.
func configDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "clack")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "clack")
}

type settings struct {
	UserToken   string `json:"user_token"`
	AppToken    string `json:"app_token"`
	AutoAway    string `json:"auto_away,omitempty"` // idle time before going away, such as "10m"; empty is never
	AutoBack    bool   `json:"auto_back,omitempty"` // typing while away comes back
	Quiet       bool   `json:"quiet,omitempty"`     // no notifications
	ToastCorner string `json:"toast_corner,omitempty"`
	Theme       string `json:"theme,omitempty"`      // one of themes; empty is the default
	Effect      string `json:"effect,omitempty"`     // one of effects; empty is none
	FXPing      bool   `json:"fx_ping,omitempty"`    // glitch the window when a DM or mention arrives
	FXShimmer   bool   `json:"fx_shimmer,omitempty"` // color sweeps through messages that mention the user
	FXSidebar   bool   `json:"fx_sidebar,omitempty"` // animated sidebar background
}

func loadSettings(path string) (settings, error) {
	var s settings
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(data, &s)
}

func saveSettings(path string, s settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func tilde(path string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

type app struct {
	dir   string
	quit  func()
	debug bool // log Slack's live events

	dock dock
	gifs gifCache

	mu         sync.Mutex
	store      *chat.Store
	stop       context.CancelFunc // ends store's live events
	connecting bool
	err        string // why the latest attempt failed
	gen        int    // connections made so far
	subs       map[int]func()
	nextSub    int
	focused    map[*screen]bool // sessions whose window has focus
	onChat     map[*screen]bool
	screens    map[*screen]bool
}

func (a *app) setFocused(sc *screen, focused bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.focused == nil {
		a.focused = map[*screen]bool{}
	}
	if focused {
		a.focused[sc] = true
	} else {
		delete(a.focused, sc)
	}
}

func (a *app) setOnChat(sc *screen, on bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.onChat == nil {
		a.onChat = map[*screen]bool{}
	}
	if on {
		a.onChat[sc] = true
	} else {
		delete(a.onChat, sc)
	}
}

func (a *app) watching() []*screen {
	a.mu.Lock()
	defer a.mu.Unlock()
	var scs []*screen
	for sc := range a.focused {
		if a.onChat[sc] {
			scs = append(scs, sc)
		}
	}
	return scs
}

// conn is a snapshot of the app's connection state
type conn struct {
	store      *chat.Store
	connecting bool
	err        string
	gen        int
}

func (a *app) conn() conn {
	a.mu.Lock()
	defer a.mu.Unlock()
	return conn{a.store, a.connecting, a.err, a.gen}
}

func (a *app) current() *chat.Store { return a.conn().store }

func (a *app) settingsPath() string { return filepath.Join(a.dir, "settings.json") }

func (a *app) updateSettings(fn func(*settings)) error {
	s, err := loadSettings(a.settingsPath())
	if err != nil {
		return err
	}
	fn(&s)
	return saveSettings(a.settingsPath(), s)
}

func (a *app) setPresence(autoAway time.Duration, autoBack bool) error {
	err := a.updateSettings(func(s *settings) {
		s.AutoAway, s.AutoBack = "", autoBack
		if autoAway > 0 {
			s.AutoAway = shortDuration(autoAway)
		}
	})
	if err != nil {
		return err
	}
	if store := a.current(); store != nil {
		store.SetAutoAway(autoAway)
		store.SetAutoBack(autoBack)
	}
	return nil
}

func (a *app) applyPresence(store *chat.Store) {
	s, err := loadSettings(a.settingsPath())
	if err != nil {
		log.Printf("settings: %v", err)
		return
	}
	d, err := parseAutoAway(s.AutoAway)
	if err != nil {
		log.Printf("settings: auto_away %q: %v", s.AutoAway, err)
	}
	store.SetAutoAway(d)
	store.SetAutoBack(s.AutoBack)
}

func (a *app) tokens() settings {
	s, err := loadSettings(a.settingsPath())
	if err != nil {
		log.Printf("settings: %v", err)
	}
	if v := os.Getenv("SLACK_USER_TOKEN"); v != "" {
		s.UserToken = v
	}
	if v := os.Getenv("SLACK_APP_TOKEN"); v != "" {
		s.AppToken = v
	}
	return s
}

func (a *app) subscribe(fn func()) (cancel func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.subs == nil {
		a.subs = map[int]func(){}
	}
	id := a.nextSub
	a.nextSub++
	a.subs[id] = fn
	return func() {
		a.mu.Lock()
		delete(a.subs, id)
		a.mu.Unlock()
	}
}

func (a *app) notify() {
	a.mu.Lock()
	subs := make([]func(), 0, len(a.subs))
	for _, fn := range a.subs {
		subs = append(subs, fn)
	}
	a.mu.Unlock()
	for _, fn := range subs {
		fn()
	}
}

func (a *app) use(store *chat.Store, stop context.CancelFunc) {
	a.applyPresence(store)
	store.OnAlert(func(al chat.Alert) { a.alert(store, al) })
	store.Subscribe(func(chat.Change) { a.dock.update(store) })
	a.dock.update(store)
	a.mu.Lock()
	if a.stop != nil {
		a.stop()
	}
	a.store, a.stop, a.err = store, stop, ""
	a.gen++
	a.mu.Unlock()
	a.notify()
}

func (a *app) connect(t settings, save bool) {
	a.mu.Lock()
	a.connecting, a.err = true, ""
	a.mu.Unlock()
	a.notify()
	go func() {
		store, stop, err := a.open(t)
		a.mu.Lock()
		a.connecting = false
		if err != nil {
			log.Printf("slack: %v", err)
			a.err = err.Error()
		}
		a.mu.Unlock()
		if err != nil {
			a.notify()
			return
		}
		if save {
			err := a.updateSettings(func(s *settings) { s.UserToken, s.AppToken = t.UserToken, t.AppToken })
			if err != nil {
				log.Printf("save settings: %v", err)
			}
		}
		a.use(store, stop)
	}()
}

func (a *app) open(t settings) (*chat.Store, context.CancelFunc, error) {
	if !strings.HasPrefix(t.UserToken, "xoxp-") {
		return nil, nil, errors.New("the User OAuth Token starts with xoxp-")
	}
	if !strings.HasPrefix(t.AppToken, "xapp-") {
		return nil, nil, errors.New("the app-level token starts with xapp-")
	}
	backend := slackapi.New(t.UserToken, t.AppToken)
	backend.Debug = a.debug
	store := chat.NewStore(backend, a.dir)
	ctx, stop := context.WithCancel(context.Background())
	started := make(chan error, 1)
	go func() { started <- store.Start(ctx) }()
	select {
	case err := <-started:
		if err != nil {
			stop()
			return nil, nil, err
		}
		return store, stop, nil
	case <-time.After(connectTimeout):
		stop()
		return nil, nil, fmt.Errorf("Slack didn't answer within %v", connectTimeout)
	}
}

const connectTimeout = 30 * time.Second

func (a *app) mount(s *caution.Session) *caution.Node {
	useFonts(s)
	s.SetTheme(a.theme())
	s.SetTitle("clack")
	sc := &screen{a: a, s: s, focused: true} // a window opens in front
	sc.bindKeys()
	sc.bindDrops()
	a.setFocused(sc, true)
	s.OnFocus(func(focused bool) {
		sc.focused = focused
		a.setFocused(sc, focused)
		if focused && sc.win != nil {
			sc.win.markRead()
		}
	})
	cancel := a.subscribe(func() { s.Update(sc.refresh) })
	a.addScreen(sc, true)
	go func() { <-s.Done(); cancel(); a.setFocused(sc, false); a.setOnChat(sc, false); a.addScreen(sc, false) }()
	sc.root = sc.view()
	sc.applyEffect()
	return sc.root
}

func (a *app) addScreen(sc *screen, open bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.screens == nil {
		a.screens = map[*screen]bool{}
	}
	if open {
		a.screens[sc] = true
	} else {
		delete(a.screens, sc)
	}
}

type screen struct {
	a     *app
	s     *caution.Session
	root  *caution.Node
	win   *window
	setup *setupView

	focused  bool
	asked    bool
	askedGen int
	fx       *caution.Node // the node carrying the effect
	fxFrag   string
	effect   string    // the window effect's name
	glitchAt time.Time // when the latest ping glitch started, zero when none is running
}

func (sc *screen) isFocused() bool { return sc.focused }

func (sc *screen) refresh() {
	sc.s.SetTheme(sc.a.theme())
	if n := sc.view(); n != sc.root {
		sc.root = n
		sc.s.SetRoot(n)
	}
	sc.applyEffect()
}

// applyEffect puts the chosen effects on the root and the window
func (sc *screen) applyEffect() {
	cfg := sc.a.fx()
	if sc.win != nil {
		sc.win.setFX(cfg, sc.a.theme())
	}
	sc.effect = cfg.effect
	fx := rootFX(sc.effect, sc.glitchLeft())
	if sc.fx == sc.root && sc.fxFrag == fx.Frag {
		return
	}
	if sc.fx != nil && sc.fx != sc.root {
		sc.fx.Effect(caution.FX{}) // the setup view and window swap roots
	}
	sc.fx, sc.fxFrag = sc.root, fx.Frag
	sc.root.Effect(fx)
}

// glitchLeft is the ping glitch's strength, 1 as it starts down to 0
func (sc *screen) glitchLeft() float64 {
	if sc.glitchAt.IsZero() {
		return 0
	}
	return max(0, 1-float64(time.Since(sc.glitchAt))/float64(glitchFor))
}

// glitch runs the ping glitch, restarting it when one is already running
func (sc *screen) glitch() {
	running := !sc.glitchAt.IsZero()
	sc.glitchAt = time.Now()
	sc.applyEffect()
	if running {
		return
	}
	go func() {
		tick := time.NewTicker(40 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-sc.s.Done():
				return
			case <-tick.C:
			}
			ended := make(chan bool, 1)
			sc.s.Update(func() {
				g := sc.glitchLeft()
				if g == 0 {
					sc.glitchAt = time.Time{}
					sc.applyEffect()
				} else {
					sc.root.Effect(rootFX(sc.effect, g)) // effects take no uniform patches, so the whole effect goes again
				}
				ended <- g == 0
			})
			select {
			case <-sc.s.Done():
				return
			case end := <-ended:
				if end {
					return
				}
			}
		}
	}()
}

func (sc *screen) view() *caution.Node {
	c := sc.a.conn()
	if sc.asked && c.gen > sc.askedGen {
		sc.asked = false // a new connection answers the request
	}
	if c.store != nil && !sc.asked {
		sc.setup = nil
		if sc.win == nil || sc.win.store != c.store {
			if sc.win != nil {
				sc.win.close()
			}
			sc.win = newWindow(sc, c.store)
		}
		sc.a.setOnChat(sc, true)
		return sc.win.root
	}
	sc.a.setOnChat(sc, false)
	if sc.setup == nil {
		sc.setup = newSetupView(sc.a, sc.cancelTokens)
	}
	sc.setup.show(c, c.store != nil)
	return sc.setup.root
}

func (sc *screen) askTokens() {
	sc.asked, sc.askedGen = true, sc.a.conn().gen
	sc.refresh()
}

func (sc *screen) cancelTokens() {
	sc.asked = false
	sc.refresh()
	if sc.win != nil {
		sc.win.composer.Focus()
	}
}

type setupView struct {
	root, card, title, status *caution.Node
	form, userField           *caution.Node
	cancelSlot, cancel        *caution.Node
}

//go:embed slack-manifest.json
var slackManifest []byte

// newAppURL opens slack's create app page with the manifest filled in
func newAppURL() string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, slackManifest); err != nil {
		panic("slack-manifest.json: " + err.Error()) // caught by the tests
	}
	return "https://api.slack.com/apps?new_app=1&manifest_json=" + url.QueryEscape(compact.String())
}

func newSetupView(a *app, cancel func()) *setupView {
	v := &setupView{}
	t := a.tokens()
	v.title = caution.Label("").FontSize(20).Weight(700)
	v.status = caution.Label("").FontSize(13).Color("$danger").Wrap()

	field := func(value, placeholder string, set func(string)) *caution.Node {
		return caution.TextField(value).Placeholder(placeholder).Mono().Sensitive().OnInput(set)
	}
	submit := func() {
		a.connect(settings{UserToken: strings.TrimSpace(t.UserToken), AppToken: strings.TrimSpace(t.AppToken)}, true)
	}
	var appField *caution.Node
	v.userField = field(t.UserToken, "xoxp-…", func(s string) { t.UserToken = s }).OnSubmit(func(string) { appField.Focus() })
	appField = field(t.AppToken, "xapp-…", func(s string) { t.AppToken = s }).OnSubmit(func(string) { submit() })
	caption := func(text string) *caution.Node {
		return caution.Label(text).FontSize(12).Weight(600).Color("$inkFaint")
	}
	heading := func(text string) *caution.Node { return caution.Label(text).FontSize(16).Weight(700) }
	v.cancel = caution.Button("Close").OnClick(cancel)
	// Close leaves the whole screen, so it ends the card. presence saves as it changes
	v.cancelSlot = caution.HStack().Kids(caution.Panel().Fill(1))
	v.form = caution.VStack().Gap(10).Align("stretch").Kids(
		heading("Slack"),
		caution.Label("clack talks to Slack through an app you make in your workspace. "+
			"Create it, install it, then make an app-level token with the connections:write scope "+
			"under Basic Information.").
			FontSize(13).Color("$inkDim").Wrap(),
		caution.HStack().Kids(caution.Button("Create the Slack app").OnClick(func() {
			if err := openURL(newAppURL()); err != nil {
				v.status.SetText("couldn't open the browser: " + err.Error())
			}
		})),
		caption("User OAuth Token"),
		v.userField,
		caption("App-level token"),
		appField,
		caution.HStack().Kids(caution.Button("Connect").Primary().OnClick(submit)),
		caution.Panel().H(12),
		heading("Presence and notifications"),
	)
	v.form.Kids(presenceFields(a, t, caption)...)
	v.form.Kids(v.cancelSlot)
	v.card = caution.VStack().Gap(10).Align("stretch").W(520).
		Anchor(caution.A{CenterX: caution.Px(0), CenterY: caution.Px(0)}).
		Kids(v.title, v.status)
	v.root = caution.Panel().Kids(v.card)
	return v
}

func presenceFields(a *app, saved settings, caption func(string) *caution.Node) []*caution.Node {
	note := caution.Label("").FontSize(12).Color("$inkFaint")
	autoAway, _ := parseAutoAway(saved.AutoAway)
	autoBack := saved.AutoBack
	save := func() {
		if err := a.setPresence(autoAway, autoBack); err != nil {
			note.SetText("couldn't save: " + err.Error())
			return
		}
		note.SetText("saved")
	}
	away := caution.TextField(saved.AutoAway).Placeholder("off").Mono().OnCommit(func(s string) {
		d, err := parseAutoAway(s)
		if err != nil {
			note.SetText("auto-away wants a time like 10m or 90s, or nothing for off")
			return
		}
		autoAway = d
		save()
	})
	back := caution.Checkbox("Typing while away brings me back", autoBack).OnToggle(func(on bool) {
		autoBack = on
		save()
	})
	notifyBox := caution.Checkbox("Notify me of DMs, mentions and replies in my threads", !saved.Quiet).OnToggle(func(on bool) {
		if err := a.updateSettings(func(s *settings) { s.Quiet = !on }); err != nil {
			note.SetText("couldn't save: " + err.Error())
			return
		}
		note.SetText("saved")
	})
	corner := caution.Select(toastCorners, max(0, slices.Index(toastCorners, a.toastCorner()))).OnSelect(func(i int) {
		if err := a.updateSettings(func(s *settings) { s.ToastCorner = toastCorners[i] }); err != nil {
			note.SetText("couldn't save: " + err.Error())
			return
		}
		note.SetText("saved")
	})
	return []*caution.Node{caption("Go away after this long idle in clack"), away, back, notifyBox,
		caption("Corner for notifications while clack has focus"), caution.HStack().Kids(corner), note}
}

func (v *setupView) show(c conn, canCancel bool) {
	if c.connecting {
		v.title.SetText("Connecting to Slack…")
		v.status.SetText("")
		if v.form.Parent() != nil {
			v.form.Remove()
		}
		return
	}
	v.title.SetText("Connect clack to Slack")
	if canCancel {
		v.title.SetText("Settings")
	}
	v.status.SetText(c.err)
	if v.form.Parent() == nil {
		v.card.Add(v.form)
		v.userField.Focus()
	}
	if hasCancel := v.cancel.Parent() != nil; canCancel != hasCancel {
		if canCancel {
			v.cancelSlot.Add(v.cancel)
		} else {
			v.cancel.Remove()
		}
	}
}
