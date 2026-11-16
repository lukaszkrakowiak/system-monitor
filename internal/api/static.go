package api

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

const csp = "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; " +
	"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// SPAHandler serwi zbudowany frontend: istniejące pliki wprost (assets/ z długim cache),
// a nieznane ścieżki bez rozszerzenia (trasy React Router) dostają index.html.
// Zwraca nil, gdy w fsys nie ma index.html (frontend niezbudowany) — wtedy działa samo API.
func SPAHandler(fsys fs.FS) http.Handler {
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		return nil
	}
	files := http.FileServerFS(fsys)
	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Security-Policy", csp)
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p == "" || p == "index.html" {
			serveIndex(w, r)
			return
		}
		if st, err := fs.Stat(fsys, p); err == nil && !st.IsDir() {
			w.Header().Set("Content-Security-Policy", csp)
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			files.ServeHTTP(w, r)
			return
		}
		if path.Ext(p) != "" { // brakujący plik statyczny, nie trasa aplikacji
			http.NotFound(w, r)
			return
		}
		serveIndex(w, r)
	})
}
