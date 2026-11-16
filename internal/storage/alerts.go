package storage

import (
	"database/sql"
	"errors"
	"time"
)

// ---- konektory ----

// Connector to wiersz notification_connectors. SecretEnc to zaszyfrowany sekret (np. hasło SMTP);
// nigdy nie jest zwracany przez API.
type Connector struct {
	Type       string
	Enabled    bool
	ConfigJSON string
	SecretEnc  string
	UpdatedAt  time.Time
}

type ConnectorRepo struct{ db *DB }

// Get zwraca konektor; (nil, nil) gdy nie skonfigurowano.
func (r *ConnectorRepo) Get(typ string) (*Connector, error) {
	var c Connector
	var en int
	var ts string
	err := r.db.read.QueryRow(`SELECT type, enabled, config_json, secret_enc, updated_at
		FROM notification_connectors WHERE type = ?`, typ).Scan(&c.Type, &en, &c.ConfigJSON, &c.SecretEnc, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Enabled = en != 0
	if c.UpdatedAt, err = parseTime(ts); err != nil {
		return nil, err
	}
	return &c, nil
}

// Upsert zapisuje konektor (pełny stan, razem z sekretem).
func (r *ConnectorRepo) Upsert(c Connector, now time.Time) error {
	en := 0
	if c.Enabled {
		en = 1
	}
	_, err := r.db.write.Exec(`INSERT INTO notification_connectors(type, enabled, config_json, secret_enc, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(type) DO UPDATE SET enabled = excluded.enabled, config_json = excluded.config_json,
			secret_enc = excluded.secret_enc, updated_at = excluded.updated_at`,
		c.Type, en, c.ConfigJSON, c.SecretEnc, formatTime(now))
	return err
}

// ---- ustawienia alertów (jeden dokument JSON) ----

type AlertSettingsRepo struct{ db *DB }

// Get zwraca zapisany dokument; ("", false, nil) gdy jeszcze nie zapisano.
func (r *AlertSettingsRepo) Get() (string, bool, error) {
	var s string
	err := r.db.read.QueryRow(`SELECT settings_json FROM alert_settings WHERE id = 1`).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return s, err == nil, err
}

func (r *AlertSettingsRepo) Set(settingsJSON string, now time.Time) error {
	_, err := r.db.write.Exec(`INSERT INTO alert_settings(id, settings_json, updated_at) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET settings_json = excluded.settings_json, updated_at = excluded.updated_at`,
		settingsJSON, formatTime(now))
	return err
}

// ---- stan silnika alertów (kursory, ostatnie stany usług) ----

type AlertStateRepo struct{ db *DB }

func (r *AlertStateRepo) Get(key string) (string, bool, error) {
	var v string
	err := r.db.read.QueryRow(`SELECT value FROM alert_state WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (r *AlertStateRepo) Set(key, value string, now time.Time) error {
	_, err := r.db.write.Exec(`INSERT INTO alert_state(key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, formatTime(now))
	return err
}

func (r *AlertStateRepo) Delete(key string) error {
	_, err := r.db.write.Exec(`DELETE FROM alert_state WHERE key = ?`, key)
	return err
}

// ListPrefix zwraca klucz->wartość dla kluczy zaczynających się od prefix (dosłownie, bez LIKE).
func (r *AlertStateRepo) ListPrefix(prefix string) (map[string]string, error) {
	rows, err := r.db.read.Query(`SELECT key, value FROM alert_state WHERE substr(key, 1, ?) = ?`, len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// ---- kolejka powiadomień (outbox) ----

// Notification to wiersz notifications: jedna dostawa przez jeden konektor.
type Notification struct {
	ID            int64
	DedupeKey     string
	Connector     string
	Kind          string
	Subject       string
	Body          string
	Status        string // pending | sent | failed
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
	SentAt        *time.Time
}

type NotificationRepo struct{ db *DB }

// Enqueue dodaje powiadomienie; para (dedupe_key, connector) jest unikalna, więc powtórne
// zgłoszenie tego samego zdarzenia (np. po restarcie) jest ignorowane. Zwraca true, gdy dodano.
func (r *NotificationRepo) Enqueue(n Notification) (bool, error) {
	res, err := r.db.write.Exec(`INSERT OR IGNORE INTO notifications
		(dedupe_key, connector, kind, subject, body, status, attempts, next_attempt_at, created_at)
		VALUES (?, ?, ?, ?, ?, 'pending', 0, ?, ?)`,
		n.DedupeKey, n.Connector, n.Kind, n.Subject, n.Body, formatTime(n.NextAttemptAt), formatTime(n.CreatedAt))
	if err != nil {
		return false, err
	}
	c, err := res.RowsAffected()
	return c > 0, err
}

const notifCols = `id, dedupe_key, connector, kind, subject, body, status, attempts, next_attempt_at, last_error, created_at, sent_at`

func scanNotification(rows *sql.Rows) (Notification, error) {
	var n Notification
	var next, created string
	var sent sql.NullString
	if err := rows.Scan(&n.ID, &n.DedupeKey, &n.Connector, &n.Kind, &n.Subject, &n.Body, &n.Status, &n.Attempts,
		&next, &n.LastError, &created, &sent); err != nil {
		return n, err
	}
	var err error
	if n.NextAttemptAt, err = parseTime(next); err != nil {
		return n, err
	}
	if n.CreatedAt, err = parseTime(created); err != nil {
		return n, err
	}
	if sent.Valid {
		t, err := parseTime(sent.String)
		if err != nil {
			return n, err
		}
		n.SentAt = &t
	}
	return n, nil
}

func collectNotifications(rows *sql.Rows, err error) ([]Notification, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Due zwraca oczekujące powiadomienia, których czas kolejnej próby już minął (najstarsze pierwsze).
func (r *NotificationRepo) Due(now time.Time, limit int) ([]Notification, error) {
	return collectNotifications(r.db.read.Query(`SELECT `+notifCols+` FROM notifications
		WHERE status = 'pending' AND next_attempt_at <= ? ORDER BY id ASC LIMIT ?`, formatTime(now), limit))
}

func (r *NotificationRepo) MarkSent(id int64, at time.Time) error {
	_, err := r.db.write.Exec(`UPDATE notifications SET status = 'sent', attempts = attempts + 1, last_error = '', sent_at = ?
		WHERE id = ?`, formatTime(at), id)
	return err
}

// MarkAttemptFailed zapisuje nieudaną próbę; gdy giveUp, status = failed, w przeciwnym razie
// powiadomienie zostaje pending z nowym terminem kolejnej próby.
func (r *NotificationRepo) MarkAttemptFailed(id int64, errMsg string, next time.Time, giveUp bool) error {
	status := "pending"
	if giveUp {
		status = "failed"
	}
	_, err := r.db.write.Exec(`UPDATE notifications SET status = ?, attempts = attempts + 1, last_error = ?, next_attempt_at = ?
		WHERE id = ?`, status, errMsg, formatTime(next), id)
	return err
}

// List zwraca historię (najnowsze pierwsze).
func (r *NotificationRepo) List(limit, offset int) ([]Notification, error) {
	limit, offset = clampPage(limit, offset)
	return collectNotifications(r.db.read.Query(`SELECT `+notifCols+` FROM notifications
		ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset))
}
