package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// EmailConfig to konfiguracja konektora e-mail (hasło jest przechowywane osobno, zaszyfrowane).
type EmailConfig struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Security string   `json:"security"` // starttls | tls | none
	Username string   `json:"username"`
	From     string   `json:"from"`
	To       []string `json:"to"`
}

const (
	emailDialTimeout = 10 * time.Second
	emailTotalLimit  = 30 * time.Second
	maxRecipients    = 20
)

func init() {
	register(Type{
		Name: "email", Label: "Email (SMTP)", HasSecret: true,
		Validate: validateEmail,
		New: func(cfg []byte, secret string) (Connector, error) {
			var c EmailConfig
			if err := json.Unmarshal(cfg, &c); err != nil {
				return nil, err
			}
			return &Email{Config: c, Password: secret}, nil
		},
	})
}

func hasCRLF(s string) bool { return strings.ContainsAny(s, "\r\n\x00") }

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Normalize sprawdza i porządkuje konfigurację. Odrzuca znaki nowej linii (wstrzykiwanie nagłówków).
func (c EmailConfig) Normalize() (EmailConfig, error) {
	c.Host = strings.TrimSpace(c.Host)
	c.Username = strings.TrimSpace(c.Username)
	c.Security = strings.ToLower(strings.TrimSpace(c.Security))
	c.From = strings.TrimSpace(c.From)
	if c.Host == "" || hasCRLF(c.Host) || strings.ContainsAny(c.Host, " /\\@") {
		return c, errors.New("host is required (hostname or IP, without scheme or port)")
	}
	if c.Port < 1 || c.Port > 65535 {
		return c, errors.New("port must be between 1 and 65535")
	}
	switch c.Security {
	case "starttls", "tls", "none":
	default:
		return c, errors.New(`security must be "starttls", "tls" or "none"`)
	}
	if hasCRLF(c.Username) || len(c.Username) > 255 {
		return c, errors.New("invalid username")
	}
	if c.Username != "" && c.Security == "none" && !isLoopback(c.Host) {
		return c, errors.New("refusing to send credentials without TLS: use starttls or tls")
	}
	if hasCRLF(c.From) {
		return c, errors.New("invalid sender address")
	}
	if _, err := mail.ParseAddress(c.From); err != nil {
		return c, errors.New("sender (from) must be a valid email address")
	}
	if len(c.To) == 0 || len(c.To) > maxRecipients {
		return c, fmt.Errorf("provide between 1 and %d recipients", maxRecipients)
	}
	to := make([]string, 0, len(c.To))
	for _, r := range c.To {
		r = strings.TrimSpace(r)
		if hasCRLF(r) {
			return c, errors.New("invalid recipient address")
		}
		if _, err := mail.ParseAddress(r); err != nil {
			return c, fmt.Errorf("invalid recipient address %q", r)
		}
		to = append(to, r)
	}
	c.To = to
	return c, nil
}

func validateEmail(raw []byte) ([]byte, error) {
	var c EmailConfig
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, errors.New("invalid email configuration")
	}
	c, err := c.Normalize()
	if err != nil {
		return nil, err
	}
	return json.Marshal(c)
}

// Email wysyła wiadomości przez SMTP (STARTTLS, TLS lub bez szyfrowania; AUTH PLAIN gdy podano użytkownika).
type Email struct {
	Config   EmailConfig
	Password string
}

// BuildMessage składa wiadomość RFC 5322 (UTF-8, quoted-printable, temat kodowany RFC 2047).
func (e *Email) BuildMessage(m Message, now time.Time) ([]byte, error) {
	c, err := e.Config.Normalize()
	if err != nil {
		return nil, err
	}
	subject := strings.NewReplacer("\r", " ", "\n", " ").Replace(m.Subject)
	idb := make([]byte, 12)
	if _, err := rand.Read(idb); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	h := func(k, v string) { buf.WriteString(k + ": " + v + "\r\n") }
	h("From", c.From)
	h("To", strings.Join(c.To, ", "))
	h("Subject", mime.QEncoding.Encode("utf-8", subject))
	h("Date", now.Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(idb)+"@"+c.Host+">")
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=UTF-8")
	h("Content-Transfer-Encoding", "quoted-printable")
	h("Auto-Submitted", "auto-generated")
	buf.WriteString("\r\n")
	w := quotedprintable.NewWriter(&buf)
	if _, err := w.Write([]byte(strings.ReplaceAll(m.Body, "\r\n", "\n"))); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Send łączy się z serwerem SMTP i wysyła wiadomość do wszystkich odbiorców.
func (e *Email) Send(ctx context.Context, m Message) error {
	c, err := e.Config.Normalize()
	if err != nil {
		return err
	}
	msg, err := e.BuildMessage(m, time.Now())
	if err != nil {
		return err
	}
	from, _ := mail.ParseAddress(c.From)

	deadline := time.Now().Add(emailTotalLimit)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	dialer := net.Dialer{Timeout: emailDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(c.Host, strconv.Itoa(c.Port)))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	conn.SetDeadline(deadline)
	tlsCfg := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	if c.Security == "tls" {
		tc := tls.Client(conn, tlsCfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return fmt.Errorf("tls handshake: %w", err)
		}
		conn = tc
	}
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp greeting: %w", err)
	}
	defer cl.Close()
	if c.Security == "starttls" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return errors.New("server does not support STARTTLS")
		}
		if err := cl.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if c.Username != "" {
		if err := cl.Auth(smtp.PlainAuth("", c.Username, e.Password, c.Host)); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	if err := cl.Mail(from.Address); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, r := range c.To {
		a, _ := mail.ParseAddress(r)
		if err := cl.Rcpt(a.Address); err != nil {
			return fmt.Errorf("RCPT TO %s: %w", a.Address, err)
		}
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("send message: %w", err)
	}
	cl.Quit()
	return nil
}
