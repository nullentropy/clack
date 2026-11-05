package main

import (
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
)

// nickColors is how many nick colors a theme has
const nickColors = 16

var themes = map[string]map[string]string{
	"dark": {
		"bg": "#1a1d21", "panel": "#1a1d21", "panelAlt": "#19171d", "panelInset": "#1b1f26",
		"ink": "#d1d2d3", "inkDim": "#9aa3b2", "inkFaint": "#6b7280",
		"edge": "#3a4150", "edgeSoft": "#333a46", "titlebar": "#2e343e",
		"control": "#2e343e", "controlEdge": "#4a5262", "accent": "#1164a3",
		"danger": "#e06c75", "highlight": "#e5c07b", "scrim": "#00000099",
		"code": "#3a4150", "link": "#6cb6ff",
		"nick0": "#fda9b5", "nick1": "#e5715a", "nick2": "#f7b385", "nick3": "#cb8a00",
		"nick4": "#d3c678", "nick5": "#88a629", "nick6": "#9bd69c", "nick7": "#00b585",
		"nick8": "#6ed9d2", "nick9": "#00afce", "nick10": "#81cffc", "nick11": "#5998f3",
		"nick12": "#b7bdff", "nick13": "#ab7fe4", "nick14": "#e5aee8", "nick15": "#d96ea9",
	},
	"light": {
		"bg": "#ffffff", "panel": "#ffffff", "panelAlt": "#f4f5f7", "panelInset": "#eceef2",
		"ink": "#1d1c1d", "inkDim": "#5b6472", "inkFaint": "#8a93a2",
		"edge": "#d4d9e0", "edgeSoft": "#e2e6ec", "titlebar": "#e8ebef",
		"control": "#f1f2f5", "controlEdge": "#c6cdd7", "accent": "#1264a3",
		"danger": "#c0392b", "highlight": "#a8650a", "scrim": "#00000055",
		"code": "#e8eaef", "link": "#1264a3",
		"nick0": "#812a3e", "nick1": "#b84f3c", "nick2": "#7c3600", "nick3": "#a16700",
		"nick4": "#5d4d00", "nick5": "#668000", "nick6": "#135d1d", "nick7": "#008d64",
		"nick8": "#00605c", "nick9": "#0088a4", "nick10": "#005584", "nick11": "#3a74c5",
		"nick12": "#42428c", "nick13": "#855db7", "nick14": "#6c3270", "nick15": "#ad4d83",
	},
	"terminal": {
		"bg": "#000000", "panel": "#000000", "panelAlt": "#050a05", "panelInset": "#000000",
		"ink": "#33ff66", "inkDim": "#1fae45", "inkFaint": "#147a2f",
		"edge": "#0f5a23", "edgeSoft": "#0b3f19", "titlebar": "#031203",
		"control": "#031203", "controlEdge": "#147a2f", "accent": "#0f5a23",
		"danger": "#ff5555", "highlight": "#ccff99", "scrim": "#000000cc",
		"code": "#0a2410", "link": "#66ffcc",
		"nick0": "#81a904", "nick1": "#92c35f", "nick2": "#96e36e", "nick3": "#63ad59",
		"nick4": "#5acd6d", "nick5": "#7de59e", "nick6": "#00b66e", "nick7": "#45cc98",
		"nick8": "#00edb7", "nick9": "#00b392", "nick10": "#00d3b5", "nick11": "#30e8d7",
		"nick12": "#00b7b1", "nick13": "#00cccf", "nick14": "#00e9f9", "nick15": "#00afc4",
	},
}

const defaultTheme = "dark"

// every opaque color gets an Away variant, faded for people who are away
func init() {
	for _, t := range themes {
		for k, v := range maps.Clone(t) {
			if len(v) == len("#rrggbb") {
				t[k+"Away"] = v + "8c"
			}
		}
	}
}

// faded is a theme color as it's shown for someone away
func faded(color string) string { return color + "Away" }

func themeNames() []string { return slices.Sorted(maps.Keys(themes)) }

func (a *app) theme() map[string]string {
	s, err := loadSettings(a.settingsPath())
	if err != nil {
		log.Printf("settings: %v", err)
	}
	if t, ok := themes[s.Theme]; ok {
		return t
	}
	return themes[defaultTheme]
}

func (a *app) setTheme(name string) error {
	if _, ok := themes[name]; !ok {
		return fmt.Errorf("no theme called %s; there's %s", name, strings.Join(themeNames(), ", "))
	}
	if err := a.updateSettings(func(s *settings) { s.Theme = name }); err != nil {
		return err
	}
	a.notify()
	return nil
}
