package api

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func labelsEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, nil)
	e.h = New(Config{Health: e.db.Health(), Services: e.db.Services(), Sessions: e.db.Sessions(), Commands: e.db.Commands(),
		IPLabels: e.db.IPLabels(), Units: e.units, Token: "tok", Now: func() time.Time { return fixedNow }, Logf: func(string, ...any) {}})
	return e
}

func putLabel(e *env, ip, body string) int {
	return e.do("PUT", "/api/ip-labels/"+url.PathEscape(ip), body, bearerJSON).Code
}

func TestIPLabelsRequireAuthMethodAndStore(t *testing.T) {
	e := labelsEnv(t)
	for _, c := range [][2]string{{"GET", "/api/ip-labels"}, {"PUT", "/api/ip-labels/192.0.2.10"}, {"DELETE", "/api/ip-labels/192.0.2.10"}} {
		if w := e.do(c[0], c[1], `{"label":"x"}`, map[string]string{"Content-Type": jsonCT}); w.Code != 401 {
			t.Errorf("%v without creds: %d", c, w.Code)
		}
	}
	if w := e.do("POST", "/api/ip-labels", "", bearer); w.Code != 405 {
		t.Errorf("POST list: %d", w.Code)
	}
	if w := e.do("GET", "/api/ip-labels/192.0.2.10", "", bearer); w.Code != 405 || !strings.Contains(w.Header().Get("Allow"), "PUT") {
		t.Errorf("GET item: %d allow=%q", w.Code, w.Header().Get("Allow"))
	}
	none := newEnv(t, nil) // bez IPLabels w Config
	if w := none.do("GET", "/api/ip-labels", "", bearer); w.Code != 503 {
		t.Errorf("no store: %d", w.Code)
	}
}

func TestIPLabelsCRUD(t *testing.T) {
	e := labelsEnv(t)
	if w := e.do("GET", "/api/ip-labels", "", bearer); w.Code != 200 || !strings.Contains(w.Body.String(), `"labels":[]`) {
		t.Fatalf("empty must be [] not null: %d %s", w.Code, w.Body.String())
	}
	if c := putLabel(e, "192.0.2.10", `{"label":"  pnl  "}`); c != 200 {
		t.Fatalf("put: %d", c)
	}
	// IPv4-mapped IPv6 i IPv6 w różnych zapisach trafiają do jednego klucza
	if c := putLabel(e, "::ffff:192.0.2.10", `{"label":"panel"}`); c != 200 {
		t.Fatalf("put mapped: %d", c)
	}
	if c := putLabel(e, "2001:DB8::1", `{"label":"mój komputer"}`); c != 200 {
		t.Fatalf("put v6: %d", c)
	}
	got := decode[IPLabelsResponse](t, e.do("GET", "/api/ip-labels", "", bearer))
	if len(got.Labels) != 2 || got.Labels[0] != (IPLabelDTO{IP: "192.0.2.10", Label: "panel"}) ||
		got.Labels[1] != (IPLabelDTO{IP: "2001:db8::1", Label: "mój komputer"}) {
		t.Fatalf("labels: %+v", got.Labels)
	}
	if w := e.do("DELETE", "/api/ip-labels/"+url.PathEscape("2001:db8::1"), "", bearer); w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("delete: %d %q", w.Code, w.Body.String())
	}
	if w := e.do("DELETE", "/api/ip-labels/203.0.113.1", "", bearer); w.Code != 204 { // idempotentne
		t.Fatalf("delete missing: %d", w.Code)
	}
	if got := decode[IPLabelsResponse](t, e.do("GET", "/api/ip-labels", "", bearer)); len(got.Labels) != 1 {
		t.Fatalf("after delete: %+v", got.Labels)
	}
}

func TestIPLabelsValidation(t *testing.T) {
	e := labelsEnv(t)
	for _, ip := range []string{"not-an-ip", "192.0.2.0%2F24", "fe80::1%25eth0", "999.1.1.1"} {
		if c := putLabel(e, ip, `{"label":"x"}`); c != 400 {
			t.Errorf("bad ip %q: %d", ip, c)
		}
		if w := e.do("DELETE", "/api/ip-labels/"+ip, "", bearer); w.Code != 400 {
			t.Errorf("delete bad ip %q: %d", ip, w.Code)
		}
	}
	for name, body := range map[string]string{
		"empty":     `{"label":"  "}`,
		"too long":  `{"label":"` + strings.Repeat("a", 65) + `"}`,
		"control":   `{"label":"a\nBcc: evil@example.org"}`,
		"unknown":   `{"label":"x","extra":1}`,
		"not json":  `label=x`,
		"no label":  `{}`,
		"wrong typ": `{"label":5}`,
	} {
		if c := putLabel(e, "192.0.2.10", body); c != 400 {
			t.Errorf("%s: %d", name, c)
		}
	}
	if w := e.do("PUT", "/api/ip-labels/192.0.2.10", `{"label":"x"}`, bearer); w.Code != 415 { // CSRF: wymaga application/json
		t.Errorf("no content type: %d", w.Code)
	}
}

func TestIPLabelsLimit(t *testing.T) {
	e := labelsEnv(t)
	for i := 0; i < maxIPLabels; i++ {
		if err := e.db.IPLabels().Set("10.0."+strconv.Itoa(i/256)+"."+strconv.Itoa(i%256), "x", fixedNow); err != nil {
			t.Fatal(err)
		}
	}
	if c := putLabel(e, "192.0.2.10", `{"label":"new"}`); c != 400 {
		t.Errorf("over the limit: %d", c)
	}
	if c := putLabel(e, "10.0.0.0", `{"label":"renamed"}`); c != 200 { // zmiana istniejącej nadal działa
		t.Errorf("update at the limit: %d", c)
	}
}
