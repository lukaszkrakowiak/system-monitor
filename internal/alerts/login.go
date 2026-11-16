package alerts

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// Finding to powód powiadomienia o logowaniu.
type Finding struct {
	Kind   string // login_time | login_ip
	Reason string
}

// inWindow sprawdza, czy chwila t (w strefie ustawień) mieści się w oknie.
func inWindow(t time.Time, w Window) bool {
	day := dayNames[t.Weekday()]
	match := false
	for _, d := range w.Days {
		if d == day {
			match = true
			break
		}
	}
	if !match {
		return false
	}
	from, err1 := parseClock(w.From)
	to, err2 := parseClock(w.To)
	if err1 != nil || err2 != nil {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	switch {
	case from == to:
		return true // cały dzień
	case from < to:
		return m >= from && m < to
	default: // przez północ
		return m >= from || m < to
	}
}

func describeWindow(w Window) string {
	days := strings.Join(w.Days, ", ")
	if w.From == w.To {
		return fmt.Sprintf("%s, all day", days)
	}
	return fmt.Sprintf("%s %s–%s", days, w.From, w.To)
}

// ipAllowed sprawdza adres względem białej listy. Adresy IPv4 zmapowane w IPv6 są sprowadzane do IPv4.
func ipAllowed(ip string, allowed []string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false // nieparsowalny adres traktujemy jak nieznany
	}
	addr = addr.Unmap()
	for _, a := range allowed {
		if strings.Contains(a, "/") {
			if p, err := netip.ParsePrefix(a); err == nil && p.Contains(addr) {
				return true
			}
		} else if x, err := netip.ParseAddr(a); err == nil && x.Unmap() == addr {
			return true
		}
	}
	return false
}

// LoginFindings zwraca powody, dla których udane logowanie SSH wymaga powiadomienia (pusta lista = nic).
// Logowania bez adresu zdalnego (konsola lokalna) nie podlegają regule białej listy IP.
func (s Settings) LoginFindings(remoteIP string, at time.Time) []Finding {
	var out []Finding
	if s.LoginTime.Enabled {
		local := at.In(s.Location())
		for _, w := range s.LoginTime.Windows {
			if inWindow(local, w) {
				out = append(out, Finding{"login_time", fmt.Sprintf("login at %s falls in the alert window (%s)",
					local.Format("Mon 15:04 MST"), describeWindow(w))})
				break
			}
		}
	}
	if s.LoginIP.Enabled && strings.TrimSpace(remoteIP) != "" && !ipAllowed(remoteIP, s.LoginIP.Allowed) {
		out = append(out, Finding{"login_ip", fmt.Sprintf("source IP %s is not on the allowed list", remoteIP)})
	}
	return out
}
