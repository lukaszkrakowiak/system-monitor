package api

import (
	"testing"
	"time"

	"server-monitor/internal/storage"
)

func TestLoginFailuresEndpoint(t *testing.T) {
	e := newEnv(t, nil)
	// newEnv nie zna LoginFailures (potrzebuje db), więc składamy handler jeszcze raz na tej samej bazie.
	e.h = New(Config{Health: e.db.Health(), Services: e.db.Services(), Sessions: e.db.Sessions(), Commands: e.db.Commands(),
		LoginFailures: e.db.LoginFailures(), Units: e.units, Token: "tok", Now: func() time.Time { return fixedNow },
		Logf: func(string, ...any) {}})

	if w := e.do("GET", "/api/audit/login-failures", "", nil); w.Code != 401 {
		t.Fatalf("no creds: %d", w.Code)
	}
	if w := e.do("POST", "/api/audit/login-failures", "", bearer); w.Code != 405 {
		t.Fatalf("POST: %d", w.Code)
	}
	if w := e.do("GET", "/api/audit/login-failures", "", bearer); w.Code != 200 {
		t.Fatalf("empty: %d", w.Code)
	} else if r := decode[LoginFailuresResponse](t, w); r.Failures == nil || len(r.Failures) != 0 || r.Limit != 100 {
		t.Fatalf("empty list must be [] (not null): %s", w.Body.String())
	}

	base := time.Date(2026, 11, 16, 7, 36, 54, 0, time.UTC)
	for i, f := range []storage.LoginFailure{
		{AuditEventID: "1.1:1", Username: "alice", RemoteIP: "192.0.2.10", Service: "sshd", AttemptedAt: base},
		{AuditEventID: "1.2:2", Username: "admin", RemoteIP: "203.0.113.7", Service: "sshd", AttemptedAt: base.Add(time.Second)},
		{AuditEventID: "1.3:3", Username: "root", RemoteIP: "203.0.113.7", Service: "sshd", AttemptedAt: base.Add(2 * time.Second)},
	} {
		if _, err := e.db.LoginFailures().Insert(f); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	r := decode[LoginFailuresResponse](t, e.do("GET", "/api/audit/login-failures", "", bearer))
	if len(r.Failures) != 3 || r.Failures[0].Username != "root" || r.Failures[0].RemoteIP != "203.0.113.7" ||
		r.Failures[0].Service != "sshd" || r.Failures[0].AttemptedAt != "2026-11-16T07:36:56Z" {
		t.Fatalf("%+v", r)
	}
	if r := decode[LoginFailuresResponse](t, e.do("GET", "/api/audit/login-failures?ip=203.0.113.7", "", bearer)); len(r.Failures) != 2 {
		t.Fatalf("ip filter: %+v", r)
	}
	if r := decode[LoginFailuresResponse](t, e.do("GET", "/api/audit/login-failures?user=alice&limit=1&offset=0", "", bearer)); len(r.Failures) != 1 || r.Limit != 1 {
		t.Fatalf("user filter: %+v", r)
	}
	for _, bad := range []string{"?limit=0", "?limit=501", "?offset=-1", "?limit=x"} {
		if w := e.do("GET", "/api/audit/login-failures"+bad, "", bearer); w.Code != 400 {
			t.Errorf("%s: %d", bad, w.Code)
		}
	}
}

// Bez skonfigurowanego store endpoint odpowiada pustą listą (nie panikuje).
func TestLoginFailuresNilStore(t *testing.T) {
	e := newEnv(t, nil)
	w := e.do("GET", "/api/audit/login-failures", "", bearer)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if r := decode[LoginFailuresResponse](t, w); r.Failures == nil || len(r.Failures) != 0 {
		t.Fatalf("%s", w.Body.String())
	}
}
