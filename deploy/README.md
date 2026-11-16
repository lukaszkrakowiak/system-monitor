# Wdrożenie (szablony)

Pliki w tym katalogu są **szablonami** — kopiuje się je ręcznie na docelową maszynę. Nie instaluj ich na maszynie deweloperskiej.

| Szablon | Cel na serwerze |
|---|---|
| `systemd/server-monitor.service` | `/etc/systemd/system/` |
| `systemd/server-monitor.env.example` | `/etc/server-monitor/server-monitor.env` (0640 root:monitor) |
| `systemd/sysusers-server-monitor.conf` | `/usr/lib/sysusers.d/server-monitor.conf` |
| `systemd/tmpfiles-server-monitor.conf` | `/usr/lib/tmpfiles.d/server-monitor.conf` |
| `auditd/server-monitor.rules` + `auditd/README.md` | `/etc/audit/rules.d/`, ustawienie `log_group = adm` |
| `pam/sshd-server-monitor` | dopisać do `/etc/pam.d/sshd` |
| `supervisord/server-monitor.conf` | fragment konfiguracji supervisord |

Binarka: `/usr/local/bin/server-monitor` (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" ./cmd/server-monitor`).

## Uwaga: uprawnienia bazy a `ingest-pam`

`pam_exec` uruchamia `ingest-pam` poza procesem demona (zwykle jako root), więc może utworzyć pliki
`state.db-wal`/`-shm`. Katalog `/var/lib/server-monitor` ma dlatego `2770 monitor:monitor` (setgid,
patrz `tmpfiles-server-monitor.conf`), a `UMask=0007` w unicie utrzymuje grupowy zapis. Bez tego demon mógłby
stracić możliwość zapisu do bazy po pierwszym logowaniu SSH.
