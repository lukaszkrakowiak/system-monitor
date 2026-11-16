package storage

import (
	"testing"
	"time"
)

func TestLoginFailuresInsertDedupListFilter(t *testing.T) {
	db := openTest(t)
	r := db.LoginFailures()
	base := time.Date(2026, 11, 16, 7, 36, 54, 806_000_000, time.UTC)
	rows := []LoginFailure{
		{AuditEventID: "1.1:1", Username: "alice", RemoteIP: "192.0.2.10", Service: "sshd", AttemptedAt: base},
		{AuditEventID: "1.2:2", Username: "admin", RemoteIP: "203.0.113.7", Service: "sshd", AttemptedAt: base.Add(time.Second)},
		{AuditEventID: "1.3:3", Username: "alice", RemoteIP: "203.0.113.7", Service: "sshd", AttemptedAt: base.Add(2 * time.Second)},
	}
	for _, f := range rows {
		if ok, err := r.Insert(f); err != nil || !ok {
			t.Fatalf("insert %s: %v %v", f.AuditEventID, ok, err)
		}
	}
	if ok, err := r.Insert(rows[0]); err != nil || ok { // duplikat
		t.Fatalf("duplicate insert: %v %v", ok, err)
	}

	all, err := r.List("", "", 100, 0)
	if err != nil || len(all) != 3 {
		t.Fatalf("list: %v %v", all, err)
	}
	if all[0].AuditEventID != "1.3:3" || all[2].AuditEventID != "1.1:1" { // najnowsze pierwsze
		t.Errorf("order: %v", all)
	}
	if !all[2].AttemptedAt.Equal(base) {
		t.Errorf("time round trip: %v", all[2].AttemptedAt)
	}
	if l, _ := r.List("alice", "", 100, 0); len(l) != 2 {
		t.Errorf("user filter: %v", l)
	}
	if l, _ := r.List("", "203.0.113.7", 100, 0); len(l) != 2 {
		t.Errorf("ip filter: %v", l)
	}
	if l, _ := r.List("alice", "203.0.113.7", 100, 0); len(l) != 1 || l[0].AuditEventID != "1.3:3" {
		t.Errorf("user+ip filter: %v", l)
	}
	if l, _ := r.List("", "", 1, 1); len(l) != 1 || l[0].AuditEventID != "1.2:2" {
		t.Errorf("paging: %v", l)
	}
	// filtr to dokładne dopasowanie, a nie wzorzec LIKE
	if l, _ := r.List("%", "", 100, 0); len(l) != 0 {
		t.Errorf("wildcard leaked: %v", l)
	}
}
