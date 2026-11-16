---
description: Realizuje wybrany etap implementacji (1-4) na podstawie CLAUDE.md i docs/SPEC.md
argument-hint: <numer-etapu: 1 | 2 | 3 | 4>
---

Przeczytaj `CLAUDE.md` oraz `docs/SPEC.md`, a następnie zrealizuj wyłącznie etap **$ARGUMENTS** zgodnie z poniższym podziałem:

- **Etap 1 (Fundament i SQLite):** Inicjalizacja modułu Go (`go.mod`), migracje SQLite w trybie WAL (`modernc.org/sqlite`), repozytoria w `internal/storage` oraz testy jednostkowe warstwy bazy danych.
- **Etap 2 (Parsery i kolektory):** Najpierw utwórz pliki testowe w `internal/parser/fixtures/` i testy jednostkowe parserów (`systemctl`, grupowanie `supervisor`, korelacja wieloliniowa `auditd` z dekodowaniem hex, odpowiedzi ES/Redis/Kibana). Po przejściu `go test -race ./internal/parser/...` zaimplementuj kolektory w `internal/collector` (z timeoutami 1500 ms) oraz `internal/scheduler`.
- **Etap 3 (Audyt OS i REST API):** Zaimplementuj subkomendę `ingest-pam`, śledzenie `audit.log` z obsługą rotacji, handlery REST w `internal/api` oraz pliki wdrożeniowe w `deploy/` (`systemd`, `auditd`, `pam`).
- **Etap 4 (Frontend Meta Astryx i Embed):** Zbuduj aplikację w `web/` (React 19 + TypeScript + `@astryxdesign/core`) z widokami `/health`, `/services` i `/audit`, spójną z kontraktami API, a następnie podepnij `embed.go` i zweryfikuj pełny build binarki.

Po zakończeniu etapu uruchom `go test -race ./...` oraz `go vet ./...` i podsumuj wprowadzone zmiany.
