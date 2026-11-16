package collector

import (
	"fmt"
	"testing"
)

func failLine(serial int, acct, addr string) string {
	return fmt.Sprintf("type=USER_LOGIN msg=audit(1763278%03d.806:%d): pid=6262 uid=0 auid=4294967295 ses=4294967295 "+
		"msg='op=login acct=\"%s\" exe=\"/usr/sbin/sshd\" hostname=? addr=%s terminal=sshd res=failed'\n", serial%1000, serial, acct, addr)
}

func (e *tailEnv) failures() []string {
	e.t.Helper()
	list, err := e.db.LoginFailures().List("", "", 1000, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for i := len(list) - 1; i >= 0; i-- {
		out = append(out, list[i].Username+"@"+list[i].RemoteIP)
	}
	return out
}

func TestTailRecordsLoginFailuresAlongsideCommands(t *testing.T) {
	e := newTailEnv(t)
	e.tail.Failures = e.db.LoginFailures()
	e.write(e.path, failLine(1, "root", "203.0.113.7")+evLines(2, "cli_audit", "ls")+
		failLine(3, "admin", "203.0.113.7")+evLines(4, "cli_audit", "pwd")+
		"type=USER_LOGIN msg=audit(1763278005.806:5): pid=1 msg='op=login id=1000 exe=\"/usr/sbin/sshd\" addr=1.2.3.4 terminal=ssh res=success'\n", false)
	e.poll()
	eq(t, e.failures(), "root@203.0.113.7", "admin@203.0.113.7") // udane logowanie pominięte
	eq(t, e.commands(), "ls", "pwd")                             // "pwd" zamyka rekord udanego logowania

	e.poll() // ponowny odczyt bez zmian nie duplikuje
	eq(t, e.failures(), "root@203.0.113.7", "admin@203.0.113.7")

	e.write(e.path, failLine(6, "bob", "198.51.100.2"), true)
	e.poll()
	eq(t, e.failures(), "root@203.0.113.7", "admin@203.0.113.7", "bob@198.51.100.2")
}

func TestTailRestartDoesNotDuplicateLoginFailures(t *testing.T) {
	e := newTailEnv(t)
	e.tail.Failures = e.db.LoginFailures()
	// rekord w toku (tu: ostatnia linia pliku) cofa zapisany offset przed siebie, więc po restarcie jest czytany ponownie
	e.write(e.path, evLines(1, "cli_audit", "ls")+evLines(2, "cli_audit", "pwd")+failLine(3, "root", "203.0.113.7"), false)
	e.poll()
	t2 := e.newTailer(e.db.Commands())
	t2.Failures = e.db.LoginFailures()
	e.tail = t2 // "restart"
	e.poll()
	eq(t, e.failures(), "root@203.0.113.7")
}

func TestTailWithoutFailureSinkIgnoresLoginFailures(t *testing.T) {
	e := newTailEnv(t) // Failures == nil
	e.write(e.path, failLine(1, "root", "203.0.113.7")+evLines(2, "cli_audit", "ls")+evLines(3, "cli_audit", "pwd"), false)
	e.poll()
	eq(t, e.commands(), "ls")
	eq(t, e.failures())
}
