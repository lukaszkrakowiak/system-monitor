package servermonitor

import (
	"io/fs"
	"testing"
)

func TestEmbeddedFrontend(t *testing.T) {
	fsys, err := WebFS()
	if err != nil {
		t.Fatal(err)
	}
	b, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		t.Skip("web/dist/index.html not built (cd web && npm ci && npm run build)")
	}
	if len(b) == 0 {
		t.Fatal("index.html is empty")
	}
	assets, _ := fs.Glob(fsys, "assets/*.js")
	if len(assets) == 0 {
		t.Fatal("no JS bundle embedded")
	}
}
