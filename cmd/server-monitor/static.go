package main

import (
	"log"
	"net/http"

	servermonitor "server-monitor"
	"server-monitor/internal/api"
)

// staticHandler zwraca osadzony frontend (//go:embed all:web/dist w embed.go).
// Gdy frontend nie został zbudowany (sam .gitkeep), zwraca nil i działa samo API.
func staticHandler() http.Handler {
	fsys, err := servermonitor.WebFS()
	if err != nil {
		log.Printf("embedded frontend unavailable: %v", err)
		return nil
	}
	h := api.SPAHandler(fsys)
	if h == nil {
		log.Printf("embedded frontend is empty (run: cd web && npm ci && npm run build); serving API only")
	}
	return h
}
