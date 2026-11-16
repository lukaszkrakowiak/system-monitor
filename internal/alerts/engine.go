package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"server-monitor/internal/notify"
	"server-monitor/internal/secret"
	"server-monitor/internal/storage"
)

// Interfejsy zależności (spełniane przez repozytoria storage; podmieniane w testach).
type (
	SessionSource interface {
		MaxID() (int64, error)
		After(afterID int64, limit int) ([]storage.SSHSession, error)
	}
	HealthSource interface {
		All() ([]storage.HealthEntry, error)
	}
	SettingsSource interface{ Get() (string, bool, error) }
	StateStore     interface {
		Get(key string) (string, bool, error)
		Set(key, value string, now time.Time) error
		Delete(key string) error
		ListPrefix(prefix string) (map[string]string, error)
	}
	Outbox interface {
		Enqueue(n storage.Notification) (bool, error)
		Due(now time.Time, limit int) ([]storage.Notification, error)
		MarkSent(id int64, at time.Time) error
		MarkAttemptFailed(id int64, errMsg string, next time.Time, giveUp bool) error
	}
	ConnectorSource interface {
		Get(typ string) (*storage.Connector, error)
	}
)

const (
	cursorKey       = "cursor:sessions"
	statePrefix     = "svc:"
	defaultInterval = 5 * time.Second
	sessionBatch    = 100
	deliverBatch    = 20
	maxAttempts     = 5
	sendTimeout     = 30 * time.Second
)

// retryDelays: opóźnienie przed kolejną próbą wysyłki, według numeru nieudanej próby.
var retryDelays = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}

// Engine to pętla tła: wykrywa zdarzenia (nowe logowania, zmiany stanu usług), kolejkuje powiadomienia
// w outboksie i dostarcza je konektorami z ponawianiem. Wszystko w jednej gorutynie (bez wyścigów).
type Engine struct {
	Sessions   SessionSource
	Health     HealthSource
	Settings   SettingsSource
	State      StateStore
	Outbox     Outbox
	Connectors ConnectorSource
	Secrets    *secret.Box

	Host     string
	Interval time.Duration
	Now      func() time.Time
	Logf     func(string, ...any)
	// Build tworzy konektor (domyślnie z rejestru notify); podmieniane w testach.
	Build func(typ string, cfg []byte, secret string) (notify.Connector, error)
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) logf(f string, a ...any) {
	if e.Logf != nil {
		e.Logf(f, a...)
		return
	}
	log.Printf(f, a...)
}

func (e *Engine) host() string {
	if e.Host != "" {
		return e.Host
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "server"
}

// KnownChannel mówi, czy typ konektora jest zarejestrowany.
func KnownChannel(name string) bool {
	_, ok := notify.Lookup(name)
	return ok
}

// LoadSettings czyta ustawienia (domyślne, gdy brak lub uszkodzone).
func LoadSettings(src SettingsSource) (Settings, error) {
	raw, ok, err := src.Get()
	if err != nil {
		return Default(), err
	}
	if !ok {
		return Default(), nil
	}
	var s Settings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return Default(), fmt.Errorf("stored alert settings are unreadable: %w", err)
	}
	n, err := s.Normalize(KnownChannel)
	if err != nil {
		return Default(), fmt.Errorf("stored alert settings are invalid: %w", err)
	}
	return n, nil
}

// Run wykonuje Tick co Interval do anulowania ctx.
func (e *Engine) Run(ctx context.Context) {
	iv := e.Interval
	if iv <= 0 {
		iv = defaultInterval
	}
	tk := time.NewTicker(iv)
	defer tk.Stop()
	last := ""
	for {
		if err := e.Tick(ctx); err != nil {
			if msg := err.Error(); msg != last {
				e.logf("alerts: %v", err)
				last = msg
			}
		} else {
			last = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}

// Tick wykonuje jedną iterację: wykrycie zdarzeń i dostarczenie zaległych powiadomień.
func (e *Engine) Tick(ctx context.Context) error {
	set, err := LoadSettings(e.Settings)
	if err != nil {
		e.logf("alerts: %v (using defaults: no rules active)", err)
	}
	var errs []error
	if err := e.tickLogins(set); err != nil {
		errs = append(errs, fmt.Errorf("logins: %w", err))
	}
	if err := e.tickServices(set); err != nil {
		errs = append(errs, fmt.Errorf("services: %w", err))
	}
	if err := e.deliver(ctx); err != nil {
		errs = append(errs, fmt.Errorf("delivery: %w", err))
	}
	return errors.Join(errs...)
}

// channels zwraca kanały z ustawień, które mają włączony konektor.
func (e *Engine) channels(set Settings) []string {
	var out []string
	for _, ch := range set.Channels {
		c, err := e.Connectors.Get(ch)
		if err != nil {
			e.logf("alerts: connector %s: %v", ch, err)
			continue
		}
		if c == nil || !c.Enabled {
			continue
		}
		out = append(out, ch)
	}
	return out
}

func (e *Engine) enqueue(set Settings, kind, dedupe string, m notify.Message) error {
	now := e.now()
	for _, ch := range e.channels(set) {
		if _, err := e.Outbox.Enqueue(storage.Notification{DedupeKey: dedupe, Connector: ch, Kind: kind,
			Subject: m.Subject, Body: m.Body, NextAttemptAt: now, CreatedAt: now}); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) tickLogins(set Settings) error {
	cur, ok, err := e.State.Get(cursorKey)
	if err != nil {
		return err
	}
	var cursor int64
	if !ok {
		// pierwsze uruchomienie: nie alarmujemy o historii, startujemy od ostatniej sesji
		if cursor, err = e.Sessions.MaxID(); err != nil {
			return err
		}
		return e.State.Set(cursorKey, strconv.FormatInt(cursor, 10), e.now())
	}
	if cursor, err = strconv.ParseInt(cur, 10, 64); err != nil {
		return fmt.Errorf("bad cursor %q", cur)
	}
	for {
		sessions, err := e.Sessions.After(cursor, sessionBatch)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			return nil
		}
		for _, s := range sessions {
			if set.LoginTime.Enabled || set.LoginIP.Enabled {
				if f := set.LoginFindings(s.RemoteIP, s.LoginAt); len(f) > 0 {
					m := LoginMessage(e.host(), s.Username, s.RemoteIP, s.TTY, s.PAMService, s.LoginAt, set.Location(), f)
					if err := e.enqueue(set, KindLogin, "login:"+strconv.FormatInt(s.ID, 10), m); err != nil {
						return err // kursor nie przesunięty: ponowimy (outbox jest idempotentny)
					}
				}
			}
			cursor = s.ID
			if err := e.State.Set(cursorKey, strconv.FormatInt(cursor, 10), e.now()); err != nil {
				return err
			}
		}
	}
}

func (e *Engine) tickServices(set Settings) error {
	entries, err := e.Health.All()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	items := Flatten(entries)
	states, err := e.State.ListPrefix(statePrefix)
	if err != nil {
		return err
	}
	present := map[string]bool{}
	now := e.now()
	loc := set.Location()
	for _, it := range items {
		key := statePrefix + it.Key
		present[key] = true
		var st itemState
		if raw, ok := states[key]; ok {
			if json.Unmarshal([]byte(raw), &st) != nil {
				st = itemState{}
			}
		}
		confirm := set.ServiceState.ConfirmChecks
		if confirm < 1 {
			confirm = 1
		}
		next, tr := observe(st, it, confirm)
		if tr != nil && set.ServiceState.Enabled {
			recovery := tr.Item.Status == "OK"
			if !recovery || (tr.Previous != "" && set.ServiceState.NotifyRecovery) {
				kind := KindService
				if recovery {
					kind = KindRecovery
				}
				dedupe := fmt.Sprintf("svc:%s:%s:%d", it.Key, tr.Item.Status, tr.Since.Unix())
				if err := e.enqueue(set, kind, dedupe, ServiceMessage(e.host(), *tr, now, loc)); err != nil {
					return err
				}
			}
		}
		if next != st {
			b, _ := json.Marshal(next)
			if err := e.State.Set(key, string(b), now); err != nil {
				return err
			}
		}
	}
	for key := range states { // element zniknął (np. usługa usunięta z obserwowanych): zapomnij stan
		if !present[key] {
			if err := e.State.Delete(key); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) build(typ string, cfg []byte, sec string) (notify.Connector, error) {
	if e.Build != nil {
		return e.Build(typ, cfg, sec)
	}
	t, ok := notify.Lookup(typ)
	if !ok {
		return nil, fmt.Errorf("unknown connector type %q", typ)
	}
	return t.New(cfg, sec)
}

// open buduje konektor z zapisanej konfiguracji (z odszyfrowanym sekretem).
func (e *Engine) open(typ string) (notify.Connector, *storage.Connector, error) {
	c, err := e.Connectors.Get(typ)
	if err != nil {
		return nil, nil, err
	}
	if c == nil {
		return nil, nil, fmt.Errorf("connector %q is not configured", typ)
	}
	sec, err := e.Secrets.Decrypt(c.SecretEnc)
	if err != nil {
		return nil, c, err
	}
	conn, err := e.build(typ, []byte(c.ConfigJSON), sec)
	return conn, c, err
}

// SendTest wysyła wiadomość próbną zapisanym konektorem (także wyłączonym, żeby dało się go sprawdzić przed włączeniem).
func (e *Engine) SendTest(ctx context.Context, typ string) error {
	conn, _, err := e.open(typ)
	if err != nil {
		return err
	}
	set, _ := LoadSettings(e.Settings) // strefa czasowa z ustawień (przy błędzie: domyślna)
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	return conn.Send(ctx, TestMessage(e.host(), e.now(), set.Location()))
}

func trimErr(err error) string {
	s := oneLine(err.Error())
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

func (e *Engine) deliver(ctx context.Context) error {
	due, err := e.Outbox.Due(e.now(), deliverBatch)
	if err != nil {
		return err
	}
	for _, n := range due {
		if ctx.Err() != nil {
			return nil
		}
		sendErr := func() error {
			conn, c, err := e.open(n.Connector)
			if err != nil {
				return err
			}
			if !c.Enabled {
				return errors.New("connector is disabled")
			}
			sctx, cancel := context.WithTimeout(ctx, sendTimeout)
			defer cancel()
			return conn.Send(sctx, notify.Message{Subject: n.Subject, Body: n.Body})
		}()
		now := e.now()
		if sendErr == nil {
			if err := e.Outbox.MarkSent(n.ID, now); err != nil {
				return err
			}
			continue
		}
		giveUp := n.Attempts+1 >= maxAttempts
		delay := retryDelays[min(n.Attempts, len(retryDelays)-1)]
		e.logf("alerts: delivery of notification %d via %s failed (attempt %d): %v", n.ID, n.Connector, n.Attempts+1, sendErr)
		if err := e.Outbox.MarkAttemptFailed(n.ID, trimErr(sendErr), now.Add(delay), giveUp); err != nil {
			return err
		}
	}
	return nil
}
