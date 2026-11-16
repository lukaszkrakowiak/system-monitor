package alerts

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"server-monitor/internal/collector"
	"server-monitor/internal/storage"
)

// Item to pojedynczy obserwowany element (usługa systemd, grupa supervisora, sonda klastra).
type Item struct {
	Key     string // stabilny identyfikator, np. "systemd:nginx.service", "redis"
	Name    string // nazwa dla człowieka
	Status  string // OK | WARN | CRIT
	Detail  string
	Checked time.Time
}

// supervisorStatus mapuje statusy grup supervisora na OK/WARN/CRIT.
func supervisorStatus(s string) string {
	switch s {
	case "OK":
		return "OK"
	case "DEGRADED":
		return "WARN"
	default:
		return "CRIT"
	}
}

func normStatus(s string) string {
	switch s {
	case "OK", "WARN", "CRIT":
		return s
	}
	return "CRIT" // nieznany stan to nie OK
}

// Flatten rozbija wpisy health_checks_cache na pojedyncze elementy. Wpisy z uszkodzonym payloadem
// dają jeden element CRIT, żeby awaria danych nie była cicha.
func Flatten(entries []storage.HealthEntry) []Item {
	var items []Item
	for _, e := range entries {
		bad := func(err error) {
			items = append(items, Item{Key: e.ServiceKey, Name: e.ServiceKey, Status: "CRIT",
				Detail: "unreadable health data: " + err.Error(), Checked: e.CheckedAt})
		}
		switch e.ServiceKey {
		case "systemd":
			var p collector.SystemdPayload
			if err := json.Unmarshal([]byte(e.PayloadJSON), &p); err != nil {
				bad(err)
				continue
			}
			if p.Error != "" {
				items = append(items, Item{Key: "systemd", Name: "systemd collector", Status: normStatus(e.Status), Detail: p.Error, Checked: e.CheckedAt})
			}
			for _, u := range p.Units {
				items = append(items, Item{Key: "systemd:" + u.Unit, Name: u.Unit, Status: normStatus(u.Status),
					Detail: fmt.Sprintf("active=%s sub=%s", u.Active, u.Sub), Checked: e.CheckedAt})
			}
		case "supervisor":
			var p collector.SupervisorPayload
			if err := json.Unmarshal([]byte(e.PayloadJSON), &p); err != nil {
				bad(err)
				continue
			}
			if p.Error != "" {
				items = append(items, Item{Key: "supervisor", Name: "supervisor", Status: normStatus(e.Status), Detail: p.Error, Checked: e.CheckedAt})
			}
			for _, g := range p.Groups {
				var failing []string
				for _, w := range g.Workers {
					if w.State != "RUNNING" {
						failing = append(failing, fmt.Sprintf("%s (%s)", w.Name, w.State))
					}
				}
				d := fmt.Sprintf("%d/%d workers running", g.Running, g.Total)
				if len(failing) > 0 {
					d += "; not running: " + strings.Join(failing, ", ")
				}
				items = append(items, Item{Key: "supervisor:" + g.Group, Name: "supervisor group " + g.Group,
					Status: supervisorStatus(g.Status), Detail: d, Checked: e.CheckedAt})
			}
		case "elasticsearch":
			var p collector.ElasticsearchPayload
			if err := json.Unmarshal([]byte(e.PayloadJSON), &p); err != nil {
				bad(err)
				continue
			}
			d := p.Error
			if d == "" {
				d = fmt.Sprintf("cluster %q is %s, %d node(s), %d unassigned shard(s)", p.ClusterName, p.ClusterStatus, p.Nodes, p.UnassignedShards)
			}
			items = append(items, Item{Key: "elasticsearch", Name: "Elasticsearch", Status: normStatus(p.Status), Detail: d, Checked: e.CheckedAt})
		case "redis":
			var p collector.RedisPayload
			if err := json.Unmarshal([]byte(e.PayloadJSON), &p); err != nil {
				bad(err)
				continue
			}
			d := p.Error
			if d == "" {
				d = fmt.Sprintf("ping %.2f ms, %d client(s)", p.PingMS, p.ConnectedClients)
			}
			items = append(items, Item{Key: "redis", Name: "Redis", Status: normStatus(p.Status), Detail: d, Checked: e.CheckedAt})
		case "kibana":
			var p collector.KibanaPayload
			if err := json.Unmarshal([]byte(e.PayloadJSON), &p); err != nil {
				bad(err)
				continue
			}
			d := p.Error
			if d == "" {
				d = fmt.Sprintf("level %s, response %d ms", p.OverallLevel, p.ResponseMS)
			}
			items = append(items, Item{Key: "kibana", Name: "Kibana", Status: normStatus(p.Status), Detail: d, Checked: e.CheckedAt})
		default:
			items = append(items, Item{Key: e.ServiceKey, Name: e.ServiceKey, Status: normStatus(e.Status), Checked: e.CheckedAt})
		}
	}
	return items
}

// itemState to utrwalony stan elementu: ostatni POTWIERDZONY status i kandydat na nowy status.
type itemState struct {
	Status       string    `json:"status"` // pusty = jeszcze nie widziano
	Since        time.Time `json:"since"`
	Pending      string    `json:"pending,omitempty"`
	PendingCount int       `json:"pending_count,omitempty"`
	PendingSince time.Time `json:"pending_since,omitempty"`
	Checked      time.Time `json:"checked"` // checked_at ostatnio przetworzonej obserwacji
}

// Transition to potwierdzona zmiana stanu elementu.
type Transition struct {
	Item     Item
	Previous string // "" gdy element widziany pierwszy raz
	Since    time.Time
}

// observe przetwarza jedną obserwację elementu i zwraca nowy stan oraz (ewentualnie) potwierdzone przejście.
// Zmiana jest potwierdzana dopiero po `confirm` kolejnych RÓŻNYCH sprawdzeniach (checked_at) z tym samym
// nowym stanem, co chroni przed alertem po pojedynczym nieudanym pomiarze. Pierwsza obserwacja OK nie
// jest przejściem; pierwsza obserwacja nie-OK także musi zostać potwierdzona.
func observe(st itemState, it Item, confirm int) (itemState, *Transition) {
	if !st.Checked.IsZero() && !it.Checked.After(st.Checked) {
		return st, nil // ta sama (już przetworzona) obserwacja z cache
	}
	st.Checked = it.Checked
	if st.Status == "" && it.Status == "OK" {
		st.Status, st.Since = "OK", it.Checked
		return st, nil
	}
	if it.Status == st.Status {
		st.Pending, st.PendingCount = "", 0
		return st, nil
	}
	if st.Pending == it.Status {
		st.PendingCount++
	} else {
		st.Pending, st.PendingCount, st.PendingSince = it.Status, 1, it.Checked
	}
	if st.PendingCount < confirm {
		return st, nil
	}
	prev, since := st.Status, st.PendingSince
	st.Status, st.Since = it.Status, since
	st.Pending, st.PendingCount = "", 0
	return st, &Transition{Item: it, Previous: prev, Since: since}
}
