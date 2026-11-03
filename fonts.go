package main

import (
	"embed"
	"net/http"

	"github.com/nullentropy/caution"
)

// JetBrains Mono, variable weight, under the SIL Open Font License (fonts/OFL.txt)
//
// Noto Sans JP cut to kana, CJK punctuation, vertical and fullwidth forms
// (fonts/OFL-NotoSansJP.txt). Made from google/fonts ofl/notosansjp/NotoSansJP[wght].ttf with
//
//	pyftsubset NotoSansJP[wght].ttf --unicodes=U+3000-30FF,U+31F0-31FF,U+FE30-FE4F,U+FF00-FFEF \
//		--layout-features='*' --output-file=NotoSansJP-Kana.ttf
//
//go:embed fonts/*.ttf
var fontFiles embed.FS

const (
	termFont = "JetBrains Mono"
	kanaFont = "Noto Sans JP"
)

func serveFonts() { http.Handle("/fonts/", http.FileServerFS(fontFiles)) }

func useFonts(s *caution.Session) {
	s.AddFont(termFont, "/fonts/JetBrainsMono.ttf", "/fonts/JetBrainsMono-Italic.ttf")
	s.AddFont(kanaFont, "/fonts/NotoSansJP-Kana.ttf")
	s.SetFonts(termFont, termFont, kanaFont)
}
