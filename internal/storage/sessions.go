package storage

import (
	"database/sql"
	"time"
)

// SSHSession to wiersz ssh_sessions.
type SSHSession struct {
	ID         int64
	Username   string
	RemoteIP   string
	TTY        string
	PAMService string
	LoginAt    time.Time
	LogoutAt   *time.Time
}

type SessionRepo struct{ db *DB }

// Open wstawia nową sesję (PAM open_session).
func (r *SessionRepo) Open(s SSHSession) (int64, error) {
	res, err := r.db.write.Exec(`INSERT INTO ssh_sessions(username, remote_ip, tty, pam_service, login_at)
		VALUES (?, ?, ?, ?, ?)`, s.Username, s.RemoteIP, s.TTY, s.PAMService, formatTime(s.LoginAt))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Close uzupełnia logout_at najnowszego otwartego rekordu username+tty (PAM close_session).
// Zwraca false, gdy nie było otwartej sesji.
func (r *SessionRepo) Close(username, tty string, at time.Time) (bool, error) {
	res, err := r.db.write.Exec(`UPDATE ssh_sessions SET logout_at = ?
		WHERE id = (SELECT id FROM ssh_sessions
			WHERE username = ? AND tty = ? AND logout_at IS NULL
			ORDER BY login_at DESC, id DESC LIMIT 1)`, formatTime(at), username, tty)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// List zwraca sesje (najnowsze pierwsze); user == "" oznacza brak filtra.
func (r *SessionRepo) List(user string, limit, offset int) ([]SSHSession, error) {
	limit, offset = clampPage(limit, offset)
	rows, err := r.db.read.Query(`SELECT id, username, remote_ip, tty, pam_service, login_at, logout_at
		FROM ssh_sessions WHERE (? = '' OR username = ?)
		ORDER BY login_at DESC, id DESC LIMIT ? OFFSET ?`, user, user, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SSHSession
	for rows.Next() {
		var s SSHSession
		var login string
		var logout sql.NullString
		if err := rows.Scan(&s.ID, &s.Username, &s.RemoteIP, &s.TTY, &s.PAMService, &login, &logout); err != nil {
			return nil, err
		}
		if s.LoginAt, err = parseTime(login); err != nil {
			return nil, err
		}
		if logout.Valid {
			t, err := parseTime(logout.String)
			if err != nil {
				return nil, err
			}
			s.LogoutAt = &t
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func clampPage(limit, offset int) (int, int) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
