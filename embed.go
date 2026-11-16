// Package servermonitor osadza zbudowany frontend (web/dist) w binarce.
package servermonitor

import (
	"embed"
	"io/fs"
)

// Katalog web/dist musi istnieć w czasie kompilacji (w repozytorium trzyma go .gitkeep;
// `cd web && npm ci && npm run build` wypełnia go aplikacją).
//
//go:embed all:web/dist
var dist embed.FS

// WebFS zwraca zawartość web/dist jako korzeń systemu plików.
func WebFS() (fs.FS, error) {
	return fs.Sub(dist, "web/dist")
}
