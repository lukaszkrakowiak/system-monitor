// Package storage zawiera migracje SQLite (WAL) oraz repozytoria aplikacji.
package storage

import (
	"database/sql"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite" // czysty sterownik Go, bez CGO
)

// timeLayout to stały format czasu (UTC, stała liczba cyfr ułamkowych),
// dzięki czemu porównania i ORDER BY po tekście zachowują porządek chronologiczny.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// parseTime jest tolerancyjny: sterownik modernc przepisuje kolumny DATETIME
// przy odczycie (np. obcina końcowe zera ułamka), więc nie wymagamy stałej szerokości.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("storage: parse time %q: %w", s, err)
	}
	return t, nil
}

// DB opakowuje dwie pule połączeń: jedno połączenie zapisu (SetMaxOpenConns(1),
// eliminuje SQLITE_BUSY) oraz osobną pulę do odczytów.
type DB struct {
	write *sql.DB
	read  *sql.DB
}

func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	return "file:" + path + "?" + q.Encode()
}

// Open otwiera bazę pod wskazaną ścieżką i uruchamia migracje.
func Open(path string) (*DB, error) {
	w, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("storage: open write: %w", err)
	}
	w.SetMaxOpenConns(1)
	if err := w.Ping(); err != nil {
		w.Close()
		return nil, fmt.Errorf("storage: ping write: %w", err)
	}
	if err := migrate(w); err != nil {
		w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("storage: open read: %w", err)
	}
	r.SetMaxOpenConns(4)
	return &DB{write: w, read: r}, nil
}

// Close zamyka obie pule.
func (d *DB) Close() error {
	e1 := d.read.Close()
	e2 := d.write.Close()
	if e2 != nil {
		return e2
	}
	return e1
}

// Services zwraca repozytorium monitored_services.
func (d *DB) Services() *ServiceRepo { return &ServiceRepo{d} }

// Health zwraca repozytorium health_checks_cache.
func (d *DB) Health() *HealthRepo { return &HealthRepo{d} }

// Sessions zwraca repozytorium ssh_sessions.
func (d *DB) Sessions() *SessionRepo { return &SessionRepo{d} }

// Commands zwraca repozytorium cli_command_logs.
func (d *DB) Commands() *CommandRepo { return &CommandRepo{d} }

// LoginFailures zwraca repozytorium login_failures.
func (d *DB) LoginFailures() *LoginFailureRepo { return &LoginFailureRepo{d} }

// Connectors zwraca repozytorium notification_connectors.
func (d *DB) Connectors() *ConnectorRepo { return &ConnectorRepo{d} }

// AlertSettings zwraca repozytorium alert_settings.
func (d *DB) AlertSettings() *AlertSettingsRepo { return &AlertSettingsRepo{d} }

// AlertState zwraca repozytorium alert_state.
func (d *DB) AlertState() *AlertStateRepo { return &AlertStateRepo{d} }

// Notifications zwraca repozytorium notifications (outbox).
func (d *DB) Notifications() *NotificationRepo { return &NotificationRepo{d} }

// AuditState zwraca repozytorium stanu śledzenia audit.log.
func (d *DB) AuditState() *AuditStateRepo { return &AuditStateRepo{d} }
