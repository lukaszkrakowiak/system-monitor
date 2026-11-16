package collector

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"server-monitor/internal/storage"
)

func envMap(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestIngestPAMOpenAndClose(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Sessions()
	t0 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	env := map[string]string{"PAM_USER": "anna", "PAM_RHOST": "10.1.2.3", "PAM_TTY": "ssh", "PAM_SERVICE": "sshd", "PAM_TYPE": "open_session"}
	if err := IngestPAM(envMap(env), s, t0); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List("anna", 10, 0)
	if len(list) != 1 || list[0].RemoteIP != "10.1.2.3" || list[0].TTY != "ssh" || list[0].PAMService != "sshd" ||
		!list[0].LoginAt.Equal(t0) || list[0].LogoutAt != nil {
		t.Fatalf("%+v", list)
	}
	env["PAM_TYPE"] = "close_session"
	if err := IngestPAM(envMap(env), s, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	list, _ = s.List("anna", 10, 0)
	if list[0].LogoutAt == nil || !list[0].LogoutAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("%+v", list[0])
	}
	// zamknięcie bez otwartej sesji nie jest błędem
	if err := IngestPAM(envMap(env), s, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

type recSessions struct{ opened, closed int }

func (r *recSessions) Open(storage.SSHSession) (int64, error)        { r.opened++; return 1, nil }
func (r *recSessions) Close(string, string, time.Time) (bool, error) { r.closed++; return true, nil }

func TestIngestPAMIgnoresOtherTypesAndValidates(t *testing.T) {
	r := &recSessions{}
	for _, typ := range []string{"", "auth", "account", "open_session_x"} {
		if err := IngestPAM(envMap(map[string]string{"PAM_TYPE": typ, "PAM_USER": "a"}), r, time.Now()); err != nil {
			t.Fatalf("%q: %v", typ, err)
		}
	}
	if r.opened+r.closed != 0 {
		t.Fatal("unexpected calls")
	}
	if err := IngestPAM(envMap(map[string]string{"PAM_TYPE": "open_session"}), r, time.Now()); err == nil {
		t.Fatal("missing PAM_USER must fail")
	}
	long := strings.Repeat("x", 1000)
	db, _ := storage.Open(filepath.Join(t.TempDir(), "s.db"))
	defer db.Close()
	IngestPAM(envMap(map[string]string{"PAM_TYPE": "open_session", "PAM_USER": long, "PAM_RHOST": long}), db.Sessions(), time.Now())
	l, _ := db.Sessions().List("", 10, 0)
	if len(l) != 1 || len(l[0].Username) != 256 || len(l[0].RemoteIP) != 256 {
		t.Fatalf("fields must be clipped: %+v", l)
	}
}
