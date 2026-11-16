package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"server-monitor/internal/alerts"
	"server-monitor/internal/notify"
	"server-monitor/internal/secret"
	"server-monitor/internal/storage"
)

// ConnectorStore, AlertSettingsStore i NotificationStore to zależności endpointów alertów.
type ConnectorStore interface {
	Get(typ string) (*storage.Connector, error)
	Upsert(c storage.Connector, now time.Time) error
}
type AlertSettingsStore interface {
	Get() (string, bool, error)
	Set(settingsJSON string, now time.Time) error
}
type NotificationStore interface {
	List(limit, offset int) ([]storage.Notification, error)
}

// ConnectorDTO opisuje konektor w GET /api/settings/connectors. Sekret nigdy nie jest zwracany —
// tylko informacja, że jest zapisany (has_secret).
type ConnectorDTO struct {
	Type       string         `json:"type"`
	Label      string         `json:"label"`
	Configured bool           `json:"configured"`
	Enabled    bool           `json:"enabled"`
	HasSecret  bool           `json:"has_secret"`
	Config     map[string]any `json:"config"`
}

// ConnectorsResponse to odpowiedź GET /api/settings/connectors (wszystkie dostępne typy konektorów).
type ConnectorsResponse struct {
	Connectors    []ConnectorDTO `json:"connectors"`
	SecretStorage bool           `json:"secret_storage"` // false = brak SM_SECRET_KEY, haseł nie da się zapisać
}

// ConnectorUpdateRequest to body PUT /api/settings/connectors/{type}.
// password: null = zostaw zapisane hasło, "" = usuń, niepusty = ustaw nowe.
type ConnectorUpdateRequest struct {
	Enabled  bool           `json:"enabled"`
	Config   map[string]any `json:"config"`
	Password *string        `json:"password"`
}

// TestConnectorResponse to odpowiedź POST /api/settings/connectors/{type}/test.
type TestConnectorResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// NotificationDTO to element GET /api/alerts/history.
type NotificationDTO struct {
	ID        int64   `json:"id"`
	Kind      string  `json:"kind"`
	Connector string  `json:"connector"`
	Subject   string  `json:"subject"`
	Body      string  `json:"body"`
	Status    string  `json:"status"`
	Attempts  int     `json:"attempts"`
	LastError string  `json:"last_error"`
	CreatedAt string  `json:"created_at"`
	SentAt    *string `json:"sent_at"`
}

// NotificationsResponse to odpowiedź GET /api/alerts/history.
type NotificationsResponse struct {
	Notifications []NotificationDTO `json:"notifications"`
	Limit         int               `json:"limit"`
	Offset        int               `json:"offset"`
}

func (s *server) alertsUnavailable(w http.ResponseWriter, ok bool) bool {
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "alerting is not available")
	}
	return !ok
}

// readJSON wymaga Content-Type: application/json (ochrona przed CSRF), ogranicza rozmiar i odrzuca nieznane pola.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil || dec.More() {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func (s *server) handleGetAlertSettings(w http.ResponseWriter, r *http.Request) {
	if s.alertsUnavailable(w, s.cfg.AlertSettings != nil) {
		return
	}
	set, err := alerts.LoadSettings(s.cfg.AlertSettings)
	if err != nil {
		s.logf("api: %v", err) // uszkodzone ustawienia: UI dostaje bezpieczne domyślne i może je nadpisać
	}
	writeJSON(w, http.StatusOK, set)
}

func (s *server) handlePutAlertSettings(w http.ResponseWriter, r *http.Request) {
	if s.alertsUnavailable(w, s.cfg.AlertSettings != nil) {
		return
	}
	var in alerts.Settings
	if !readJSON(w, r, &in) {
		return
	}
	norm, err := in.Normalize(alerts.KnownChannel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	b, err := json.Marshal(norm)
	if err != nil {
		s.internal(w, "marshal alert settings", err)
		return
	}
	if err := s.cfg.AlertSettings.Set(string(b), s.now()); err != nil {
		s.internal(w, "save alert settings", err)
		return
	}
	writeJSON(w, http.StatusOK, norm)
}

func (s *server) connectorDTO(t notify.Type, c *storage.Connector) ConnectorDTO {
	d := ConnectorDTO{Type: t.Name, Label: t.Label, Config: map[string]any{}}
	if c == nil {
		return d
	}
	d.Configured, d.Enabled, d.HasSecret = true, c.Enabled, c.SecretEnc != ""
	if err := json.Unmarshal([]byte(c.ConfigJSON), &d.Config); err != nil || d.Config == nil {
		d.Config = map[string]any{}
	}
	return d
}

func (s *server) handleListConnectors(w http.ResponseWriter, r *http.Request) {
	if s.alertsUnavailable(w, s.cfg.Connectors != nil) {
		return
	}
	out := ConnectorsResponse{Connectors: []ConnectorDTO{}, SecretStorage: s.cfg.Secrets != nil}
	for _, t := range notify.Types() {
		c, err := s.cfg.Connectors.Get(t.Name)
		if err != nil {
			s.internal(w, "get connector", err)
			return
		}
		out.Connectors = append(out.Connectors, s.connectorDTO(t, c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handlePutConnector(w http.ResponseWriter, r *http.Request) {
	if s.alertsUnavailable(w, s.cfg.Connectors != nil) {
		return
	}
	typ, ok := notify.Lookup(r.PathValue("type"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown connector type")
		return
	}
	var in ConnectorUpdateRequest
	if !readJSON(w, r, &in) {
		return
	}
	rawCfg, err := json.Marshal(in.Config)
	if err != nil || in.Config == nil {
		writeError(w, http.StatusBadRequest, `"config" is required`)
		return
	}
	cfg, err := typ.Validate(rawCfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := s.cfg.Connectors.Get(typ.Name)
	if err != nil {
		s.internal(w, "get connector", err)
		return
	}
	enc := ""
	if existing != nil {
		enc = existing.SecretEnc
	}
	if in.Password != nil {
		if !typ.HasSecret && *in.Password != "" {
			writeError(w, http.StatusBadRequest, "this connector does not use a password")
			return
		}
		if enc, err = s.cfg.Secrets.Encrypt(*in.Password); err != nil {
			if errors.Is(err, secret.ErrNoKey) {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			s.internal(w, "encrypt secret", err)
			return
		}
	}
	saved := storage.Connector{Type: typ.Name, Enabled: in.Enabled, ConfigJSON: string(cfg), SecretEnc: enc}
	if err := s.cfg.Connectors.Upsert(saved, s.now()); err != nil {
		s.internal(w, "save connector", err)
		return
	}
	writeJSON(w, http.StatusOK, s.connectorDTO(typ, &saved))
}

func (s *server) handleTestConnector(w http.ResponseWriter, r *http.Request) {
	if s.alertsUnavailable(w, s.cfg.Connectors != nil && s.cfg.TestConnector != nil) {
		return
	}
	typ, ok := notify.Lookup(r.PathValue("type"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown connector type")
		return
	}
	var in struct{}
	if !readJSON(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 18*time.Second) // < WriteTimeout serwera
	defer cancel()
	if err := s.cfg.TestConnector(ctx, typ.Name); err != nil {
		msg := strings.Join(strings.Fields(err.Error()), " ")
		if len(msg) > 300 {
			msg = msg[:300]
		}
		writeJSON(w, http.StatusOK, TestConnectorResponse{OK: false, Error: msg})
		return
	}
	writeJSON(w, http.StatusOK, TestConnectorResponse{OK: true})
}

func (s *server) handleAlertHistory(w http.ResponseWriter, r *http.Request) {
	if s.alertsUnavailable(w, s.cfg.Notifications != nil) {
		return
	}
	limit, offset, err := pageParams(r.URL.Query(), 50)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := s.cfg.Notifications.List(limit, offset)
	if err != nil {
		s.internal(w, "list notifications", err)
		return
	}
	out := NotificationsResponse{Notifications: make([]NotificationDTO, 0, len(rows)), Limit: limit, Offset: offset}
	for _, n := range rows {
		d := NotificationDTO{ID: n.ID, Kind: n.Kind, Connector: n.Connector, Subject: n.Subject, Body: n.Body,
			Status: n.Status, Attempts: n.Attempts, LastError: n.LastError, CreatedAt: ts(n.CreatedAt)}
		if n.SentAt != nil {
			v := ts(*n.SentAt)
			d.SentAt = &v
		}
		out.Notifications = append(out.Notifications, d)
	}
	writeJSON(w, http.StatusOK, out)
}
