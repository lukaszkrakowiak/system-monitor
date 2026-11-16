package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"server-monitor/internal/collector"
	"server-monitor/internal/storage"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadConfigDefaultsAndOverrides(t *testing.T) {
	c, err := loadConfig(env(map[string]string{"SUPERVISOR_SOCKET": "off", "SM_AUDIT_LOG": "off"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DBPath != defaultDBPath || c.Listen != defaultListen || c.HealthPublic || c.SupervisorSocket != "" || c.AuditLog != "" ||
		c.RedisNetwork != "tcp" || c.RefreshInterval != 12*time.Second {
		t.Fatalf("%+v", c)
	}
	c, err = loadConfig(env(map[string]string{
		"SM_DB_PATH": "/x/db", "SM_LISTEN": "0.0.0.0:9", "HEALTH_PUBLIC": "true", "ES_URL": "https://es:9200", "ES_API_KEY": "k",
		"KIBANA_URL": "http://kb:5601", "REDIS_ADDR": "/run/redis.sock", "REDIS_NETWORK": "unix", "SUPERVISOR_SOCKET": "/s.sock",
		"SM_AUDIT_LOG": "/a.log", "SM_REFRESH_INTERVAL": "15s"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DBPath != "/x/db" || !c.HealthPublic || c.ESAuth.APIKey != "k" || c.RedisNetwork != "unix" || c.SupervisorSocket != "/s.sock" ||
		c.AuditLog != "/a.log" || c.RefreshInterval != 15*time.Second {
		t.Fatalf("%+v", c)
	}
}

func TestLoadConfigRejectsInvalid(t *testing.T) {
	for k, v := range map[string]string{"HEALTH_PUBLIC": "maybe", "ES_URL": "ftp://x", "KIBANA_URL": "not a url", "REDIS_NETWORK": "udp",
		"SM_REFRESH_INTERVAL": "1s"} {
		if _, err := loadConfig(env(map[string]string{k: v, "SUPERVISOR_SOCKET": "off"})); err == nil {
			t.Errorf("%s=%s should be rejected", k, v)
		}
	}
	if _, err := loadConfig(env(map[string]string{"SM_REFRESH_INTERVAL": "20s"})); err == nil {
		t.Error("interval above 15s must be rejected")
	}
}

func TestValidateServeFailsClosed(t *testing.T) {
	if (config{}).validateServe() == nil {
		t.Fatal("no credentials must fail")
	}
	if (config{Token: "short"}).validateServe() == nil {
		t.Fatal("short token must fail")
	}
	if (config{BasicUser: "a"}).validateServe() == nil {
		t.Fatal("basic user without password must fail")
	}
	if err := (config{Token: strings.Repeat("t", 16)}).validateServe(); err != nil {
		t.Fatal(err)
	}
	if err := (config{BasicUser: "a", BasicPass: "b"}).validateServe(); err != nil {
		t.Fatal(err)
	}
}

func TestProbesComposition(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sysd := &collector.Systemd{Runner: collector.ExecRunner{}}
	keys := func(c config) string {
		var k []string
		for _, p := range c.probes(db, sysd) {
			k = append(k, p.Key())
		}
		return strings.Join(k, ",")
	}
	if got := keys(config{}); got != "systemd" {
		t.Fatal(got)
	}
	full := config{SupervisorSocket: "/s", ESURL: "http://e", RedisAddr: "r:1", KibanaURL: "http://k"}
	if got := keys(full); got != "systemd,supervisor,elasticsearch,redis,kibana" {
		t.Fatal(got)
	}
}

func TestRunDispatch(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, env(nil), &out, &errb); code != 2 || !strings.Contains(errb.String(), "usage") {
		t.Fatalf("%d %s", code, errb.String())
	}
	errb.Reset()
	if code := run([]string{"bogus"}, env(map[string]string{"SUPERVISOR_SOCKET": "off"}), &out, &errb); code != 2 || !strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("%d %s", code, errb.String())
	}
	errb.Reset()
	if code := run([]string{"serve"}, env(map[string]string{"SUPERVISOR_SOCKET": "off"}), &out, &errb); code != 2 || !strings.Contains(errb.String(), "credentials") {
		t.Fatalf("serve without credentials must refuse to start: %d %s", code, errb.String())
	}
}

func TestIngestPAMSubcommand(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	base := map[string]string{"SM_DB_PATH": db, "SUPERVISOR_SOCKET": "off", "PAM_USER": "anna", "PAM_RHOST": "10.0.0.9",
		"PAM_TTY": "ssh", "PAM_SERVICE": "sshd", "PAM_TYPE": "open_session"}
	var out, errb bytes.Buffer
	if code := run([]string{"ingest-pam"}, env(base), &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	base["PAM_TYPE"] = "close_session"
	if code := run([]string{"ingest-pam"}, env(base), &out, &errb); code != 0 {
		t.Fatalf("%d %s", code, errb.String())
	}
	s, err := storage.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l, _ := s.Sessions().List("anna", 10, 0)
	if len(l) != 1 || l[0].RemoteIP != "10.0.0.9" || l[0].LogoutAt == nil {
		t.Fatalf("%+v", l)
	}
	base["PAM_USER"] = ""
	base["PAM_TYPE"] = "open_session"
	if code := run([]string{"ingest-pam"}, env(base), &out, &errb); code != 1 {
		t.Fatalf("missing PAM_USER must exit 1, got %d", code)
	}
}

// check bez skonfigurowanych usług niczego nie uruchamia (pusty wybór systemd) i zapisuje cache.
func TestCheckWithNoProbesConfigured(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state.db")
	var out, errb bytes.Buffer
	code := run([]string{"check"}, env(map[string]string{"SM_DB_PATH": db, "SUPERVISOR_SOCKET": "off"}), &out, &errb)
	if code != 0 || !strings.Contains(out.String(), `"service":"systemd"`) || !strings.Contains(out.String(), `"status":"OK"`) {
		t.Fatalf("%d out=%s err=%s", code, out.String(), errb.String())
	}
}
