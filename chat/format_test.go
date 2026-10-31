package chat

import (
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestFormat(t *testing.T) {
	user := func(id string) string { return map[string]string{"U1": "Arthur Dent"}[id] }
	channel := func(id string) string { return map[string]string{"C1": "general"}[id] }
	cases := map[string][]Run{
		"plain &amp; simple":              {{Text: "plain & simple"}},
		"a *bold* move":                   {{Text: "a "}, {Text: "bold", Bold: true}, {Text: " move"}},
		"_it_ and ~gone~":                 {{Text: "it", Italic: true}, {Text: " and "}, {Text: "gone", Strike: true}},
		"*_both_*":                        {{Text: "both", Bold: true, Italic: true}},
		"run `go test` now":               {{Text: "run "}, {Text: "go test", Code: true}, {Text: " now"}},
		"`*not bold*`":                    {{Text: "*not bold*", Code: true}},
		"snake_case_name":                 {{Text: "snake_case_name"}},
		"2*3*4":                           {{Text: "2*3*4"}},
		"* not a list *":                  {{Text: "* not a list *"}},
		"*open\nline*":                    {{Text: "*open\nline*"}},
		"hi <@U1>!":                       {{Text: "hi "}, {Text: "@Arthur Dent", User: "U1"}, {Text: "!"}},
		"see <#C1>":                       {{Text: "see "}, {Text: "#general", Channel: "C1"}},
		"<https://go.dev|Go> rocks":       {{Text: "Go", Link: "https://go.dev"}, {Text: " rocks"}},
		"*<https://go.dev>*":              {{Text: "https://go.dev", Link: "https://go.dev", Bold: true}},
		"before\n```\nx := 1\n```\nafter": {{Text: "before"}, {Text: "x := 1", Pre: true}, {Text: "after"}},
		"```a &lt; b```":                  {{Text: "a < b", Pre: true}},
		"unclosed ```code":                {{Text: "unclosed ```code"}},
	}
	for in, want := range cases {
		if got := Format(in, user, channel); !slices.Equal(got, want) {
			t.Errorf("Format(%q)\n got %+v\nwant %+v", in, got, want)
		}
	}
}

func TestDateReferences(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.Local)
	at := time.Date(2026, 10, 30, 13, 30, 0, 0, time.Local).Unix()
	ref := func(format string) string { return fmt.Sprintf("!date^%d^%s", at, format) }
	cases := map[string]string{
		ref("{time} - {date_short}"):   "13:30 - Oct 30, 2026",
		ref("{date_long}"):             "Friday, October 30, 2026",
		ref("{date_pretty} at {time}"): "today at 13:30",
		ref("{date_num}^https://x"):    "2026-10-30",
	}
	for in, want := range cases {
		if got, _ := date(in, now); got != want {
			t.Errorf("date(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Plain("<!date^notanumber^{time}|soon>", nil, nil); got != "soon" {
		t.Errorf("a bad date falls back to its label, got %q", got)
	}
}
