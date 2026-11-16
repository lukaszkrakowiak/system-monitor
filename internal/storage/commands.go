package storage

import (
	"strings"
	"time"
)

// CLICommand to wiersz cli_command_logs.
type CLICommand struct {
	ID                int64
	AuditEventID      string
	AUID              int64
	LoginUsername     string
	EffectiveUsername string
	CWD               string
	Command           string
	ExitCode          *int
	ExecutedAt        time.Time
}

type CommandRepo struct{ db *DB }

// Insert zapisuje komendę; duplikat audit_event_id jest ignorowany (deduplikacja).
// Zwraca true, gdy wiersz faktycznie wstawiono.
func (r *CommandRepo) Insert(c CLICommand) (bool, error) {
	var exit any
	if c.ExitCode != nil {
		exit = *c.ExitCode
	}
	res, err := r.db.write.Exec(`INSERT OR IGNORE INTO cli_command_logs
		(audit_event_id, auid, login_username, effective_username, cwd, command, exit_code, executed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.AuditEventID, c.AUID, c.LoginUsername, c.EffectiveUsername, c.CWD, c.Command, exit, formatTime(c.ExecutedAt))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// List zwraca komendy (najnowsze pierwsze); user filtruje po login_username,
// q to fraza szukana (literalnie) w command. Puste wartości = brak filtra.
func (r *CommandRepo) List(user, q string, limit, offset int) ([]CLICommand, error) {
	limit, offset = clampPage(limit, offset)
	pattern := "%" + likeEscaper.Replace(q) + "%"
	rows, err := r.db.read.Query(`SELECT id, audit_event_id, auid, login_username, effective_username,
			cwd, command, exit_code, executed_at
		FROM cli_command_logs
		WHERE (? = '' OR login_username = ?) AND (? = '' OR command LIKE ? ESCAPE '\')
		ORDER BY executed_at DESC, id DESC LIMIT ? OFFSET ?`,
		user, user, q, pattern, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CLICommand
	for rows.Next() {
		var c CLICommand
		var exit *int
		var ts string
		if err := rows.Scan(&c.ID, &c.AuditEventID, &c.AUID, &c.LoginUsername, &c.EffectiveUsername,
			&c.CWD, &c.Command, &exit, &ts); err != nil {
			return nil, err
		}
		c.ExitCode = exit
		if c.ExecutedAt, err = parseTime(ts); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
