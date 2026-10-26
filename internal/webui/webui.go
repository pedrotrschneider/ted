// Package webui serves the immutable application build without exposing other files.
package webui

import (
	"bytes"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// New validates the build at startup. The filesystem must contain only trusted build output.
func New(files fs.FS) (http.Handler, error) {
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read frontend build: %w", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", 405)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' https://debridmediamanager.com/apple-touch-icon.png https://images.metahub.space https://live.metahub.space https://m.media-amazon.com https://image.tmdb.org; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-cache")
		var data []byte
		if name == "" {
			name = "index.html"
			data = index
		} else {
			if !fs.ValidPath(name) || !(strings.HasPrefix(name, "assets/") || name == "third-party-notices.txt" || name == "icon.png") {
				http.NotFound(w, r)
				return
			}
			// No directory listings or source maps, and no files outside the build allowlist.
			ext := path.Ext(name)
			if name != "icon.png" && ext != ".js" && ext != ".css" && ext != ".txt" {
				http.NotFound(w, r)
				return
			}
			content, err := fs.ReadFile(files, name)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			data = content
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
		}
		w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	}), nil
}

// WithAPI keeps missing API and health paths out of frontend handling.
func WithAPI(api, ui http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/health/") {
			api.ServeHTTP(w, r)
			return
		}
		ui.ServeHTTP(w, r)
	})
}
