package storage

import (
	"database/sql"
	"errors"
	"time"
)

// HealthEntry to wiersz health_checks_cache.
type HealthEntry struct {
	ServiceKey  string
	Status      string // OK | WARN | CRIT
	PayloadJSON string
	CheckedAt   time.Time
}

type HealthRepo struct{ db *DB }

// Upsert zapisuje najnowszy stan sondy.
func (r *HealthRepo) Upsert(e HealthEntry) error {
	switch e.Status {
	case "OK", "WARN", "CRIT":
	default:
		return errors.New("storage: invalid health status " + e.Status)
	}
	_, err := r.db.write.Exec(`INSERT INTO health_checks_cache(service_key, status, payload_json, checked_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(service_key) DO UPDATE SET status = excluded.status,
			payload_json = excluded.payload_json, checked_at = excluded.checked_at`,
		e.ServiceKey, e.Status, e.PayloadJSON, formatTime(e.CheckedAt))
	return err
}

// Get zwraca wpis dla klucza; (nil, nil) gdy brak.
func (r *HealthRepo) Get(key string) (*HealthEntry, error) {
	var e HealthEntry
	var ts string
	err := r.db.read.QueryRow(`SELECT service_key, status, payload_json, checked_at
		FROM health_checks_cache WHERE service_key = ?`, key).Scan(&e.ServiceKey, &e.Status, &e.PayloadJSON, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if e.CheckedAt, err = parseTime(ts); err != nil {
		return nil, err
	}
	return &e, nil
}

// All zwraca wszystkie wpisy cache (dla GET /api/health).
func (r *HealthRepo) All() ([]HealthEntry, error) {
	rows, err := r.db.read.Query(`SELECT service_key, status, payload_json, checked_at
		FROM health_checks_cache ORDER BY service_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HealthEntry
	for rows.Next() {
		var e HealthEntry
		var ts string
		if err := rows.Scan(&e.ServiceKey, &e.Status, &e.PayloadJSON, &ts); err != nil {
			return nil, err
		}
		if e.CheckedAt, err = parseTime(ts); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
