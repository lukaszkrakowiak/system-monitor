package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"server-monitor/internal/collector"
	"server-monitor/internal/scheduler"
	"server-monitor/internal/storage"
)

const (
	defaultDBPath     = "/var/lib/server-monitor/state.db"
	defaultListen     = "127.0.0.1:8080"
	defaultAuditLog   = "/var/log/audit/audit.log"
	defaultSupervisor = "/var/run/supervisor.sock"
	minTokenLen       = 16
)

type config struct {
	DBPath       string
	Listen       string
	HealthPublic bool

	Token, BasicUser, BasicPass string

	ESURL, KibanaURL        string
	ESAuth, KibanaAuth      collector.HTTPAuth
	RedisNetwork, RedisAddr string
	RedisPassword           string
	SupervisorSocket        string // pusty = sonda wyłączona
	AuditLog                string // pusty = śledzenie wyłączone
	RefreshInterval         time.Duration
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func checkURL(name, v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%s must be an http(s) URL", name)
	}
	return nil
}

// loadConfig czyta konfigurację ze zmiennych środowiskowych (sekretów nie loguje).
func loadConfig(getenv func(string) string) (config, error) {
	c := config{
		DBPath:        orDefault(getenv("SM_DB_PATH"), defaultDBPath),
		Listen:        orDefault(getenv("SM_LISTEN"), defaultListen),
		Token:         getenv("SM_API_TOKEN"),
		BasicUser:     getenv("SM_BASIC_USER"),
		BasicPass:     getenv("SM_BASIC_PASSWORD"),
		ESURL:         getenv("ES_URL"),
		KibanaURL:     getenv("KIBANA_URL"),
		RedisNetwork:  orDefault(getenv("REDIS_NETWORK"), "tcp"),
		RedisAddr:     getenv("REDIS_ADDR"),
		RedisPassword: getenv("REDIS_PASSWORD"),
		ESAuth:        collector.HTTPAuth{Username: getenv("ES_USER"), Password: getenv("ES_PASSWORD"), APIKey: getenv("ES_API_KEY")},
		KibanaAuth: collector.HTTPAuth{Username: getenv("KIBANA_USER"), Password: getenv("KIBANA_PASSWORD"),
			APIKey: getenv("KIBANA_API_KEY")},
		RefreshInterval: scheduler.DefaultInterval,
	}
	switch strings.ToLower(getenv("HEALTH_PUBLIC")) {
	case "", "0", "false":
	case "1", "true":
		c.HealthPublic = true
	default:
		return c, errors.New("HEALTH_PUBLIC must be true or false")
	}
	if err := checkURL("ES_URL", c.ESURL); err != nil {
		return c, err
	}
	if err := checkURL("KIBANA_URL", c.KibanaURL); err != nil {
		return c, err
	}
	if c.RedisNetwork != "tcp" && c.RedisNetwork != "unix" {
		return c, errors.New("REDIS_NETWORK must be tcp or unix")
	}
	// Supervisor: jawne "off" wyłącza; domyślne gniazdo tylko gdy istnieje.
	switch v := getenv("SUPERVISOR_SOCKET"); v {
	case "off":
	case "":
		if _, err := os.Stat(defaultSupervisor); err == nil {
			c.SupervisorSocket = defaultSupervisor
		}
	default:
		c.SupervisorSocket = v
	}
	if v := getenv("SM_AUDIT_LOG"); v != "off" {
		c.AuditLog = orDefault(v, defaultAuditLog)
	}
	if v := getenv("SM_REFRESH_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 10*time.Second || d > 15*time.Second {
			return c, errors.New("SM_REFRESH_INTERVAL must be a duration between 10s and 15s")
		}
		c.RefreshInterval = d
	}
	return c, nil
}

// validateServe wymaga danych dostępu dla chronionych endpointów (fail closed).
func (c config) validateServe() error {
	if c.Token == "" && c.BasicUser == "" {
		return errors.New("set SM_API_TOKEN and/or SM_BASIC_USER+SM_BASIC_PASSWORD: protected endpoints need credentials")
	}
	if c.Token != "" && len(c.Token) < minTokenLen {
		return fmt.Errorf("SM_API_TOKEN must have at least %d characters", minTokenLen)
	}
	if c.BasicUser != "" && c.BasicPass == "" {
		return errors.New("SM_BASIC_PASSWORD is required when SM_BASIC_USER is set")
	}
	return nil
}

// probes składa sondy według konfiguracji; sondy sieciowe tylko dla skonfigurowanych usług.
func (c config) probes(db *storage.DB, sysd *collector.Systemd) []collector.Probe {
	probes := []collector.Probe{&collector.SystemdProbe{Systemd: sysd, Units: db.Services().ListEnabled}}
	if c.SupervisorSocket != "" {
		probes = append(probes, &collector.SupervisorProbe{Supervisor: &collector.Supervisor{SocketPath: c.SupervisorSocket}})
	}
	if c.ESURL != "" {
		probes = append(probes, &collector.Elasticsearch{URL: c.ESURL, Auth: c.ESAuth})
	}
	if c.RedisAddr != "" {
		probes = append(probes, &collector.Redis{Network: c.RedisNetwork, Addr: c.RedisAddr, Password: c.RedisPassword})
	}
	if c.KibanaURL != "" {
		probes = append(probes, &collector.Kibana{URL: c.KibanaURL, Auth: c.KibanaAuth})
	}
	return probes
}
