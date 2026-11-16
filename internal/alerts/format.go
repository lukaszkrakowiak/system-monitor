package alerts

import (
	"fmt"
	"strings"
	"time"

	"server-monitor/internal/notify"
)

// Rodzaje powiadomień (kolumna notifications.kind).
const (
	KindLogin    = "login"
	KindService  = "service_state"
	KindRecovery = "service_recovery"
	KindTest     = "test"
)

const timeFmt = "2006-01-02 15:04:05 MST"

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// LoginMessage buduje powiadomienie o podejrzanym logowaniu SSH.
// label to opcjonalna etykieta adresu IP nadana przez użytkownika (pusta = brak).
func LoginMessage(host, user, ip, label, tty, service string, at time.Time, loc *time.Location, findings []Finding) notify.Message {
	if ip == "" {
		ip = "(local)"
	} else if label = oneLine(label); label != "" {
		ip += " (" + label + ")"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Host:     %s\nTime:     %s\nUser:     %s\nFrom IP:  %s\nTTY:      %s\nService:  %s\n\nWhy you are notified:\n",
		host, at.In(loc).Format(timeFmt), user, ip, orDash(tty), orDash(service))
	for _, f := range findings {
		fmt.Fprintf(&b, " - %s\n", f.Reason)
	}
	return notify.Message{
		Subject: fmt.Sprintf("[%s] Suspicious SSH login: %s from %s", host, oneLine(user), ip),
		Body:    b.String(),
	}
}

// ServiceMessage buduje powiadomienie o zmianie stanu elementu (awaria lub powrót do OK).
func ServiceMessage(host string, t Transition, now time.Time, loc *time.Location) notify.Message {
	prev := t.Previous
	if prev == "" {
		prev = "unknown"
	}
	var subj string
	if t.Item.Status == "OK" {
		subj = fmt.Sprintf("[%s] RECOVERED: %s (was %s)", host, t.Item.Name, prev)
	} else {
		subj = fmt.Sprintf("[%s] %s: %s (was %s)", host, t.Item.Status, t.Item.Name, prev)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Host:     %s\nTime:     %s\nService:  %s\nState:    %s (was %s)\nSince:    %s\n",
		host, now.In(loc).Format(timeFmt), t.Item.Name, t.Item.Status, prev, t.Since.In(loc).Format(timeFmt))
	if t.Item.Detail != "" {
		fmt.Fprintf(&b, "Details:  %s\n", oneLine(t.Item.Detail))
	}
	return notify.Message{Subject: oneLine(subj), Body: b.String()}
}

// TestMessage to wiadomość próbna z ustawień konektora.
func TestMessage(host string, now time.Time, loc *time.Location) notify.Message {
	return notify.Message{
		Subject: fmt.Sprintf("[%s] Server Monitor test notification", host),
		Body: fmt.Sprintf("This is a test notification from Server Monitor.\n\nHost: %s\nTime: %s\n\nIf you can read this, the connector works.\n",
			host, now.In(loc).Format(timeFmt)),
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
