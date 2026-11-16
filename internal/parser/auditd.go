package parser

import (
	"encoding/hex"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AuditKey to klucz reguł auditd (-k cli_audit) wybierający zdarzenia CLI.
const AuditKey = "cli_audit"

// CommandEvent to skorelowane zdarzenie SYSCALL+EXECVE+CWD.
type CommandEvent struct {
	EventID  string // "1731661200.123:4581"
	Time     time.Time
	AUID     int64 // kto się zalogował
	UID      int64 // z czyimi uprawnieniami uruchomiono
	ExitCode *int
	Success  bool
	CWD      string
	Command  string
}

var (
	msgRe  = regexp.MustCompile(`^type=(\S+) msg=audit\((\d+)\.(\d+):(\d+)\):\s*(.*)$`)
	argKey = regexp.MustCompile(`^a(\d+)(?:\[(\d+)\])?$`)
)

type auditField struct {
	val    string
	quoted bool
}

// tokenize rozbija "k=v k2="a b" k3=HEX" na pola; wartości w cudzysłowie mogą zawierać spacje.
func tokenize(s string) map[string]auditField {
	out := map[string]auditField{}
	i := 0
	for i < len(s) {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		start := i
		for i < len(s) && s[i] != '=' && s[i] != ' ' {
			i++
		}
		if i >= len(s) || s[i] != '=' {
			continue
		}
		key := s[start:i]
		i++
		if i < len(s) && (s[i] == '"' || s[i] == '\'') {
			q := s[i]
			i++
			vs := i
			for i < len(s) && s[i] != q {
				i++
			}
			out[key] = auditField{val: s[vs:i], quoted: q == '"'}
			if i < len(s) {
				i++
			}
			continue
		}
		vs := i
		for i < len(s) && s[i] != ' ' {
			i++
		}
		out[key] = auditField{val: s[vs:i]}
	}
	return out
}

func isHex(s string) bool {
	if s == "" || len(s)%2 != 0 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// decode: wartość w cudzysłowie jest literalna; bez cudzysłowia auditd koduje hex-em.
func (f auditField) decode() string {
	if f.quoted || f.val == "(null)" {
		return f.val
	}
	if isHex(f.val) {
		if b, err := hex.DecodeString(f.val); err == nil {
			return string(b)
		}
	}
	return f.val
}

type argChunk struct {
	chunk int
	val   string
}

type pendingEvent struct {
	id, sec, frac string
	syscall       map[string]auditField
	cwd           string
	args          map[int][]argChunk
	proctitle     string
}

// Correlator skleja wieloliniowe rekordy audit.log w zdarzenia. Plik audit.log
// zwykle nie zawiera rekordów EOE, więc zdarzenie jest zamykane, gdy pojawi się
// rekord o innym ID (albo EOE); na końcu strumienia/przy bezczynności wołaj Flush.
type Correlator struct {
	cur *pendingEvent
}

func NewCorrelator() *Correlator { return &Correlator{} }

// Feed przetwarza jedną linię i zwraca zakończone zdarzenia CLI (zwykle 0 lub 1).
// Linie niepasujące do formatu są ignorowane.
func (c *Correlator) Feed(line string) []CommandEvent {
	m := msgRe.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
	if m == nil {
		return nil
	}
	typ, sec, frac, serial, rest := m[1], m[2], m[3], m[4], m[5]
	id := sec + "." + frac + ":" + serial
	var out []CommandEvent
	if c.cur != nil && c.cur.id != id {
		out = c.emit(out)
	}
	if typ == "EOE" {
		return c.emit(out)
	}
	if c.cur == nil {
		c.cur = &pendingEvent{id: id, sec: sec, frac: frac, args: map[int][]argChunk{}}
	}
	f := tokenize(rest)
	switch typ {
	case "SYSCALL":
		c.cur.syscall = f
	case "CWD":
		if v, ok := f["cwd"]; ok {
			c.cur.cwd = v.decode()
		}
	case "PROCTITLE":
		if v, ok := f["proctitle"]; ok {
			c.cur.proctitle = strings.TrimSpace(strings.ReplaceAll(v.decode(), "\x00", " "))
		}
	case "EXECVE":
		for k, v := range f {
			if am := argKey.FindStringSubmatch(k); am != nil {
				n, _ := strconv.Atoi(am[1])
				ch := 0
				if am[2] != "" {
					ch, _ = strconv.Atoi(am[2])
				}
				c.cur.args[n] = append(c.cur.args[n], argChunk{ch, v.decode()})
			}
		}
	}
	return out
}

// PendingID zwraca ID zdarzenia w toku (jeszcze niezamkniętego); false gdy brak.
func (c *Correlator) PendingID() (string, bool) {
	if c.cur == nil {
		return "", false
	}
	return c.cur.id, true
}

// Flush zamyka zdarzenie w toku.
func (c *Correlator) Flush() []CommandEvent { return c.emit(nil) }

func (c *Correlator) emit(out []CommandEvent) []CommandEvent {
	ev := c.cur
	c.cur = nil
	if ev == nil || ev.syscall == nil {
		return out
	}
	if !strings.Contains(ev.syscall["key"].decode(), AuditKey) {
		return out
	}
	idxs := make([]int, 0, len(ev.args))
	for n := range ev.args {
		idxs = append(idxs, n)
	}
	sort.Ints(idxs)
	parts := make([]string, 0, len(idxs))
	for _, n := range idxs {
		ch := ev.args[n]
		sort.Slice(ch, func(i, j int) bool { return ch[i].chunk < ch[j].chunk })
		var sb strings.Builder
		for _, x := range ch {
			sb.WriteString(x.val)
		}
		parts = append(parts, sb.String())
	}
	cmd := strings.Join(parts, " ")
	if cmd == "" {
		cmd = ev.proctitle
	}
	if cmd == "" {
		return out
	}
	s, _ := strconv.ParseInt(ev.sec, 10, 64)
	fr := (ev.frac + "000000000")[:9]
	ns, _ := strconv.ParseInt(fr, 10, 64)
	ce := CommandEvent{
		EventID: ev.id, Time: time.Unix(s, ns).UTC(), CWD: ev.cwd, Command: cmd,
		Success: ev.syscall["success"].val == "yes",
	}
	ce.AUID, _ = strconv.ParseInt(ev.syscall["auid"].val, 10, 64)
	ce.UID, _ = strconv.ParseInt(ev.syscall["uid"].val, 10, 64)
	if v, ok := ev.syscall["exit"]; ok {
		if n, err := strconv.Atoi(v.val); err == nil {
			ce.ExitCode = &n
		}
	}
	return append(out, ce)
}
