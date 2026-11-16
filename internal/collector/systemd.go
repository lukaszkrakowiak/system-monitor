package collector

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"time"

	"server-monitor/internal/parser"
	"server-monitor/internal/storage"
)

var unitNameRe = regexp.MustCompile(`^[A-Za-z0-9_.@:\\][A-Za-z0-9_.@:\\-]*\.service$`)

// ValidUnitName sprawdza nazwę unitu przed przekazaniem do narzędzia systemd:
// wymaga sufiksu .service i zabrania wiodącego '-' (brak wstrzyknięcia opcji).
func ValidUnitName(s string) bool {
	return len(s) <= 256 && unitNameRe.MatchString(s)
}

// Systemd odczytuje stan usług bez sudo.
type Systemd struct {
	Runner Runner
	Bin    string // ścieżka/nazwa narzędzia systemd; domyślnie "systemctl"
}

func (s *Systemd) bin() string {
	if s.Bin == "" {
		return "systemctl"
	}
	return s.Bin
}

// ListUnits zwraca wszystkie usługi (GET /api/systemd/units).
func (s *Systemd) ListUnits(ctx context.Context) ([]parser.Unit, error) {
	ctx, cancel := context.WithTimeout(ctx, CommandTimeout)
	defer cancel()
	out, err := s.Runner.Run(ctx, s.bin(), "list-units", "--type=service", "--all", "--no-pager", "--output=json")
	if err != nil {
		return nil, fmt.Errorf("list-units: %w", err)
	}
	return parser.ParseListUnits(out)
}

// Show pobiera stan wskazanych unitów jednym wywołaniem.
func (s *Systemd) Show(ctx context.Context, units []string) ([]parser.UnitShow, error) {
	if len(units) == 0 {
		return nil, nil
	}
	for _, u := range units {
		if !ValidUnitName(u) {
			return nil, fmt.Errorf("invalid unit name %q", u)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, CommandTimeout)
	defer cancel()
	args := append([]string{"show", "--property=Id,ActiveState,SubState,MainPID,MemoryCurrent,ExecMainStartTimestamp"}, units...)
	out, err := s.Runner.Run(ctx, s.bin(), args...)
	if err != nil {
		return nil, fmt.Errorf("show: %w", err)
	}
	return parser.ParseShow(out)
}

// SystemdUnit to element tablicy "systemd" w GET /api/health.
type SystemdUnit struct {
	Unit        string `json:"unit"`
	DisplayName string `json:"display_name"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Status      string `json:"status"`
	MemoryBytes *int64 `json:"memory_bytes"`
	Uptime      string `json:"uptime"`
}

// SystemdPayload to payload_json wpisu 'systemd' w cache.
type SystemdPayload struct {
	Units []SystemdUnit `json:"units"`
	Error string        `json:"error,omitempty"`
}

// SystemdProbe odświeża stan wybranych usług (is_enabled = 1).
type SystemdProbe struct {
	Systemd *Systemd
	Units   func() ([]storage.MonitoredService, error)
	Now     func() time.Time // domyślnie time.Now
	Loc     *time.Location   // strefa do parsowania znaczników systemd; domyślnie time.Local
}

func (p *SystemdProbe) Key() string { return "systemd" }

func (p *SystemdProbe) Check(ctx context.Context) Result {
	fail := func(err error) Result {
		return Result{"CRIT", SystemdPayload{Units: []SystemdUnit{}, Error: err.Error()}}
	}
	svcs, err := p.Units()
	if err != nil {
		return fail(err)
	}
	names := make([]string, 0, len(svcs))
	for _, s := range svcs {
		names = append(names, s.UnitName)
	}
	shows, err := p.Systemd.Show(ctx, names)
	if err != nil {
		return fail(err)
	}
	byID := make(map[string]parser.UnitShow, len(shows))
	for _, s := range shows {
		byID[s.ID] = s
	}
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	loc := p.Loc
	if loc == nil {
		loc = time.Local
	}
	units := make([]SystemdUnit, 0, len(svcs))
	statuses := make([]string, 0, len(svcs))
	for _, s := range svcs {
		u := SystemdUnit{Unit: s.UnitName, DisplayName: s.DisplayName}
		sh, ok := byID[s.UnitName]
		if !ok {
			u.Active, u.Status = "unknown", "WARN"
		} else {
			u.Active, u.Sub = sh.ActiveState, sh.SubState
			u.Status = parser.SystemdStatus(sh.ActiveState, sh.SubState)
			if sh.MemoryCurrent != nil && *sh.MemoryCurrent <= math.MaxInt64 {
				m := int64(*sh.MemoryCurrent)
				u.MemoryBytes = &m
			}
			if sh.ActiveState == "active" {
				if t, ok := parser.ParseSystemdTimestamp(sh.ExecMainStart, loc); ok {
					u.Uptime = parser.FormatUptime(now.Sub(t))
				}
			}
		}
		units = append(units, u)
		statuses = append(statuses, u.Status)
	}
	return Result{Worst(statuses...), SystemdPayload{Units: units}}
}
