package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"server-monitor/internal/collector"
	"server-monitor/internal/parser"
	"server-monitor/internal/storage"
)

var fixedNow = time.Date(2026, 11, 15, 10, 30, 0, 0, time.UTC)

type env struct {
	db      *storage.DB
	h       http.Handler
	units   *fakeUnits
	changed atomic.Int32
}

type fakeUnits struct {
	list  []parser.Unit
	err   error
	calls atomic.Int32
}

func (f *fakeUnits) ListUnits(context.Context) ([]parser.Unit, error) {
	f.calls.Add(1)
	return f.list, f.err
}

func newEnv(t *testing.T, mod func(*Config)) *env {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{db: db, units: &fakeUnits{list: []parser.Unit{
		{Name: "nginx.service", Load: "loaded", Active: "active", Sub: "running", Description: "web"},
		{Name: "redis.service", Load: "loaded", Active: "inactive", Sub: "dead", Description: "kv"},
	}}}
	cfg := Config{Health: db.Health(), Services: db.Services(), Sessions: db.Sessions(), Commands: db.Commands(),
		Units: e.units, Token: "tok", BasicUser: "admin", BasicPass: "pw", Now: func() time.Time { return fixedNow },
		OnSelectionChanged: func() { e.changed.Add(1) }, Logf: func(string, ...any) {}}
	if mod != nil {
		mod(&cfg)
	}
	e.h = New(cfg)
	return e
}

func (e *env) do(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	r := httptest.NewRequest(method, path, rd)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

var bearer = map[string]string{"Authorization": "Bearer tok"}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return v
}

func seedHealth(t *testing.T, db *storage.DB, at time.Time) {
	t.Helper()
	mem := int64(18432000)
	sd, _ := json.Marshal(collector.SystemdPayload{Units: []collector.SystemdUnit{{Unit: "nginx.service", DisplayName: "Nginx",
		Active: "active", Sub: "running", Status: "OK", MemoryBytes: &mem, Uptime: "12d 4h"}}})
	sv, _ := json.Marshal(collector.SupervisorPayload{Groups: []parser.SupervisorGroup{{Group: "messenger_async", Running: 4, Total: 4,
		Status: "OK", Workers: []parser.SupervisorWorker{}}}})
	es, _ := json.Marshal(collector.ElasticsearchPayload{Status: "OK", ClusterName: "prod-es", ClusterStatus: "green", Nodes: 3})
	rd, _ := json.Marshal(collector.RedisPayload{Status: "OK", PingMS: 1.2, UsedMemoryHuman: "142.5M", ConnectedClients: 28, UptimeSeconds: 5})
	kb, _ := json.Marshal(collector.KibanaPayload{Status: "OK", OverallLevel: "available", ResponseMS: 42})
	for k, p := range map[string][]byte{"systemd": sd, "supervisor": sv, "elasticsearch": es, "redis": rd, "kibana": kb} {
		if err := db.Health().Upsert(storage.HealthEntry{ServiceKey: k, Status: "OK", PayloadJSON: string(p), CheckedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAuthRequired(t *testing.T) {
	e := newEnv(t, nil)
	for _, c := range [][2]string{{"GET", "/api/health"}, {"GET", "/api/systemd/units"}, {"PUT", "/api/systemd/units/selection"},
		{"GET", "/api/audit/sessions"}, {"GET", "/api/audit/commands"}} {
		if w := e.do(c[0], c[1], "", nil); w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%v without creds: %d", c, w.Code)
		}
		if w := e.do(c[0], c[1], "", map[string]string{"Authorization": "Bearer wrong"}); w.Code != 401 {
			t.Errorf("%v wrong token: %d", c, w.Code)
		}
	}
	if w := e.do("GET", "/api/audit/sessions", "", bearer); w.Code != 200 {
		t.Fatalf("bearer: %d", w.Code)
	}
	basic := httptest.NewRequest("GET", "/api/audit/sessions", nil)
	basic.SetBasicAuth("admin", "pw")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, basic)
	if w.Code != 200 {
		t.Fatalf("basic: %d", w.Code)
	}
	for _, bad := range [][2]string{{"admin", "nope"}, {"root", "pw"}, {"", ""}} {
		r := httptest.NewRequest("GET", "/api/audit/sessions", nil)
		r.SetBasicAuth(bad[0], bad[1])
		w := httptest.NewRecorder()
		e.h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Errorf("basic %v: %d", bad, w.Code)
		}
	}
}

func TestFailClosedWithoutCredentials(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.Token, c.BasicUser, c.BasicPass = "", "", "" })
	for _, hdr := range []map[string]string{nil, {"Authorization": "Bearer "}, {"Authorization": "Bearer"}} {
		if w := e.do("GET", "/api/audit/sessions", "", hdr); w.Code != 401 {
			t.Fatalf("%v: %d", hdr, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "/api/audit/sessions", nil)
	r.SetBasicAuth("", "")
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("empty basic creds must not pass when none configured")
	}
}

func TestHealthPublic(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.HealthPublic = true })
	if w := e.do("GET", "/api/health", "", nil); w.Code != 200 {
		t.Fatalf("public health: %d", w.Code)
	}
	if w := e.do("GET", "/api/audit/commands", "", nil); w.Code != 401 {
		t.Fatal("audit must stay protected")
	}
}

func TestHealthContractShape(t *testing.T) {
	e := newEnv(t, nil)
	seedHealth(t, e.db, fixedNow.Add(-5*time.Second))
	w := e.do("GET", "/api/health", "", bearer)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	var raw map[string]any
	json.Unmarshal(w.Body.Bytes(), &raw)
	keys := func(m map[string]any) []string {
		var k []string
		for x := range m {
			k = append(k, x)
		}
		return k
	}
	for _, k := range []string{"overall_status", "generated_at", "systemd", "supervisor", "clusters"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing %q in %v", k, keys(raw))
		}
	}
	if len(raw) != 5 {
		t.Errorf("unexpected top-level keys: %v", keys(raw))
	}
	h := decode[HealthResponse](t, w)
	if h.OverallStatus != "OK" || h.GeneratedAt != "2026-11-15T10:30:00Z" {
		t.Fatalf("%+v", h)
	}
	u := raw["systemd"].([]any)[0].(map[string]any)
	want := []string{"unit", "display_name", "active", "sub", "status", "memory_bytes", "uptime"}
	for _, k := range want {
		if _, ok := u[k]; !ok {
			t.Errorf("systemd unit missing %q", k)
		}
	}
	if len(u) != len(want) {
		t.Errorf("systemd unit keys: %v", keys(u))
	}
	g := raw["supervisor"].([]any)[0].(map[string]any)
	if g["group"] != "messenger_async" || g["running"].(float64) != 4 || g["status"] != "OK" || g["workers"] == nil {
		t.Fatalf("%v", g)
	}
	cl := raw["clusters"].(map[string]any)
	if cl["elasticsearch"].(map[string]any)["cluster_name"] != "prod-es" || cl["redis"].(map[string]any)["ping_ms"].(float64) != 1.2 ||
		cl["kibana"].(map[string]any)["overall_level"] != "available" {
		t.Fatalf("%v", cl)
	}
	if _, has := cl["redis"].(map[string]any)["error"]; has {
		t.Error("error must be omitted when empty")
	}
}

func TestHealthOverallStatusRules(t *testing.T) {
	e := newEnv(t, nil)
	// brak danych -> WARN i puste tablice (nie null)
	h := decode[HealthResponse](t, e.do("GET", "/api/health", "", bearer))
	if h.OverallStatus != "WARN" || h.Systemd == nil || h.Supervisor == nil || len(h.Systemd) != 0 {
		t.Fatalf("%+v", h)
	}
	if !strings.Contains(e.do("GET", "/api/health", "", bearer).Body.String(), `"systemd":[]`) {
		t.Fatal("systemd must serialize as []")
	}
	seedHealth(t, e.db, fixedNow)
	e.db.Health().Upsert(storage.HealthEntry{ServiceKey: "redis", Status: "WARN", PayloadJSON: `{"status":"WARN"}`, CheckedAt: fixedNow})
	if h := decode[HealthResponse](t, e.do("GET", "/api/health", "", bearer)); h.OverallStatus != "WARN" {
		t.Fatalf("worst-of: %s", h.OverallStatus)
	}
	e.db.Health().Upsert(storage.HealthEntry{ServiceKey: "kibana", Status: "CRIT", PayloadJSON: `{"status":"CRIT","error":"x"}`, CheckedAt: fixedNow})
	if h := decode[HealthResponse](t, e.do("GET", "/api/health", "", bearer)); h.OverallStatus != "CRIT" || h.Clusters.Kibana.Error != "x" {
		t.Fatalf("%+v", h)
	}
	// nieświeże dane -> CRIT, nawet gdy wszystkie wpisy są OK
	e2 := newEnv(t, nil)
	seedHealth(t, e2.db, fixedNow.Add(-2*time.Minute))
	if h := decode[HealthResponse](t, e2.do("GET", "/api/health", "", bearer)); h.OverallStatus != "CRIT" {
		t.Fatalf("stale: %s", h.OverallStatus)
	}
	// uszkodzony payload -> CRIT
	e3 := newEnv(t, nil)
	e3.db.Health().Upsert(storage.HealthEntry{ServiceKey: "redis", Status: "OK", PayloadJSON: `not json`, CheckedAt: fixedNow})
	if h := decode[HealthResponse](t, e3.do("GET", "/api/health", "", bearer)); h.OverallStatus != "CRIT" {
		t.Fatalf("bad payload: %s", h.OverallStatus)
	}
}

// Health nie może wołać niczego poza cache (ani exec, ani sond) i ma odpowiadać < 20 ms.
func TestHealthDoesNotTouchSystemAndIsFast(t *testing.T) {
	e := newEnv(t, nil)
	seedHealth(t, e.db, fixedNow)
	best := time.Hour
	for i := 0; i < 20; i++ {
		start := time.Now()
		if w := e.do("GET", "/api/health", "", bearer); w.Code != 200 {
			t.Fatal(w.Code)
		}
		if d := time.Since(start); d < best {
			best = d
		}
	}
	if best > 20*time.Millisecond {
		t.Fatalf("best-of-20 latency %v exceeds 20ms", best)
	}
	if e.units.calls.Load() != 0 {
		t.Fatal("/api/health must not call ListUnits")
	}
}

func TestHealthStoreError(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.Health = errHealth{} })
	w := e.do("GET", "/api/health", "", bearer)
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

type errHealth struct{}

func (errHealth) All() ([]storage.HealthEntry, error) {
	return nil, errors.New("secret db path leaked")
}

func TestUnitsListMergesSelection(t *testing.T) {
	e := newEnv(t, nil)
	e.db.Services().SetSelection([]string{"redis.service"})
	w := e.do("GET", "/api/systemd/units", "", bearer)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	r := decode[UnitsResponse](t, w)
	if len(r.Units) != 2 || r.Units[0].Selected || !r.Units[1].Selected || r.Units[0].Description != "web" || r.Units[0].Sub != "running" {
		t.Fatalf("%+v", r)
	}
	e.units.err = errors.New("systemctl missing")
	if w := e.do("GET", "/api/systemd/units", "", bearer); w.Code != 500 || strings.Contains(w.Body.String(), "systemctl") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestSelectionPut(t *testing.T) {
	e := newEnv(t, nil)
	js := map[string]string{"Authorization": "Bearer tok", "Content-Type": "application/json"}
	w := e.do("PUT", "/api/systemd/units/selection", `{"units":["nginx.service","php8.4-fpm.service","nginx.service"]}`, js)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if got := decode[SelectionRequest](t, w).Units; !reflect.DeepEqual(got, []string{"nginx.service", "php8.4-fpm.service"}) {
		t.Fatalf("%v", got)
	}
	en, _ := e.db.Services().ListEnabled()
	if len(en) != 2 || en[0].UnitName != "nginx.service" {
		t.Fatalf("%+v", en)
	}
	deadline := time.Now().Add(time.Second)
	for e.changed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if e.changed.Load() != 1 {
		t.Fatal("OnSelectionChanged not called")
	}
	// wyczyszczenie wyboru jest dozwolone
	if w := e.do("PUT", "/api/systemd/units/selection", `{"units":[]}`, js); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if en, _ := e.db.Services().ListEnabled(); len(en) != 0 {
		t.Fatal("selection not cleared")
	}
}

func TestSelectionValidation(t *testing.T) {
	e := newEnv(t, nil)
	e.db.Services().SetSelection([]string{"keep.service"})
	js := map[string]string{"Authorization": "Bearer tok", "Content-Type": "application/json"}
	many := make([]string, maxSelection+1)
	for i := range many {
		many[i] = "a.service"
	}
	tooMany, _ := json.Marshal(SelectionRequest{Units: many})
	cases := []struct {
		name, body string
		hdr        map[string]string
		want       int
	}{
		{"missing units", `{}`, js, 400},
		{"null units", `{"units":null}`, js, 400},
		{"bad name", `{"units":["../etc/passwd"]}`, js, 400},
		{"option injection", `{"units":["--now.service"]}`, js, 400},
		{"no suffix", `{"units":["nginx"]}`, js, 400},
		{"unknown field", `{"units":[],"x":1}`, js, 400},
		{"trailing garbage", `{"units":[]} {"units":[]}`, js, 400},
		{"not json", `units`, js, 400},
		{"too many", string(tooMany), js, 400},
		{"wrong content type", `{"units":[]}`, map[string]string{"Authorization": "Bearer tok", "Content-Type": "text/plain"}, 415},
		{"no content type", `{"units":[]}`, bearer, 415},
		{"no auth", `{"units":[]}`, map[string]string{"Content-Type": "application/json"}, 401},
	}
	for _, c := range cases {
		if w := e.do("PUT", "/api/systemd/units/selection", c.body, c.hdr); w.Code != c.want {
			t.Errorf("%s: got %d want %d (%s)", c.name, w.Code, c.want, w.Body.String())
		}
	}
	if en, _ := e.db.Services().ListEnabled(); len(en) != 1 || en[0].UnitName != "keep.service" {
		t.Fatalf("rejected requests must not modify selection: %+v", en)
	}
	big := `{"units":["` + strings.Repeat("a", 300<<10) + `"]}`
	if w := e.do("PUT", "/api/systemd/units/selection", big, js); w.Code != 400 {
		t.Fatalf("oversized body: %d", w.Code)
	}
}

func TestAuditSessions(t *testing.T) {
	e := newEnv(t, nil)
	t0 := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	e.db.Sessions().Open(storage.SSHSession{Username: "anna", RemoteIP: "10.0.0.1", TTY: "ssh", PAMService: "sshd", LoginAt: t0})
	e.db.Sessions().Open(storage.SSHSession{Username: "bob", RemoteIP: "10.0.0.2", TTY: "ssh", PAMService: "sshd", LoginAt: t0.Add(time.Hour)})
	e.db.Sessions().Close("anna", "ssh", t0.Add(30*time.Minute))

	r := decode[SessionsResponse](t, e.do("GET", "/api/audit/sessions", "", bearer))
	if len(r.Sessions) != 2 || r.Limit != 50 || r.Offset != 0 || r.Sessions[0].Username != "bob" || r.Sessions[0].LogoutAt != nil {
		t.Fatalf("%+v", r)
	}
	if r.Sessions[1].LogoutAt == nil || *r.Sessions[1].LogoutAt != "2026-11-15T09:30:00Z" || r.Sessions[1].LoginAt != "2026-11-15T09:00:00Z" {
		t.Fatalf("%+v", r.Sessions[1])
	}
	if !strings.Contains(e.do("GET", "/api/audit/sessions", "", bearer).Body.String(), `"logout_at":null`) {
		t.Fatal("logout_at must be null when open")
	}
	r = decode[SessionsResponse](t, e.do("GET", "/api/audit/sessions?user=anna&limit=1&offset=0", "", bearer))
	if len(r.Sessions) != 1 || r.Sessions[0].Username != "anna" || r.Limit != 1 {
		t.Fatalf("%+v", r)
	}
	if r = decode[SessionsResponse](t, e.do("GET", "/api/audit/sessions?user=nobody", "", bearer)); r.Sessions == nil || len(r.Sessions) != 0 {
		t.Fatalf("empty must be [] not null: %+v", r)
	}
}

func TestAuditCommands(t *testing.T) {
	e := newEnv(t, nil)
	t0 := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	zero := 0
	e.db.Commands().Insert(storage.CLICommand{AuditEventID: "1:1", AUID: 1000, LoginUsername: "anna", EffectiveUsername: "root", CWD: "/root",
		Command: "systemctl restart nginx", ExitCode: &zero, ExecutedAt: t0})
	e.db.Commands().Insert(storage.CLICommand{AuditEventID: "2:2", AUID: 1001, LoginUsername: "bob", EffectiveUsername: "bob", CWD: "/",
		Command: "ls", ExecutedAt: t0.Add(time.Minute)})
	r := decode[CommandsResponse](t, e.do("GET", "/api/audit/commands", "", bearer))
	if len(r.Commands) != 2 || r.Limit != 100 || r.Commands[0].Command != "ls" || r.Commands[0].ExitCode != nil {
		t.Fatalf("%+v", r)
	}
	c := r.Commands[1]
	if c.LoginUsername != "anna" || c.EffectiveUsername != "root" || c.CWD != "/root" || c.AUID != 1000 || c.ExitCode == nil ||
		*c.ExitCode != 0 || c.ExecutedAt != "2026-11-15T09:00:00Z" {
		t.Fatalf("%+v", c)
	}
	if r = decode[CommandsResponse](t, e.do("GET", "/api/audit/commands?q=nginx", "", bearer)); len(r.Commands) != 1 {
		t.Fatalf("%+v", r)
	}
	if r = decode[CommandsResponse](t, e.do("GET", "/api/audit/commands?user=bob&q=l", "", bearer)); len(r.Commands) != 1 || r.Commands[0].Command != "ls" {
		t.Fatalf("%+v", r)
	}
	if r = decode[CommandsResponse](t, e.do("GET", "/api/audit/commands?limit=1&offset=1", "", bearer)); len(r.Commands) != 1 || r.Commands[0].LoginUsername != "anna" {
		t.Fatalf("%+v", r)
	}
	if w := e.do("GET", "/api/audit/commands?q='%3B+DROP+TABLE+cli_command_logs%3B--", "", bearer); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if r = decode[CommandsResponse](t, e.do("GET", "/api/audit/commands", "", bearer)); len(r.Commands) != 2 {
		t.Fatal("table damaged by injection attempt")
	}
}

func TestPagingValidation(t *testing.T) {
	e := newEnv(t, nil)
	for _, p := range []string{"limit=0", "limit=-1", "limit=abc", "limit=501", "offset=-1", "offset=x", "offset=2000000",
		"user=" + strings.Repeat("x", 201), "q=" + strings.Repeat("x", 201)} {
		for _, ep := range []string{"/api/audit/sessions?", "/api/audit/commands?"} {
			if strings.HasPrefix(p, "q=") && strings.Contains(ep, "sessions") {
				continue
			}
			if w := e.do("GET", ep+p, "", bearer); w.Code != 400 {
				t.Errorf("%s%s: %d", ep, p, w.Code)
			}
		}
	}
	if w := e.do("GET", "/api/audit/commands?limit=500&offset=0", "", bearer); w.Code != 200 {
		t.Fatal("limit=500 must be accepted")
	}
}

func TestRoutingAndStatic(t *testing.T) {
	static := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("SPA " + r.URL.Path)) })
	e := newEnv(t, func(c *Config) { c.Static = static })
	if w := e.do("GET", "/health", "", nil); w.Code != 200 || w.Body.String() != "SPA /health" {
		t.Fatalf("static: %d %q", w.Code, w.Body.String())
	}
	if w := e.do("GET", "/api/nope", "", bearer); w.Code != 404 || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("api 404: %d %q", w.Code, w.Body.String())
	}
	if w := e.do("POST", "/api/health", "", bearer); w.Code != 405 {
		t.Fatalf("method: %d", w.Code)
	}
	if w := e.do("DELETE", "/api/systemd/units/selection", "", bearer); w.Code != 405 {
		t.Fatalf("method: %d", w.Code)
	}
	if w := e.do("GET", "/health", "", nil); w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers")
	}
	// bez Static korzeń to 404
	e2 := newEnv(t, nil)
	if w := e2.do("GET", "/", "", nil); w.Code != 404 {
		t.Fatalf("%d", w.Code)
	}
}

func TestPanicRecovered(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.Health = panicHealth{} })
	if w := e.do("GET", "/api/health", "", bearer); w.Code != 500 || !bytes.Contains(w.Body.Bytes(), []byte("internal error")) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

type panicHealth struct{}

func (panicHealth) All() ([]storage.HealthEntry, error) { panic("boom") }
