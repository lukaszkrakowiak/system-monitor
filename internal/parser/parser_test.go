package parser

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fx(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseListUnits(t *testing.T) {
	u, err := ParseListUnits(fx(t, "systemctl_list_units.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(u) != 6 || u[0].Name != "nginx.service" || u[0].Active != "active" || u[0].Sub != "running" || u[5].Load != "not-found" {
		t.Fatalf("%+v", u)
	}
	if _, err := ParseListUnits([]byte("not json")); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseShow(t *testing.T) {
	u, err := ParseShow(fx(t, "systemctl_show.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(u) != 4 {
		t.Fatalf("len=%d", len(u))
	}
	if u[0].ID != "nginx.service" || u[0].MainPID != 1234 || u[0].MemoryCurrent == nil || *u[0].MemoryCurrent != 18432000 {
		t.Fatalf("%+v", u[0])
	}
	if u[1].MemoryCurrent != nil || u[2].MemoryCurrent != nil {
		t.Fatal("[not set] and max uint64 must be nil")
	}
	if u[2].ExecMainStart != "" || u[2].ActiveState != "activating" {
		t.Fatalf("%+v", u[2])
	}
	if _, err := ParseShow([]byte("ActiveState=active\n")); err == nil {
		t.Fatal("block without Id must fail")
	}
	if _, err := ParseShow([]byte("Id=a.service\nMainPID=x\n")); err == nil {
		t.Fatal("bad MainPID must fail")
	}
	if u, err := ParseShow(nil); err != nil || len(u) != 0 {
		t.Fatal("empty input")
	}
}

func TestSystemdStatusAndTime(t *testing.T) {
	cases := []struct{ a, s, want string }{
		{"active", "running", "OK"}, {"active", "exited", "OK"}, {"activating", "start", "WARN"},
		{"reloading", "reload", "WARN"}, {"failed", "failed", "CRIT"}, {"inactive", "dead", "CRIT"}, {"weird", "", "WARN"},
	}
	for _, c := range cases {
		if got := SystemdStatus(c.a, c.s); got != c.want {
			t.Errorf("%s/%s = %s, want %s", c.a, c.s, got, c.want)
		}
	}
	ts, ok := ParseSystemdTimestamp("Wed 2026-11-04 06:00:00 UTC", time.UTC)
	if !ok || !ts.Equal(time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("%v %v", ts, ok)
	}
	for _, s := range []string{"", "n/a", "garbage"} {
		if _, ok := ParseSystemdTimestamp(s, nil); ok {
			t.Errorf("%q should not parse", s)
		}
	}
	up := time.Date(2026, 11, 15, 10, 30, 0, 0, time.UTC).Sub(ts)
	if got := FormatUptime(up); got != "11d 4h" {
		t.Fatalf("uptime %q", got)
	}
	if FormatUptime(4*time.Hour+12*time.Minute) != "4h 12m" || FormatUptime(90*time.Second) != "1m" || FormatUptime(-time.Hour) != "0m" {
		t.Fatal("uptime formats")
	}
}

func TestSupervisorParseAndGroup(t *testing.T) {
	procs, err := ParseSupervisorProcessInfo(fx(t, "supervisor_getallprocessinfo.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 7 || procs[0].PID != 4101 || procs[5].PID != 4300 {
		t.Fatalf("%+v", procs)
	}
	groups := GroupSupervisor(procs)
	want := map[string][3]any{ // running, total, status
		"messenger_async": {2, 2, "OK"},
		"mailer":          {1, 2, "DEGRADED"},
		"cron-sync":       {0, 1, "DOWN"},
		"horizon":         {1, 1, "OK"},
		"reports":         {1, 1, "OK"}, // reports_07 w grupie reports_07 -> sufiks _\d+ ucięty
	}
	if len(groups) != len(want) {
		t.Fatalf("groups: %+v", groups)
	}
	for _, g := range groups {
		w, ok := want[g.Group]
		if !ok || g.Running != w[0] || g.Total != w[1] || g.Status != w[2] {
			t.Errorf("group %+v, want %v", g, w)
		}
		if g.Workers == nil || len(g.Workers) != g.Total {
			t.Errorf("workers %+v", g)
		}
	}
	if groups[0].Group != "cron-sync" { // sortowanie
		t.Fatalf("not sorted: %v", groups[0].Group)
	}
}

func TestSupervisorGroupNameRules(t *testing.T) {
	cases := [][3]string{
		{"grp:proc_00", "", "grp"}, {"grp:proc_00", "other", "grp"},
		{"worker_03", "", "worker"}, {"worker", "", "worker"}, {"_5", "", "_5"},
		{"a", "bigGroup", "bigGroup"},
	}
	for _, c := range cases {
		if got := SupervisorGroupName(c[0], c[1]); got != c[2] {
			t.Errorf("%v -> %q", c, got)
		}
	}
	if SupervisorCacheStatus("OK") != "OK" || SupervisorCacheStatus("DEGRADED") != "WARN" || SupervisorCacheStatus("DOWN") != "CRIT" {
		t.Fatal("cache status")
	}
	if g := GroupSupervisor(nil); len(g) != 0 {
		t.Fatal("empty")
	}
}

func TestSupervisorFaultAndGarbage(t *testing.T) {
	if _, err := ParseSupervisorProcessInfo(fx(t, "supervisor_fault.xml")); err == nil || !strings.Contains(err.Error(), "SHUTDOWN_STATE") {
		t.Fatalf("fault: %v", err)
	}
	if _, err := ParseSupervisorProcessInfo([]byte("<html>")); err == nil {
		t.Fatal("garbage must fail")
	}
}

func feedAll(t *testing.T, name string) []CommandEvent {
	t.Helper()
	c := NewCorrelator()
	var out []CommandEvent
	for _, l := range strings.Split(string(fx(t, name)), "\n") {
		out = append(out, c.Feed(l)...)
	}
	return append(out, c.Flush()...)
}

func TestAuditCorrelation(t *testing.T) {
	ev := feedAll(t, "audit_multiline.log")
	// 4581, 4590, (4600 odfiltrowany: inny klucz), 4610 (failed), 4620 (chunki), 4630 (proctitle)
	if len(ev) != 5 {
		t.Fatalf("got %d events: %+v", len(ev), ev)
	}
	e := ev[0]
	if e.EventID != "1731661200.123:4581" || e.AUID != 1000 || e.UID != 0 || e.Command != "ls -la" || e.CWD != "/root" ||
		!e.Success || e.ExitCode == nil || *e.ExitCode != 0 {
		t.Fatalf("ev0 %+v", e)
	}
	if !e.Time.Equal(time.Unix(1731661200, 123_000_000)) {
		t.Fatalf("time %v", e.Time)
	}
	// hex: argument ze spacjami i cwd ze spacją
	if ev[1].Command != "bash -c echo hello world" || ev[1].CWD != "/home/anna/my dir" {
		t.Fatalf("ev1 %+v", ev[1])
	}
	// nieudany exec + EOE
	if ev[2].EventID != "1731661400.000:4610" || ev[2].Success || *ev[2].ExitCode != -2 || ev[2].AUID != 1001 {
		t.Fatalf("ev2 %+v", ev[2])
	}
	// argument rozbity na a1[0], a1[1]
	if ev[3].Command != "x aaaabbbb" || ev[3].CWD != "/home/anna" {
		t.Fatalf("ev3 %+v", ev[3])
	}
	// brak EXECVE -> fallback na PROCTITLE (hex z NUL), zdarzenie zamknięte przez Flush
	if ev[4].Command != "ls -la" || ev[4].EventID != "1731661600.000:4630" {
		t.Fatalf("ev4 %+v", ev[4])
	}
}

func TestAuditEdgeCases(t *testing.T) {
	c := NewCorrelator()
	if got := c.Feed("garbage"); got != nil {
		t.Fatal("garbage")
	}
	if got := c.Flush(); len(got) != 0 {
		t.Fatal("flush on empty")
	}
	// zdarzenie bez SYSCALL jest pomijane
	c.Feed(`type=EXECVE msg=audit(1.1:1): argc=1 a0="ls"`)
	if got := c.Flush(); len(got) != 0 {
		t.Fatal("no syscall -> no event")
	}
	// klucz zakodowany hex-em
	k := "636C695F6175646974" // cli_audit
	c.Feed(`type=SYSCALL msg=audit(5.5:9): success=yes exit=0 auid=7 uid=7 key=` + k)
	c.Feed(`type=EXECVE msg=audit(5.5:9): argc=1 a0="id"`)
	got := c.Flush()
	if len(got) != 1 || got[0].Command != "id" || got[0].AUID != 7 {
		t.Fatalf("%+v", got)
	}
	// ułamek krótszy niż 9 cyfr
	if !got[0].Time.Equal(time.Unix(5, 500_000_000)) {
		t.Fatalf("time %v", got[0].Time)
	}
}

func TestTokenizeQuotedSpaces(t *testing.T) {
	f := tokenize(`a="x y" b=12 c='q r' d=`)
	if f["a"].val != "x y" || !f["a"].quoted || f["b"].val != "12" || f["c"].val != "q r" {
		t.Fatalf("%+v", f)
	}
}

func TestESHealth(t *testing.T) {
	h, err := ParseESHealth(fx(t, "es_health_green.json"))
	if err != nil || h.Status != "OK" || h.ClusterName != "prod-es" || h.Nodes != 3 || h.ActiveShardsPercent != 100 {
		t.Fatalf("%+v %v", h, err)
	}
	h, err = ParseESHealth(fx(t, "es_health_red.json"))
	if err != nil || h.Status != "CRIT" || h.UnassignedShards != 14 || h.ActiveShardsPercent != 83.5 {
		t.Fatalf("%+v %v", h, err)
	}
	if h, _ := ParseESHealth([]byte(`{"status":"yellow"}`)); h.Status != "WARN" {
		t.Fatal("yellow")
	}
	if _, err := ParseESHealth(fx(t, "es_unauthorized.json")); err == nil {
		t.Fatal("error body must fail")
	}
	if _, err := ParseESHealth([]byte(`{"status":"purple"}`)); err == nil {
		t.Fatal("unknown status must fail")
	}
}

func TestKibanaStatus(t *testing.T) {
	for file, want := range map[string]KibanaStatus{
		"kibana_status_8x.json":       {"available", "OK"},
		"kibana_status_degraded.json": {"degraded", "WARN"},
		"kibana_status_legacy.json":   {"critical", "CRIT"},
	} {
		got, err := ParseKibanaStatus(fx(t, file))
		if err != nil || got != want {
			t.Errorf("%s: %+v %v", file, got, err)
		}
	}
	if _, err := ParseKibanaStatus([]byte(`{}`)); err == nil {
		t.Fatal("missing level must fail")
	}
}

func TestRESPAndInfo(t *testing.T) {
	r := bufio.NewReader(bytes.NewReader(fx(t, "redis_resp_pong.bin")))
	if v, err := ReadRESP(r); err != nil || v.Kind != '+' || v.Str != "PONG" {
		t.Fatalf("%+v %v", v, err)
	}
	v, err := ReadRESP(bufio.NewReader(bytes.NewReader(fx(t, "redis_resp_info.bin"))))
	if err != nil || v.Kind != '$' {
		t.Fatalf("%+v %v", v, err)
	}
	info, err := ParseRedisInfo(v.Str)
	if err != nil || info.UsedMemoryHuman != "142.50M" || info.ConnectedClients != 28 || info.UptimeSeconds != 864000 {
		t.Fatalf("%+v %v", info, err)
	}
	if v, _ := ReadRESP(bufio.NewReader(bytes.NewReader(fx(t, "redis_resp_noauth.bin")))); v.Kind != '-' || !strings.HasPrefix(v.Str, "NOAUTH") {
		t.Fatalf("%+v", v)
	}
	if v, _ := ReadRESP(bufio.NewReader(bytes.NewReader(fx(t, "redis_resp_nil.bin")))); !v.Nil {
		t.Fatalf("%+v", v)
	}
	if v, err := ReadRESP(bufio.NewReader(strings.NewReader(":42\r\n"))); err != nil || v.Int != 42 {
		t.Fatal("integer")
	}
	for _, bad := range []string{"", "+PONG", "$5\r\nab\r\n", "$99999999\r\n", "*1\r\n", "$2\r\nabXX"} {
		if _, err := ReadRESP(bufio.NewReader(strings.NewReader(bad))); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
	if _, err := ParseRedisInfo("# Server\nfoo:bar\n"); err == nil {
		t.Fatal("missing fields must fail")
	}
}
