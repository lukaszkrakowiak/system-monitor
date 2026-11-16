// Package parser zawiera czyste parsery (bez I/O sieciowego i exec).
package parser

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Unit to wpis z `systemctl list-units --output=json`.
type Unit struct {
	Name        string `json:"unit"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Description string `json:"description"`
}

// ParseListUnits parsuje wyjście `systemctl list-units --type=service --all --output=json`.
func ParseListUnits(data []byte) ([]Unit, error) {
	var units []Unit
	if err := json.Unmarshal(data, &units); err != nil {
		return nil, fmt.Errorf("parser: systemctl list-units: %w", err)
	}
	return units, nil
}

// UnitShow to jeden blok z `systemctl show --property=...`.
type UnitShow struct {
	ID            string
	ActiveState   string
	SubState      string
	MainPID       int
	MemoryCurrent *uint64 // nil gdy "[not set]" lub wartość sentinel (max uint64)
	ExecMainStart string  // surowy znacznik czasu; pusty gdy brak
}

// ParseShow parsuje wyjście `systemctl show a b c`: bloki key=value rozdzielone pustą linią.
func ParseShow(data []byte) ([]UnitShow, error) {
	var out []UnitShow
	cur := map[string]string{}
	flush := func() error {
		if len(cur) == 0 {
			return nil
		}
		defer func() { cur = map[string]string{} }()
		u := UnitShow{ID: cur["Id"], ActiveState: cur["ActiveState"], SubState: cur["SubState"],
			ExecMainStart: cur["ExecMainStartTimestamp"]}
		if u.ID == "" {
			return fmt.Errorf("parser: systemctl show: block without Id")
		}
		if v := cur["MainPID"]; v != "" {
			pid, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("parser: systemctl show %s: MainPID %q: %w", u.ID, v, err)
			}
			u.MainPID = pid
		}
		if v := cur["MemoryCurrent"]; v != "" && v != "[not set]" {
			m, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				return fmt.Errorf("parser: systemctl show %s: MemoryCurrent %q: %w", u.ID, v, err)
			}
			if m != math.MaxUint64 {
				u.MemoryCurrent = &m
			}
		}
		out = append(out, u)
		return nil
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		cur[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

// SystemdStatus mapuje ActiveState/SubState na OK | WARN | CRIT.
// active (w tym exited dla oneshot z RemainAfterExit) -> OK;
// activating/reloading/deactivating -> WARN; failed/inactive -> CRIT; nieznane -> WARN.
func SystemdStatus(active, sub string) string {
	switch active {
	case "active":
		if sub == "failed" || sub == "dead" {
			return "CRIT"
		}
		return "OK"
	case "activating", "reloading", "deactivating":
		return "WARN"
	case "failed", "inactive":
		return "CRIT"
	}
	return "WARN"
}

const systemdTimeLayout = "Mon 2006-01-02 15:04:05 MST"

// ParseSystemdTimestamp parsuje np. "Wed 2026-11-04 06:00:00 UTC". Skrót strefy jest
// rozwiązywany względem loc (strefa lokalna hosta, jak w systemd). Pusty/"n/a" -> false.
func ParseSystemdTimestamp(s string, loc *time.Location) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "n/a" || s == "0" {
		return time.Time{}, false
	}
	if loc == nil {
		loc = time.UTC
	}
	t, err := time.ParseInLocation(systemdTimeLayout, s, loc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// FormatUptime zwraca "12d 4h", "4h 12m" lub "12m".
func FormatUptime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d / (24 * time.Hour))
	hours := int(d/time.Hour) % 24
	mins := int(d/time.Minute) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}
