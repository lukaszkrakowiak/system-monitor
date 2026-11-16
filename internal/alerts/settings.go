// Package alerts ocenia zdarzenia (logowania SSH, stany usług) według reguł użytkownika i kolejkuje
// powiadomienia do wysłania przez wybrane konektory (internal/notify).
package alerts

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"
)

// Settings to reguły alertów (jeden dokument JSON w tabeli alert_settings).
type Settings struct {
	Timezone     string           `json:"timezone"` // nazwa IANA, np. "Europe/Warsaw"; pusta = strefa serwera
	Channels     []string         `json:"channels"` // typy konektorów, którymi wysyłamy (np. "email")
	LoginTime    LoginTimeRule    `json:"login_time"`
	LoginIP      LoginIPRule      `json:"login_ip"`
	ServiceState ServiceStateRule `json:"service_state"`
}

// LoginTimeRule: alert, gdy udane logowanie SSH wypada w jednym z okien czasowych.
type LoginTimeRule struct {
	Enabled bool     `json:"enabled"`
	Windows []Window `json:"windows"`
}

// Window to okno alarmowe. Dla każdego z wybranych dni tygodnia (dzień kalendarzowy chwili logowania,
// w strefie z ustawień) obejmuje godziny od From do To. From > To oznacza przejście przez północ
// (np. 17:00–06:00 = 17:00–24:00 i 00:00–06:00 tego samego dnia); From == To oznacza cały dzień.
type Window struct {
	Days []string `json:"days"` // mon, tue, wed, thu, fri, sat, sun
	From string   `json:"from"` // HH:MM
	To   string   `json:"to"`   // HH:MM
}

// LoginIPRule: alert, gdy logowanie pochodzi z adresu spoza białej listy.
type LoginIPRule struct {
	Enabled bool     `json:"enabled"`
	Allowed []string `json:"allowed"` // adresy IP lub sieci CIDR
}

// ServiceStateRule: alert, gdy obserwowana usługa ma stan inny niż OK.
type ServiceStateRule struct {
	Enabled        bool `json:"enabled"`
	NotifyRecovery bool `json:"notify_recovery"` // powiadom też o powrocie do OK
	ConfirmChecks  int  `json:"confirm_checks"`  // ile kolejnych sprawdzeń potwierdza zmianę stanu (1–10)
}

const (
	maxWindows = 20
	maxAllowed = 200
)

var dayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"} // indeks = time.Weekday

// Default zwraca ustawienia startowe: wszystko wyłączone, żadnego kanału.
func Default() Settings {
	return Settings{
		Channels:     []string{},
		LoginTime:    LoginTimeRule{Windows: []Window{}},
		LoginIP:      LoginIPRule{Allowed: []string{}},
		ServiceState: ServiceStateRule{NotifyRecovery: true, ConfirmChecks: 2},
	}
}

func parseClock(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("time %q must be HH:MM", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Normalize waliduje ustawienia i zwraca je w postaci kanonicznej. knownChannel mówi, czy typ konektora istnieje.
func (s Settings) Normalize(knownChannel func(string) bool) (Settings, error) {
	out := Default()
	out.Timezone = strings.TrimSpace(s.Timezone)
	if out.Timezone != "" {
		if _, err := time.LoadLocation(out.Timezone); err != nil {
			return s, fmt.Errorf("unknown timezone %q (use an IANA name, e.g. Europe/Warsaw)", out.Timezone)
		}
	}

	seen := map[string]bool{}
	for _, c := range s.Channels {
		c = strings.TrimSpace(c)
		if !knownChannel(c) {
			return s, fmt.Errorf("unknown notification channel %q", c)
		}
		if !seen[c] {
			seen[c] = true
			out.Channels = append(out.Channels, c)
		}
	}

	out.LoginTime.Enabled = s.LoginTime.Enabled
	if len(s.LoginTime.Windows) > maxWindows {
		return s, fmt.Errorf("too many time windows (max %d)", maxWindows)
	}
	for i, w := range s.LoginTime.Windows {
		if _, err := parseClock(w.From); err != nil {
			return s, fmt.Errorf("window %d: %w", i+1, err)
		}
		if _, err := parseClock(w.To); err != nil {
			return s, fmt.Errorf("window %d: %w", i+1, err)
		}
		days := map[string]bool{}
		for _, d := range w.Days {
			d = strings.ToLower(strings.TrimSpace(d))
			if dayIndex(d) < 0 {
				return s, fmt.Errorf("window %d: unknown day %q", i+1, d)
			}
			days[d] = true
		}
		if len(days) == 0 {
			return s, fmt.Errorf("window %d: select at least one day", i+1)
		}
		nw := Window{From: strings.TrimSpace(w.From), To: strings.TrimSpace(w.To), Days: []string{}}
		for _, d := range dayNames[1:] { // kolejność pon..niedz
			if days[d] {
				nw.Days = append(nw.Days, d)
			}
		}
		if days["sun"] {
			nw.Days = append(nw.Days, "sun")
		}
		out.LoginTime.Windows = append(out.LoginTime.Windows, nw)
	}
	if out.LoginTime.Enabled && len(out.LoginTime.Windows) == 0 {
		return s, errors.New("login time rule is enabled but has no time windows")
	}

	out.LoginIP.Enabled = s.LoginIP.Enabled
	if len(s.LoginIP.Allowed) > maxAllowed {
		return s, fmt.Errorf("too many allowed addresses (max %d)", maxAllowed)
	}
	dup := map[string]bool{}
	for _, a := range s.LoginIP.Allowed {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		n, err := normalizeNet(a)
		if err != nil {
			return s, fmt.Errorf("allowed address %q: %w", a, err)
		}
		if !dup[n] {
			dup[n] = true
			out.LoginIP.Allowed = append(out.LoginIP.Allowed, n)
		}
	}
	sort.Strings(out.LoginIP.Allowed)
	if out.LoginIP.Enabled && len(out.LoginIP.Allowed) == 0 {
		return s, errors.New("login IP rule is enabled but the allowed list is empty (every login would alert)")
	}

	out.ServiceState = s.ServiceState
	if out.ServiceState.ConfirmChecks < 1 || out.ServiceState.ConfirmChecks > 10 {
		return s, errors.New("confirm_checks must be between 1 and 10")
	}
	return out, nil
}

func dayIndex(d string) int {
	for i, n := range dayNames {
		if n == d {
			return i
		}
	}
	return -1
}

func normalizeNet(a string) (string, error) {
	if strings.Contains(a, "/") {
		p, err := netip.ParsePrefix(a)
		if err != nil {
			return "", errors.New("not a valid IP address or CIDR")
		}
		return p.Masked().String(), nil
	}
	ip, err := netip.ParseAddr(a)
	if err != nil {
		return "", errors.New("not a valid IP address or CIDR")
	}
	return ip.Unmap().String(), nil
}

// Location zwraca strefę czasową ustawień (strefa serwera, gdy pusta lub nieznana).
func (s Settings) Location() *time.Location {
	if s.Timezone != "" {
		if loc, err := time.LoadLocation(s.Timezone); err == nil {
			return loc
		}
	}
	return time.Local
}
