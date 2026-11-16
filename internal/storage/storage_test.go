package storage

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestPragmasAndMigrationsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := db.write.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, err %v", mode, err)
	}
	var sync, busy, fk int
	db.write.QueryRow(`PRAGMA synchronous`).Scan(&sync)
	db.write.QueryRow(`PRAGMA busy_timeout`).Scan(&busy)
	db.write.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	if sync != 1 || busy != 5000 || fk != 1 {
		t.Fatalf("pragmas synchronous=%d busy=%d fk=%d", sync, busy, fk)
	}
	if got := db.write.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("write MaxOpenConnections = %d", got)
	}
	db.Close()

	db, err = Open(path) // ponowne otwarcie: migracje nie mogą się powtórzyć
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	db.read.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n)
	if n != len(migrations) {
		t.Fatalf("applied migrations = %d, want %d", n, len(migrations))
	}
}

func TestServiceSelection(t *testing.T) {
	r := openTest(t).Services()
	if err := r.SetSelection([]string{"nginx.service", "redis.service", "nginx.service", ""}); err != nil {
		t.Fatal(err)
	}
	got, _ := r.ListEnabled()
	if len(got) != 2 || got[0].UnitName != "nginx.service" || got[1].UnitName != "redis.service" || got[0].DisplayName != "nginx" {
		t.Fatalf("unexpected: %+v", got)
	}
	if err := r.SetSelection([]string{"redis.service", "php8.4-fpm.service"}); err != nil {
		t.Fatal(err)
	}
	en, _ := r.ListEnabled()
	if len(en) != 2 || en[0].UnitName != "redis.service" || en[1].UnitName != "php8.4-fpm.service" {
		t.Fatalf("enabled: %+v", en)
	}
	all, _ := r.List()
	if len(all) != 3 {
		t.Fatalf("all: %+v", all)
	}
	if err := r.SetSelection(nil); err != nil {
		t.Fatal(err)
	}
	if en, _ := r.ListEnabled(); len(en) != 0 {
		t.Fatalf("expected none, got %+v", en)
	}
	r.SetDisplayName("redis.service", "Redis Cache")
	all, _ = r.List()
	for _, s := range all {
		if s.UnitName == "redis.service" && s.DisplayName != "Redis Cache" {
			t.Fatalf("display name: %+v", s)
		}
	}
}

func TestHealthCache(t *testing.T) {
	r := openTest(t).Health()
	if e, err := r.Get("redis"); e != nil || err != nil {
		t.Fatalf("expected nil,nil got %v %v", e, err)
	}
	now := time.Now().UTC().Truncate(time.Nanosecond)
	if err := r.Upsert(HealthEntry{"redis", "OK", `{"a":1}`, now}); err != nil {
		t.Fatal(err)
	}
	if err := r.Upsert(HealthEntry{"redis", "CRIT", `{"a":2}`, now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	r.Upsert(HealthEntry{"kibana", "WARN", `{}`, now})
	e, _ := r.Get("redis")
	if e.Status != "CRIT" || e.PayloadJSON != `{"a":2}` || !e.CheckedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("got %+v", e)
	}
	all, _ := r.All()
	if len(all) != 2 || all[0].ServiceKey != "kibana" {
		t.Fatalf("all: %+v", all)
	}
	if err := r.Upsert(HealthEntry{"x", "BOGUS", "{}", now}); err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestSSHSessions(t *testing.T) {
	r := openTest(t).Sessions()
	t0 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	r.Open(SSHSession{Username: "anna", RemoteIP: "10.0.0.1", TTY: "ssh", PAMService: "sshd", LoginAt: t0})
	id2, _ := r.Open(SSHSession{Username: "anna", RemoteIP: "10.0.0.2", TTY: "ssh", PAMService: "sshd", LoginAt: t0.Add(time.Hour)})
	r.Open(SSHSession{Username: "bob", RemoteIP: "10.0.0.3", TTY: "ssh", PAMService: "sshd", LoginAt: t0.Add(2 * time.Hour)})

	ok, err := r.Close("anna", "ssh", t0.Add(3*time.Hour))
	if err != nil || !ok {
		t.Fatalf("close: %v %v", ok, err)
	}
	list, _ := r.List("anna", 50, 0)
	if len(list) != 2 || list[0].ID != id2 || list[0].LogoutAt == nil || list[1].LogoutAt != nil {
		t.Fatalf("newest open session should be closed: %+v", list)
	}
	if ok, _ := r.Close("nobody", "ssh", t0); ok {
		t.Fatal("close of non-existent session returned true")
	}
	all, _ := r.List("", 50, 0)
	if len(all) != 3 || all[0].Username != "bob" {
		t.Fatalf("all: %+v", all)
	}
	page, _ := r.List("", 1, 1)
	if len(page) != 1 || page[0].ID != id2 {
		t.Fatalf("paging: %+v", page)
	}
}

func TestCommandsInsertDedupAndFilter(t *testing.T) {
	r := openTest(t).Commands()
	t0 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	exit := 0
	mk := func(id, user, cmd string, off int) CLICommand {
		return CLICommand{AuditEventID: id, AUID: 1000, LoginUsername: user, EffectiveUsername: "root",
			CWD: "/root", Command: cmd, ExitCode: &exit, ExecutedAt: t0.Add(time.Duration(off) * time.Second)}
	}
	if ins, err := r.Insert(mk("1.1:1", "anna", "ls -la", 0)); !ins || err != nil {
		t.Fatalf("insert: %v %v", ins, err)
	}
	if ins, _ := r.Insert(mk("1.1:1", "anna", "ls -la", 0)); ins {
		t.Fatal("duplicate audit_event_id should be ignored")
	}
	r.Insert(mk("2.2:2", "bob", "rm -rf /tmp/100%_done", 1))
	r.Insert(CLICommand{AuditEventID: "3.3:3", AUID: 1001, LoginUsername: "anna", EffectiveUsername: "anna",
		CWD: "/", Command: "whoami", ExecutedAt: t0.Add(2 * time.Second)})

	all, _ := r.List("", "", 100, 0)
	if len(all) != 3 || all[0].Command != "whoami" || all[0].ExitCode != nil || all[2].ExitCode == nil {
		t.Fatalf("all: %+v", all)
	}
	if got, _ := r.List("anna", "", 100, 0); len(got) != 2 {
		t.Fatalf("user filter: %+v", got)
	}
	// % i _ muszą być traktowane literalnie
	if got, _ := r.List("", "100%_", 100, 0); len(got) != 1 || got[0].LoginUsername != "bob" {
		t.Fatalf("literal LIKE: %+v", got)
	}
	if got, _ := r.List("", "%", 100, 0); len(got) != 1 {
		t.Fatalf("bare %% should match literally, got %+v", got)
	}
	// próba SQL injection przez parametr jest tylko frazą
	if got, err := r.List("", "'; DROP TABLE cli_command_logs;--", 100, 0); err != nil || len(got) != 0 {
		t.Fatalf("injection: %v %v", got, err)
	}
	if got, _ := r.List("anna", "ls", 100, 0); len(got) != 1 || got[0].AuditEventID != "1.1:1" {
		t.Fatalf("combined: %+v", got)
	}
}

func TestAuditState(t *testing.T) {
	r := openTest(t).AuditState()
	if s, err := r.Get(); s != nil || err != nil {
		t.Fatalf("expected nil,nil: %v %v", s, err)
	}
	r.Set(AuditState{Inode: 12345, Offset: 99})
	r.Set(AuditState{Inode: 1<<63 + 5, Offset: 100}) // inode > MaxInt64 round-trip
	s, _ := r.Get()
	if s.Inode != 1<<63+5 || s.Offset != 100 {
		t.Fatalf("got %+v", s)
	}
}

func TestConcurrentWritesNoBusy(t *testing.T) {
	db := openTest(t)
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				_, err := db.Commands().Insert(CLICommand{AuditEventID: fmt.Sprintf("%d:%d", g, i), AUID: 1000,
					LoginUsername: "u", EffectiveUsername: "u", CWD: "/", Command: "x", ExecutedAt: time.Now()})
				if err != nil {
					errs <- err
				}
				if _, err := db.Commands().List("", "", 10, 0); err != nil {
					errs <- err
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got, _ := db.Commands().List("", "", 1000, 0); len(got) != 200 {
		t.Fatalf("rows = %d", len(got))
	}
}
