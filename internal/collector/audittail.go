package collector

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/user"
	"strconv"
	"sync"
	"syscall"
	"time"

	"server-monitor/internal/parser"
	"server-monitor/internal/storage"
)

// AuditStateStore przechowuje pozycję (inode + offset) śledzonego pliku.
type AuditStateStore interface {
	Get() (*storage.AuditState, error)
	Set(storage.AuditState) error
}

// CommandSink zapisuje komendy; duplikaty (audit_event_id) muszą być ignorowane.
type CommandSink interface {
	Insert(storage.CLICommand) (bool, error)
}

// LoginFailureSink zapisuje nieudane logowania; duplikaty (audit_event_id) muszą być ignorowane.
type LoginFailureSink interface {
	Insert(storage.LoginFailure) (bool, error)
}

// UserResolver mapuje UID na nazwę użytkownika.
type UserResolver interface {
	Username(uid int64) string
}

// SystemUsers rozwiązuje UID przez os/user.LookupId z cache w pamięci.
type SystemUsers struct {
	mu    sync.Mutex
	cache map[int64]string
}

const auditUnsetUID = 4294967295

func (s *SystemUsers) Username(uid int64) string {
	if uid < 0 || uid == auditUnsetUID {
		return "unset"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.cache[uid]; ok {
		return n
	}
	name := strconv.FormatInt(uid, 10)
	if u, err := user.LookupId(name); err == nil {
		name = u.Username
	}
	if s.cache == nil {
		s.cache = map[int64]string{}
	}
	s.cache[uid] = name
	return name
}

// AuditTailer śledzi audit.log: czyta tylko kompletne linie, korelacja wieloliniowa,
// pozycja (inode+offset) zapisywana w SQLite, obsługa rotacji (zmiana inode, wcześniejsze
// dokończenie pliku .1) i obcięcia pliku. Zapis jest idempotentny (UNIQUE audit_event_id),
// więc po awarii ponowne przeczytanie fragmentu nie tworzy duplikatów.
type AuditTailer struct {
	Path     string
	State    AuditStateStore
	Sink     CommandSink
	Failures LoginFailureSink // opcjonalny; nil = rekordy nieudanych logowań są pomijane
	Users    UserResolver
	Interval time.Duration // domyślnie 1 s
	Logf     func(string, ...any)

	corr         *parser.Correlator
	hasPending   bool
	pendingID    string
	pendingStart int64

	// pozycja odczytu w pamięci (nie cofa się za zdarzenie w toku)
	memValid bool
	memInode uint64
	memOff   int64
	// ostatnio zapisana pozycja (żeby nie pisać do SQLite bez zmian)
	persValid bool
	persState storage.AuditState
}

func (t *AuditTailer) logf(f string, a ...any) {
	if t.Logf != nil {
		t.Logf(f, a...)
		return
	}
	log.Printf(f, a...)
}

func (t *AuditTailer) reset() {
	t.corr = parser.NewCorrelator()
	t.hasPending = false
	t.memValid = false
}

func inodeOf(fi os.FileInfo) (uint64, error) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("audit tail: inode unavailable on this platform")
	}
	return st.Ino, nil
}

// Run odpytuje plik do anulowania ctx; błędy loguje (bez powtarzania tego samego komunikatu).
func (t *AuditTailer) Run(ctx context.Context) {
	iv := t.Interval
	if iv <= 0 {
		iv = time.Second
	}
	tk := time.NewTicker(iv)
	defer tk.Stop()
	last := ""
	for {
		if err := t.Poll(); err != nil {
			if msg := err.Error(); msg != last {
				t.logf("audit tail: %v", err)
				last = msg
			}
		} else {
			last = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}

// Poll wykonuje jedną iterację śledzenia. Wywołania nie mogą być współbieżne.
func (t *AuditTailer) Poll() error {
	if t.corr == nil {
		t.reset()
	}
	err := t.poll()
	if err != nil {
		t.reset() // następna iteracja zaczyna od zapisanej pozycji
	}
	return err
}

func (t *AuditTailer) poll() error {
	fi, err := os.Stat(t.Path)
	if err != nil {
		return err
	}
	ino, err := inodeOf(fi)
	if err != nil {
		return err
	}
	var cur storage.AuditState
	if t.memValid {
		cur = storage.AuditState{Inode: t.memInode, Offset: t.memOff}
	} else { // start lub po błędzie: wznów od pozycji zapisanej w SQLite
		saved, err := t.State.Get()
		if err != nil {
			return err
		}
		t.reset()
		cur = storage.AuditState{Inode: ino}
		if saved != nil {
			cur = *saved
			t.persValid, t.persState = true, *saved
		}
	}
	switch {
	case cur.Inode != ino: // rotacja
		if err := t.drainRotated(cur); err != nil {
			return err
		}
		t.reset()
		cur = storage.AuditState{Inode: ino}
	case fi.Size() < cur.Offset: // obcięcie
		t.reset()
		cur.Offset = 0
	}
	end, lines, err := t.consume(t.Path, cur.Offset)
	if err != nil {
		return err
	}
	if lines == 0 && t.hasPending { // cisza: zdarzenie w toku jest kompletne
		if err := t.insert(t.corr.Flush()); err != nil {
			return err
		}
		t.hasPending = false
	}
	t.memValid, t.memInode, t.memOff = true, ino, end
	off := end
	if t.hasPending {
		off = t.pendingStart // po restarcie wznów od początku niedokończonego zdarzenia
	}
	next := storage.AuditState{Inode: ino, Offset: off}
	if t.persValid && t.persState == next {
		return nil
	}
	if err := t.State.Set(next); err != nil {
		return err
	}
	t.persValid, t.persState = true, next
	return nil
}

// drainRotated dokańcza odczyt poprzedniego pliku (audit.log.1), jeśli to ten sam inode.
func (t *AuditTailer) drainRotated(old storage.AuditState) error {
	rot := t.Path + ".1"
	fi, err := os.Stat(rot)
	if err != nil {
		t.logf("audit tail: rotated file %s not found; skipping remainder of old log", rot)
		return nil
	}
	if ino, err := inodeOf(fi); err != nil || ino != old.Inode {
		t.logf("audit tail: %s has a different inode than tracked; skipping remainder of old log", rot)
		return nil
	}
	t.reset()
	if _, _, err := t.consume(rot, old.Offset); err != nil {
		return err
	}
	return t.insert(t.corr.Flush())
}

// consume czyta kompletne linie od offsetu; zwraca pozycję za ostatnią kompletną linią.
func (t *AuditTailer) consume(path string, offset int64) (end int64, lines int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, 0, err
	}
	br := bufio.NewReaderSize(f, 64*1024)
	pos := offset
	for {
		line, rerr := br.ReadString('\n')
		if rerr != nil { // EOF: niekompletna końcówka zostaje na następną iterację
			if rerr == io.EOF {
				return pos, lines, nil
			}
			return pos, lines, rerr
		}
		start := pos
		pos += int64(len(line))
		lines++
		if err := t.insert(t.corr.Feed(line)); err != nil {
			return pos, lines, err
		}
		if err := t.recordLoginFailure(line); err != nil {
			return pos, lines, err
		}
		if id, ok := t.corr.PendingID(); ok {
			if !t.hasPending || id != t.pendingID {
				t.hasPending, t.pendingID, t.pendingStart = true, id, start
			}
		} else {
			t.hasPending = false
		}
	}
}

// recordLoginFailure zapisuje nieudane logowanie z jednoliniowego rekordu USER_LOGIN.
// Ponowne przeczytanie linii (po awarii) jest bezpieczne: UNIQUE audit_event_id.
func (t *AuditTailer) recordLoginFailure(line string) error {
	if t.Failures == nil {
		return nil
	}
	lf, ok := parser.ParseLoginFailure(line)
	if !ok {
		return nil
	}
	_, err := t.Failures.Insert(storage.LoginFailure{
		AuditEventID: lf.EventID, Username: lf.Username, RemoteIP: lf.RemoteAddr,
		Service: lf.Service, AttemptedAt: lf.Time,
	})
	if err != nil {
		return fmt.Errorf("insert login failure %s: %w", lf.EventID, err)
	}
	return nil
}

func (t *AuditTailer) insert(events []parser.CommandEvent) error {
	for _, e := range events {
		c := storage.CLICommand{
			AuditEventID:      e.EventID,
			AUID:              e.AUID,
			LoginUsername:     t.Users.Username(e.AUID),
			EffectiveUsername: t.Users.Username(e.UID),
			CWD:               e.CWD,
			Command:           e.Command,
			ExitCode:          e.ExitCode,
			ExecutedAt:        e.Time,
		}
		if _, err := t.Sink.Insert(c); err != nil {
			return fmt.Errorf("insert %s: %w", e.EventID, err)
		}
	}
	return nil
}
