package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func spaFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":       {Data: []byte("<html>app</html>")},
		"assets/app-1.js":  {Data: []byte("console.log(1)")},
		"assets/app-1.css": {Data: []byte("a{}")},
		"favicon.svg":      {Data: []byte("<svg/>")},
	}
}

func get(h http.Handler, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestSPAHandlerNilWithoutIndex(t *testing.T) {
	if SPAHandler(fstest.MapFS{".gitkeep": {}}) != nil {
		t.Fatal("handler must be nil when frontend is not built")
	}
}

func TestSPAHandlerRoutesAndCaching(t *testing.T) {
	h := SPAHandler(spaFS())
	for _, p := range []string{"/", "/health", "/services", "/audit", "/audit/deep/route", "/index.html"} {
		w := get(h, "GET", p)
		if w.Code != 200 || w.Body.String() != "<html>app</html>" || w.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: %d %q %v", p, w.Code, w.Body.String(), w.Header())
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'self'") {
			t.Errorf("%s: missing CSP", p)
		}
	}
	w := get(h, "GET", "/assets/app-1.js")
	if w.Code != 200 || w.Body.String() != "console.log(1)" || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %v", w.Code, w.Header())
	}
	if w := get(h, "GET", "/favicon.svg"); w.Code != 200 || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("root file: %d %v", w.Code, w.Header())
	}
	if w := get(h, "GET", "/assets/missing.js"); w.Code != 404 {
		t.Fatalf("missing asset must be 404, got %d", w.Code)
	}
	if w := get(h, "HEAD", "/health"); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("HEAD: %d %q", w.Code, w.Body.String())
	}
	if w := get(h, "POST", "/health"); w.Code != 405 {
		t.Fatalf("POST: %d", w.Code)
	}
}

func TestSPAHandlerPathTraversal(t *testing.T) {
	h := SPAHandler(spaFS())
	for _, p := range []string{"/../etc/passwd", "/assets/../../etc/passwd", "/%2e%2e/%2e%2e/etc/passwd"} {
		w := get(h, "GET", p)
		if strings.Contains(w.Body.String(), "root:") {
			t.Fatalf("%s leaked a file", p)
		}
	}
}

// API ma pierwszeństwo przed SPA: nieznana ścieżka /api/* to JSON 404, nie index.html.
func TestAPIWinsOverSPA(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.Static = SPAHandler(spaFS()) })
	if w := e.do("GET", "/api/nope", "", bearer); w.Code != 404 || strings.Contains(w.Body.String(), "<html>") {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	if w := e.do("GET", "/audit", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "<html>") {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
}
