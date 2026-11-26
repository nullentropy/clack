package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var version = "dev"

const updateRepo = "nullentropy/clack"

type updater struct {
	version, goos, goarch string
	dir                   string
	api, dl               string
	client                *http.Client
}

func newUpdater(configDir string) updater {
	return updater{version: version, goos: runtime.GOOS, goarch: runtime.GOARCH,
		dir: filepath.Join(configDir, "update"),
		api: "https://api.github.com", dl: "https://github.com",
		client: &http.Client{Timeout: 10 * time.Minute}}
}

type staged struct {
	Tag     string `json:"tag"`
	Payload string `json:"payload"`
}

func assetFor(goos, goarch string) (string, error) {
	switch {
	case goos == "darwin":
		return "clack-macos-universal.zip", nil
	case goos == "linux" && (goarch == "amd64" || goarch == "arm64"):
		return "clack-linux-" + goarch + ".tar.gz", nil
	case goos == "windows" && goarch == "amd64":
		return "clack-windows-amd64.zip", nil
	}
	return "", fmt.Errorf("no release file for %s/%s", goos, goarch)
}

func parseTag(s string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func newerTag(cur, next string) bool {
	a, oka := parseTag(cur)
	b, okb := parseTag(next)
	if !oka || !okb {
		return false
	}
	for i := range 3 {
		if a[i] != b[i] {
			return b[i] > a[i]
		}
	}
	return false
}

func (u updater) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "clack-updater")
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

func (u updater) latestTag(ctx context.Context) (string, error) {
	resp, err := u.get(ctx, u.api+"/repos/"+updateRepo+"/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var r struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("read the latest release: %w", err)
	}
	return r.Tag, nil
}

func (u updater) download(ctx context.Context, tag, name string) error {
	resp, err := u.get(ctx, u.dl+"/"+updateRepo+"/releases/download/"+tag+"/"+name)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.Create(filepath.Join(u.dir, name))
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (u updater) checkAndStage(ctx context.Context) (string, error) {
	if _, ok := parseTag(u.version); !ok {
		return "", nil
	}
	asset, err := assetFor(u.goos, u.goarch)
	if err != nil {
		return "", err
	}
	tag, err := u.latestTag(ctx)
	if err != nil {
		return "", err
	}
	if !newerTag(u.version, tag) {
		return "", nil
	}
	if s, err := u.readStaged(); err == nil && s.Tag == tag {
		if _, err := os.Stat(s.Payload); err == nil {
			return tag, nil
		}
	}
	if err := os.RemoveAll(u.dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(u.dir, 0o700); err != nil {
		return "", err
	}
	for _, name := range []string{asset, "SHA256SUMS"} {
		if err := u.download(ctx, tag, name); err != nil {
			os.RemoveAll(u.dir)
			return "", err
		}
	}
	payload, err := u.unpack(ctx, asset)
	if err != nil {
		os.RemoveAll(u.dir)
		return "", err
	}
	return tag, u.writeStaged(staged{Tag: tag, Payload: payload})
}

func (u updater) unpack(ctx context.Context, asset string) (string, error) {
	if err := verifySum(u.dir, asset); err != nil {
		return "", err
	}
	src := filepath.Join(u.dir, asset)
	switch u.goos {
	case "darwin":
		if err := exec.CommandContext(ctx, "ditto", "-x", "-k", src, u.dir).Run(); err != nil {
			return "", fmt.Errorf("unpack %s: %w", asset, err)
		}
		return filepath.Join(u.dir, "clack.app"), nil
	case "windows":
		return filepath.Join(u.dir, "clack.exe"), unzipOne(src, "clack.exe", filepath.Join(u.dir, "clack.exe"))
	default:
		return filepath.Join(u.dir, "clack"), untarOne(src, "bin/clack", filepath.Join(u.dir, "clack"))
	}
}

func verifySum(dir, asset string) error {
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return err
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		if f := strings.Fields(line); len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			want = f[0]
		}
	}
	if want == "" {
		return fmt.Errorf("the release has no checksum for %s", asset)
	}
	f, err := os.Open(filepath.Join(dir, asset))
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != want {
		return fmt.Errorf("%s does not match its checksum", asset)
	}
	return nil
}

func writeExec(dst string, r io.Reader) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func untarOne(src, name, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s has no %s", filepath.Base(src), name)
		}
		if err != nil {
			return err
		}
		if strings.TrimPrefix(h.Name, "./") == name && h.Typeflag == tar.TypeReg {
			return writeExec(dst, tr)
		}
	}
}

func unzipOne(src, name, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == name {
			r, err := f.Open()
			if err != nil {
				return err
			}
			defer r.Close()
			return writeExec(dst, r)
		}
	}
	return fmt.Errorf("%s has no %s", filepath.Base(src), name)
}

func (u updater) stagedPath() string { return filepath.Join(u.dir, "staged.json") }

func (u updater) readStaged() (staged, error) {
	var s staged
	b, err := os.ReadFile(u.stagedPath())
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

func (u updater) writeStaged(s staged) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(u.stagedPath(), b, 0o600)
}

func (u updater) readyTag() string {
	s, err := u.readStaged()
	if err != nil || !newerTag(u.version, s.Tag) {
		return ""
	}
	return s.Tag
}

func installRoot() (root, exe string, err error) {
	if exe, err = os.Executable(); err != nil {
		return "", "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", "", err
	}
	if runtime.GOOS == "darwin" {
		if root := filepath.Dir(filepath.Dir(filepath.Dir(exe))); strings.HasSuffix(root, ".app") {
			return root, exe, nil
		}
	}
	return exe, exe, nil
}

func (u updater) installStaged(root string) (bool, error) {
	s, err := u.readStaged()
	if err != nil {
		return false, nil
	}
	if !newerTag(u.version, s.Tag) {
		return false, os.RemoveAll(u.dir)
	}
	if strings.HasSuffix(s.Payload, ".app") != strings.HasSuffix(root, ".app") {
		return false, fmt.Errorf("%s is not an installed release; reinstall with install.sh", root)
	}
	if err := swapInstall(root, s.Payload); err != nil {
		return false, err
	}
	os.RemoveAll(u.dir)
	return true, nil
}

func (u updater) atLaunch(set settings, root string) (bool, error) {
	os.RemoveAll(root + ".old")
	if set.UpdatesOff {
		return false, nil
	}
	return u.installStaged(root)
}

func swapInstall(root, src string) error {
	old := root + ".old"
	os.RemoveAll(old)
	if err := os.Rename(root, old); err != nil {
		return fmt.Errorf("cannot replace %s: %w", root, err)
	}
	if err := moveInto(src, root); err != nil {
		os.RemoveAll(root)
		if back := os.Rename(old, root); back != nil {
			return fmt.Errorf("update failed (%w) and %s could not be put back: %w", err, root, back)
		}
		return err
	}
	os.RemoveAll(old)
	return nil
}

func moveInto(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if _, err := os.Stat(src); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		return exec.Command("ditto", src, dst).Run()
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeExec(dst, in)
}

func startUpdates(a *app, flushWindow func()) {
	u := newUpdater(a.dir)
	if _, ok := parseTag(u.version); !ok {
		return
	}
	root, exe, err := installRoot()
	if err != nil {
		log.Printf("update: find the install: %v", err)
		return
	}
	if ok, err := u.atLaunch(a.currentSettings(), root); err != nil {
		log.Printf("update: %v", err)
	} else if ok {
		log.Printf("update: installed, relaunching")
		if err := relaunch(exe); err != nil {
			log.Printf("update: relaunch: %v", err)
		}
	}
	a.updater = &u
	a.restart = func() error {
		ok, err := u.installStaged(root)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("no update is ready")
		}
		if err := saveDrafts(a.draftsPath(), a.collectDrafts()); err != nil {
			log.Printf("update: save drafts: %v", err)
		}
		flushWindow()
		return relaunch(exe)
	}
	go a.watchUpdates(u)
}

func (a *app) currentSettings() settings {
	s, err := loadSettings(a.settingsPath())
	if err != nil {
		log.Printf("settings: %v", err)
	}
	return s
}

func (a *app) watchUpdates(u updater) {
	if tag := u.readyTag(); tag != "" {
		a.setUpdateReady(tag)
	}
	last := ""
	for {
		if !a.currentSettings().UpdatesOff {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			tag, err := a.stageUpdate(ctx)
			cancel()
			switch {
			case err != nil:
				if msg := err.Error(); msg != last {
					log.Printf("update check: %v", err)
					last = msg
				}
			case tag != "":
				last = ""
				a.setUpdateReady(tag)
			}
		}
		time.Sleep(time.Hour)
	}
}

func (a *app) stageUpdate(ctx context.Context) (string, error) {
	a.stageMu.Lock()
	defer a.stageMu.Unlock()
	return a.updater.checkAndStage(ctx)
}

var (
	errNoSelfUpdate = errors.New("this build of clack does not update itself")
	errDevBuild     = errors.New("this is a development build of clack, which does not update itself")
)

func (a *app) noUpdater() error {
	if _, ok := parseTag(version); !ok {
		return errDevBuild
	}
	return errNoSelfUpdate
}

func (a *app) CheckForUpdates(ctx context.Context) (tag string, ready bool, err error) {
	if a.updater == nil {
		return "", false, a.noUpdater()
	}
	if a.currentSettings().UpdatesOff {
		latest, err := a.updater.latestTag(ctx)
		if err != nil || !newerTag(a.updater.version, latest) {
			return "", false, err
		}
		return latest, false, nil
	}
	tag, err = a.stageUpdate(ctx)
	if err != nil || tag == "" {
		return "", false, err
	}
	a.setUpdateReady(tag)
	return tag, true, nil
}

func (a *app) DownloadUpdate(ctx context.Context) (string, error) {
	if a.updater == nil {
		return "", a.noUpdater()
	}
	tag, err := a.stageUpdate(ctx)
	if err == nil && tag == "" {
		err = errors.New("no newer release was found")
	}
	return tag, err
}

func (a *app) setUpdateReady(tag string) {
	a.mu.Lock()
	changed := a.updateTag != tag
	a.updateTag = tag
	a.mu.Unlock()
	if changed {
		a.notify()
	}
}

func (a *app) UpdateReady() string {
	if a.currentSettings().UpdatesOff {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.updateTag
}

func (a *app) RestartToUpdate() error {
	if a.restart == nil {
		return errors.New("this window cannot restart clack")
	}
	return a.restart()
}
