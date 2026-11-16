Technical Specification: Server Monitor V1

1. Schemat bazy SQLite (/var/lib/server-monitor/state.db)

PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;

CREATE TABLE IF NOT EXISTS monitored_services (
    unit_name    TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    is_enabled   INTEGER NOT NULL DEFAULT 1,
    sort_order   INTEGER NOT NULL DEFAULT 0,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS health_checks_cache (
    service_key  TEXT PRIMARY KEY, -- np. 'systemd', 'supervisor', 'elasticsearch', 'redis', 'kibana'
    status       TEXT NOT NULL,    -- 'OK' | 'WARN' | 'CRIT'
    payload_json TEXT NOT NULL,
    checked_at   DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS ssh_sessions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    username     TEXT NOT NULL,
    remote_ip    TEXT NOT NULL,
    tty          TEXT NOT NULL,
    pam_service  TEXT NOT NULL,
    login_at     DATETIME NOT NULL,
    logout_at    DATETIME
);
CREATE INDEX IF NOT EXISTS idx_ssh_sessions_user_time ON ssh_sessions(username, login_at DESC);

CREATE TABLE IF NOT EXISTS cli_command_logs (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    audit_event_id     TEXT NOT NULL UNIQUE, -- np. '1731661200.123:4581' (deduplikacja)
    auid               INTEGER NOT NULL,
    login_username     TEXT NOT NULL,
    effective_username TEXT NOT NULL,
    cwd                TEXT NOT NULL,
    command            TEXT NOT NULL,
    exit_code          INTEGER,
    executed_at        DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_cli_logs_user_time ON cli_command_logs(login_username, executed_at DESC);
CREATE INDEX IF NOT EXISTS idx_cli_logs_executed_at ON cli_command_logs(executed_at DESC);


2. Logika kolektorów i parserów

A. systemctl (Discovery + /health)

Lista wszystkich usług (GET /api/systemd/units):
Wywołuje systemctl list-units --type=service --all --no-pager --output=json, łączy wynik z tabelą monitored_services, zwracając flagę selected: bool.

Odpytywanie wybranych usług w tle:
Dla unitów z is_enabled = 1 wywołuje systemctl show <units...> --property=Id,ActiveState,SubState,MainPID,MemoryCurrent,ExecMainStartTimestamp.
Mapowanie statusu:

active + running -> OK

activating / reloading -> WARN

failed / inactive -> CRIT

B. Parser supervisor (grupowanie procesów)

Odczyt przez XML-RPC po UNIX socket /var/run/supervisor.sock (supervisor.getAllProcessInfo) lub parsowanie wyjścia supervisorctl status.

Reguła grupowania:

Jeśli nazwa ma postać grupa:proces_00, nazwą grupy jest część przed : (lub nazwa po usunięciu sufiksu _\d+$, gdy nie ma dwukropka).

Agregacja zlicza running (stan RUNNING) oraz total (wszystkie skonfigurowane procesy w grupie).

Status grupy:

running == total && total > 0 -> OK

0 < running < total -> DEGRADED

running == 0 -> DOWN

C. Sondy klastrowe (sztywny timeout 1500ms)

Elasticsearch: GET {ES_URL}/_cluster/health (opcjonalny Basic Auth lub Authorization: ApiKey ...).
Pobierane pola: cluster_name, status (green / yellow / red), number_of_nodes, unassigned_shards, active_shards_percent. Mapowanie: green -> OK, yellow -> WARN, red -> CRIT.

Redis: Połączenie TCP lub UNIX socket -> komendy PING + INFO server,memory,clients.
Pobierane pola: ping_ms, used_memory_human, connected_clients, uptime_in_seconds. Brak PONG lub przekroczenie czasu -> CRIT.

Kibana: GET {KIBANA_URL}/api/status.
Pobierane pola: overall_level (available / degraded / critical), response_ms.

3. Audyt SSH (PAM) i komend CLI (auditd)

Warstwa 1: PAM Hook (/etc/pam.d/sshd)

session optional pam_exec.so seteuid /usr/local/bin/server-monitor ingest-pam


Subkomenda ingest-pam odczytuje zmienne środowiskowe PAM_USER, PAM_RHOST, PAM_TTY, PAM_SERVICE, PAM_TYPE:

open_session wstawia nowy rekord do ssh_sessions.

close_session uzupełnia logout_at dla najnowszego otwartego rekordu danego username + tty.

Warstwa 2: auditd (/etc/audit/rules.d/server-monitor.rules)

-a always,exit -F arch=b64 -S execve -F auid>=1000 -F auid!=4294967295 -k cli_audit
-a always,exit -F arch=b32 -S execve -F auid>=1000 -F auid!=4294967295 -k cli_audit


Parser śledzi /var/log/audit/audit.log (z obsługą rotacji plików i zapisem offsetu/inode w SQLite). Grupuje linie o tym samym ID zdarzenia audit(timestamp:serial) z kluczem cli_audit:

Z rekordu SYSCALL wyciąga auid (kto się zalogował), uid (z czyimi uprawnieniami uruchomiono proces) oraz exit / success.

Z rekordu CWD wyciąga cwd.

Z rekordu EXECVE składa pełną komendę z argumentów a0, a1, ..., an (dekodując wartości zapisane w hex przez auditd, gdy argument zawiera spacje lub znaki specjalne).

Mapuje auid -> login_username oraz uid -> effective_username przez os/user.LookupId (z cachem w pamięci).

4. Kontrakt REST API

GET /api/health — publiczny lub chroniony tokenem (konfigurowalne flagą HEALTH_PUBLIC=true); zwraca zagregowany obiekt:

{
  "overall_status": "OK",
  "generated_at": "2026-11-15T10:30:00Z",
  "systemd": [
    {
      "unit": "nginx.service",
      "display_name": "Nginx",
      "active": "active",
      "sub": "running",
      "status": "OK",
      "memory_bytes": 18432000,
      "uptime": "12d 4h"
    }
  ],
  "supervisor": [
    {
      "group": "messenger_async",
      "running": 4,
      "total": 4,
      "status": "OK",
      "workers": []
    }
  ],
  "clusters": {
    "elasticsearch": {
      "status": "OK",
      "cluster_name": "prod-es",
      "cluster_status": "green",
      "nodes": 3,
      "unassigned_shards": 0
    },
    "redis": {
      "status": "OK",
      "ping_ms": 1.2,
      "used_memory_human": "142.5M",
      "connected_clients": 28
    },
    "kibana": {
      "status": "OK",
      "overall_level": "available",
      "response_ms": 42
    }
  }
}


GET /api/systemd/units — lista wszystkich usług z flagą selected (chronione).

PUT /api/systemd/units/selection — body {"units": ["nginx.service", "php8.4-fpm.service"]} (chronione Basic Auth / Bearer Token).

GET /api/audit/sessions?user=&limit=50&offset=0 — historia sesji SSH (chronione).

GET /api/audit/commands?user=&q=&limit=100&offset=0 — historia komend CLI z filtrowaniem po użytkowniku i frazie (chronione).

GET /api/audit/login-failures?user=&ip=&limit=100&offset=0 — nieudane próby logowania (szczegóły w sekcji 9) (chronione).

5. Widoki Frontendu (React 19 + Meta Astryx)

/health — Kafelki statusu ogólnego, tabela wybranych usług systemd, karty zgrupowanych workerów supervisor (z rozwijaną listą poszczególnych instancji) oraz sekcja stanu klastrów Elasticsearch, Redis i Kibana. Auto-odświeżanie co 10 s.

/services — Wyszukiwarka wszystkich unitów systemd na serwerze z przełącznikami zapisującymi wybór na /health.

/audit — Trzy zakładki (trzecia, Nieudane logowania: czas, próbowany użytkownik, IP, usługa): Historia komend CLI (tabela z czasem, użytkownikiem logującym, efektywnym użytkownikiem np. root, katalogiem cwd i pełną komendą + filtr po userze i frazie) oraz Sesje SSH (logowania/wylogowania z IP i TTY).


6. Uzupełnienia kontraktu (etap 3)

Poniższe doprecyzowania nie zmieniają kontraktu z sekcji 4; opisują kształty odpowiedzi, których sekcja 4 nie podawała.

GET /api/health — dodatkowe zasady:
- Czyta wyłącznie health_checks_cache (zero sond sieciowych i exec w żądaniu).
- overall_status = najgorszy ze statusów wpisów cache (OK < WARN < CRIT). Brak jakichkolwiek wpisów -> "WARN".
  Wpis starszy niż 60 s (scheduler nie odświeża) lub z niepoprawnym payloadem -> overall_status "CRIT".
- systemd i supervisor to zawsze tablice (puste [] gdy brak danych). W clusters brakujące sondy (niekonfigurowane) są pomijane.
- Pola opcjonalne: clusters.*.error (string, tylko przy awarii sondy), clusters.elasticsearch.active_shards_percent (number),
  clusters.redis.uptime_in_seconds (number). systemd[].memory_bytes to number albo null. systemd[].uptime to "" gdy nieznany.
- Pole ES active_shards_percent pochodzi z active_shards_percent_as_number w odpowiedzi Elasticsearch.

GET /api/systemd/units -> {"units": [{"unit","description","load","active","sub","selected"}]}
PUT /api/systemd/units/selection (Content-Type: application/json wymagany, inaczej 415) body {"units": [...]} (pole wymagane;
[] czyści wybór) -> 200 {"units": [...]} (zapisana lista bez duplikatów). Nazwy muszą pasować do ^[A-Za-z0-9_.@:\\][A-Za-z0-9_.@:\\-]*\.service$,
maks. 500 pozycji; błąd walidacji -> 400 i brak zmian.

GET /api/audit/sessions?user=&limit=50&offset=0 -> {"sessions": [{"id","username","remote_ip","tty","pam_service","login_at","logout_at"|null}], "limit","offset"}
GET /api/audit/commands?user=&q=&limit=100&offset=0 -> {"commands": [{"id","auid","login_username","effective_username","cwd","command","exit_code"|null,"executed_at"}], "limit","offset"}
- Czasy w RFC 3339 UTC. limit 1..500, offset 0..1000000, user/q do 200 znaków; naruszenie -> 400. q to fraza literalna (bez wildcardów).

Błędy: {"error": "<komunikat>"} z kodami 400, 401 (WWW-Authenticate), 404, 405, 415, 500 (bez szczegółów wewnętrznych).
Uwierzytelnianie: Authorization: Bearer <SM_API_TOKEN> lub Basic (SM_BASIC_USER/SM_BASIC_PASSWORD); bez skonfigurowanych danych `serve` nie startuje.

7. Konfiguracja (zmienne środowiskowe)

SM_DB_PATH, SM_LISTEN, HEALTH_PUBLIC, SM_API_TOKEN (min. 16 znaków), SM_BASIC_USER, SM_BASIC_PASSWORD, ES_URL, ES_USER, ES_PASSWORD, ES_API_KEY,
KIBANA_URL, KIBANA_USER, KIBANA_PASSWORD, KIBANA_API_KEY, REDIS_ADDR, REDIS_NETWORK (tcp|unix), REDIS_PASSWORD, SUPERVISOR_SOCKET ("off" wyłącza),
SM_AUDIT_LOG ("off" wyłącza), SM_REFRESH_INTERVAL (10s–15s), SM_SECRET_KEY (min. 32 znaki; klucz szyfrowania haseł konektorów, sekcja 10).
Szczegóły: deploy/systemd/server-monitor.env.example.

8. Tabela audit_log_state (migracja 2)

audit_log_state(id INTEGER PRIMARY KEY CHECK(id=1), inode INTEGER, offset INTEGER, updated_at DATETIME) — pozycja śledzenia audit.log.
Offset wskazuje początek niedokończonego zdarzenia auditd (po restarcie jest czytane ponownie; duplikaty odrzuca UNIQUE audit_event_id).

9. Nieudane logowania (migracja 3)

login_failures(id INTEGER PRIMARY KEY AUTOINCREMENT, audit_event_id TEXT NOT NULL UNIQUE, username TEXT NOT NULL, remote_ip TEXT NOT NULL,
service TEXT NOT NULL, attempted_at DATETIME NOT NULL) + indeksy po attempted_at oraz (remote_ip, attempted_at).

Źródło: ten sam audit.log co historia komend. Rekord jednoliniowy `type=USER_LOGIN ... msg='op=login acct="<user>" exe="/usr/sbin/sshd" addr=<ip> ... res=failed'`
(starsze jądra: res=0) tworzy jeden wiersz. USER_AUTH pomijamy (to ta sama próba widziana przez PAM, liczylibyśmy ją podwójnie), udane logowania też.
acct bez cudzysłowu jest dekodowany z hex; brak konta -> "unknown"; addr "?" -> pusty remote_ip (wtedy używany jest hostname, jeśli jest). Rekordy
USER_LOGIN generuje sshd niezależnie od reguł auditd (reguły w deploy/auditd dotyczą tylko execve). Hook PAM (ingest-pam) NIE widzi nieudanych prób,
bo pam_exec w sekcji session działa dopiero po uwierzytelnieniu.

GET /api/audit/login-failures?user=&ip=&limit=100&offset=0 (chronione) -> {"failures": [{"id","username","remote_ip","service","attempted_at"}], "limit","offset"}
- user i ip to dokładne dopasowania (do 200 znaków); limit/offset jak w pozostałych endpointach audytu; najnowsze pierwsze.

Widok: zakładka „Failed logins" na /audit (czas, próbowany użytkownik, IP, usługa; filtr po user i Remote IP). Alarmowanie (progi, status WARN/CRIT) — później.

10. Alerty i konektory powiadomień (migracja 4)

Architektura: demon `serve` uruchamia w tle silnik alertów (internal/alerts), który co ~5 s (a) bierze nowe sesje SSH z ssh_sessions (kursor
w alert_state; pierwsze uruchomienie startuje od ostatniej sesji, historia nie generuje alertów), (b) porównuje stany z health_checks_cache,
(c) zapisuje powiadomienia w kolejce notifications (outbox) i (d) dostarcza je konektorami z ponawianiem. `ingest-pam` i `GET /api/health`
nie wysyłają niczego — silnik działa wyłącznie w demonie, a awaria SMTP nie wpływa na monitoring.

Tabele (migracja 4):
- notification_connectors(type PK, enabled, config_json, secret_enc, updated_at) — konfiguracja konektora bez sekretu + zaszyfrowany sekret.
- alert_settings(id=1, settings_json, updated_at) — reguły (jeden dokument JSON, struktura jak w API).
- alert_state(key PK, value, updated_at) — kursor sesji (`cursor:sessions`) i stany elementów (`svc:<klucz>`).
- notifications(id, dedupe_key, connector, kind, subject, body, status pending|sent|failed, attempts, next_attempt_at, last_error, created_at, sent_at,
  UNIQUE(dedupe_key, connector)) — jedna dostawa jednym konektorem; UNIQUE chroni przed duplikatami po restarcie.

Sekrety: hasło konektora jest szyfrowane AES-256-GCM kluczem z SM_SECRET_KEY (nie z bazy) i nigdy nie jest zwracane przez API (tylko has_secret).
Bez SM_SECRET_KEY zapis hasła jest odrzucany (400), a UI pokazuje ostrzeżenie. Konfiguracja SMTP bez hasła (np. lokalny relay) działa bez klucza.

Reguły (alerts.Settings):
{"timezone": "Europe/Warsaw"|"" (pusta = strefa serwera), "channels": ["email"],
 "login_time": {"enabled", "windows": [{"days": ["mon".."sun"], "from": "HH:MM", "to": "HH:MM"}]},
 "login_ip": {"enabled", "allowed": ["192.0.2.10", "10.0.0.0/8", "2001:db8::/32"]},
 "service_state": {"enabled", "notify_recovery", "confirm_checks": 1..10}}
- login_time: alert, gdy UDANE logowanie SSH wypada w którymkolwiek oknie. Okno dotyczy dni tygodnia wybranych według DNIA KALENDARZOWEGO chwili
  logowania (w strefie z ustawień); from > to przechodzi przez północ (17:00–06:00 = 17:00–24:00 i 00:00–06:00 danego dnia), from == to = cały dzień.
  Przykłady: „pon–pt 17:00–06:00" + „sob, niedz 00:00–00:00" (cały weekend).
- login_ip: alert, gdy adres źródłowy nie należy do białej listy (adresy i CIDR, IPv4 zmapowany w IPv6 jest sprowadzany do IPv4). Logowania bez adresu
  (konsola lokalna) są pomijane. Włączona reguła z pustą listą jest odrzucana (każde logowanie dawałoby alert). Jedno logowanie spełniające obie reguły
  daje jedno powiadomienie z dwoma powodami.
- service_state: alert, gdy element (usługa systemd, grupa supervisora, ES/Redis/Kibana, błąd samego kolektora) ma stan inny niż OK (WARN/DEGRADED/CRIT/DOWN).
  Zmiana stanu jest potwierdzana po `confirm_checks` kolejnych RÓŻNYCH pomiarach (checked_at), więc pojedyncza nieudana sonda nie alarmuje, a ponowne
  odczytanie tego samego wpisu cache nie liczy się dwa razy. Alert idzie raz na zmianę stanu (nie co pomiar); opcjonalnie także o powrocie do OK.
  Stan jest śledzony także przy wyłączonej regule, więc jej włączenie nie wywołuje „starych" alertów. Elementy, które zniknęły (np. usunięta z obserwowanych
  usługa), są zapominane bez powiadomienia.
- Zdarzenie jest kolejkowane dla każdego kanału z `channels`, który ma włączony i skonfigurowany konektor; bez kanału zdarzenie nie jest nigdzie wysyłane.

Dostarczanie: do 5 prób, odstępy między kolejnymi próbami 30 s, 2 min, 10 min i 30 min; po piątej nieudanej status = failed. Błąd ostatniej próby jest widoczny w historii.
Konektor e-mail: net/smtp (STARTTLS, TLS lub brak szyfrowania; AUTH PLAIN tylko przez TLS albo z pętli zwrotnej), UTF-8, temat RFC 2047, znaki nowej linii
w polach są odrzucane (ochrona przed wstrzykiwaniem nagłówków), limit 30 s na wysyłkę. Nowe konektory (Slack, Teams) dodaje się przez rejestr
w internal/notify (Type{Name, Validate, New}) bez zmian w silniku.

API (chronione; zapisy wymagają Content-Type: application/json, inaczej 415):
GET  /api/settings/alerts -> alerts.Settings (domyślne, gdy brak zapisanych)
PUT  /api/settings/alerts  body alerts.Settings -> 200 zapisane (znormalizowane) | 400 z opisem błędu
GET  /api/settings/connectors -> {"connectors": [{"type","label","configured","enabled","has_secret","config":{...}}], "secret_storage": bool}
PUT  /api/settings/connectors/{type} body {"enabled", "config": {...}, "password": null|""|"tekst"} (null = zostaw hasło, "" = usuń) -> 200 {connector} | 400 | 404
POST /api/settings/connectors/{type}/test body {} -> 200 {"ok": bool, "error": ""} (wiadomość próbna zapisanym konektorem; błąd wysyłki jest w ciele, nie jako 5xx)
GET  /api/alerts/history?limit=50&offset=0 -> {"notifications": [{"id","kind","connector","subject","body","status","attempts","last_error","created_at","sent_at"|null}], "limit","offset"}
Config konektora email: {"host","port","security": "starttls"|"tls"|"none","username","from","to": [adresy]}.
Endpointy zwracają 503, gdy alerty nie są skonfigurowane w tym procesie.

Widok: /settings — zakładki „Alert rules" (kanały, strefa, okna czasowe z presetami, biała lista IP, stany usług), „Connectors" (formularz SMTP, wysyłka próbna)
i „History" (kolejka i wynik dostarczania).
