package storage

import (
	"database/sql"
	"fmt"
	"time"
)

type migration struct {
	version int
	sql     string
}

var migrations = []migration{
	{1, `
CREATE TABLE IF NOT EXISTS monitored_services (
    unit_name    TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    is_enabled   INTEGER NOT NULL DEFAULT 1,
    sort_order   INTEGER NOT NULL DEFAULT 0,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS health_checks_cache (
    service_key  TEXT PRIMARY KEY,
    status       TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    checked_at   DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS ssh_sessions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    username     TEXT NOT NULL,
    remote_ip    TEXT NOT NULL,
    tty          TEXT NOT NULL,
    pam_service  TEXT NOT NULL,
    login_at     DATETIME NOT NULL,
    logout_at    DATETIME
);
CREATE INDEX IF NOT EXISTS idx_ssh_sessions_user_time ON ssh_sessions(username, login_at DESC);
CREATE TABLE IF NOT EXISTS cli_command_logs (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    audit_event_id     TEXT NOT NULL UNIQUE,
    auid               INTEGER NOT NULL,
    login_username     TEXT NOT NULL,
    effective_username TEXT NOT NULL,
    cwd                TEXT NOT NULL,
    command            TEXT NOT NULL,
    exit_code          INTEGER,
    executed_at        DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_cli_logs_user_time ON cli_command_logs(login_username, executed_at DESC);
CREATE INDEX IF NOT EXISTS idx_cli_logs_executed_at ON cli_command_logs(executed_at DESC);
`},
	// Offset/inode śledzonego audit.log (SPEC §3: "zapis offsetu/inode w SQLite").
	{2, `
CREATE TABLE IF NOT EXISTS audit_log_state (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    inode      INTEGER NOT NULL,
    offset     INTEGER NOT NULL,
    updated_at DATETIME NOT NULL
);
`},
	// Nieudane logowania (auditd USER_LOGIN res=failed): kto, skąd, kiedy.
	{3, `
CREATE TABLE IF NOT EXISTS login_failures (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    audit_event_id TEXT NOT NULL UNIQUE,
    username       TEXT NOT NULL,
    remote_ip      TEXT NOT NULL,
    service        TEXT NOT NULL,
    attempted_at   DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_login_failures_time ON login_failures(attempted_at DESC);
CREATE INDEX IF NOT EXISTS idx_login_failures_ip ON login_failures(remote_ip, attempted_at DESC);
`},
	// Alerty: konektory powiadomień, reguły, stan silnika i kolejka wysyłki (outbox).
	{4, `
CREATE TABLE IF NOT EXISTS notification_connectors (
    type        TEXT PRIMARY KEY,
    enabled     INTEGER NOT NULL DEFAULT 0,
    config_json TEXT NOT NULL,
    secret_enc  TEXT NOT NULL DEFAULT '',
    updated_at  DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS alert_settings (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    settings_json TEXT NOT NULL,
    updated_at    DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS alert_state (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS notifications (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    dedupe_key      TEXT NOT NULL,
    connector       TEXT NOT NULL,
    kind            TEXT NOT NULL,
    subject         TEXT NOT NULL,
    body            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at DATETIME NOT NULL,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL,
    sent_at         DATETIME,
    UNIQUE (dedupe_key, connector)
);
CREATE INDEX IF NOT EXISTS idx_notifications_due ON notifications(status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_notifications_created ON notifications(created_at DESC);
`},
	// Etykiety adresów IP nadawane przez użytkownika (np. "biuro", "mój komputer").
	{5, `
CREATE TABLE IF NOT EXISTS ip_labels (
    ip         TEXT PRIMARY KEY,
    label      TEXT NOT NULL,
    updated_at DATETIME NOT NULL
);
`},
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL)`); err != nil {
		return fmt.Errorf("storage: schema_migrations: %w", err)
	}
	for _, m := range migrations {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&n); err != nil {
			return fmt.Errorf("storage: check migration %d: %w", m.version, err)
		}
		if n > 0 {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("storage: migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
			m.version, formatTime(time.Now())); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
