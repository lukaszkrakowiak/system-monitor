package alerts

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"server-monitor/internal/collector"
	"server-monitor/internal/parser"
	"server-monitor/internal/storage"
)

var base = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func it(status string, n int) Item {
	return Item{Key: "systemd:nginx.service", Name: "nginx.service", Status: status, Checked: base.Add(time.Duration(n) * 12 * time.Second)}
}

func TestObserveConfirmsOnlyAfterEnoughDistinctChecks(t *testing.T) {
	var st itemState
	var tr *Transition

	st, tr = observe(st, it("OK", 0), 2)
	if tr != nil || st.Status != "OK" {
		t.Fatalf("first OK must be silent: %+v %v", st, tr)
	}
	st, tr = observe(st, it("CRIT", 1), 2)
	if tr != nil || st.Status != "OK" || st.PendingCount != 1 {
		t.Fatalf("one failed check must not alert: %+v %v", st, tr)
	}
	// ta sama obserwacja z cache (kolejny tick silnika) nie liczy się drugi raz
	st, tr = observe(st, it("CRIT", 1), 2)
	if tr != nil || st.PendingCount != 1 {
		t.Fatalf("same checked_at must be ignored: %+v %v", st, tr)
	}
	st, tr = observe(st, it("CRIT", 2), 2)
	if tr == nil || tr.Previous != "OK" || tr.Item.Status != "CRIT" || !tr.Since.Equal(it("CRIT", 1).Checked) {
		t.Fatalf("second distinct check must confirm: %+v %v", st, tr)
	}
	if st.Status != "CRIT" || st.Pending != "" {
		t.Fatalf("state after transition: %+v", st)
	}
	// dalej CRIT: bez nowych alertów
	for i := 3; i < 6; i++ {
		if st, tr = observe(st, it("CRIT", i), 2); tr != nil {
			t.Fatalf("repeated CRIT must not alert again (i=%d)", i)
		}
	}
	// zmiana CRIT -> WARN to osobne przejście
	st, _ = observe(st, it("WARN", 6), 2)
	st, tr = observe(st, it("WARN", 7), 2)
	if tr == nil || tr.Previous != "CRIT" || tr.Item.Status != "WARN" {
		t.Fatalf("CRIT->WARN: %+v", tr)
	}
	// powrót do OK
	st, _ = observe(st, it("OK", 8), 2)
	_, tr = observe(st, it("OK", 9), 2)
	if tr == nil || tr.Previous != "WARN" || tr.Item.Status != "OK" {
		t.Fatalf("recovery: %+v", tr)
	}
}

func TestObserveFlapIsAbsorbed(t *testing.T) {
	var st itemState
	st, _ = observe(st, it("OK", 0), 3)
	var tr *Transition
	for i, s := range []string{"CRIT", "OK", "CRIT", "OK", "CRIT", "OK"} { // nigdy 3 pod rząd
		if st, tr = observe(st, it(s, i+1), 3); tr != nil {
			t.Fatalf("flapping must not alert (step %d)", i)
		}
	}
	if st.Status != "OK" {
		t.Fatalf("%+v", st)
	}
}

func TestObserveFirstSightingNotOKIsAlsoConfirmed(t *testing.T) {
	var st itemState
	var tr *Transition
	st, tr = observe(st, it("CRIT", 0), 2)
	if tr != nil {
		t.Fatal("needs confirmation")
	}
	st, tr = observe(st, it("CRIT", 0), 2) // ten sam pomiar — nadal bez potwierdzenia
	if tr != nil {
		t.Fatal("same observation counted twice")
	}
	_, tr = observe(st, it("CRIT", 1), 2)
	if tr == nil || tr.Previous != "" || tr.Item.Status != "CRIT" {
		t.Fatalf("%+v", tr)
	}
	// confirm=1: od razu
	if _, tr = observe(itemState{}, it("WARN", 0), 1); tr == nil {
		t.Fatal("confirm=1 must alert immediately")
	}
}

func entry(t *testing.T, key, status string, payload any, checked time.Time) storage.HealthEntry {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return storage.HealthEntry{ServiceKey: key, Status: status, PayloadJSON: string(b), CheckedAt: checked}
}

func TestFlattenAllSources(t *testing.T) {
	entries := []storage.HealthEntry{
		entry(t, "systemd", "CRIT", collector.SystemdPayload{Units: []collector.SystemdUnit{
			{Unit: "nginx.service", Active: "active", Sub: "running", Status: "OK"},
			{Unit: "php.service", Active: "failed", Sub: "failed", Status: "CRIT"},
		}}, base),
		entry(t, "supervisor", "WARN", collector.SupervisorPayload{Groups: []parser.SupervisorGroup{
			{Group: "mailer", Running: 1, Total: 2, Status: "DEGRADED", Workers: []parser.SupervisorWorker{
				{Name: "mailer_00", State: "RUNNING"}, {Name: "mailer_01", State: "FATAL"}}},
			{Group: "queue", Running: 0, Total: 1, Status: "DOWN"},
			{Group: "ok", Running: 1, Total: 1, Status: "OK"},
		}}, base),
		entry(t, "redis", "CRIT", collector.RedisPayload{Status: "CRIT", Error: "dial tcp: connection refused"}, base),
		entry(t, "elasticsearch", "WARN", collector.ElasticsearchPayload{Status: "WARN", ClusterName: "prod", ClusterStatus: "yellow", Nodes: 1, UnassignedShards: 2}, base),
		entry(t, "kibana", "OK", collector.KibanaPayload{Status: "OK", OverallLevel: "available", ResponseMS: 6}, base),
	}
	got := map[string]Item{}
	for _, i := range Flatten(entries) {
		got[i.Key] = i
	}
	check := func(key, status string, detail ...string) {
		t.Helper()
		i, ok := got[key]
		if !ok || i.Status != status {
			t.Errorf("%s: %+v (want %s)", key, i, status)
			return
		}
		for _, d := range detail {
			if !strings.Contains(i.Detail, d) {
				t.Errorf("%s detail %q lacks %q", key, i.Detail, d)
			}
		}
	}
	check("systemd:nginx.service", "OK")
	check("systemd:php.service", "CRIT", "active=failed", "sub=failed")
	check("supervisor:mailer", "WARN", "1/2", "mailer_01 (FATAL)")
	check("supervisor:queue", "CRIT", "0/1")
	check("supervisor:ok", "OK")
	check("redis", "CRIT", "connection refused")
	check("elasticsearch", "WARN", "yellow", "2 unassigned")
	check("kibana", "OK", "available")
	if len(got) != 8 {
		t.Errorf("%d items: %v", len(got), got)
	}
}

func TestFlattenCollectorErrorsAndBadPayloads(t *testing.T) {
	items := Flatten([]storage.HealthEntry{
		entry(t, "supervisor", "CRIT", collector.SupervisorPayload{Groups: []parser.SupervisorGroup{}, Error: "dial unix /var/run/supervisor.sock: permission denied"}, base),
		entry(t, "systemd", "CRIT", collector.SystemdPayload{Units: []collector.SystemdUnit{}, Error: "systemctl failed"}, base),
		{ServiceKey: "redis", Status: "OK", PayloadJSON: "{not json", CheckedAt: base},
		{ServiceKey: "custom", Status: "WARN", PayloadJSON: "{}", CheckedAt: base},
		{ServiceKey: "weird", Status: "BOGUS", PayloadJSON: "{}", CheckedAt: base},
	})
	got := map[string]Item{}
	for _, i := range items {
		got[i.Key] = i
	}
	if i := got["supervisor"]; i.Status != "CRIT" || !strings.Contains(i.Detail, "permission denied") {
		t.Errorf("supervisor collector error: %+v", i)
	}
	if i := got["systemd"]; i.Status != "CRIT" || !strings.Contains(i.Detail, "systemctl failed") {
		t.Errorf("systemd collector error: %+v", i)
	}
	if i := got["redis"]; i.Status != "CRIT" || !strings.Contains(i.Detail, "unreadable") {
		t.Errorf("bad payload must be CRIT, not silent: %+v", i)
	}
	if got["custom"].Status != "WARN" || got["weird"].Status != "CRIT" {
		t.Errorf("custom/unknown: %+v %+v", got["custom"], got["weird"])
	}
}
