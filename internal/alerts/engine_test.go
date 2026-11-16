package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"server-monitor/internal/collector"
	"server-monitor/internal/notify"
	"server-monitor/internal/secret"
	"server-monitor/internal/storage"
)

type recorder struct {
	sent    []notify.Message
	failing bool
	secrets []string
}

func (r *recorder) Send(_ context.Context, m notify.Message) error {
	if r.failing {
		return errors.New("smtp: connection refused")
	}
	r.sent = append(r.sent, m)
	return nil
}

type env struct {
	t     *testing.T
	db    *storage.DB
	eng   *Engine
	rec   *recorder
	now   time.Time
	logs  []string
	setts Settings
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	box, err := secret.New("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: db, rec: &recorder{}, now: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	e.eng = &Engine{
		Sessions: db.Sessions(), Health: db.Health(), Settings: db.AlertSettings(), State: db.AlertState(),
		Outbox: db.Notifications(), Connectors: db.Connectors(), Secrets: box, Host: "srv1",
		Now:  func() time.Time { return e.now },
		Logf: func(f string, a ...any) { e.logs = append(e.logs, f) },
		Build: func(typ string, cfg []byte, sec string) (notify.Connector, error) {
			e.rec.secrets = append(e.rec.secrets, sec)
			return e.rec, nil
		},
	}
	enc, _ := box.Encrypt("smtp-pass")
	if err := db.Connectors().Upsert(storage.Connector{Type: "email", Enabled: true, ConfigJSON: `{}`, SecretEnc: enc}, e.now); err != nil {
		t.Fatal(err)
	}
	e.setts = Default()
	e.setts.Timezone = "UTC"
	e.setts.Channels = []string{"email"}
	return e
}

func (e *env) save(mut func(*Settings)) {
	e.t.Helper()
	if mut != nil {
		mut(&e.setts)
	}
	n, err := e.setts.Normalize(known)
	if err != nil {
		e.t.Fatal(err)
	}
	b, _ := json.Marshal(n)
	if err := e.db.AlertSettings().Set(string(b), e.now); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) tick() {
	e.t.Helper()
	if err := e.eng.Tick(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) session(user, ip string) int64 {
	e.t.Helper()
	id, err := e.db.Sessions().Open(storage.SSHSession{Username: user, RemoteIP: ip, TTY: "ssh", PAMService: "sshd", LoginAt: e.now})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) history() []storage.Notification {
	e.t.Helper()
	l, err := e.db.Notifications().List(100, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	return l
}

func (e *env) health(redisStatus string) {
	e.t.Helper()
	p, _ := json.Marshal(collector.RedisPayload{Status: redisStatus, Error: map[bool]string{true: "dial tcp: connection refused"}[redisStatus != "OK"]})
	if err := e.db.Health().Upsert(storage.HealthEntry{ServiceKey: "redis", Status: redisStatus, PayloadJSON: string(p), CheckedAt: e.now}); err != nil {
		e.t.Fatal(err)
	}
}

func TestLoginAlertsFireOncePerSessionAndSkipHistory(t *testing.T) {
	e := newEnv(t)
	e.save(func(s *Settings) { s.LoginIP = LoginIPRule{Enabled: true, Allowed: []string{"192.0.2.0/24"}} })

	e.session("old", "203.0.113.1") // historia sprzed uruchomienia silnika: bez alertu
	e.tick()
	if len(e.history()) != 0 || len(e.rec.sent) != 0 {
		t.Fatalf("history must not alert: %v", e.history())
	}

	e.session("alice", "192.0.2.10") // z białej listy
	e.session("mallory", "203.0.113.9")
	e.session("console", "") // lokalne logowanie
	e.now = e.now.Add(time.Second)
	e.tick()
	if len(e.rec.sent) != 1 {
		t.Fatalf("want exactly 1 alert, got %d: %+v", len(e.rec.sent), e.rec.sent)
	}
	m := e.rec.sent[0]
	if !strings.Contains(m.Subject, "mallory") || !strings.Contains(m.Subject, "203.0.113.9") || !strings.Contains(m.Subject, "[srv1]") ||
		!strings.Contains(m.Body, "not on the allowed list") || !strings.Contains(m.Body, "mallory") {
		t.Fatalf("message: %+v", m)
	}
	if got := e.rec.secrets[len(e.rec.secrets)-1]; got != "smtp-pass" {
		t.Fatalf("connector must receive the decrypted secret, got %q", got)
	}
	h := e.history()
	if len(h) != 1 || h[0].Status != "sent" || h[0].Kind != KindLogin || h[0].Connector != "email" || h[0].SentAt == nil {
		t.Fatalf("history: %+v", h)
	}
	// kolejne ticki nie powtarzają alertu
	e.tick()
	e.tick()
	if len(e.rec.sent) != 1 || len(e.history()) != 1 {
		t.Fatal("alert repeated")
	}
}

func TestLoginTimeRuleAlerts(t *testing.T) {
	e := newEnv(t)
	e.save(func(s *Settings) {
		s.LoginTime = LoginTimeRule{Enabled: true, Windows: []Window{{Days: []string{"mon"}, From: "17:00", To: "06:00"}}}
	})
	e.tick() // inicjalizacja kursora
	e.now = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	e.session("day", "192.0.2.1") // pon 12:00 — poza oknem
	e.now = time.Date(2026, 11, 16, 23, 30, 0, 0, time.UTC)
	e.session("night", "192.0.2.1") // pon 23:30 — w oknie
	e.tick()
	if len(e.rec.sent) != 1 || !strings.Contains(e.rec.sent[0].Subject, "night") || !strings.Contains(e.rec.sent[0].Body, "falls in the alert window") {
		t.Fatalf("%+v", e.rec.sent)
	}
}

func TestNoChannelOrDisabledConnectorMeansNoNotification(t *testing.T) {
	e := newEnv(t)
	e.save(func(s *Settings) { s.LoginIP = LoginIPRule{Enabled: true, Allowed: []string{"192.0.2.1"}} })
	e.tick()

	e.save(func(s *Settings) { s.Channels = []string{} }) // brak wybranego kanału
	e.session("a", "203.0.113.1")
	e.tick()
	if len(e.history()) != 0 {
		t.Fatalf("no channel selected: %v", e.history())
	}

	e.save(func(s *Settings) { s.Channels = []string{"email"} })
	enc, _ := e.eng.Secrets.Encrypt("x")
	e.db.Connectors().Upsert(storage.Connector{Type: "email", Enabled: false, ConfigJSON: `{}`, SecretEnc: enc}, e.now) // wyłączony
	e.session("b", "203.0.113.2")
	e.tick()
	if len(e.history()) != 0 {
		t.Fatalf("disabled connector: %v", e.history())
	}
}

func TestServiceAlertConfirmDedupeAndRecovery(t *testing.T) {
	e := newEnv(t)
	e.save(func(s *Settings) {
		s.ServiceState = ServiceStateRule{Enabled: true, NotifyRecovery: true, ConfirmChecks: 2}
	})

	e.health("OK")
	e.tick()
	// pierwszy nieudany pomiar — jeszcze bez alertu
	e.now = e.now.Add(12 * time.Second)
	e.health("CRIT")
	e.tick()
	e.tick() // ten sam pomiar w cache, kolejny tick silnika
	if len(e.rec.sent) != 0 {
		t.Fatalf("one failed check must not alert: %+v", e.rec.sent)
	}
	// drugi pomiar potwierdza
	e.now = e.now.Add(12 * time.Second)
	e.health("CRIT")
	e.tick()
	if len(e.rec.sent) != 1 {
		t.Fatalf("want 1 alert: %+v", e.rec.sent)
	}
	m := e.rec.sent[0]
	if !strings.HasPrefix(m.Subject, "[srv1] CRIT: Redis (was OK)") || !strings.Contains(m.Body, "connection refused") || !strings.Contains(m.Body, "Since:") {
		t.Fatalf("service alert: %+v", m)
	}
	// dalej CRIT: cisza
	for i := 0; i < 3; i++ {
		e.now = e.now.Add(12 * time.Second)
		e.health("CRIT")
		e.tick()
	}
	if len(e.rec.sent) != 1 {
		t.Fatalf("repeated CRIT alerted again: %d", len(e.rec.sent))
	}
	// powrót do OK po dwóch pomiarach
	for i := 0; i < 2; i++ {
		e.now = e.now.Add(12 * time.Second)
		e.health("OK")
		e.tick()
	}
	if len(e.rec.sent) != 2 || !strings.Contains(e.rec.sent[1].Subject, "RECOVERED: Redis (was CRIT)") {
		t.Fatalf("recovery: %+v", e.rec.sent)
	}
	kinds := []string{}
	for _, n := range e.history() {
		kinds = append(kinds, n.Kind)
	}
	if strings.Join(kinds, ",") != KindRecovery+","+KindService {
		t.Fatalf("kinds %v", kinds)
	}
}

func TestServiceRecoveryCanBeDisabledAndRuleOffIsSilentButTracksState(t *testing.T) {
	e := newEnv(t)
	e.save(func(s *Settings) {
		s.ServiceState = ServiceStateRule{Enabled: true, NotifyRecovery: false, ConfirmChecks: 1}
	})
	step := func(status string) {
		e.now = e.now.Add(12 * time.Second)
		e.health(status)
		e.tick()
	}
	step("OK")
	step("CRIT")
	step("OK")
	if len(e.rec.sent) != 1 {
		t.Fatalf("recovery notifications disabled: %+v", e.rec.sent)
	}
	// reguła wyłączona: stan jest śledzony, więc po włączeniu nie ma „starych" alertów
	e.save(func(s *Settings) { s.ServiceState.Enabled = false })
	step("CRIT")
	if len(e.rec.sent) != 1 {
		t.Fatal("rule off must not alert")
	}
	e.save(func(s *Settings) { s.ServiceState.Enabled = true })
	step("CRIT")
	if len(e.rec.sent) != 1 {
		t.Fatalf("already-known CRIT must not alert after enabling: %d", len(e.rec.sent))
	}
}

func TestStaleStateForRemovedItemsIsForgotten(t *testing.T) {
	e := newEnv(t)
	e.save(func(s *Settings) { s.ServiceState = ServiceStateRule{Enabled: true, ConfirmChecks: 1} })
	e.health("OK")
	e.tick()
	if m, _ := e.db.AlertState().ListPrefix(statePrefix); len(m) != 1 {
		t.Fatalf("state %v", m)
	}
	p, _ := json.Marshal(collector.KibanaPayload{Status: "OK"})
	e.now = e.now.Add(time.Minute)
	e.db.Health().Upsert(storage.HealthEntry{ServiceKey: "kibana", Status: "OK", PayloadJSON: string(p), CheckedAt: e.now})
	e.db.Health().Upsert(storage.HealthEntry{ServiceKey: "redis", Status: "OK", PayloadJSON: `{"status":"OK"}`, CheckedAt: e.now})
	e.tick()
	if m, _ := e.db.AlertState().ListPrefix(statePrefix); len(m) != 2 {
		t.Fatalf("state %v", m)
	}
}

func TestDeliveryRetriesWithBackoffAndGivesUp(t *testing.T) {
	e := newEnv(t)
	e.save(func(s *Settings) { s.LoginIP = LoginIPRule{Enabled: true, Allowed: []string{"192.0.2.1"}} })
	e.tick()
	e.rec.failing = true
	e.session("mallory", "203.0.113.9")
	e.tick()

	h := e.history()
	if len(h) != 1 || h[0].Status != "pending" || h[0].Attempts != 1 || !strings.Contains(h[0].LastError, "connection refused") {
		t.Fatalf("after first failure: %+v", h)
	}
	if !h[0].NextAttemptAt.Equal(e.now.Add(30 * time.Second)) {
		t.Fatalf("backoff: %v", h[0].NextAttemptAt)
	}
	// przed terminem nic się nie dzieje
	e.now = e.now.Add(10 * time.Second)
	e.tick()
	if h := e.history(); h[0].Attempts != 1 {
		t.Fatalf("retried too early: %+v", h)
	}
	// SMTP wraca: dostarczenie przy kolejnej próbie
	e.rec.failing = false
	e.now = e.now.Add(time.Minute)
	e.tick()
	if h := e.history(); h[0].Status != "sent" || h[0].Attempts != 2 || len(e.rec.sent) != 1 {
		t.Fatalf("after recovery: %+v", h)
	}

	// trwała awaria: po maxAttempts status failed i koniec prób
	e.rec.failing = true
	e.session("eve", "203.0.113.10")
	e.tick()
	for i := 0; i < 10; i++ {
		e.now = e.now.Add(time.Hour)
		e.tick()
	}
	h = e.history()
	if h[0].Status != "failed" || h[0].Attempts != maxAttempts {
		t.Fatalf("give up: %+v", h[0])
	}
}

func TestDeliverySurfacesSecretAndConnectorProblems(t *testing.T) {
	e := newEnv(t)
	e.save(func(s *Settings) { s.LoginIP = LoginIPRule{Enabled: true, Allowed: []string{"192.0.2.1"}} })
	e.tick()
	// sekret zaszyfrowany innym kluczem — nie da się odszyfrować
	other, _ := secret.New(strings.Repeat("k", 40))
	enc, _ := other.Encrypt("pw")
	e.db.Connectors().Upsert(storage.Connector{Type: "email", Enabled: true, ConfigJSON: `{}`, SecretEnc: enc}, e.now)
	e.session("mallory", "203.0.113.9")
	e.tick()
	h := e.history()
	if len(h) != 1 || h[0].Status != "pending" || !strings.Contains(h[0].LastError, "cannot decrypt") {
		t.Fatalf("%+v", h)
	}
	if len(e.rec.sent) != 0 {
		t.Fatal("must not send")
	}
}

func TestSendTest(t *testing.T) {
	e := newEnv(t)
	e.save(nil)
	if err := e.eng.SendTest(context.Background(), "email"); err != nil {
		t.Fatal(err)
	}
	if len(e.rec.sent) != 1 || !strings.Contains(e.rec.sent[0].Subject, "test notification") {
		t.Fatalf("%+v", e.rec.sent)
	}
	if err := e.eng.SendTest(context.Background(), "slack"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unconfigured connector: %v", err)
	}
	e.rec.failing = true
	if err := e.eng.SendTest(context.Background(), "email"); err == nil {
		t.Fatal("send error must be returned")
	}
}

func TestLoadSettingsFallsBackOnCorruptData(t *testing.T) {
	e := newEnv(t)
	e.db.AlertSettings().Set("{broken", e.now)
	s, err := LoadSettings(e.db.AlertSettings())
	if err == nil || s.LoginIP.Enabled || s.LoginTime.Enabled || s.ServiceState.Enabled {
		t.Fatalf("corrupt settings must yield safe defaults + error: %+v %v", s, err)
	}
	e.db.AlertSettings().Set(`{"channels":["pager"],"service_state":{"confirm_checks":2}}`, e.now)
	if _, err := LoadSettings(e.db.AlertSettings()); err == nil {
		t.Fatal("invalid stored settings must be reported")
	}
	if err := e.eng.Tick(context.Background()); err != nil { // Tick się nie wywraca
		t.Fatal(err)
	}
}
