package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"server-monitor/internal/secret"
	"server-monitor/internal/storage"
)

const jsonCT = "application/json"

var bearerJSON = map[string]string{"Authorization": "Bearer tok", "Content-Type": jsonCT}

func alertEnv(t *testing.T, withKey bool, test func(context.Context, string) error) (*env, *secret.Box) {
	t.Helper()
	e := newEnv(t, nil)
	var box *secret.Box
	if withKey {
		var err error
		if box, err = secret.New("0123456789abcdef0123456789abcdef"); err != nil {
			t.Fatal(err)
		}
	}
	e.h = New(Config{Health: e.db.Health(), Services: e.db.Services(), Sessions: e.db.Sessions(), Commands: e.db.Commands(),
		Units: e.units, Token: "tok", Now: func() time.Time { return fixedNow }, Logf: func(string, ...any) {},
		Connectors: e.db.Connectors(), AlertSettings: e.db.AlertSettings(), Notifications: e.db.Notifications(),
		Secrets: box, TestConnector: test})
	return e, box
}

const goodEmail = `{"enabled":true,"password":"s3cr3t","config":{"host":"smtp.example.org","port":587,"security":"starttls",` +
	`"username":"mon","from":"Monitor <monitor@example.org>","to":["admin@example.org"]}}`

func TestAlertEndpointsRequireAuthAndCorrectMethod(t *testing.T) {
	e, _ := alertEnv(t, true, func(context.Context, string) error { return nil })
	for _, c := range [][2]string{{"GET", "/api/settings/alerts"}, {"PUT", "/api/settings/alerts"}, {"GET", "/api/settings/connectors"},
		{"PUT", "/api/settings/connectors/email"}, {"POST", "/api/settings/connectors/email/test"}, {"GET", "/api/alerts/history"}} {
		if w := e.do(c[0], c[1], "{}", map[string]string{"Content-Type": jsonCT}); w.Code != 401 {
			t.Errorf("%v without creds: %d", c, w.Code)
		}
	}
	for _, c := range [][2]string{{"POST", "/api/settings/alerts"}, {"DELETE", "/api/settings/connectors"}, {"GET", "/api/settings/connectors/email"},
		{"GET", "/api/settings/connectors/email/test"}, {"POST", "/api/alerts/history"}} {
		if w := e.do(c[0], c[1], "", bearer); w.Code != 405 {
			t.Errorf("%v wrong method: %d", c, w.Code)
		}
	}
}

func TestAlertEndpointsUnavailableWithoutStores(t *testing.T) {
	e := newEnv(t, nil)
	for _, p := range []string{"/api/settings/alerts", "/api/settings/connectors", "/api/alerts/history"} {
		if w := e.do("GET", p, "", bearer); w.Code != 503 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

func TestAlertSettingsGetDefaultsAndPutNormalizes(t *testing.T) {
	e, _ := alertEnv(t, true, nil)
	w := e.do("GET", "/api/settings/alerts", "", bearer)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"channels":[]`) || !strings.Contains(w.Body.String(), `"windows":[]`) ||
		!strings.Contains(w.Body.String(), `"allowed":[]`) || !strings.Contains(w.Body.String(), `"confirm_checks":2`) {
		t.Fatalf("defaults must use [] (not null): %d %s", w.Code, w.Body.String())
	}

	body := `{"timezone":"Europe/Warsaw","channels":["email","email"],
	  "login_time":{"enabled":true,"windows":[{"days":["SUN","mon"],"from":"17:00","to":"06:00"}]},
	  "login_ip":{"enabled":true,"allowed":["192.0.2.10","10.9.9.9/8"]},
	  "service_state":{"enabled":true,"notify_recovery":false,"confirm_checks":3}}`
	w = e.do("PUT", "/api/settings/alerts", body, bearerJSON)
	if w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	got := decode[alertsSettingsView](t, e.do("GET", "/api/settings/alerts", "", bearer))
	if got.Timezone != "Europe/Warsaw" || len(got.Channels) != 1 || got.LoginTime.Windows[0].Days[0] != "mon" ||
		got.LoginIP.Allowed[0] != "10.0.0.0/8" || got.ServiceState.ConfirmChecks != 3 || got.ServiceState.NotifyRecovery {
		t.Fatalf("persisted/normalized: %+v", got)
	}
}

type alertsSettingsView struct {
	Timezone  string   `json:"timezone"`
	Channels  []string `json:"channels"`
	LoginTime struct {
		Windows []struct {
			Days []string `json:"days"`
		} `json:"windows"`
	} `json:"login_time"`
	LoginIP struct {
		Allowed []string `json:"allowed"`
	} `json:"login_ip"`
	ServiceState struct {
		NotifyRecovery bool `json:"notify_recovery"`
		ConfirmChecks  int  `json:"confirm_checks"`
	} `json:"service_state"`
}

func TestAlertSettingsPutValidation(t *testing.T) {
	e, _ := alertEnv(t, true, nil)
	ok := `"service_state":{"confirm_checks":2}`
	for name, body := range map[string]string{
		"bad timezone": `{"timezone":"Mars/Base",` + ok + `}`,
		"bad channel":  `{"channels":["pager"],` + ok + `}`,
		"bad window":   `{"login_time":{"enabled":true,"windows":[{"days":[],"from":"17:00","to":"06:00"}]},` + ok + `}`,
		"bad ip":       `{"login_ip":{"enabled":true,"allowed":["nope"]},` + ok + `}`,
		"empty list":   `{"login_ip":{"enabled":true,"allowed":[]},` + ok + `}`,
		"confirm":      `{"service_state":{"confirm_checks":99}}`,
		"unknown":      `{"surprise":1,` + ok + `}`,
		"not json":     `[`,
	} {
		if w := e.do("PUT", "/api/settings/alerts", body, bearerJSON); w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := e.do("PUT", "/api/settings/alerts", `{}`, bearer); w.Code != 415 { // bez Content-Type (CSRF)
		t.Errorf("missing content-type: %d", w.Code)
	}
	if got := decode[alertsSettingsView](t, e.do("GET", "/api/settings/alerts", "", bearer)); got.Timezone != "" {
		t.Errorf("rejected input must not be stored: %+v", got)
	}
}

func TestConnectorLifecycleAndSecretHandling(t *testing.T) {
	e, box := alertEnv(t, true, nil)

	w := e.do("GET", "/api/settings/connectors", "", bearer)
	list := decode[ConnectorsResponse](t, w)
	if w.Code != 200 || len(list.Connectors) != 1 || list.Connectors[0].Type != "email" || list.Connectors[0].Configured || !list.SecretStorage {
		t.Fatalf("initial: %d %s", w.Code, w.Body.String())
	}

	w = e.do("PUT", "/api/settings/connectors/email", goodEmail, bearerJSON)
	if w.Code != 200 || strings.Contains(w.Body.String(), "s3cr3t") {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	dto := decode[ConnectorDTO](t, w)
	if !dto.Configured || !dto.Enabled || !dto.HasSecret || dto.Config["host"] != "smtp.example.org" {
		t.Fatalf("dto: %+v", dto)
	}
	// hasło nigdy nie wraca w GET i jest zaszyfrowane w bazie
	if body := e.do("GET", "/api/settings/connectors", "", bearer).Body.String(); strings.Contains(body, "s3cr3t") || !strings.Contains(body, `"has_secret":true`) {
		t.Fatalf("GET leaked or lost secret flag: %s", body)
	}
	row, _ := e.db.Connectors().Get("email")
	if strings.Contains(row.SecretEnc, "s3cr3t") || strings.Contains(row.ConfigJSON, "s3cr3t") {
		t.Fatalf("plaintext in database: %+v", row)
	}
	if pw, err := box.Decrypt(row.SecretEnc); err != nil || pw != "s3cr3t" {
		t.Fatalf("stored secret: %q %v", pw, err)
	}

	update := func(password string) {
		t.Helper()
		body := `{"enabled":false,"password":` + password + `,"config":{"host":"smtp2.example.org","port":465,"security":"tls","username":"mon","from":"m@example.org","to":["a@example.org"]}}`
		if w := e.do("PUT", "/api/settings/connectors/email", body, bearerJSON); w.Code != 200 {
			t.Fatalf("update %s: %d %s", password, w.Code, w.Body.String())
		}
	}
	update("null") // null = zostaw hasło
	row, _ = e.db.Connectors().Get("email")
	if pw, _ := box.Decrypt(row.SecretEnc); pw != "s3cr3t" || row.Enabled || !strings.Contains(row.ConfigJSON, "smtp2.example.org") {
		t.Fatalf("null must keep the password: %+v", row)
	}
	update(`"new-pass"`)
	row, _ = e.db.Connectors().Get("email")
	if pw, _ := box.Decrypt(row.SecretEnc); pw != "new-pass" {
		t.Fatalf("password replace: %q", pw)
	}
	update(`""`) // "" = usuń
	row, _ = e.db.Connectors().Get("email")
	if row.SecretEnc != "" {
		t.Fatalf("password clear: %+v", row)
	}
}

func TestConnectorPutErrors(t *testing.T) {
	e, _ := alertEnv(t, true, nil)
	cfg := `"config":{"host":"smtp.example.org","port":587,"security":"starttls","from":"m@example.org","to":["a@example.org"]}`
	for name, c := range map[string]struct {
		path, body string
		code       int
	}{
		"unknown type":   {"/api/settings/connectors/pager", `{"enabled":true,"password":null,` + cfg + `}`, 404},
		"no config":      {"/api/settings/connectors/email", `{"enabled":true,"password":null}`, 400},
		"invalid config": {"/api/settings/connectors/email", `{"enabled":true,"password":null,"config":{"host":"","port":587,"security":"starttls","from":"m@example.org","to":["a@example.org"]}}`, 400},
		"unknown field":  {"/api/settings/connectors/email", `{"enabled":true,"password":null,"extra":1,` + cfg + `}`, 400},
		"not json":       {"/api/settings/connectors/email", `{`, 400},
	} {
		if w := e.do("PUT", c.path, c.body, bearerJSON); w.Code != c.code {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if w := e.do("PUT", "/api/settings/connectors/email", `{}`, bearer); w.Code != 415 {
		t.Errorf("content type: %d", w.Code)
	}
	if row, _ := e.db.Connectors().Get("email"); row != nil {
		t.Fatalf("failed requests must not store anything: %+v", row)
	}
}

func TestConnectorPasswordNeedsSecretKey(t *testing.T) {
	e, _ := alertEnv(t, false, nil)
	if l := decode[ConnectorsResponse](t, e.do("GET", "/api/settings/connectors", "", bearer)); l.SecretStorage {
		t.Fatal("secret_storage must be false without SM_SECRET_KEY")
	}
	if w := e.do("PUT", "/api/settings/connectors/email", goodEmail, bearerJSON); w.Code != 400 || !strings.Contains(w.Body.String(), "SM_SECRET_KEY") {
		t.Fatalf("password without key: %d %s", w.Code, w.Body.String())
	}
	if row, _ := e.db.Connectors().Get("email"); row != nil {
		t.Fatal("nothing may be stored when the secret cannot be protected")
	}
	// bez hasła (np. lokalny relay bez uwierzytelniania) można zapisać także bez klucza
	noAuth := `{"enabled":true,"password":null,"config":{"host":"127.0.0.1","port":25,"security":"none","from":"m@example.org","to":["a@example.org"]}}`
	if w := e.do("PUT", "/api/settings/connectors/email", noAuth, bearerJSON); w.Code != 200 {
		t.Fatalf("no password: %d %s", w.Code, w.Body.String())
	}
}

func TestConnectorTestEndpoint(t *testing.T) {
	var calls []string
	var failWith error
	e, _ := alertEnv(t, true, func(_ context.Context, typ string) error { calls = append(calls, typ); return failWith })

	if w := e.do("POST", "/api/settings/connectors/email/test", `{}`, bearerJSON); w.Code != 200 || !decode[TestConnectorResponse](t, w).OK {
		t.Fatalf("ok case: %d %s", w.Code, w.Body.String())
	}
	failWith = errors.New("connect: dial tcp 10.0.0.5:587:\n i/o timeout")
	r := decode[TestConnectorResponse](t, e.do("POST", "/api/settings/connectors/email/test", `{}`, bearerJSON))
	if r.OK || r.Error != "connect: dial tcp 10.0.0.5:587: i/o timeout" {
		t.Fatalf("failure must be reported in the body, one line: %+v", r)
	}
	if w := e.do("POST", "/api/settings/connectors/pager/test", `{}`, bearerJSON); w.Code != 404 {
		t.Errorf("unknown type: %d", w.Code)
	}
	if w := e.do("POST", "/api/settings/connectors/email/test", `{}`, bearer); w.Code != 415 {
		t.Errorf("content type: %d", w.Code)
	}
	if len(calls) != 2 || calls[0] != "email" {
		t.Fatalf("calls %v", calls)
	}
}

func TestAlertHistoryEndpoint(t *testing.T) {
	e, _ := alertEnv(t, true, nil)
	r := decode[NotificationsResponse](t, e.do("GET", "/api/alerts/history", "", bearer))
	if r.Notifications == nil || len(r.Notifications) != 0 || r.Limit != 50 {
		t.Fatalf("empty history must be []: %+v", r)
	}
	n := e.db.Notifications()
	base := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
	n.Enqueue(storage.Notification{DedupeKey: "a", Connector: "email", Kind: "login", Subject: "first", Body: "b1", NextAttemptAt: base, CreatedAt: base})
	n.Enqueue(storage.Notification{DedupeKey: "b", Connector: "email", Kind: "service_state", Subject: "second", Body: "b2", NextAttemptAt: base, CreatedAt: base})
	due, _ := n.Due(base, 10)
	n.MarkSent(due[0].ID, base.Add(time.Second))
	n.MarkAttemptFailed(due[1].ID, "smtp down", base.Add(time.Minute), false)

	r = decode[NotificationsResponse](t, e.do("GET", "/api/alerts/history?limit=1", "", bearer))
	if len(r.Notifications) != 1 || r.Notifications[0].Subject != "second" || r.Notifications[0].Status != "pending" ||
		r.Notifications[0].LastError != "smtp down" || r.Notifications[0].SentAt != nil || r.Notifications[0].Attempts != 1 {
		t.Fatalf("%+v", r)
	}
	r = decode[NotificationsResponse](t, e.do("GET", "/api/alerts/history", "", bearer))
	if len(r.Notifications) != 2 || r.Notifications[1].Status != "sent" || r.Notifications[1].SentAt == nil ||
		*r.Notifications[1].SentAt != "2026-11-16T08:00:01Z" || r.Notifications[1].Body != "b1" {
		t.Fatalf("%+v", r)
	}
	if w := e.do("GET", "/api/alerts/history?limit=0", "", bearer); w.Code != 400 {
		t.Errorf("limit: %d", w.Code)
	}
}
