package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/nullentropy/clack/chat"

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

// saveFile downloads an attachment's original into dir
func saveFile(ctx context.Context, store *chat.Store, id, dir string) (string, error) {
	f, ok := store.File(id)
	if !ok {
		return "", fmt.Errorf("no file %s", id)
	}
	name := filepath.Base(f.Name)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		name = f.ID
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 0; ; n++ {
		path := filepath.Join(dir, name)
		if n > 0 {
			path = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, n, ext))
		}
		out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		err = store.Download(ctx, id, true, out)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(path)
			return "", err
		}
		return path, nil
	}
}

func downloadsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Downloads"), nil
}
