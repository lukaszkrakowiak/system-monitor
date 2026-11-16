package notify

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP to minimalny serwer SMTP: AUTH PLAIN, MAIL/RCPT/DATA, zapisuje odebrane wiadomości.
type fakeSMTP struct {
	ln       net.Listener
	mu       sync.Mutex
	messages []string
	rcpts    []string
	from     string
	authed   string
	wantUser string
	wantPass string
	rejectTo string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSMTP{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *fakeSMTP) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(c)
	say := func(f string, a ...any) { fmt.Fprintf(c, f+"\r\n", a...) }
	say("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			say("250-fake")
			say("250-AUTH PLAIN")
			say("250 8BITMIME")
		case strings.HasPrefix(up, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			parts := strings.Split(string(raw), "\x00")
			if len(parts) == 3 && parts[1] == s.wantUser && parts[2] == s.wantPass {
				s.mu.Lock()
				s.authed = parts[1]
				s.mu.Unlock()
				say("235 ok")
			} else {
				say("535 bad credentials")
			}
		case strings.HasPrefix(up, "MAIL FROM:"):
			s.mu.Lock()
			s.from = line
			s.mu.Unlock()
			say("250 ok")
		case strings.HasPrefix(up, "RCPT TO:"):
			if s.rejectTo != "" && strings.Contains(line, s.rejectTo) {
				say("550 no such user")
				continue
			}
			s.mu.Lock()
			s.rcpts = append(s.rcpts, line)
			s.mu.Unlock()
			say("250 ok")
		case up == "DATA":
			say("354 go")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				sb.WriteString(l)
			}
			s.mu.Lock()
			s.messages = append(s.messages, sb.String())
			s.mu.Unlock()
			say("250 queued")
		case up == "QUIT":
			say("221 bye")
			return
		default:
			say("502 unsupported")
		}
	}
}

func goodConfig(port int) EmailConfig {
	return EmailConfig{Host: "127.0.0.1", Port: port, Security: "none", Username: "mon", From: "Monitor <monitor@example.org>",
		To: []string{"admin@example.org", "Ops <ops@example.org>"}}
}

func TestEmailSendDeliversToAllRecipientsWithAuth(t *testing.T) {
	srv := newFakeSMTP(t)
	srv.wantUser, srv.wantPass = "mon", "pa55"
	e := &Email{Config: goodConfig(srv.port()), Password: "pa55"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Send(ctx, Message{Subject: "[host] CRIT: nginx — failed", Body: "Line 1\nLine ż 2\n"}); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.authed != "mon" || len(srv.rcpts) != 2 || !strings.Contains(srv.from, "monitor@example.org") {
		t.Fatalf("authed=%q rcpts=%v from=%q", srv.authed, srv.rcpts, srv.from)
	}
	msg, err := mail.ReadMessage(strings.NewReader(srv.messages[0]))
	if err != nil {
		t.Fatal(err)
	}
	subj, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subj != "[host] CRIT: nginx — failed" {
		t.Fatalf("subject %q %v", subj, err)
	}
	body, _ := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if strings.ReplaceAll(string(body), "\r\n", "\n") != "Line 1\nLine ż 2\n" {
		t.Fatalf("body %q", body)
	}
	if msg.Header.Get("Content-Type") != "text/plain; charset=UTF-8" || msg.Header.Get("Message-ID") == "" || msg.Header.Get("Date") == "" {
		t.Fatalf("headers %v", msg.Header)
	}
}

func TestEmailSendErrors(t *testing.T) {
	srv := newFakeSMTP(t)
	srv.wantUser, srv.wantPass = "mon", "right"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := (&Email{Config: goodConfig(srv.port()), Password: "wrong"}).Send(ctx, Message{Subject: "s", Body: "b"}); err == nil ||
		!strings.Contains(err.Error(), "auth") {
		t.Fatalf("bad password: %v", err)
	}
	srv.rejectTo = "ops@example.org"
	if err := (&Email{Config: goodConfig(srv.port()), Password: "right"}).Send(ctx, Message{Subject: "s", Body: "b"}); err == nil ||
		!strings.Contains(err.Error(), "RCPT TO") {
		t.Fatalf("rejected recipient: %v", err)
	}
	// nikt nie nasłuchuje
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if err := (&Email{Config: goodConfig(port)}).Send(ctx, Message{Subject: "s", Body: "b"}); err == nil ||
		!strings.Contains(err.Error(), "connect") {
		t.Fatalf("refused: %v", err)
	}
	// STARTTLS wymagany, a serwer go nie oferuje
	c := goodConfig(srv.port())
	c.Security = "starttls"
	if err := (&Email{Config: c, Password: "right"}).Send(ctx, Message{Subject: "s", Body: "b"}); err == nil ||
		!strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("starttls missing: %v", err)
	}
}

func TestEmailSendHonorsContextDeadline(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0") // akceptuje, ale nic nie mówi
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := (&Email{Config: goodConfig(ln.Addr().(*net.TCPAddr).Port)}).Send(ctx, Message{Subject: "s", Body: "b"})
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("must give up at the deadline: %v after %v", err, time.Since(start))
	}
}

func TestEmailHeaderInjectionIsRejectedOrNeutralized(t *testing.T) {
	c := goodConfig(25)
	for name, mut := range map[string]func(*EmailConfig){
		"from":      func(c *EmailConfig) { c.From = "a@example.org\r\nBcc: evil@example.org" },
		"recipient": func(c *EmailConfig) { c.To = []string{"a@example.org\nBcc: evil@example.org"} },
		"host":      func(c *EmailConfig) { c.Host = "mx\r\nX: y" },
		"username":  func(c *EmailConfig) { c.Username = "u\r\nX: y" },
	} {
		cc := c
		cc.To = append([]string(nil), c.To...)
		mut(&cc)
		if _, err := cc.Normalize(); err == nil {
			t.Errorf("%s: CRLF must be rejected", name)
		}
	}
	// temat z CRLF nie może dodać nagłówka
	raw, err := (&Email{Config: c}).BuildMessage(Message{Subject: "hi\r\nBcc: evil@example.org", Body: "x"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("Bcc") != "" {
		t.Fatal("subject injected a header")
	}
}

func TestEmailConfigValidation(t *testing.T) {
	typ, ok := Lookup("email")
	if !ok || !typ.HasSecret {
		t.Fatal("email type must be registered with a secret")
	}
	good := `{"host":" smtp.example.org ","port":587,"security":"STARTTLS","username":"u","from":"m@example.org","to":[" a@example.org "]}`
	norm, err := typ.Validate([]byte(good))
	if err != nil || !strings.Contains(string(norm), `"host":"smtp.example.org"`) || !strings.Contains(string(norm), `"security":"starttls"`) ||
		!strings.Contains(string(norm), `"a@example.org"`) {
		t.Fatalf("normalize: %s %v", norm, err)
	}
	for name, bad := range map[string]string{
		"no host":           `{"host":"","port":25,"security":"none","from":"m@example.org","to":["a@example.org"]}`,
		"url as host":       `{"host":"smtp://x","port":25,"security":"none","from":"m@example.org","to":["a@example.org"]}`,
		"port 0":            `{"host":"h","port":0,"security":"none","from":"m@example.org","to":["a@example.org"]}`,
		"bad security":      `{"host":"h","port":25,"security":"ssl","from":"m@example.org","to":["a@example.org"]}`,
		"bad from":          `{"host":"h","port":25,"security":"none","from":"nope","to":["a@example.org"]}`,
		"no recipients":     `{"host":"h","port":25,"security":"none","from":"m@example.org","to":[]}`,
		"bad recipient":     `{"host":"h","port":25,"security":"none","from":"m@example.org","to":["x"]}`,
		"creds without tls": `{"host":"smtp.example.org","port":25,"security":"none","username":"u","from":"m@example.org","to":["a@example.org"]}`,
		"unknown field":     `{"host":"h","port":25,"security":"none","from":"m@example.org","to":["a@example.org"],"password":"x"}`,
		"not json":          `[`,
	} {
		if _, err := typ.Validate([]byte(bad)); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	// poświadczenia bez TLS są dozwolone tylko dla pętli zwrotnej (np. lokalny relay)
	if _, err := typ.Validate([]byte(`{"host":"127.0.0.1","port":25,"security":"none","username":"u","from":"m@example.org","to":["a@example.org"]}`)); err != nil {
		t.Errorf("loopback credentials: %v", err)
	}
}
