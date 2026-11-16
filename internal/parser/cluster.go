package parser

import (
	"encoding/json"
	"fmt"
)

// ESHealth to wynik GET /_cluster/health.
type ESHealth struct {
	ClusterName         string
	ClusterStatus       string // green | yellow | red
	Status              string // OK | WARN | CRIT
	Nodes               int
	UnassignedShards    int
	ActiveShardsPercent float64
}

// ParseESHealth parsuje odpowiedź _cluster/health. Odpowiedź błędu (status liczbowy)
// kończy się błędem.
func ParseESHealth(data []byte) (ESHealth, error) {
	var raw struct {
		ClusterName      string  `json:"cluster_name"`
		Status           string  `json:"status"`
		Nodes            int     `json:"number_of_nodes"`
		Unassigned       int     `json:"unassigned_shards"`
		ActivePercentNum float64 `json:"active_shards_percent_as_number"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return ESHealth{}, fmt.Errorf("parser: elasticsearch health: %w", err)
	}
	h := ESHealth{ClusterName: raw.ClusterName, ClusterStatus: raw.Status, Nodes: raw.Nodes,
		UnassignedShards: raw.Unassigned, ActiveShardsPercent: raw.ActivePercentNum}
	switch raw.Status {
	case "green":
		h.Status = "OK"
	case "yellow":
		h.Status = "WARN"
	case "red":
		h.Status = "CRIT"
	default:
		return ESHealth{}, fmt.Errorf("parser: elasticsearch health: unknown status %q", raw.Status)
	}
	return h, nil
}

// KibanaStatus to wynik GET /api/status.
type KibanaStatus struct {
	OverallLevel string // available | degraded | critical
	Status       string // OK | WARN | CRIT
}

// ParseKibanaStatus obsługuje format 8.x (status.overall.level) oraz starszy
// (status.overall.state: green/yellow/red).
func ParseKibanaStatus(data []byte) (KibanaStatus, error) {
	var raw struct {
		Status struct {
			Overall struct {
				Level string `json:"level"`
				State string `json:"state"`
			} `json:"overall"`
		} `json:"status"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return KibanaStatus{}, fmt.Errorf("parser: kibana status: %w", err)
	}
	lvl := raw.Status.Overall.Level
	if lvl == "" {
		switch raw.Status.Overall.State {
		case "green":
			lvl = "available"
		case "yellow":
			lvl = "degraded"
		case "red":
			lvl = "critical"
		}
	}
	switch lvl {
	case "available":
		return KibanaStatus{lvl, "OK"}, nil
	case "degraded":
		return KibanaStatus{lvl, "WARN"}, nil
	case "critical", "unavailable":
		return KibanaStatus{lvl, "CRIT"}, nil
	}
	return KibanaStatus{}, fmt.Errorf("parser: kibana status: unknown overall level %q", lvl)
}
