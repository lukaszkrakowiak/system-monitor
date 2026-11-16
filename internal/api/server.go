package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"server-monitor/internal/collector"
	"server-monitor/internal/parser"
	"server-monitor/internal/secret"
	"server-monitor/internal/storage"
)

// HealthStore czyta cache sond (jedyne źródło danych GET /api/health).
type HealthStore interface {
	All() ([]storage.HealthEntry, error)
}

// ServiceStore zarządza wyborem monitorowanych usług.
type ServiceStore interface {
	ListEnabled() ([]storage.MonitoredService, error)
	SetSelection(units []string) error
}

// SessionStore i CommandStore czytają historię audytu.
type SessionStore interface {
	List(user string, limit, offset int) ([]storage.SSHSession, error)
}
type CommandStore interface {
	List(user, q string, limit, offset int) ([]storage.CLICommand, error)
}
type LoginFailureStore interface {
	List(user, ip string, limit, offset int) ([]storage.LoginFailure, error)
}

// UnitLister zwraca wszystkie usługi systemd (wołany tylko z /api/systemd/units).
type UnitLister interface {
	ListUnits(ctx context.Context) ([]parser.Unit, error)
}

// Config konfiguruje serwer API.
type Config struct {
	Health   HealthStore
	Services ServiceStore
	Sessions SessionStore
	Commands CommandStore
	// LoginFailures jest opcjonalne; gdy nil, endpoint zwraca pustą listę.
	LoginFailures LoginFailureStore
	Units         UnitLister

	// Alerty (opcjonalne; gdy nil, odpowiednie endpointy zwracają 503).
	Connectors    ConnectorStore
	AlertSettings AlertSettingsStore
	Notifications NotificationStore
	Secrets       *secret.Box                                 // szyfrowanie haseł konektorów; nil = zapis sekretów niemożliwy
	TestConnector func(ctx context.Context, typ string) error // wiadomość próbna zapisanym konektorem

	Token     string // Bearer token (opcjonalny)
	BasicUser string // Basic Auth (opcjonalny)
	BasicPass string

	HealthPublic bool          // HEALTH_PUBLIC=true: GET /api/health bez uwierzytelniania
	StaleAfter   time.Duration // wpis cache starszy niż to => overall_status CRIT (domyślnie 60 s)

	Now                func() time.Time
	OnSelectionChanged func() // wołane asynchronicznie po udanym PUT selection
	Static             http.Handler
	Logf               func(string, ...any)
}

type server struct {
	cfg Config
}

const (
	defaultStale  = 60 * time.Second
	maxSelection  = 500
	maxBodyBytes  = 256 << 10
	maxFilterLen  = 200
	maxPageLimit  = 500
	maxPageOffset = 1_000_000
)

// New buduje handler HTTP.
func New(cfg Config) http.Handler {
	s := &server{cfg: cfg}
	mux := http.NewServeMux()
	health := s.handleHealth
	if !cfg.HealthPublic {
		health = s.auth(health)
	}
	methods := map[string][]string{} // ścieżka -> dozwolone metody (kolejność rejestracji)
	var paths []string
	route := func(method, path string, h http.HandlerFunc) {
		mux.HandleFunc(method+" "+path, h)
		if _, ok := methods[path]; !ok {
			paths = append(paths, path)
		}
		methods[path] = append(methods[path], method)
	}
	route("GET", "/api/health", health)
	route("GET", "/api/systemd/units", s.auth(s.handleUnits))
	route("PUT", "/api/systemd/units/selection", s.auth(s.handleSelection))
	route("GET", "/api/audit/sessions", s.auth(s.handleSessions))
	route("GET", "/api/audit/commands", s.auth(s.handleCommands))
	route("GET", "/api/audit/login-failures", s.auth(s.handleLoginFailures))
	route("GET", "/api/settings/alerts", s.auth(s.handleGetAlertSettings))
	route("PUT", "/api/settings/alerts", s.auth(s.handlePutAlertSettings))
	route("GET", "/api/settings/connectors", s.auth(s.handleListConnectors))
	route("PUT", "/api/settings/connectors/{type}", s.auth(s.handlePutConnector))
	route("POST", "/api/settings/connectors/{type}/test", s.auth(s.handleTestConnector))
	route("GET", "/api/alerts/history", s.auth(s.handleAlertHistory))
	// inne metody na znanej ścieżce -> 405 z nagłówkiem Allow (wzorzec z metodą jest bardziej szczegółowy i wygrywa)
	for _, p := range paths {
		allow := strings.Join(methods[p], ", ")
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", allow)
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		})
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	if cfg.Static != nil {
		mux.Handle("/", cfg.Static)
	}
	return s.wrap(mux)
}

func (s *server) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now()
}

func (s *server) logf(f string, a ...any) {
	if s.cfg.Logf != nil {
		s.cfg.Logf(f, a...)
		return
	}
	log.Printf(f, a...)
}

// wrap: nagłówki bezpieczeństwa + odzyskiwanie po panice.
func (s *server) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		defer func() {
			if rec := recover(); rec != nil {
				s.logf("api: panic: %v", rec)
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func equalSecret(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// authorized sprawdza Bearer token lub Basic Auth (stałoczasowo). Bez skonfigurowanych
// danych dostępu nikt nie jest autoryzowany (fail closed).
func (s *server) authorized(r *http.Request) bool {
	if s.cfg.Token != "" {
		if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && equalSecret(tok, s.cfg.Token) {
			return true
		}
	}
	if s.cfg.BasicUser != "" {
		if u, p, ok := r.BasicAuth(); ok {
			userOK := equalSecret(u, s.cfg.BasicUser)
			passOK := equalSecret(p, s.cfg.BasicPass)
			return userOK && passOK
		}
	}
	return false
}

func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			if s.cfg.BasicUser != "" {
				w.Header().Set("WWW-Authenticate", `Basic realm="server-monitor", charset="UTF-8"`)
			} else {
				w.Header().Set("WWW-Authenticate", "Bearer")
			}
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}

func (s *server) internal(w http.ResponseWriter, what string, err error) {
	s.logf("api: %s: %v", what, err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

// handleHealth zwraca wyłącznie ostatni stan z health_checks_cache — bez sond sieciowych
// i bez exec podczas żądania.
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	entries, err := s.cfg.Health.All()
	if err != nil {
		s.internal(w, "health cache", err)
		return
	}
	now := s.now()
	stale := s.cfg.StaleAfter
	if stale <= 0 {
		stale = defaultStale
	}
	resp := HealthResponse{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Systemd:     []collector.SystemdUnit{},
		Supervisor:  []parser.SupervisorGroup{},
	}
	statuses := make([]string, 0, len(entries))
	decode := func(e storage.HealthEntry, dst any) {
		if err := json.Unmarshal([]byte(e.PayloadJSON), dst); err != nil {
			s.logf("api: cache payload %q: %v", e.ServiceKey, err)
			statuses = append(statuses, "CRIT")
		}
	}
	for _, e := range entries {
		statuses = append(statuses, e.Status)
		if now.Sub(e.CheckedAt) > stale {
			statuses = append(statuses, "CRIT") // scheduler nie odświeża danych
		}
		switch e.ServiceKey {
		case "systemd":
			var p collector.SystemdPayload
			decode(e, &p)
			if p.Units != nil {
				resp.Systemd = p.Units
			}
		case "supervisor":
			var p collector.SupervisorPayload
			decode(e, &p)
			if p.Groups != nil {
				resp.Supervisor = p.Groups
			}
		case "elasticsearch":
			var p collector.ElasticsearchPayload
			decode(e, &p)
			resp.Clusters.Elasticsearch = &p
		case "redis":
			var p collector.RedisPayload
			decode(e, &p)
			resp.Clusters.Redis = &p
		case "kibana":
			var p collector.KibanaPayload
			decode(e, &p)
			resp.Clusters.Kibana = &p
		}
	}
	if len(entries) == 0 {
		resp.OverallStatus = "WARN" // brak danych (np. tuż po starcie)
	} else {
		resp.OverallStatus = collector.Worst(statuses...)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleUnits(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), collector.CommandTimeout+time.Second)
	defer cancel()
	units, err := s.cfg.Units.ListUnits(ctx)
	if err != nil {
		s.internal(w, "list units", err)
		return
	}
	sel, err := s.cfg.Services.ListEnabled()
	if err != nil {
		s.internal(w, "list selection", err)
		return
	}
	selected := make(map[string]bool, len(sel))
	for _, m := range sel {
		selected[m.UnitName] = true
	}
	out := UnitsResponse{Units: make([]UnitDTO, 0, len(units))}
	for _, u := range units {
		out.Units = append(out.Units, UnitDTO{Unit: u.Name, Description: u.Description, Load: u.Load,
			Active: u.Active, Sub: u.Sub, Selected: selected[u.Name]})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleSelection(w http.ResponseWriter, r *http.Request) {
	// Wymóg application/json wymusza preflight CORS, co chroni przed CSRF
	// przy uwierzytelnianiu Basic zapamiętanym przez przeglądarkę.
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req SelectionRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Units == nil {
		writeError(w, http.StatusBadRequest, `"units" is required`)
		return
	}
	if len(req.Units) > maxSelection {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("too many units (max %d)", maxSelection))
		return
	}
	clean := make([]string, 0, len(req.Units))
	seen := map[string]bool{}
	for _, u := range req.Units {
		if !collector.ValidUnitName(u) {
			writeError(w, http.StatusBadRequest, "invalid unit name")
			return
		}
		if !seen[u] {
			seen[u] = true
			clean = append(clean, u)
		}
	}
	if err := s.cfg.Services.SetSelection(clean); err != nil {
		s.internal(w, "set selection", err)
		return
	}
	if s.cfg.OnSelectionChanged != nil {
		go s.cfg.OnSelectionChanged()
	}
	writeJSON(w, http.StatusOK, SelectionRequest{Units: clean})
}

func pageParams(q url.Values, defLimit int) (limit, offset int, err error) {
	limit = defLimit
	if v := q.Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil || limit < 1 || limit > maxPageLimit {
			return 0, 0, fmt.Errorf("limit must be between 1 and %d", maxPageLimit)
		}
	}
	if v := q.Get("offset"); v != "" {
		offset, err = strconv.Atoi(v)
		if err != nil || offset < 0 || offset > maxPageOffset {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
	}
	return limit, offset, nil
}

func filterParam(q url.Values, name string) (string, error) {
	v := q.Get(name)
	if len(v) > maxFilterLen {
		return "", fmt.Errorf("%s is too long", name)
	}
	return v, nil
}

func ts(t interface{ UTC() time.Time }) string { return t.UTC().Format(time.RFC3339) }

func (s *server) handleSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset, err := pageParams(q, 50)
	if err == nil {
		var user string
		if user, err = filterParam(q, "user"); err == nil {
			var rows []storage.SSHSession
			if rows, err = s.cfg.Sessions.List(user, limit, offset); err != nil {
				s.internal(w, "list sessions", err)
				return
			}
			out := SessionsResponse{Sessions: make([]SessionDTO, 0, len(rows)), Limit: limit, Offset: offset}
			for _, x := range rows {
				d := SessionDTO{ID: x.ID, Username: x.Username, RemoteIP: x.RemoteIP, TTY: x.TTY,
					PAMService: x.PAMService, LoginAt: ts(x.LoginAt)}
				if x.LogoutAt != nil {
					v := ts(*x.LogoutAt)
					d.LogoutAt = &v
				}
				out.Sessions = append(out.Sessions, d)
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

func (s *server) handleLoginFailures(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset, err := pageParams(q, 100)
	var user, ip string
	if err == nil {
		user, err = filterParam(q, "user")
	}
	if err == nil {
		ip, err = filterParam(q, "ip")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var rows []storage.LoginFailure
	if s.cfg.LoginFailures != nil {
		if rows, err = s.cfg.LoginFailures.List(user, ip, limit, offset); err != nil {
			s.internal(w, "list login failures", err)
			return
		}
	}
	out := LoginFailuresResponse{Failures: make([]LoginFailureDTO, 0, len(rows)), Limit: limit, Offset: offset}
	for _, f := range rows {
		out.Failures = append(out.Failures, LoginFailureDTO{ID: f.ID, Username: f.Username, RemoteIP: f.RemoteIP,
			Service: f.Service, AttemptedAt: ts(f.AttemptedAt)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleCommands(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset, err := pageParams(q, 100)
	var user, phrase string
	if err == nil {
		user, err = filterParam(q, "user")
	}
	if err == nil {
		phrase, err = filterParam(q, "q")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.cfg.Commands.List(user, phrase, limit, offset)
	if err != nil {
		s.internal(w, "list commands", err)
		return
	}
	out := CommandsResponse{Commands: make([]CommandDTO, 0, len(rows)), Limit: limit, Offset: offset}
	for _, c := range rows {
		out.Commands = append(out.Commands, CommandDTO{ID: c.ID, AUID: c.AUID, LoginUsername: c.LoginUsername,
			EffectiveUsername: c.EffectiveUsername, CWD: c.CWD, Command: c.Command, ExitCode: c.ExitCode,
			ExecutedAt: ts(c.ExecutedAt)})
	}
	writeJSON(w, http.StatusOK, out)
}
