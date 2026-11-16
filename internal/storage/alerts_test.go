package storage

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

func TestConnectorAndSettingsAndStateRoundTrip(t *testing.T) {
	db := openTest(t)

	if c, err := db.Connectors().Get("email"); err != nil || c != nil {
		t.Fatalf("missing connector: %v %v", c, err)
	}
	in := Connector{Type: "email", Enabled: true, ConfigJSON: `{"host":"smtp.example.org"}`, SecretEnc: "enc"}
	if err := db.Connectors().Upsert(in, t0); err != nil {
		t.Fatal(err)
	}
	in.Enabled, in.SecretEnc = false, ""
	if err := db.Connectors().Upsert(in, t0.Add(time.Minute)); err != nil { // upsert nadpisuje
		t.Fatal(err)
	}
	got, err := db.Connectors().Get("email")
	if err != nil || got == nil || got.Enabled || got.SecretEnc != "" || got.ConfigJSON != in.ConfigJSON || !got.UpdatedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("connector: %+v %v", got, err)
	}

	if _, ok, err := db.AlertSettings().Get(); err != nil || ok {
		t.Fatalf("settings before save: %v %v", ok, err)
	}
	db.AlertSettings().Set(`{"a":1}`, t0)
	db.AlertSettings().Set(`{"a":2}`, t0)
	if s, ok, err := db.AlertSettings().Get(); err != nil || !ok || s != `{"a":2}` {
		t.Fatalf("settings: %q %v %v", s, ok, err)
	}

	st := db.AlertState()
	st.Set("svc:systemd:nginx", "x", t0)
	st.Set("svc:redis", "y", t0)
	st.Set("cursor:sessions", "7", t0)
	st.Set("svc:%weird_", "z", t0)
	m, err := st.ListPrefix("svc:")
	if err != nil || len(m) != 3 || m["svc:redis"] != "y" {
		t.Fatalf("prefix list: %v %v", m, err)
	}
	if m, _ := st.ListPrefix("svc:%"); len(m) != 1 { // prefiks dosłowny, bez wildcardów LIKE
		t.Fatalf("literal prefix: %v", m)
	}
	st.Delete("svc:redis")
	if _, ok, _ := st.Get("svc:redis"); ok {
		t.Fatal("delete failed")
	}
}

func TestNotificationOutbox(t *testing.T) {
	db := openTest(t)
	r := db.Notifications()
	mk := func(key, conn string) Notification {
		return Notification{DedupeKey: key, Connector: conn, Kind: "login_ip", Subject: "s", Body: "b", NextAttemptAt: t0, CreatedAt: t0}
	}
	if ok, err := r.Enqueue(mk("login:1", "email")); err != nil || !ok {
		t.Fatalf("enqueue: %v %v", ok, err)
	}
	if ok, _ := r.Enqueue(mk("login:1", "email")); ok { // duplikat (restart)
		t.Fatal("duplicate must be ignored")
	}
	if ok, _ := r.Enqueue(mk("login:1", "slack")); !ok { // ten sam klucz, inny konektor
		t.Fatal("different connector must be accepted")
	}
	late := mk("login:2", "email")
	late.NextAttemptAt = t0.Add(time.Hour)
	r.Enqueue(late)

	due, err := r.Due(t0, 10)
	if err != nil || len(due) != 2 {
		t.Fatalf("due: %v %v", due, err)
	}
	// nieudana próba: zostaje pending z późniejszym terminem
	if err := r.MarkAttemptFailed(due[0].ID, "dial refused", t0.Add(time.Minute), false); err != nil {
		t.Fatal(err)
	}
	if d, _ := r.Due(t0, 10); len(d) != 1 {
		t.Fatalf("after retry scheduling: %v", d)
	}
	if err := r.MarkSent(due[1].ID, t0); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkAttemptFailed(due[0].ID, "still down", t0, true); err != nil { // rezygnacja
		t.Fatal(err)
	}
	if d, _ := r.Due(t0.Add(2*time.Hour), 10); len(d) != 1 || d[0].DedupeKey != "login:2" {
		t.Fatalf("only the not-yet-due one should remain: %v", d)
	}

	list, err := r.List(10, 0)
	if err != nil || len(list) != 3 {
		t.Fatalf("list: %v %v", list, err)
	}
	byKey := map[string]Notification{}
	for _, n := range list {
		byKey[n.DedupeKey+"/"+n.Connector] = n
	}
	if n := byKey["login:1/email"]; n.Status != "failed" || n.Attempts != 2 || n.LastError != "still down" {
		t.Errorf("failed: %+v", n)
	}
	if n := byKey["login:1/slack"]; n.Status != "sent" || n.SentAt == nil || !n.SentAt.Equal(t0) || n.Attempts != 1 {
		t.Errorf("sent: %+v", n)
	}
	if list[0].ID < list[2].ID {
		t.Error("history must be newest first")
	}
}

func TestSessionsAfterAndMaxID(t *testing.T) {
	db := openTest(t)
	r := db.Sessions()
	if id, err := r.MaxID(); err != nil || id != 0 {
		t.Fatalf("empty max: %d %v", id, err)
	}
	var ids []int64
	for _, u := range []string{"a", "b", "c"} {
		id, err := r.Open(SSHSession{Username: u, RemoteIP: "192.0.2.1", TTY: "ssh", PAMService: "sshd", LoginAt: t0})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if id, _ := r.MaxID(); id != ids[2] {
		t.Fatalf("max id %d want %d", id, ids[2])
	}
	got, err := r.After(ids[0], 10)
	if err != nil || len(got) != 2 || got[0].Username != "b" || got[1].Username != "c" {
		t.Fatalf("after: %+v %v", got, err)
	}
	if got, _ := r.After(ids[2], 10); len(got) != 0 {
		t.Fatalf("nothing after last: %+v", got)
	}
	if got, _ := r.After(0, 1); len(got) != 1 || got[0].Username != "a" {
		t.Fatalf("limit: %+v", got)
	}
}
