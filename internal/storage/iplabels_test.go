package storage

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeIP(t *testing.T) {
	for in, want := range map[string]string{
		"192.0.2.10":            "192.0.2.10",
		" 192.0.2.10 ":          "192.0.2.10",
		"::ffff:192.0.2.10":     "192.0.2.10",
		"2001:DB8::1":           "2001:db8::1",
		"2001:0db8:0:0:0:0:0:1": "2001:db8::1",
	} {
		if got, ok := NormalizeIP(in); !ok || got != want {
			t.Errorf("NormalizeIP(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "host.example.org", "192.0.2.0/24", "192.0.2", "fe80::1%eth0", "1.2.3.4; DROP"} {
		if got, ok := NormalizeIP(bad); ok {
			t.Errorf("NormalizeIP(%q) = %q, want rejection", bad, got)
		}
	}
}

func TestCleanIPLabel(t *testing.T) {
	if got, err := CleanIPLabel("  mój komputer  "); err != nil || got != "mój komputer" {
		t.Errorf("trim/unicode: %q %v", got, err)
	}
	if _, err := CleanIPLabel(strings.Repeat("ż", MaxIPLabelLen)); err != nil {
		t.Errorf("max length in runes must be accepted: %v", err)
	}
	for name, bad := range map[string]string{
		"empty":    "  ",
		"too long": strings.Repeat("a", MaxIPLabelLen+1),
		"newline":  "a\nb",
		"crlf":     "x\r\nBcc: evil@example.org",
		"nul":      "a\x00b",
		"bad utf8": "a\xffb",
	} {
		if got, err := CleanIPLabel(bad); err == nil {
			t.Errorf("%s: accepted as %q", name, got)
		}
	}
}

func TestIPLabelRepoSetListDelete(t *testing.T) {
	db := openTest(t)
	r := db.IPLabels()

	if l, err := r.List(); err != nil || len(l) != 0 {
		t.Fatalf("empty list: %v %v", l, err)
	}
	if err := r.Set("192.0.2.10", "pnl", t0); err != nil {
		t.Fatal(err)
	}
	if err := r.Set("198.51.100.7", "office", t0); err != nil {
		t.Fatal(err)
	}
	if err := r.Set("192.0.2.10", "panel", t0.Add(time.Minute)); err != nil { // upsert
		t.Fatal(err)
	}
	l, err := r.List()
	if err != nil || len(l) != 2 || l[0].IP != "192.0.2.10" || l[0].Label != "panel" || !l[0].UpdatedAt.Equal(t0.Add(time.Minute)) || l[1].Label != "office" {
		t.Fatalf("list: %+v %v", l, err)
	}
	if err := r.Delete("192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete("203.0.113.1"); err != nil { // nieistniejąca: bez błędu
		t.Fatal(err)
	}
	if l, _ := r.List(); len(l) != 1 || l[0].IP != "198.51.100.7" {
		t.Fatalf("after delete: %+v", l)
	}
}
