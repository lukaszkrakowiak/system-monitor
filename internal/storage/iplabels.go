package storage

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxIPLabelLen to maksymalna długość etykiety w znakach (runach).
const MaxIPLabelLen = 64

// IPLabel to wiersz ip_labels: etykieta nadana adresowi IP przez użytkownika.
type IPLabel struct {
	IP        string
	Label     string
	UpdatedAt time.Time
}

// NormalizeIP zwraca kanoniczny zapis adresu (IPv4-mapped IPv6 sprowadzone do IPv4,
// IPv6 małymi literami), dzięki czemu ten sam host ma zawsze jeden klucz.
func NormalizeIP(s string) (string, bool) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || a.Zone() != "" {
		return "", false
	}
	return a.Unmap().String(), true
}

// CleanIPLabel przycina etykietę i sprawdza jej poprawność: niepusta, do MaxIPLabelLen znaków,
// poprawny UTF-8, bez znaków sterujących (etykieta trafia m.in. do nagłówków e-maili).
func CleanIPLabel(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", fmt.Errorf("label must not be empty")
	case !utf8.ValidString(s):
		return "", fmt.Errorf("label must be valid UTF-8")
	case utf8.RuneCountInString(s) > MaxIPLabelLen:
		return "", fmt.Errorf("label is too long (max %d characters)", MaxIPLabelLen)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("label must not contain control characters")
		}
	}
	return s, nil
}

type IPLabelRepo struct{ db *DB }

// List zwraca wszystkie etykiety posortowane po adresie.
func (r *IPLabelRepo) List() ([]IPLabel, error) {
	rows, err := r.db.read.Query(`SELECT ip, label, updated_at FROM ip_labels ORDER BY ip`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IPLabel
	for rows.Next() {
		var l IPLabel
		var ts string
		if err := rows.Scan(&l.IP, &l.Label, &ts); err != nil {
			return nil, err
		}
		if l.UpdatedAt, err = parseTime(ts); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Set tworzy lub nadpisuje etykietę adresu (ip i label muszą być już zwalidowane).
func (r *IPLabelRepo) Set(ip, label string, now time.Time) error {
	_, err := r.db.write.Exec(`INSERT INTO ip_labels (ip, label, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(ip) DO UPDATE SET label = excluded.label, updated_at = excluded.updated_at`,
		ip, label, formatTime(now))
	return err
}

// Delete usuwa etykietę; brak etykiety nie jest błędem.
func (r *IPLabelRepo) Delete(ip string) error {
	_, err := r.db.write.Exec(`DELETE FROM ip_labels WHERE ip = ?`, ip)
	return err
}
