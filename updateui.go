package main

import (
	"context"
	"strings"
	"time"

	"github.com/nullentropy/caution"
)

func (sc *screen) showDialog(d *caution.Node) {
	sc.closeDialog()
	if w := sc.win; w != nil && sc.root == w.root {
		w.closeOverlay()
		w.overlay = d
	}
	d.OnDismiss(sc.closeDialog)
	sc.dialog = d
	sc.root.Add(d)
}

func (sc *screen) closeDialog() {
	d := sc.dialog
	if d == nil {
		return
	}
	sc.dialog = nil
	if w := sc.win; w != nil && w.overlay == d {
		w.closeOverlay()
	} else if d.Parent() != nil {
		d.Remove()
	}
}

func (sc *screen) updateDialog(title, body string, buttons ...*caution.Node) {
	if len(buttons) == 0 && body != "" {
		buttons = []*caution.Node{caution.Button("OK").Primary().OnClick(sc.closeDialog)}
	}
	h := 110.0
	if body != "" {
		h = 170
	}
	d := caution.Dialog(title).CardSize(460, h)
	if body != "" {
		d.Add(caution.Label(body).FontSize(13).Color("$inkDim").Wrap().
			Anchor(caution.A{Left: caution.Px(20), Right: caution.Px(20), Top: caution.Px(16)}))
	}
	if len(buttons) > 0 {
		d.Add(caution.HStack().Gap(8).Anchor(caution.A{Right: caution.Px(20), Bottom: caution.Px(20)}).Kids(buttons...))
	}
	sc.showDialog(d)
}

func (sc *screen) checkForUpdates() {
	sc.updateDialog("Checking for updates…", "")
	wait := sc.dialog
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		tag, ready, err := sc.a.CheckForUpdates(ctx)
		cancel()
		sc.s.Update(func() {
			if sc.dialog != wait {
				return
			}
			have := "You have version " + strings.TrimPrefix(version, "v") + "."
			next := strings.TrimPrefix(tag, "v")
			switch {
			case err != nil:
				sc.updateDialog("Could not check for updates", sentence(err))
			case tag == "":
				sc.updateDialog("clack is up to date", have)
			case ready:
				sc.updateDialog("clack "+next+" is ready", have+" Restart clack to update.",
					caution.Button("Later").OnClick(sc.closeDialog),
					caution.Button("Restart").Primary().OnClick(sc.restartNow))
			default:
				sc.updateDialog("clack "+next+" is available", have+" Automatic updates are off.",
					caution.Button("Later").OnClick(sc.closeDialog),
					caution.Button("Update").Primary().OnClick(sc.downloadUpdate))
			}
		})
	}()
}

func (sc *screen) downloadUpdate() {
	sc.updateDialog("Downloading the update…", "")
	wait := sc.dialog
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		_, err := sc.a.DownloadUpdate(ctx)
		cancel()
		sc.s.Update(func() {
			if sc.dialog != wait {
				return
			}
			if err != nil {
				sc.updateDialog("Could not download the update", sentence(err))
				return
			}
			sc.restartNow()
		})
	}()
}

func sentence(err error) string {
	s := err.Error()
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

func (sc *screen) restartToUpdate() {
	if sc.a.UpdateReady() != "" {
		sc.restartNow()
	}
}

func (sc *screen) restartNow() {
	sc.closeDialog()
	go func() {
		err := sc.a.RestartToUpdate()
		if err == nil {
			return
		}
		sc.s.Update(func() {
			sc.updateDialog("Could not update", sentence(err)+" clack is still running the current version.")
		})
	}()
}

func updateField(a *app, saved settings) *caution.Node {
	note := caution.Label("").FontSize(12).Color("$inkFaint")
	box := caution.Checkbox("Update automatically", !saved.UpdatesOff).OnToggle(func(on bool) {
		if err := a.updateSettings(func(s *settings) { s.UpdatesOff = !on }); err != nil {
			note.SetText("couldn't save: " + err.Error())
			return
		}
		note.SetText("saved")
		a.notify()
	})
	return caution.VStack().Gap(6).Align("stretch").Kids(box, note)
}
