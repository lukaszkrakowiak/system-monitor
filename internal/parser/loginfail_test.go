package parser

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseLoginFailureFixture(t *testing.T) {
	b, err := os.ReadFile("fixtures/audit_login_failed.log")
	if err != nil {
		t.Fatal(err)
	}
	var got []LoginFailure
	for _, line := range strings.Split(string(b), "\n") {
		if lf, ok := ParseLoginFailure(line); ok {
			got = append(got, lf)
		}
	}
	// USER_AUTH (failed), udane logowanie i sudo muszą zostać pominięte.
	want := []LoginFailure{
		{EventID: "1763278614.806:455", Username: "alice", RemoteAddr: "192.0.2.10", Service: "sshd", Terminal: "sshd"},
		{EventID: "1763278617.742:457", Username: "admin", RemoteAddr: "203.0.113.7", Service: "sshd", Terminal: "sshd"},
		{EventID: "1763278620.206:458", Username: "user 1+2", RemoteAddr: "2001:db8::1", Service: "sshd", Terminal: "sshd"}, // hex
		{EventID: "1763278625.100:459", Username: "unknown", RemoteAddr: "", Service: "sshd", Terminal: "sshd"},
		{EventID: "1763278650.001:462", Username: "old", RemoteAddr: "198.51.100.9", Service: "sshd", Terminal: "sshd"}, // res=0
	}
	if len(got) != len(want) {
		t.Fatalf("got %d failures, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		g := got[i]
		w := want[i]
		w.Time = g.Time // czas sprawdzamy osobno
		if g != w {
			t.Errorf("[%d] got %+v, want %+v", i, g, w)
		}
	}
	if !got[0].Time.Equal(time.Unix(1763278614, 806_000_000)) {
		t.Errorf("time = %v", got[0].Time)
	}
}

func TestParseLoginFailureIgnoresOtherLines(t *testing.T) {
	for _, l := range []string{
		"",
		"garbage",
		`type=SYSCALL msg=audit(1763278614.806:455): arch=c000003e syscall=59 success=yes exit=0 auid=1000 key="cli_audit"`,
		`type=USER_LOGIN msg=audit(1763278614.806:455): pid=1 msg='op=login acct="x" exe="/usr/sbin/sshd" addr=1.2.3.4 res=success'`,
		`type=USER_LOGIN msg=audit(1763278614.806:455): pid=1 msg='op=logout acct="x" res=failed'`,
		`type=USER_LOGIN msg=audit(1763278614.806:455): pid=1`,
	} {
		if lf, ok := ParseLoginFailure(l); ok {
			t.Errorf("%q parsed as failure: %+v", l, lf)
		}
	}
}

func TestParseLoginFailureHostnameFallbackAndCRLF(t *testing.T) {
	lf, ok := ParseLoginFailure("type=USER_LOGIN msg=audit(1763278614.5:9): pid=1 msg='op=login acct=\"bob\" exe=\"/usr/bin/login\" hostname=10.0.0.5 addr=? terminal=tty1 res=failed'\r\n")
	if !ok || lf.RemoteAddr != "10.0.0.5" || lf.Service != "login" || lf.Username != "bob" || lf.EventID != "1763278614.5:9" {
		t.Fatalf("%v %+v", ok, lf)
	}
}
