package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestThemesDefineEveryTokenUsed(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	used := map[string]bool{}
	token := regexp.MustCompile(`"\$([a-zA-Z]+)"`)
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range token.FindAllStringSubmatch(string(src), -1) {
			used[m[1]] = true
		}
	}
	for i := range nickColors {
		used[fmt.Sprintf("nick%d", i)] = true
	}
	for name, theme := range themes {
		for tok := range used {
			if _, ok := theme[tok]; !ok {
				t.Errorf("theme %s has no %s", name, tok)
			}
		}
	}
	if _, ok := themes[defaultTheme]; !ok {
		t.Fatalf("no default theme %s", defaultTheme)
	}
}

func TestSetThemeSavesAndRefusesUnknown(t *testing.T) {
	a := &app{dir: t.TempDir()}
	if err := a.setTheme("terminal"); err != nil {
		t.Fatal(err)
	}
	if a.theme()["bg"] != themes["terminal"]["bg"] {
		t.Fatal("terminal theme didn't stick")
	}
	if err := a.setTheme("plaid"); err == nil {
		t.Fatal("an unknown theme was accepted")
	}
}

func TestAlertIconMatchesTheIconsShape(t *testing.T) {
	a, err := png.Decode(bytes.NewReader(icon))
	if err != nil {
		t.Fatal(err)
	}
	b, err := png.Decode(bytes.NewReader(alertIcon))
	if err != nil {
		t.Fatal(err)
	}
	if a.Bounds() != b.Bounds() {
		t.Fatalf("alert icon is %v, the icon %v", b.Bounds(), a.Bounds())
	}
	for _, p := range []image.Point{{5, 5}, {160, 300}, {512, 400}} {
		_, _, _, a0 := a.At(p.X, p.Y).RGBA()
		_, _, _, a1 := b.At(p.X, p.Y).RGBA()
		if a0 != a1 {
			t.Errorf("alpha at %v: icon %#x, alert %#x", p, a0, a1)
		}
	}
}
