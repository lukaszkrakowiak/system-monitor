package collector

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"

	"server-monitor/internal/parser"
)

const getAllProcessInfoCall = `<?xml version="1.0"?><methodCall><methodName>supervisor.getAllProcessInfo</methodName><params></params></methodCall>`

// Supervisor odpytuje supervisord przez XML-RPC po gnieździe UNIX (nigdy przez sudo).
type Supervisor struct {
	SocketPath string // np. /var/run/supervisor.sock
}

// Fetch wywołuje supervisor.getAllProcessInfo.
func (s *Supervisor) Fetch(ctx context.Context) ([]parser.SupervisorProcess, error) {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", s.SocketPath)
		},
		DisableKeepAlives: true,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://supervisor/RPC2", bytes.NewReader([]byte(getAllProcessInfoCall)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("supervisor socket: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("supervisor: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	return parser.ParseSupervisorProcessInfo(body)
}

// SupervisorPayload to payload_json wpisu 'supervisor'.
type SupervisorPayload struct {
	Groups []parser.SupervisorGroup `json:"groups"`
	Error  string                   `json:"error,omitempty"`
}

// SupervisorProbe agreguje procesy w grupy.
type SupervisorProbe struct{ Supervisor *Supervisor }

func (p *SupervisorProbe) Key() string { return "supervisor" }

func (p *SupervisorProbe) Check(ctx context.Context) Result {
	procs, err := p.Supervisor.Fetch(ctx)
	if err != nil {
		return Result{"CRIT", SupervisorPayload{Groups: []parser.SupervisorGroup{}, Error: err.Error()}}
	}
	groups := parser.GroupSupervisor(procs)
	st := make([]string, 0, len(groups))
	for _, g := range groups {
		st = append(st, parser.SupervisorCacheStatus(g.Status))
	}
	return Result{Worst(st...), SupervisorPayload{Groups: groups}}
}
