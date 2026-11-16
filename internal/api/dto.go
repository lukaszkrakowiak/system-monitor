// Package api zawiera router HTTP, middleware autoryzacji i handlery REST.
// Struktury JSON muszą być zgodne z web/src/types/api.ts oraz docs/SPEC.md.
package api

import (
	"server-monitor/internal/collector"
	"server-monitor/internal/parser"
)

// HealthResponse to odpowiedź GET /api/health.
type HealthResponse struct {
	OverallStatus string                   `json:"overall_status"` // OK | WARN | CRIT
	GeneratedAt   string                   `json:"generated_at"`   // RFC 3339, UTC
	Systemd       []collector.SystemdUnit  `json:"systemd"`
	Supervisor    []parser.SupervisorGroup `json:"supervisor"`
	Clusters      Clusters                 `json:"clusters"`
}

// Clusters grupuje sondy klastrowe; niekonfigurowane sondy są pomijane.
type Clusters struct {
	Elasticsearch *collector.ElasticsearchPayload `json:"elasticsearch,omitempty"`
	Redis         *collector.RedisPayload         `json:"redis,omitempty"`
	Kibana        *collector.KibanaPayload        `json:"kibana,omitempty"`
}

// UnitDTO to element GET /api/systemd/units.
type UnitDTO struct {
	Unit        string `json:"unit"`
	Description string `json:"description"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Selected    bool   `json:"selected"`
}

// UnitsResponse to odpowiedź GET /api/systemd/units.
type UnitsResponse struct {
	Units []UnitDTO `json:"units"`
}

// SelectionRequest to body PUT /api/systemd/units/selection; odpowiedź ma ten sam kształt.
type SelectionRequest struct {
	Units []string `json:"units"`
}

// SessionDTO to element GET /api/audit/sessions.
type SessionDTO struct {
	ID         int64   `json:"id"`
	Username   string  `json:"username"`
	RemoteIP   string  `json:"remote_ip"`
	TTY        string  `json:"tty"`
	PAMService string  `json:"pam_service"`
	LoginAt    string  `json:"login_at"`
	LogoutAt   *string `json:"logout_at"`
}

// SessionsResponse to odpowiedź GET /api/audit/sessions.
type SessionsResponse struct {
	Sessions []SessionDTO `json:"sessions"`
	Limit    int          `json:"limit"`
	Offset   int          `json:"offset"`
}

// CommandDTO to element GET /api/audit/commands.
type CommandDTO struct {
	ID                int64  `json:"id"`
	AUID              int64  `json:"auid"`
	LoginUsername     string `json:"login_username"`
	EffectiveUsername string `json:"effective_username"`
	CWD               string `json:"cwd"`
	Command           string `json:"command"`
	ExitCode          *int   `json:"exit_code"`
	ExecutedAt        string `json:"executed_at"`
}

// CommandsResponse to odpowiedź GET /api/audit/commands.
type CommandsResponse struct {
	Commands []CommandDTO `json:"commands"`
	Limit    int          `json:"limit"`
	Offset   int          `json:"offset"`
}

// LoginFailureDTO to element GET /api/audit/login-failures.
type LoginFailureDTO struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	RemoteIP    string `json:"remote_ip"`
	Service     string `json:"service"`
	AttemptedAt string `json:"attempted_at"`
}

// LoginFailuresResponse to odpowiedź GET /api/audit/login-failures.
type LoginFailuresResponse struct {
	Failures []LoginFailureDTO `json:"failures"`
	Limit    int               `json:"limit"`
	Offset   int               `json:"offset"`
}

// ErrorResponse to ciało błędów.
type ErrorResponse struct {
	Error string `json:"error"`
}
