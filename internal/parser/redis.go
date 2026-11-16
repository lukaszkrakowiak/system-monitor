package parser

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// RESP to odpowiedź protokołu Redis (podzbiór: +, -, :, $).
type RESP struct {
	Kind byte // '+', '-', ':', '$'
	Str  string
	Int  int64
	Nil  bool
}

const maxBulk = 1 << 20

// ReadRESP czyta jedną odpowiedź RESP. Odpowiedź błędu (-ERR) zwracana jest jako
// RESP{Kind:'-'}, nie jako error.
func ReadRESP(r *bufio.Reader) (RESP, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return RESP{}, fmt.Errorf("parser: resp: %w", err)
	}
	if len(line) < 3 || !strings.HasSuffix(line, "\r\n") {
		return RESP{}, fmt.Errorf("parser: resp: malformed line %q", line)
	}
	body := line[1 : len(line)-2]
	switch line[0] {
	case '+', '-':
		return RESP{Kind: line[0], Str: body}, nil
	case ':':
		n, err := strconv.ParseInt(body, 10, 64)
		if err != nil {
			return RESP{}, fmt.Errorf("parser: resp integer: %w", err)
		}
		return RESP{Kind: ':', Int: n}, nil
	case '$':
		n, err := strconv.Atoi(body)
		if err != nil || n < -1 || n > maxBulk {
			return RESP{}, fmt.Errorf("parser: resp bulk length %q", body)
		}
		if n == -1 {
			return RESP{Kind: '$', Nil: true}, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return RESP{}, fmt.Errorf("parser: resp bulk: %w", err)
		}
		if buf[n] != '\r' || buf[n+1] != '\n' {
			return RESP{}, fmt.Errorf("parser: resp bulk: missing terminator")
		}
		return RESP{Kind: '$', Str: string(buf[:n])}, nil
	}
	return RESP{}, fmt.Errorf("parser: resp: unsupported type %q", line[0])
}

// RedisInfo to pola wyciągnięte z INFO server,memory,clients.
type RedisInfo struct {
	UsedMemoryHuman  string
	ConnectedClients int
	UptimeSeconds    int64
}

// ParseRedisInfo parsuje tekst odpowiedzi INFO.
func ParseRedisInfo(text string) (RedisInfo, error) {
	var info RedisInfo
	found := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch k {
		case "used_memory_human":
			info.UsedMemoryHuman = v
			found++
		case "connected_clients":
			n, err := strconv.Atoi(v)
			if err != nil {
				return RedisInfo{}, fmt.Errorf("parser: redis info connected_clients %q: %w", v, err)
			}
			info.ConnectedClients = n
			found++
		case "uptime_in_seconds":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return RedisInfo{}, fmt.Errorf("parser: redis info uptime_in_seconds %q: %w", v, err)
			}
			info.UptimeSeconds = n
			found++
		}
	}
	if found < 3 {
		return RedisInfo{}, fmt.Errorf("parser: redis info: missing fields (found %d of 3)", found)
	}
	return info, nil
}
