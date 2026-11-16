package parser

import (
	"path"
	"strconv"
	"strings"
	"time"
)

// LoginFailure to nieudana próba logowania zapisana przez auditd (rekord USER_LOGIN, res=failed),
// np. błędne hasło do sshd. Rekord jest jednoliniowy, więc nie wymaga korelacji.
type LoginFailure struct {
	EventID    string // "1731661200.123:4581"
	Time       time.Time
	Username   string // acct: konto, na które próbowano się zalogować (może nie istnieć)
	RemoteAddr string // addr; puste, gdy auditd zapisał "?"
	Service    string // nazwa programu z exe, np. "sshd"
	Terminal   string
}

// ParseLoginFailure zwraca nieudane logowanie z jednej linii audit.log.
// Zwraca false dla każdej innej linii (inne typy rekordów, udane logowania, śmieci).
// Używamy USER_LOGIN (jeden rekord na próbę); USER_AUTH pomijamy, żeby nie liczyć próby dwa razy.
func ParseLoginFailure(line string) (LoginFailure, bool) {
	m := msgRe.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
	if m == nil || m[1] != "USER_LOGIN" {
		return LoginFailure{}, false
	}
	outer := tokenize(m[5])
	body, ok := outer["msg"]
	if !ok {
		return LoginFailure{}, false
	}
	f := tokenize(body.val)
	if f["op"].val != "login" {
		return LoginFailure{}, false
	}
	// nowe jądra: res=failed, starsze: res=0
	if res := f["res"].val; res != "failed" && res != "0" {
		return LoginFailure{}, false
	}
	s, _ := strconv.ParseInt(m[2], 10, 64)
	ns, _ := strconv.ParseInt((m[3] + "000000000")[:9], 10, 64)
	lf := LoginFailure{
		EventID:    m[2] + "." + m[3] + ":" + m[4],
		Time:       time.Unix(s, ns).UTC(),
		Username:   f["acct"].decode(),
		RemoteAddr: cleanAddr(f["addr"].val),
		Service:    path.Base(f["exe"].decode()),
		Terminal:   f["terminal"].val,
	}
	if lf.RemoteAddr == "" { // niektóre usługi wypełniają tylko hostname
		lf.RemoteAddr = cleanAddr(f["hostname"].val)
	}
	if lf.Username == "" || lf.Username == "?" || lf.Username == "(unknown)" {
		lf.Username = "unknown"
	}
	if lf.Service == "." || lf.Service == "/" {
		lf.Service = ""
	}
	return lf, true
}

func cleanAddr(a string) string {
	if a == "?" || a == "(null)" {
		return ""
	}
	return a
}
