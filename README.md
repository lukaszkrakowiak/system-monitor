# Server Monitor

A small, self-contained monitoring dashboard for a single Linux server. One static Go binary with the
React frontend embedded; no Node.js, web server or external database is needed in production.

## What it shows

- **Health** (`/health`): selected systemd services, supervisor process groups, and Elasticsearch / Redis / Kibana status.
- **Services** (`/services`): pick which systemd units appear on the health page.
- **Audit** (`/audit`): shell command history (auditd), SSH sessions (PAM), and failed logins (auditd).
  Any remote IP can be given your own label ("office", "my laptop") that is shown next to it and added to login alert emails.
- **Settings** (`/settings`): alert rules and notification connectors (email/SMTP today; the connector registry is built for Slack, Teams, etc.).
  Rules: SSH logins in chosen time windows (e.g. Mon–Fri 17:00–06:00, or the whole weekend), SSH logins from IPs outside an allow-list, and
  any monitored service leaving the OK state (with optional recovery notices). Delivery is queued with retries; the History tab shows every notification.

## Design

- Single static binary (`CGO_ENABLED=0`), frontend embedded with `//go:embed`.
- State is stored locally in SQLite (WAL mode, pure-Go driver). The monitor never depends on the services it watches.
- Runs as an unprivileged `monitor` user: systemd via `systemctl` without sudo, supervisor via its unix socket,
  command history and failed logins from `audit.log` (group access), SSH sessions via a `pam_exec` hook.
- `GET /api/health` only reads a cache refreshed in the background every 10–15 s; it never probes or executes anything per request.
- Network probes have a hard 1.5 s timeout.

Details: [`CLAUDE.md`](CLAUDE.md) (architecture rules) and [`docs/SPEC.md`](docs/SPEC.md) (schema, API contracts, parsers).

## Build

Requirements: Go 1.23+, Node.js 20+ (build machine only).

```sh
cd web && npm ci && npm run build && cd ..
go test -race ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o dist/server-monitor ./cmd/server-monitor
```

`npm run build` empties `web/dist`; recreate the placeholder with `touch web/dist/.gitkeep` so a fresh checkout still compiles.

## Deploy

Templates live in [`deploy/`](deploy/) (systemd unit, env file, sysusers/tmpfiles, auditd rules, PAM entry, supervisord).
Copy them to the target server by hand; see [`deploy/README.md`](deploy/README.md). Set `SM_API_TOKEN` (at least 16 characters)
in the env file — the server refuses to start without credentials. To store an SMTP password from the UI also set `SM_SECRET_KEY`
(32+ characters, e.g. `openssl rand -base64 32`); passwords are kept in SQLite encrypted with AES-256-GCM and never returned by the API.

Subcommands: `server-monitor serve`, `server-monitor ingest-pam` (called by PAM), `server-monitor check`.

## Security notes

- The API requires a Bearer token or Basic Auth (`GET /api/health` can be made public with `HEALTH_PUBLIC=true`).
- Serve it on localhost or behind a TLS reverse proxy / SSH tunnel; the binary itself speaks plain HTTP.
- Audit data (commands, failed logins) is sensitive; protect the token accordingly.

## License

MIT, see [`LICENSE`](LICENSE).
