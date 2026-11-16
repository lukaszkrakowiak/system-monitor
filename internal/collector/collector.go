// Package collector zawiera sondy (systemd, supervisor, Elasticsearch, Redis, Kibana).
// Każda sonda sieciowa ma sztywny budżet ProbeTimeout.
package collector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"
)

// ProbeTimeout to maksymalny czas pojedynczej sondy sieciowej (CLAUDE.md §1).
const ProbeTimeout = 1500 * time.Millisecond

// CommandTimeout to budżet dla poleceń lokalnych.
const CommandTimeout = 3 * time.Second

const maxBody = 4 << 20

// Result to wynik sondy: status (OK|WARN|CRIT) i payload zapisywany jako JSON w cache.
type Result struct {
	Status  string
	Payload any
}

// Probe to sonda odświeżająca jeden wpis health_checks_cache.
type Probe interface {
	Key() string
	Check(ctx context.Context) Result
}

var rank = map[string]int{"OK": 0, "WARN": 1, "CRIT": 2}

// Worst zwraca najgorszy ze statusów (OK < WARN < CRIT); pusta lista -> OK;
// nieznany status traktowany jest jak CRIT.
func Worst(statuses ...string) string {
	w := "OK"
	for _, s := range statuses {
		if _, ok := rank[s]; !ok {
			s = "CRIT"
		}
		if rank[s] > rank[w] {
			w = s
		}
	}
	return w
}

// Runner uruchamia polecenie lokalne (podmieniany w testach).
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner to produkcyjny Runner oparty o os/exec (bez shella, bez sudo).
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// HTTPAuth opisuje opcjonalne uwierzytelnianie sond HTTP (APIKey ma pierwszeństwo).
type HTTPAuth struct {
	Username, Password, APIKey string
}

func (a HTTPAuth) apply(r *http.Request) {
	switch {
	case a.APIKey != "":
		r.Header.Set("Authorization", "ApiKey "+a.APIKey)
	case a.Username != "":
		r.SetBasicAuth(a.Username, a.Password)
	}
}

// StatusError oznacza odpowiedź HTTP inną niż 200.
type StatusError struct{ Code int }

func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected HTTP status %d %s", e.Code, http.StatusText(e.Code))
}

// httpGet wykonuje GET z ctx i limitem rozmiaru; status != 200 to błąd.
func httpGet(ctx context.Context, c *http.Client, url string, auth HTTPAuth) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	auth.apply(req)
	req.Header.Set("Accept", "application/json")
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{Code: resp.StatusCode}
	}
	return body, nil
}
