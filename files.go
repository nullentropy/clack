package main

import (
	"bytes"
	"fmt"
	"image/png"
	"log"
	"net/http"
	"strings"

	"golang.org/x/image/webp"
)

// serveFile is /files/<id>
//
// preview by default or original with ?full
func (a *app) serveFile(w http.ResponseWriter, r *http.Request) {
	store := a.current()
	if store == nil {
		http.Error(w, "not connected to Slack", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/files/")
	_, ok := store.File(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	full := r.URL.Query().Has("full")
	if frame := r.URL.Query().Get("frame"); frame != "" {
		data, err := a.serveFrame(r.Context(), store, id, full, frame)
		if err != nil {
			log.Printf("file %s frame %s: %v", id, frame, err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Write(data)
		return
	}
	var buf bytes.Buffer
	err := store.Download(r.Context(), id, full, &buf)
	data := buf.Bytes()
	if err == nil {
		data, err = displayable(data)
	}
	if err != nil {
		log.Printf("file %s: %v", id, err)
		if !full {
			store.FileFailed(id, err.Error())
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(data)
}

func displayable(data []byte) ([]byte, error) {
	switch ct := http.DetectContentType(data); ct {
	case "image/png", "image/jpeg", "image/gif":
		return data, nil
	case "image/webp":
		img, err := webp.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("unreadable WebP: %w", err)
		}
		var out bytes.Buffer
		if err := png.Encode(&out, img); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	default:
		return nil, fmt.Errorf("Slack sent %s instead of an image", ct)
	}
}
