CLAUDE.md

Dokument definiuje niezmienniki architektoniczne, strukturę projektu oraz zasady pracy w repozytorium Server Monitor (pojedyncza binarka Go + osadzony frontend React 19 / Meta Astryx).

Przed rozpoczęciem implementacji dowolnego modułu przeczytaj szczegółową specyfikację techniczną w docs/SPEC.md.

1. Niezmienniki architektoniczne (Non-Negotiable)

Single Binary Delivery: Backend w Go (>= 1.23) kompiluje się do pojedynczej statycznej binarki (CGO_ENABLED=0 GOOS=linux GOARCH=amd64) z osadzonym buildem frontendu (//go:embed all:web/dist). Na serwerze produkcyjnym nie może być wymagany Node.js, PHP ani osobny serwer HTTP.

Zero External Infrastructure Dependencies: System monitoringu nie może zależeć od usług, które monitoruje (Elasticsearch, Redis, MariaDB). Stan aplikacji, wybór usług i logi audytowe są zapisywane wyłącznie w lokalnej bazie SQLite w trybie WAL (czysty sterownik Go modernc.org/sqlite, bez CGO).

Frontend Stack: Warstwa UI powstaje w React 19 + TypeScript + Vite z użyciem komponentów Meta Astryx (pakiety @astryxdesign/core i @astryxdesign/theme-neutral). Komunikacja z backendem odbywa się wyłącznie przez REST JSON API.

Least Privilege (Brak root dla daemona i HTTP):

Proces główny działa jako dedykowany użytkownik systemowy monitor:monitor.

Odczyt usług systemd działa bez sudo (systemctl list-units --output=json oraz systemctl show).

Odczyt stanu supervisor działa przez socket /var/run/supervisor.sock (uprawnienia grupy monitor), nigdy przez sudo supervisorctl.

Sesje SSH logowane są przez hook pam_exec.so wywołujący lokalne sub-polecenie server-monitor ingest-pam.

Historia komend CLI zbierana jest przez auditd (execve dla auid >= 1000 i auid != 4294967295), a daemon odczytuje logi przez uprawnienia grupy (adm / audit).

Budżety wydajnościowe i odporność:

Każda sonda sieciowa (Elasticsearch, Redis, Kibana) ma sztywny context.WithTimeout maksymalnie 1500 ms.

Endpoint GET /api/health nigdy nie wykonuje sond sieciowych ani exec.Command podczas żądania HTTP — zwraca wyłącznie ostatni stan z tabeli health_checks_cache (odświeżanej w tle co 10–15 s), odpowiadając w czasie poniżej 20 ms.

SQLite działa z PRAGMA journal_mode=WAL;, PRAGMA synchronous=NORMAL;, PRAGMA busy_timeout=5000; oraz osobnym połączeniem zapisu (SetMaxOpenConns(1)), aby wyeliminować błędy SQLITE_BUSY.

2. Struktura katalogów

├── cmd/
│   └── server-monitor/       # Punkt wejścia CLI (subkomendy: serve, ingest-pam, check)
├── internal/
│   ├── api/                  # Router HTTP, middleware autoryzacji, handlery REST, DTO
│   ├── collector/            # Sondy ES/Redis/Kibana, wywołania systemctl i supervisor.sock
│   ├── parser/               # Czyste parsery (systemctl, supervisor, auditd) + fixtures/
│   ├── scheduler/            # Pętle tła odświeżające health_checks_cache
│   └── storage/              # Migracje SQLite WAL oraz repozytoria
├── web/                      # Aplikacja React 19 + TypeScript + Meta Astryx
├── deploy/                   # Szablony: systemd unit, reguły auditd, wpis PAM, supervisord
├── docs/
│   └── SPEC.md               # Schemat bazy, kontrakty JSON API, szczegóły parserów
└── embed.go                  # //go:embed all:web/dist


3. Zasady implementacji i testowania

Izolacja parserów: Pakiet internal/parser zawiera wyłącznie czyste funkcje ([]byte / io.Reader -> struktury domenowe) bez importów os/exec ani net.

Testy przed integracją: Każdy parser (systemctl, grupowanie supervisor, korelacja wieloliniowych rekordów auditd SYSCALL+EXECVE+CWD, odpowiedzi ES/Redis/Kibana) musi mieć testy jednostkowe na statycznych plikach w internal/parser/fixtures/ przed podpięciem do internal/collector.

Bezpieczeństwo współbieżności i SQL: Wszystkie testy Go muszą przechodzić z flagą -race. Zapytania SQL używają wyłącznie parametrów bindowanych (?).

Spójność kontraktu API: Struktury JSON zwracane przez internal/api muszą być w 100% zgodne z typami w web/src/types/api.ts oraz docs/SPEC.md.

4. Komendy budowania i weryfikacji

Testy backendu: go test -race -v ./...

Analiza statyczna: go vet ./...

Build frontendu: cd web && npm ci && npm run build

Build binarki Linux: CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/server-monitor ./cmd/server-monitor


## 5. Środowisko deweloperskie vs docelowe VM
- Maszyna lokalna służy **wyłącznie** do pisania kodu, uruchamiania testów jednostkowych (`go test -race ./...` na mockach/fixtures i tymczasowych plikach SQLite w `t.TempDir()`) oraz cross-kompilacji binarki Linux (`GOOS=linux GOARCH=amd64`).
- **Nigdy** nie uruchamiaj skompilowanej binarki (`./dist/server-monitor`), nie wywołuj `systemctl`, `supervisorctl`, `auditctl` ani nie modyfikuj katalogów `/etc/`, `/var/` czy `/usr/local/bin/` na maszynie lokalnej.
- Wszystkie pliki konfiguracyjne dla Linuxa (PAM, `auditd`, `systemd`, `supervisord`) generuj wyłącznie jako szablony w katalogu `deploy/` wewnątrz repozytorium.
