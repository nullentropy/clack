package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nullentropy/clack/chat"
)

func TestSettingsRoundTripPrivately(t *testing.T) {
	a := &app{dir: t.TempDir()}
	want := settings{UserToken: "xoxp-1", AppToken: "xapp-1"}
	if err := saveSettings(a.settingsPath(), want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(a.settingsPath())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("settings.json mode = %o, want 600", perm)
	}
	t.Setenv("SLACK_USER_TOKEN", "")
	t.Setenv("SLACK_APP_TOKEN", "")
	if got := a.tokens(); got != want {
		t.Fatalf("tokens = %+v, want %+v", got, want)
	}
}

func TestEnvironmentOverridesSavedTokens(t *testing.T) {
	a := &app{dir: t.TempDir()}
	if err := saveSettings(a.settingsPath(), settings{UserToken: "xoxp-saved", AppToken: "xapp-saved"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLACK_USER_TOKEN", "xoxp-env")
	t.Setenv("SLACK_APP_TOKEN", "")
	if got := a.tokens(); got.UserToken != "xoxp-env" || got.AppToken != "xapp-saved" {
		t.Fatalf("tokens = %+v, want the env user token and the saved app token", got)
	}
}

func TestFailedConnectSaysWhyAndSavesNothing(t *testing.T) {
	a := &app{dir: t.TempDir()}
	done := make(chan struct{}, 4)
	a.subscribe(func() { done <- struct{}{} })
	a.connect(settings{UserToken: "xoxb-bot", AppToken: "xapp-1"}, true)
	<-done // connecting
	<-done // failed
	c := a.conn()
	if c.connecting || c.store != nil || !strings.Contains(c.err, "xoxp-") {
		t.Fatalf("after a bot token: %+v; want not connecting, no store, and why", c)
	}
	if _, err := os.Stat(a.settingsPath()); !os.IsNotExist(err) {
		t.Fatal("rejected tokens were saved")
	}
}

func TestServeFile(t *testing.T) {
	a := &app{dir: t.TempDir()}
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		a.serveFile(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	if code := get("/files/F1").Code; code != http.StatusServiceUnavailable {
		t.Fatalf("before connecting: %d, want 503", code)
	}

	a.store = chat.NewStore(&chat.Demo{}, a.dir)
	if err := a.store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := get("/files/F1").Code; code != http.StatusNotFound {
		t.Fatalf("a file no message has shown: %d, want 404", code)
	}
	a.store.Load(context.Background(), "C2")
	for range 100 {
		if _, ok := a.store.File("F1"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec := get("/files/F1")
	if rec.Code != http.StatusOK || !bytes.HasPrefix(rec.Body.Bytes(), []byte("\x89PNG")) {
		t.Fatalf("F1: %d, %d bytes; want a PNG", rec.Code, rec.Body.Len())
	}
	if rec := get("/files/F1?full"); rec.Code != http.StatusOK || !bytes.HasPrefix(rec.Body.Bytes(), []byte("\x89PNG")) {
		t.Fatalf("F1 full size: %d; want a PNG", rec.Code)
	}
}

func TestFailedPreviewShowsWhy(t *testing.T) {
	a := &app{dir: t.TempDir()}
	a.store = chat.NewStore(&chat.Demo{}, a.dir)
	if err := a.store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.store.Load(context.Background(), "C2")
	for range 100 {
		if _, ok := a.store.File("F2"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec := httptest.NewRecorder()
	a.serveFile(rec, httptest.NewRequest("GET", "/files/F2", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("F2 preview: %d, want 502", rec.Code)
	}
	f, _ := a.store.File("F2")
	msgs, _ := a.store.Timeline("C2")
	var onMessage string
	for _, m := range msgs {
		for _, mf := range m.Files {
			if mf.ID == "F2" {
				onMessage = mf.Err
			}
		}
	}
	if f.Err == "" || onMessage != f.Err {
		t.Fatalf("file error %q, on its message %q; want the same reason on both", f.Err, onMessage)
	}
}

func TestDisplayable(t *testing.T) {
	var png bytes.Buffer
	if err := chatPNG(&png); err != nil {
		t.Fatal(err)
	}
	if out, err := displayable(png.Bytes()); err != nil || !bytes.Equal(out, png.Bytes()) {
		t.Fatalf("PNG: %v; want it passed through", err)
	}
	if _, err := displayable([]byte("<!DOCTYPE html><html>sign in</html>")); err == nil || !strings.Contains(err.Error(), "text/html") {
		t.Fatalf("HTML page: %v; want an error naming text/html", err)
	}
}

func chatPNG(w *bytes.Buffer) error {
	return (&chat.Demo{}).Download(context.Background(), "demo:bench", w)
}

func TestPresenceAndTokensSaveWithoutClobberingEachOther(t *testing.T) {
	a := &app{dir: t.TempDir()}
	if err := saveSettings(a.settingsPath(), settings{UserToken: "xoxp-1", AppToken: "xapp-1"}); err != nil {
		t.Fatal(err)
	}
	if err := a.setPresence(10*time.Minute, true); err != nil {
		t.Fatal(err)
	}
	err := a.updateSettings(func(s *settings) { s.UserToken = "xoxp-2" })
	if err != nil {
		t.Fatal(err)
	}
	got, err := loadSettings(a.settingsPath())
	want := settings{UserToken: "xoxp-2", AppToken: "xapp-1", AutoAway: "10m", AutoBack: true}
	if err != nil || got != want {
		t.Fatalf("settings = %+v, %v; want %+v", got, err, want)
	}
}

func TestSavedPresenceAppliesToEachConnection(t *testing.T) {
	a := &app{dir: t.TempDir()}
	if err := a.setPresence(5*time.Minute, true); err != nil {
		t.Fatal(err)
	}
	store := chat.NewStore(&chat.Demo{}, a.dir)
	if err := store.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.use(store, func() {})
	if store.AutoAway() != 5*time.Minute || !store.AutoBack() {
		t.Fatalf("new store: auto-away %v, auto-back %v", store.AutoAway(), store.AutoBack())
	}
	if err := a.setPresence(0, false); err != nil {
		t.Fatal(err)
	}
	if store.AutoAway() != 0 || store.AutoBack() {
		t.Fatal("changing the settings didn't reach the connected store")
	}
}

func TestParseAutoAway(t *testing.T) {
	cases := map[string]time.Duration{"": 0, "off": 0, "10": 10 * time.Minute, "10m": 10 * time.Minute, "90s": 90 * time.Second}
	for in, want := range cases {
		if got, err := parseAutoAway(in); err != nil || got != want {
			t.Errorf("parseAutoAway(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"soon", "-5m"} {
		if _, err := parseAutoAway(bad); err == nil {
			t.Errorf("parseAutoAway(%q) accepted it", bad)
		}
	}
}

func TestNewAppLinkCarriesTheManifest(t *testing.T) {
	u, err := url.Parse(newAppURL())
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		OAuth struct {
			Scopes struct{ User []string } `json:"scopes"`
		} `json:"oauth_config"`
		Settings struct {
			Events struct {
				User []string `json:"user_events"`
			} `json:"event_subscriptions"`
			SocketMode bool `json:"socket_mode_enabled"`
		} `json:"settings"`
	}
	if err := json.Unmarshal([]byte(u.Query().Get("manifest_json")), &m); err != nil {
		t.Fatalf("manifest_json: %v", err)
	}
	// scopes clack's errors and commands name, so the app slack makes can do them
	for _, scope := range []string{"files:write", "files:read", "users:read", "users:write", "search:read", "reactions:write", "channels:write"} {
		if !slices.Contains(m.OAuth.Scopes.User, scope) {
			t.Errorf("manifest lacks the %s user scope", scope)
		}
	}
	if !slices.Contains(m.Settings.Events.User, "message.im") || !m.Settings.SocketMode {
		t.Errorf("manifest settings = %+v, want socket mode and message events", m.Settings)
	}
}
