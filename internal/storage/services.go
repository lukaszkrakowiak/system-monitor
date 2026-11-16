package storage

import (
	"fmt"
	"strings"
	"time"
)

// MonitoredService to wiersz tabeli monitored_services.
type MonitoredService struct {
	UnitName    string
	DisplayName string
	IsEnabled   bool
	SortOrder   int
	UpdatedAt   time.Time
}

type ServiceRepo struct{ db *DB }

// DefaultDisplayName wyprowadza nazwę wyświetlaną z nazwy unitu.
func DefaultDisplayName(unit string) string {
	return strings.TrimSuffix(unit, ".service")
}

// SetSelection atomowo zastępuje wybór: podane unity dostają is_enabled=1
// (kolejność = sort_order), wszystkie pozostałe is_enabled=0.
func (r *ServiceRepo) SetSelection(units []string) error {
	tx, err := r.db.write.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := formatTime(time.Now())
	if _, err := tx.Exec(`UPDATE monitored_services SET is_enabled = 0, updated_at = ?`, now); err != nil {
		return err
	}
	seen := map[string]bool{}
	order := 0
	for _, u := range units {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		if _, err := tx.Exec(`INSERT INTO monitored_services(unit_name, display_name, is_enabled, sort_order, updated_at)
			VALUES (?, ?, 1, ?, ?)
			ON CONFLICT(unit_name) DO UPDATE SET is_enabled = 1, sort_order = excluded.sort_order, updated_at = excluded.updated_at`,
			u, DefaultDisplayName(u), order, now); err != nil {
			return fmt.Errorf("storage: upsert %q: %w", u, err)
		}
		order++
	}
	return tx.Commit()
}

// SetDisplayName zmienia nazwę wyświetlaną istniejącego unitu.
func (r *ServiceRepo) SetDisplayName(unit, name string) error {
	_, err := r.db.write.Exec(`UPDATE monitored_services SET display_name = ?, updated_at = ? WHERE unit_name = ?`,
		name, formatTime(time.Now()), unit)
	return err
}

func (r *ServiceRepo) list(onlyEnabled bool) ([]MonitoredService, error) {
	q := `SELECT unit_name, display_name, is_enabled, sort_order, updated_at FROM monitored_services`
	if onlyEnabled {
		q += ` WHERE is_enabled = 1`
	}
	q += ` ORDER BY sort_order, unit_name`
	rows, err := r.db.read.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MonitoredService
	for rows.Next() {
		var s MonitoredService
		var en int
		var ts string
		if err := rows.Scan(&s.UnitName, &s.DisplayName, &en, &s.SortOrder, &ts); err != nil {
			return nil, err
		}
		s.IsEnabled = en != 0
		// updated_at może pochodzić z DEFAULT CURRENT_TIMESTAMP (inny format) — tolerujemy.
		if t, err := parseTime(ts); err == nil {
			s.UpdatedAt = t
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// List zwraca wszystkie wiersze; ListEnabled tylko wybrane (is_enabled = 1).
func (r *ServiceRepo) List() ([]MonitoredService, error)        { return r.list(false) }
func (r *ServiceRepo) ListEnabled() ([]MonitoredService, error) { return r.list(true) }
