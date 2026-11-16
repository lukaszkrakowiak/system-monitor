package alerts

import (
	"strings"
	"testing"
	"time"
)

func known(n string) bool { return n == "email" || n == "slack" }

func warsaw(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Skip("tzdata unavailable:", err)
	}
	return loc
}

func TestNormalizeValidSettings(t *testing.T) {
	s := Default()
	s.Timezone = " Europe/Warsaw "
	s.Channels = []string{"email", "email"}
	s.LoginTime = LoginTimeRule{Enabled: true, Windows: []Window{
		{Days: []string{"SUN", "fri", "mon", "mon"}, From: "17:00", To: "06:00"},
		{Days: []string{"sat"}, From: "00:00", To: "00:00"},
	}}
	s.LoginIP = LoginIPRule{Enabled: true, Allowed: []string{"192.0.2.10", "10.1.2.3/8", " ::ffff:192.0.2.10 ", "", "2001:db8::1"}}
	n, err := s.Normalize(known)
	if err != nil {
		t.Fatal(err)
	}
	if n.Timezone != "Europe/Warsaw" || len(n.Channels) != 1 {
		t.Errorf("tz/channels: %+v", n)
	}
	if got := strings.Join(n.LoginTime.Windows[0].Days, ","); got != "mon,fri,sun" {
		t.Errorf("days must be deduplicated and ordered Mon..Sun, got %s", got)
	}
	if got := strings.Join(n.LoginIP.Allowed, ","); got != "10.0.0.0/8,192.0.2.10,2001:db8::1" { // CIDR zmaskowany, v4-mapped sprowadzony, duplikat usunięty
		t.Errorf("allowed: %s", got)
	}
}

func TestNormalizeRejectsBadSettings(t *testing.T) {
	mut := func(f func(*Settings)) Settings { s := Default(); f(&s); return s }
	win := Window{Days: []string{"mon"}, From: "17:00", To: "06:00"}
	for name, s := range map[string]Settings{
		"bad timezone":    mut(func(s *Settings) { s.Timezone = "Mars/Base" }),
		"unknown channel": mut(func(s *Settings) { s.Channels = []string{"pager"} }),
		"bad from":        mut(func(s *Settings) { s.LoginTime.Windows = []Window{{Days: []string{"mon"}, From: "25:00", To: "06:00"}} }),
		"bad to":          mut(func(s *Settings) { s.LoginTime.Windows = []Window{{Days: []string{"mon"}, From: "17:00", To: "6"}} }),
		"no days":         mut(func(s *Settings) { s.LoginTime.Windows = []Window{{From: "17:00", To: "06:00"}} }),
		"bad day": mut(func(s *Settings) {
			s.LoginTime.Windows = []Window{{Days: []string{"funday"}, From: "17:00", To: "06:00"}}
		}),
		"enabled, no win": mut(func(s *Settings) { s.LoginTime.Enabled = true }),
		"too many wins": mut(func(s *Settings) {
			for i := 0; i < maxWindows+1; i++ {
				s.LoginTime.Windows = append(s.LoginTime.Windows, win)
			}
		}),
		"bad ip":           mut(func(s *Settings) { s.LoginIP.Allowed = []string{"not-an-ip"} }),
		"bad cidr":         mut(func(s *Settings) { s.LoginIP.Allowed = []string{"10.0.0.0/33"} }),
		"ip enabled, none": mut(func(s *Settings) { s.LoginIP.Enabled = true }),
		"confirm 0":        mut(func(s *Settings) { s.ServiceState.ConfirmChecks = 0 }),
		"confirm 11":       mut(func(s *Settings) { s.ServiceState.ConfirmChecks = 11 }),
	} {
		if _, err := s.Normalize(known); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func at(t *testing.T, loc *time.Location, value string) time.Time {
	t.Helper()
	ts, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// 2026-11-16 to poniedziałek.
func TestLoginTimeWindows(t *testing.T) {
	loc := warsaw(t)
	s := Default()
	s.Timezone = "Europe/Warsaw"
	s.LoginTime = LoginTimeRule{Enabled: true, Windows: []Window{
		{Days: []string{"mon", "tue", "wed", "thu", "fri"}, From: "17:00", To: "06:00"}, // poza godzinami pracy
		{Days: []string{"sat", "sun"}, From: "00:00", To: "00:00"},                      // cały weekend
	}}
	cases := []struct {
		when string
		want bool
	}{
		{"2026-11-16 12:00", false}, // pon w pracy
		{"2026-11-16 16:59", false},
		{"2026-11-16 17:00", true}, // pon 17:00 — początek okna
		{"2026-11-16 23:59", true},
		{"2026-11-17 00:00", true}, // wt tuż po północy
		{"2026-11-17 05:59", true},
		{"2026-11-17 06:00", false}, // koniec okna wyłączny
		{"2026-11-20 22:00", true},  // pt wieczór
		{"2026-11-21 03:00", true},  // sob (weekendowe okno całodniowe)
		{"2026-11-22 12:00", true},  // niedziela w południe
		{"2026-11-23 05:00", true},  // pon rano
	}
	for _, c := range cases {
		got := len(s.LoginFindings("", at(t, loc, c.when))) > 0
		if got != c.want {
			t.Errorf("%s: alert=%v want %v", c.when, got, c.want)
		}
	}
	// ta sama chwila w UTC, ale oceniana w strefie z ustawień
	utc := time.Date(2026, 11, 16, 16, 30, 0, 0, time.UTC) // 17:30 w Warszawie (CET)
	if f := s.LoginFindings("", utc); len(f) != 1 || f[0].Kind != "login_time" || !strings.Contains(f[0].Reason, "Mon 17:30") {
		t.Fatalf("timezone conversion: %+v", f)
	}
	// reguła wyłączona
	s.LoginTime.Enabled = false
	if f := s.LoginFindings("", utc); len(f) != 0 {
		t.Fatalf("disabled rule alerted: %+v", f)
	}
}

func TestLoginTimeWindowBoundariesWithoutWrap(t *testing.T) {
	loc := warsaw(t)
	s := Default()
	s.Timezone = "Europe/Warsaw"
	s.LoginTime = LoginTimeRule{Enabled: true, Windows: []Window{{Days: []string{"mon"}, From: "09:00", To: "10:30"}}}
	for when, want := range map[string]bool{
		"2026-11-16 08:59": false, "2026-11-16 09:00": true, "2026-11-16 10:29": true, "2026-11-16 10:30": false, "2026-11-17 09:30": false,
	} {
		if got := len(s.LoginFindings("", at(t, loc, when))) > 0; got != want {
			t.Errorf("%s: %v want %v", when, got, want)
		}
	}
}

func TestLoginIPWhitelist(t *testing.T) {
	s := Default()
	s.LoginIP = LoginIPRule{Enabled: true, Allowed: []string{"192.0.2.10", "10.0.0.0/8", "2001:db8::/32"}}
	now := time.Now()
	for ip, wantAlert := range map[string]bool{
		"192.0.2.10":         false,
		"10.200.3.4":         false,
		"::ffff:192.0.2.10":  false, // IPv4 zmapowany w IPv6
		"2001:db8:1::5":      false,
		"203.0.113.9":        true,
		"192.0.2.11":         true,
		"2001:dead::1":       true,
		"definitely-no-host": true,  // nieparsowalny adres = nieznany
		"":                   false, // logowanie lokalne bez IP nie podlega regule
		"  ":                 false,
	} {
		f := s.LoginFindings(ip, now)
		if (len(f) > 0) != wantAlert {
			t.Errorf("ip %q: findings %+v, want alert=%v", ip, f, wantAlert)
		}
		if wantAlert && (f[0].Kind != "login_ip" || !strings.Contains(f[0].Reason, "not on the allowed list")) {
			t.Errorf("ip %q: %+v", ip, f)
		}
	}
	s.LoginIP.Enabled = false
	if f := s.LoginFindings("203.0.113.9", now); len(f) != 0 {
		t.Fatalf("disabled rule alerted: %+v", f)
	}
}

func TestLoginBothRulesGiveTwoReasons(t *testing.T) {
	loc := warsaw(t)
	s := Default()
	s.Timezone = "Europe/Warsaw"
	s.LoginTime = LoginTimeRule{Enabled: true, Windows: []Window{{Days: []string{"mon"}, From: "17:00", To: "06:00"}}}
	s.LoginIP = LoginIPRule{Enabled: true, Allowed: []string{"192.0.2.10"}}
	f := s.LoginFindings("203.0.113.9", at(t, loc, "2026-11-16 23:00"))
	if len(f) != 2 || f[0].Kind != "login_time" || f[1].Kind != "login_ip" {
		t.Fatalf("%+v", f)
	}
}
