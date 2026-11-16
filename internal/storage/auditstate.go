package storage

import (
	"database/sql"
	"errors"
	"time"
)

// AuditState opisuje pozycję śledzenia audit.log (do wykrywania rotacji po inode).
type AuditState struct {
	Inode  uint64
	Offset int64
}

type AuditStateRepo struct{ db *DB }

// Get zwraca zapisany stan; (nil, nil) gdy jeszcze nie zapisano.
func (r *AuditStateRepo) Get() (*AuditState, error) {
	var inode, off int64
	err := r.db.read.QueryRow(`SELECT inode, offset FROM audit_log_state WHERE id = 1`).Scan(&inode, &off)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &AuditState{Inode: uint64(inode), Offset: off}, nil
}

// Set zapisuje stan (singleton id = 1).
func (r *AuditStateRepo) Set(s AuditState) error {
	_, err := r.db.write.Exec(`INSERT INTO audit_log_state(id, inode, offset, updated_at) VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET inode = excluded.inode, offset = excluded.offset, updated_at = excluded.updated_at`,
		int64(s.Inode), s.Offset, formatTime(time.Now()))
	return err
}
