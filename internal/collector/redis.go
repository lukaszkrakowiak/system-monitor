package collector

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"server-monitor/internal/parser"
)

// RedisPayload: clusters.redis w GET /api/health.
type RedisPayload struct {
	Status           string  `json:"status"`
	PingMS           float64 `json:"ping_ms"`
	UsedMemoryHuman  string  `json:"used_memory_human"`
	ConnectedClients int     `json:"connected_clients"`
	UptimeSeconds    int64   `json:"uptime_in_seconds"`
	Error            string  `json:"error,omitempty"`
}

// Redis sonduje przez TCP lub gniazdo UNIX: [AUTH] + PING + INFO.
type Redis struct {
	Network  string // "tcp" (domyślnie) lub "unix"
	Addr     string
	Password string
}

func (p *Redis) Key() string { return "redis" }

func encodeCommand(args ...string) []byte {
	b := []byte("*" + strconv.Itoa(len(args)) + "\r\n")
	for _, a := range args {
		b = append(b, "$"+strconv.Itoa(len(a))+"\r\n"...)
		b = append(b, a...)
		b = append(b, "\r\n"...)
	}
	return b
}

func roundMS(d time.Duration) float64 {
	return float64(d.Microseconds()/10) / 100
}

func (p *Redis) Check(ctx context.Context) Result {
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	res, err := p.probe(ctx)
	if err != nil {
		return Result{"CRIT", RedisPayload{Status: "CRIT", Error: err.Error()}}
	}
	return Result{"OK", res}
}

func (p *Redis) probe(ctx context.Context) (RedisPayload, error) {
	network := p.Network
	if network == "" {
		network = "tcp"
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, network, p.Addr)
	if err != nil {
		return RedisPayload{}, err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	br := bufio.NewReader(conn)
	do := func(args ...string) (parser.RESP, error) {
		if _, err := conn.Write(encodeCommand(args...)); err != nil {
			return parser.RESP{}, err
		}
		return parser.ReadRESP(br)
	}
	if p.Password != "" {
		r, err := do("AUTH", p.Password)
		if err != nil {
			return RedisPayload{}, err
		}
		if r.Kind == '-' {
			return RedisPayload{}, fmt.Errorf("auth failed: %s", r.Str)
		}
	}
	start := time.Now()
	r, err := do("PING")
	if err != nil {
		return RedisPayload{}, err
	}
	ping := time.Since(start)
	if r.Kind != '+' || r.Str != "PONG" {
		return RedisPayload{}, fmt.Errorf("unexpected PING reply %q", string(r.Kind)+r.Str)
	}
	r, err = do("INFO", "server", "memory", "clients")
	if err == nil && r.Kind == '-' { // starsze wersje Redisa: bez wielu sekcji
		r, err = do("INFO")
	}
	if err != nil {
		return RedisPayload{}, err
	}
	if r.Kind != '$' || r.Nil {
		return RedisPayload{}, fmt.Errorf("unexpected INFO reply %q", string(r.Kind)+r.Str)
	}
	info, err := parser.ParseRedisInfo(r.Str)
	if err != nil {
		return RedisPayload{}, err
	}
	return RedisPayload{Status: "OK", PingMS: roundMS(ping), UsedMemoryHuman: info.UsedMemoryHuman,
		ConnectedClients: info.ConnectedClients, UptimeSeconds: info.UptimeSeconds}, nil
}
