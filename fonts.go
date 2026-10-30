package main

import (
	"embed"
	"net/http"

	"github.com/nullentropy/caution"
)

// JetBrains Mono, variable weight, under the SIL Open Font License (fonts/OFL.txt)
//
//go:embed fonts/*.ttf
var fontFiles embed.FS

const termFont = "JetBrains Mono"

func serveFonts() { http.Handle("/fonts/", http.FileServerFS(fontFiles)) }

func useFonts(s *caution.Session) {
	s.AddFont(termFont, "/fonts/JetBrainsMono.ttf", "/fonts/JetBrainsMono-Italic.ttf")
	s.SetFonts(termFont, termFont)
}
