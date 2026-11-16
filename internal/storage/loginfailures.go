package storage

import "time"

// LoginFailure to wiersz login_failures (nieudana próba logowania).
type LoginFailure struct {
	ID           int64
	AuditEventID string
	Username     string
	RemoteIP     string
	Service      string
	AttemptedAt  time.Time
}

type LoginFailureRepo struct{ db *DB }

// Insert zapisuje próbę; duplikat audit_event_id jest ignorowany. Zwraca true, gdy wstawiono wiersz.
func (r *LoginFailureRepo) Insert(f LoginFailure) (bool, error) {
	res, err := r.db.write.Exec(`INSERT OR IGNORE INTO login_failures
		(audit_event_id, username, remote_ip, service, attempted_at) VALUES (?, ?, ?, ?, ?)`,
		f.AuditEventID, f.Username, f.RemoteIP, f.Service, formatTime(f.AttemptedAt))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// List zwraca próby (najnowsze pierwsze); user i ip to dokładne dopasowania, puste = brak filtra.
func (r *LoginFailureRepo) List(user, ip string, limit, offset int) ([]LoginFailure, error) {
	limit, offset = clampPage(limit, offset)
	rows, err := r.db.read.Query(`SELECT id, audit_event_id, username, remote_ip, service, attempted_at
		FROM login_failures
		WHERE (? = '' OR username = ?) AND (? = '' OR remote_ip = ?)
		ORDER BY attempted_at DESC, id DESC LIMIT ? OFFSET ?`, user, user, ip, ip, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoginFailure
	for rows.Next() {
		var f LoginFailure
		var ts string
		if err := rows.Scan(&f.ID, &f.AuditEventID, &f.Username, &f.RemoteIP, &f.Service, &ts); err != nil {
			return nil, err
		}
		if f.AttemptedAt, err = parseTime(ts); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
