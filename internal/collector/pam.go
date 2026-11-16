package collector

import (
	"errors"
	"fmt"
	"time"

	"server-monitor/internal/storage"
)

// SessionStore to wycinek repozytorium ssh_sessions używany przez ingest-pam.
type SessionStore interface {
	Open(storage.SSHSession) (int64, error)
	Close(username, tty string, at time.Time) (bool, error)
}

const maxPAMField = 256

func clip(s string) string {
	if len(s) > maxPAMField {
		return s[:maxPAMField]
	}
	return s
}

// IngestPAM obsługuje wywołanie pam_exec (zmienne PAM_USER, PAM_RHOST, PAM_TTY,
// PAM_SERVICE, PAM_TYPE): open_session wstawia sesję, close_session ustawia logout_at
// najnowszej otwartej sesji username+tty. Inne typy są ignorowane (nil).
func IngestPAM(getenv func(string) string, s SessionStore, now time.Time) error {
	typ := getenv("PAM_TYPE")
	if typ != "open_session" && typ != "close_session" {
		return nil
	}
	username := clip(getenv("PAM_USER"))
	if username == "" {
		return errors.New("ingest-pam: PAM_USER is empty")
	}
	tty := clip(getenv("PAM_TTY"))
	if typ == "open_session" {
		_, err := s.Open(storage.SSHSession{
			Username:   username,
			RemoteIP:   clip(getenv("PAM_RHOST")),
			TTY:        tty,
			PAMService: clip(getenv("PAM_SERVICE")),
			LoginAt:    now,
		})
		if err != nil {
			return fmt.Errorf("ingest-pam: open session: %w", err)
		}
		return nil
	}
	if _, err := s.Close(username, tty, now); err != nil {
		return fmt.Errorf("ingest-pam: close session: %w", err)
	}
	return nil
}
