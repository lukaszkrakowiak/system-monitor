package collector

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"server-monitor/internal/storage"
)

type fakeUsers map[int64]string

func (f fakeUsers) Username(uid int64) string {
	if n, ok := f[uid]; ok {
		return n
	}
	return fmt.Sprint(uid)
}

func evLines(serial int, key, cmd string) string {
	id := fmt.Sprintf("audit(1731661%03d.100:%d)", serial%1000, serial)
	return fmt.Sprintf("type=SYSCALL msg=%s: arch=c000003e syscall=59 success=yes exit=0 ppid=1 pid=2 auid=1000 uid=0 comm=\"x\" exe=\"/bin/x\" key=\"%s\"\n"+
		"type=EXECVE msg=%s: argc=1 a0=\"%s\"\n"+
		"type=CWD msg=%s: cwd=\"/root\"\n", id, key, id, cmd, id)
}

type tailEnv struct {
	t    *testing.T
	db   *storage.DB
	path string
	tail *AuditTailer
}

func newTailEnv(t *testing.T) *tailEnv {
	t.Helper()
	dir := t.TempDir()
	db, err := storage.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &tailEnv{t: t, db: db, path: filepath.Join(dir, "audit.log")}
	e.tail = e.newTailer(db.Commands())
	return e
}

func (e *tailEnv) newTailer(sink CommandSink) *AuditTailer {
	return &AuditTailer{Path: e.path, State: e.db.AuditState(), Sink: sink,
		Users: fakeUsers{1000: "anna", 0: "root"}, Logf: func(string, ...any) {}}
}

func (e *tailEnv) write(path, s string, appendMode bool) {
	e.t.Helper()
	flag := os.O_CREATE | os.O_WRONLY
	if appendMode {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flag, 0o640)
	if err != nil {
		e.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		e.t.Fatal(err)
	}
}

func (e *tailEnv) poll() {
	e.t.Helper()
	if err := e.tail.Poll(); err != nil {
		e.t.Fatal(err)
	}
}

func (e *tailEnv) commands() []string {
	e.t.Helper()
	list, err := e.db.Commands().List("", "", 1000, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for i := len(list) - 1; i >= 0; i-- { // chronologicznie
		out = append(out, list[i].Command)
	}
	return out
}

func eq(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTailBasicPendingAndIdleFlush(t *testing.T) {
	e := newTailEnv(t)
	first, second := evLines(1, "cli_audit", "ls"), evLines(2, "cli_audit", "pwd")
	e.write(e.path, first+second, false)
	e.poll()
	// 1. zdarzenie zamknięte przez 2., 2. czeka (brak EOE)
	eq(t, e.commands(), "ls")
	st, _ := e.db.AuditState().Get()
	if st == nil || st.Offset != int64(len(first)) {
		t.Fatalf("offset must stay at start of pending event: %+v (want %d)", st, len(first))
	}
	e.poll() // brak nowych linii -> flush
	eq(t, e.commands(), "ls", "pwd")
	st, _ = e.db.AuditState().Get()
	if st.Offset != int64(len(first+second)) {
		t.Fatalf("offset %d", st.Offset)
	}
	e.poll() // nic się nie zmienia, brak duplikatów
	eq(t, e.commands(), "ls", "pwd")
}

func TestTailFiltersOtherKeysAndMapsUsers(t *testing.T) {
	e := newTailEnv(t)
	e.write(e.path, evLines(1, "other", "skip")+evLines(2, "cli_audit", "keep"), false)
	e.poll()
	e.poll()
	list, _ := e.db.Commands().List("", "", 10, 0)
	if len(list) != 1 || list[0].Command != "keep" || list[0].LoginUsername != "anna" || list[0].EffectiveUsername != "root" ||
		list[0].AUID != 1000 || list[0].CWD != "/root" || list[0].ExitCode == nil {
		t.Fatalf("%+v", list)
	}
}

func TestTailPartialLineWaitsForNewline(t *testing.T) {
	e := newTailEnv(t)
	full := evLines(1, "cli_audit", "whoami")
	cut := len(full) - 10
	e.write(e.path, full[:cut], false) // ostatnia linia (CWD) niekompletna
	e.poll()
	e.poll()
	// SYSCALL+EXECVE przeczytane, ale CWD jeszcze nie -> flush przy ciszy zapisałby zdarzenie bez cwd;
	// kluczowe: niekompletna linia nie została skonsumowana
	st, _ := e.db.AuditState().Get()
	if st == nil || st.Offset > int64(cut) {
		t.Fatalf("offset %+v beyond complete lines (cut=%d)", st, cut)
	}
}

func TestTailResumeAfterRestartNoDuplicates(t *testing.T) {
	e := newTailEnv(t)
	e.write(e.path, evLines(1, "cli_audit", "a")+evLines(2, "cli_audit", "b"), false)
	e.poll() // "a" zapisane, "b" w toku (offset przed "b")
	// restart: nowy tailer, pusty korelator, ten sam stan w SQLite
	e.tail = e.newTailer(e.db.Commands())
	e.write(e.path, evLines(3, "cli_audit", "c"), true)
	e.poll()
	e.poll()
	eq(t, e.commands(), "a", "b", "c")
}

func TestTailRotation(t *testing.T) {
	e := newTailEnv(t)
	e.write(e.path, evLines(1, "cli_audit", "old1"), false)
	e.poll()
	e.poll()
	// po ostatniej iteracji do starego pliku dopisano zdarzenie, potem rotacja
	e.write(e.path, evLines(2, "cli_audit", "old2-late"), true)
	if err := os.Rename(e.path, e.path+".1"); err != nil {
		t.Fatal(err)
	}
	e.write(e.path, evLines(3, "cli_audit", "new1"), false)
	e.poll()
	e.poll()
	eq(t, e.commands(), "old1", "old2-late", "new1")
	e.write(e.path, evLines(4, "cli_audit", "new2"), true)
	e.poll()
	e.poll()
	eq(t, e.commands(), "old1", "old2-late", "new1", "new2")
}

func TestTailRotationWithoutRotatedFileStillFollowsNew(t *testing.T) {
	e := newTailEnv(t)
	e.write(e.path, evLines(1, "cli_audit", "a"), false)
	e.poll()
	e.poll()
	os.Remove(e.path) // stary plik zniknął (np. usunięty), powstaje nowy
	e.write(e.path, evLines(2, "cli_audit", "b"), false)
	e.poll()
	e.poll()
	eq(t, e.commands(), "a", "b")
}

func TestTailTruncation(t *testing.T) {
	e := newTailEnv(t)
	e.write(e.path, evLines(1, "cli_audit", "long-command-name-number-one")+evLines(2, "cli_audit", "two"), false)
	e.poll()
	e.poll()
	e.write(e.path, evLines(3, "cli_audit", "x"), false) // O_TRUNC, ten sam inode, mniejszy rozmiar
	e.poll()
	e.poll()
	eq(t, e.commands(), "long-command-name-number-one", "two", "x")
}

type flakySink struct {
	inner CommandSink
	fails atomic.Int32
}

func (f *flakySink) Insert(c storage.CLICommand) (bool, error) {
	if f.fails.Add(-1) >= 0 {
		return false, errors.New("db busy")
	}
	return f.inner.Insert(c)
}

func TestTailSinkErrorIsRetriedWithoutLoss(t *testing.T) {
	e := newTailEnv(t)
	sink := &flakySink{inner: e.db.Commands()}
	sink.fails.Store(1)
	e.tail = e.newTailer(sink)
	e.write(e.path, evLines(1, "cli_audit", "a")+evLines(2, "cli_audit", "b"), false)
	if err := e.tail.Poll(); err == nil {
		t.Fatal("expected sink error")
	}
	if st, _ := e.db.AuditState().Get(); st != nil && st.Offset != 0 {
		t.Fatalf("offset advanced despite failure: %+v", st)
	}
	e.poll()
	e.poll()
	eq(t, e.commands(), "a", "b")
}

func TestTailMissingFileIsError(t *testing.T) {
	e := newTailEnv(t)
	if err := e.tail.Poll(); err == nil {
		t.Fatal("expected error for missing audit.log")
	}
	e.write(e.path, evLines(1, "cli_audit", "a"), false)
	e.poll()
	e.poll()
	eq(t, e.commands(), "a")
}

func TestTailRunStopsOnCancel(t *testing.T) {
	e := newTailEnv(t)
	e.tail.Interval = 10 * time.Millisecond
	e.write(e.path, evLines(1, "cli_audit", "a"), false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.tail.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(e.commands()) < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
	eq(t, e.commands(), "a")
}

func TestSystemUsersFallbackAndCache(t *testing.T) {
	u := &SystemUsers{}
	if u.Username(4294967295) != "unset" || u.Username(-1) != "unset" {
		t.Fatal("unset uid")
	}
	if got := u.Username(0); got != "root" {
		t.Fatalf("uid 0 -> %q", got)
	}
	if got := u.Username(3999999); got != "3999999" { // nieistniejący UID -> liczba
		t.Fatalf("%q", got)
	}
	if u.cache[3999999] != "3999999" {
		t.Fatal("not cached")
	}
}
