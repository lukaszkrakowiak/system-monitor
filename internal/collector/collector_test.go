package collector

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"server-monitor/internal/storage"
)

func fx(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "parser", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// maxProbe to górna granica czasu sondy z timeoutem (1500 ms + margines).
const maxProbe = ProbeTimeout + 700*time.Millisecond

func TestWorst(t *testing.T) {
	if Worst() != "OK" || Worst("OK", "WARN") != "WARN" || Worst("WARN", "CRIT", "OK") != "CRIT" || Worst("OK", "???") != "CRIT" {
		t.Fatal("Worst")
	}
}

func TestValidUnitName(t *testing.T) {
	good := []string{"nginx.service", "php8.4-fpm.service", "getty@tty1.service", "systemd-fsck@dev-disk-by\\x2duuid.service"}
	bad := []string{"", "nginx", "--help.service", "-x.service", "a b.service", "a;b.service", "../x.service", "nginx.socket", "a\n.service", strings.Repeat("a", 300) + ".service"}
	for _, s := range good {
		if !ValidUnitName(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range bad {
		if ValidUnitName(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}

type fakeRunner struct {
	mu    sync.Mutex
	calls [][]string
	out   map[string][]byte // klucz: pierwszy argument (podkomenda)
	err   error
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.err != nil {
		return nil, f.err
	}
	return f.out[args[0]], nil
}

func TestSystemdListUnitsArgs(t *testing.T) {
	r := &fakeRunner{out: map[string][]byte{"list-units": fx(t, "systemctl_list_units.json")}}
	s := &Systemd{Runner: r}
	u, err := s.ListUnits(context.Background())
	if err != nil || len(u) != 6 {
		t.Fatalf("%v %v", u, err)
	}
	want := []string{"systemctl", "list-units", "--type=service", "--all", "--no-pager", "--output=json"}
	if strings.Join(r.calls[0], " ") != strings.Join(want, " ") {
		t.Fatalf("args %v", r.calls[0])
	}
	r.err = errors.New("boom")
	if _, err := s.ListUnits(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestSystemdShowRejectsInvalidNames(t *testing.T) {
	r := &fakeRunner{}
	s := &Systemd{Runner: r}
	if _, err := s.Show(context.Background(), []string{"ok.service", "--now.service"}); err == nil {
		t.Fatal("expected error")
	}
	if len(r.calls) != 0 {
		t.Fatal("runner must not be called with invalid unit names")
	}
	if got, err := s.Show(context.Background(), nil); got != nil || err != nil || len(r.calls) != 0 {
		t.Fatal("empty list must be a no-op")
	}
}

func TestSystemdProbe(t *testing.T) {
	r := &fakeRunner{out: map[string][]byte{"show": fx(t, "systemctl_show.txt")}}
	svcs := []storage.MonitoredService{
		{UnitName: "nginx.service", DisplayName: "Nginx"},
		{UnitName: "redis-server.service", DisplayName: "Redis"},
		{UnitName: "php8.4-fpm.service", DisplayName: "PHP"},
		{UnitName: "ghost.service", DisplayName: "Ghost"},
		{UnitName: "missing.service", DisplayName: "Missing"},
	}
	p := &SystemdProbe{
		Systemd: &Systemd{Runner: r},
		Units:   func() ([]storage.MonitoredService, error) { return svcs, nil },
		Now:     func() time.Time { return time.Date(2026, 11, 15, 10, 30, 0, 0, time.UTC) },
		Loc:     time.UTC,
	}
	res := p.Check(context.Background())
	if res.Status != "CRIT" || p.Key() != "systemd" {
		t.Fatalf("status %s", res.Status)
	}
	pl := res.Payload.(SystemdPayload)
	if len(pl.Units) != 5 || pl.Error != "" {
		t.Fatalf("%+v", pl)
	}
	n := pl.Units[0]
	if n.Status != "OK" || n.Active != "active" || n.Sub != "running" || n.MemoryBytes == nil || *n.MemoryBytes != 18432000 ||
		n.Uptime != "11d 4h" || n.DisplayName != "Nginx" {
		t.Fatalf("nginx %+v", n)
	}
	if pl.Units[1].MemoryBytes != nil || pl.Units[1].Uptime != "2h 0m" {
		t.Fatalf("redis %+v", pl.Units[1])
	}
	if pl.Units[2].Status != "WARN" || pl.Units[2].Uptime != "" || pl.Units[3].Status != "CRIT" || pl.Units[4].Status != "WARN" {
		t.Fatalf("%+v", pl.Units)
	}
	// kontrakt JSON: memory_bytes null gdy brak
	b, _ := json.Marshal(pl.Units[1])
	if !strings.Contains(string(b), `"memory_bytes":null`) {
		t.Fatalf("json %s", b)
	}
	// polecenie show dostaje wszystkie unity jednym wywołaniem
	if len(r.calls) != 1 || r.calls[0][1] != "show" || len(r.calls[0]) != 3+len(svcs) {
		t.Fatalf("calls %v", r.calls)
	}
}

func TestSystemdProbeFailures(t *testing.T) {
	p := &SystemdProbe{Systemd: &Systemd{Runner: &fakeRunner{err: errors.New("exec failed")}},
		Units: func() ([]storage.MonitoredService, error) {
			return []storage.MonitoredService{{UnitName: "a.service"}}, nil
		}}
	res := p.Check(context.Background())
	pl := res.Payload.(SystemdPayload)
	if res.Status != "CRIT" || pl.Error == "" || pl.Units == nil {
		t.Fatalf("%+v", res)
	}
	p.Units = func() ([]storage.MonitoredService, error) { return nil, errors.New("db down") }
	if res := p.Check(context.Background()); res.Status != "CRIT" {
		t.Fatal("db error must be CRIT")
	}
	p.Units = func() ([]storage.MonitoredService, error) { return nil, nil }
	if res := p.Check(context.Background()); res.Status != "OK" || len(res.Payload.(SystemdPayload).Units) != 0 {
		t.Fatalf("no units selected should be OK: %+v", res)
	}
}

func unixServer(t *testing.T, h http.Handler) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return path
}

func TestSupervisorProbe(t *testing.T) {
	var gotBody, gotMethod string
	sock := unixServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotMethod = string(b), r.Method
		w.Header().Set("Content-Type", "text/xml")
		w.Write(fx(t, "supervisor_getallprocessinfo.xml"))
	}))
	res := (&SupervisorProbe{&Supervisor{SocketPath: sock}}).Check(context.Background())
	pl := res.Payload.(SupervisorPayload)
	if gotMethod != "POST" || !strings.Contains(gotBody, "supervisor.getAllProcessInfo") {
		t.Fatalf("request %s %s", gotMethod, gotBody)
	}
	if res.Status != "CRIT" || len(pl.Groups) != 5 || pl.Error != "" { // cron-sync DOWN -> CRIT
		t.Fatalf("%s %+v", res.Status, pl)
	}
}

func TestSupervisorFaultAndMissingSocket(t *testing.T) {
	sock := unixServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fx(t, "supervisor_fault.xml"))
	}))
	res := (&SupervisorProbe{&Supervisor{SocketPath: sock}}).Check(context.Background())
	if res.Status != "CRIT" || !strings.Contains(res.Payload.(SupervisorPayload).Error, "SHUTDOWN_STATE") {
		t.Fatalf("%+v", res)
	}
	res = (&SupervisorProbe{&Supervisor{SocketPath: filepath.Join(t.TempDir(), "none.sock")}}).Check(context.Background())
	if res.Status != "CRIT" || res.Payload.(SupervisorPayload).Error == "" {
		t.Fatalf("%+v", res)
	}
}

func TestSupervisorAllRunningIsOK(t *testing.T) {
	xml := `<methodResponse><params><param><value><array><data>
<value><struct><member><name>name</name><value><string>w_00</string></value></member><member><name>group</name><value><string>w</string></value></member><member><name>statename</name><value><string>RUNNING</string></value></member><member><name>pid</name><value><int>1</int></value></member></struct></value>
</data></array></value></param></params></methodResponse>`
	sock := unixServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, xml) }))
	if res := (&SupervisorProbe{&Supervisor{SocketPath: sock}}).Check(context.Background()); res.Status != "OK" {
		t.Fatalf("%+v", res)
	}
}

func TestElasticsearchProbe(t *testing.T) {
	var gotPath, gotAuth, gotUser string
	body := fx(t, "es_health_green.json")
	code := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		gotUser, _, _ = r.BasicAuth()
		w.WriteHeader(code)
		w.Write(body)
	}))
	defer srv.Close()

	es := &Elasticsearch{URL: srv.URL + "/"}
	res := es.Check(context.Background())
	pl := res.Payload.(ElasticsearchPayload)
	if res.Status != "OK" || gotPath != "/_cluster/health" || pl.ClusterName != "prod-es" || pl.Nodes != 3 || pl.ClusterStatus != "green" {
		t.Fatalf("%+v %s", pl, gotPath)
	}
	if gotAuth != "" {
		t.Fatal("no auth expected")
	}

	es.Auth = HTTPAuth{Username: "elastic", Password: "pw"}
	es.Check(context.Background())
	if gotUser != "elastic" {
		t.Fatalf("basic auth user %q", gotUser)
	}
	es.Auth = HTTPAuth{Username: "elastic", Password: "pw", APIKey: "KEY123"}
	es.Check(context.Background())
	if gotAuth != "ApiKey KEY123" {
		t.Fatalf("auth %q", gotAuth)
	}

	body = fx(t, "es_health_red.json")
	if res := es.Check(context.Background()); res.Status != "CRIT" || res.Payload.(ElasticsearchPayload).UnassignedShards != 14 {
		t.Fatalf("%+v", res)
	}
	code, body = 401, fx(t, "es_unauthorized.json")
	if res := es.Check(context.Background()); res.Status != "CRIT" || !strings.Contains(res.Payload.(ElasticsearchPayload).Error, "401") {
		t.Fatalf("%+v", res)
	}
}

func blockingServer(t *testing.T) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	return srv
}

func TestProbesHonorTimeout(t *testing.T) {
	srv := blockingServer(t)
	hung, err := net.Listen("tcp", "127.0.0.1:0") // przyjmuje połączenia (backlog) i milczy
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hung.Close() }) // nie defer: podtesty równoległe startują po powrocie funkcji
	go func() {
		var conns []net.Conn
		defer func() {
			for _, c := range conns {
				c.Close()
			}
		}()
		for {
			c, err := hung.Accept()
			if err != nil {
				return
			}
			conns = append(conns, c)
		}
	}()
	sock := unixServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // bez odczytu body serwer nie wykrywa zamknięcia połączenia
		<-r.Context().Done()
	}))

	probes := map[string]Probe{
		"es":         &Elasticsearch{URL: srv.URL},
		"kibana":     &Kibana{URL: srv.URL},
		"redis":      &Redis{Addr: hung.Addr().String()},
		"supervisor": &SupervisorProbe{&Supervisor{SocketPath: sock}},
	}
	for name, p := range probes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			start := time.Now()
			res := p.Check(context.Background())
			el := time.Since(start)
			if res.Status != "CRIT" {
				t.Fatalf("status %s", res.Status)
			}
			if el < ProbeTimeout-100*time.Millisecond || el > maxProbe {
				t.Fatalf("elapsed %v, want ≈%v", el, ProbeTimeout)
			}
		})
	}
}

func TestKibanaProbe(t *testing.T) {
	var path, auth string
	body := fx(t, "kibana_status_8x.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		w.Write(body)
	}))
	defer srv.Close()
	k := &Kibana{URL: srv.URL, Auth: HTTPAuth{APIKey: "K"}}
	res := k.Check(context.Background())
	pl := res.Payload.(KibanaPayload)
	if res.Status != "OK" || path != "/api/status" || pl.OverallLevel != "available" || pl.ResponseMS < 0 || auth != "ApiKey K" {
		t.Fatalf("%+v %s", pl, path)
	}
	body = fx(t, "kibana_status_degraded.json")
	if res := k.Check(context.Background()); res.Status != "WARN" || res.Payload.(KibanaPayload).OverallLevel != "degraded" {
		t.Fatalf("%+v", res)
	}
	body = []byte("<html>")
	if res := k.Check(context.Background()); res.Status != "CRIT" {
		t.Fatalf("%+v", res)
	}
	srv.Close()
	if res := k.Check(context.Background()); res.Status != "CRIT" || res.Payload.(KibanaPayload).Error == "" {
		t.Fatalf("%+v", res)
	}
}

// fakeRedis to minimalny serwer RESP; handler dostaje argumenty komendy i zwraca surową odpowiedź.
func fakeRedis(t *testing.T, handler func(args []string) []byte) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					line, err := br.ReadString('\n')
					if err != nil || !strings.HasPrefix(line, "*") {
						return
					}
					n, _ := strconv.Atoi(strings.TrimSpace(line[1:]))
					args := make([]string, 0, n)
					for i := 0; i < n; i++ {
						hl, _ := br.ReadString('\n')
						ln, _ := strconv.Atoi(strings.TrimSpace(hl[1:]))
						buf := make([]byte, ln+2)
						if _, err := io.ReadFull(br, buf); err != nil {
							return
						}
						args = append(args, string(buf[:ln]))
					}
					c.Write(handler(args))
				}
			}()
		}
	}()
	return l.Addr().String()
}

func TestRedisProbe(t *testing.T) {
	info := fx(t, "redis_resp_info.bin")
	var mu sync.Mutex
	var seen [][]string
	addr := fakeRedis(t, func(a []string) []byte {
		mu.Lock()
		seen = append(seen, a)
		mu.Unlock()
		switch strings.ToUpper(a[0]) {
		case "AUTH":
			if a[1] == "secret" {
				return []byte("+OK\r\n")
			}
			return []byte("-WRONGPASS invalid password\r\n")
		case "PING":
			return []byte("+PONG\r\n")
		case "INFO":
			return info
		}
		return []byte("-ERR unknown\r\n")
	})
	res := (&Redis{Addr: addr, Password: "secret"}).Check(context.Background())
	pl := res.Payload.(RedisPayload)
	if res.Status != "OK" || pl.UsedMemoryHuman != "142.50M" || pl.ConnectedClients != 28 || pl.UptimeSeconds != 864000 || pl.PingMS < 0 {
		t.Fatalf("%+v", res)
	}
	if len(seen) != 3 || seen[0][0] != "AUTH" || seen[1][0] != "PING" || strings.Join(seen[2], " ") != "INFO server memory clients" {
		t.Fatalf("commands %v", seen)
	}
	// bez hasła -> brak AUTH, PING przechodzi
	if res := (&Redis{Addr: addr}).Check(context.Background()); res.Status != "OK" {
		t.Fatalf("%+v", res)
	}
	// złe hasło
	res = (&Redis{Addr: addr, Password: "bad"}).Check(context.Background())
	if res.Status != "CRIT" || !strings.Contains(res.Payload.(RedisPayload).Error, "WRONGPASS") {
		t.Fatalf("%+v", res)
	}
}

func TestRedisInfoFallbackAndBadPing(t *testing.T) {
	info := fx(t, "redis_resp_info.bin")
	addr := fakeRedis(t, func(a []string) []byte {
		switch a[0] {
		case "PING":
			return []byte("+PONG\r\n")
		case "INFO":
			if len(a) > 1 {
				return []byte("-ERR syntax error\r\n") // Redis < 7
			}
			return info
		}
		return []byte("-ERR\r\n")
	})
	if res := (&Redis{Addr: addr}).Check(context.Background()); res.Status != "OK" {
		t.Fatalf("fallback to plain INFO failed: %+v", res)
	}
	noauth := fakeRedis(t, func(a []string) []byte { return fx(t, "redis_resp_noauth.bin") })
	if res := (&Redis{Addr: noauth}).Check(context.Background()); res.Status != "CRIT" {
		t.Fatalf("%+v", res)
	}
}

func TestRedisConnectionRefusedAndUnix(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	start := time.Now()
	res := (&Redis{Addr: addr}).Check(context.Background())
	if res.Status != "CRIT" || res.Payload.(RedisPayload).Error == "" || time.Since(start) > maxProbe {
		t.Fatalf("%+v", res)
	}
	if res := (&Redis{Network: "unix", Addr: filepath.Join(t.TempDir(), "none.sock")}).Check(context.Background()); res.Status != "CRIT" {
		t.Fatalf("%+v", res)
	}
}

func TestRoundMS(t *testing.T) {
	if roundMS(1234*time.Microsecond) != 1.23 || roundMS(0) != 0 {
		t.Fatal(roundMS(1234 * time.Microsecond))
	}
}
