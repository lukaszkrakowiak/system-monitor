// Command server-monitor: subkomendy serve, ingest-pam, check.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "time/tzdata" // strefy czasowe w statycznej binarce (reguły alertów mają własną strefę)

	"server-monitor/internal/alerts"
	"server-monitor/internal/api"
	"server-monitor/internal/collector"
	"server-monitor/internal/scheduler"
	"server-monitor/internal/secret"
	"server-monitor/internal/storage"
)

const usage = `usage: server-monitor <command>

commands:
  serve        run the daemon (HTTP API + background probes + audit.log tail)
  ingest-pam   record an SSH session event from pam_exec (reads PAM_* env vars)
  check        run all probes once, store results and print them (exit 1 if any CRIT)
`

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cfg, err := loadConfig(getenv)
	if err != nil {
		fmt.Fprintln(stderr, "config:", err)
		return 2
	}
	switch args[0] {
	case "serve":
		if err := cfg.validateServe(); err != nil {
			fmt.Fprintln(stderr, "config:", err)
			return 2
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := serve(ctx, cfg); err != nil {
			fmt.Fprintln(stderr, "serve:", err)
			return 1
		}
		return 0
	case "ingest-pam":
		if err := ingestPAM(cfg, getenv); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "check":
		return check(cfg, stdout, stderr)
	}
	fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
	return 2
}

func ingestPAM(cfg config, getenv func(string) string) error {
	db, err := storage.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("ingest-pam: %w", err)
	}
	defer db.Close()
	return collector.IngestPAM(getenv, db.Sessions(), time.Now())
}

func check(cfg config, stdout, stderr io.Writer) int {
	db, err := storage.Open(cfg.DBPath)
	if err != nil {
		fmt.Fprintln(stderr, "check:", err)
		return 1
	}
	defer db.Close()
	sch := &scheduler.Scheduler{Store: db.Health(), Probes: cfg.probes(db, &collector.Systemd{Runner: collector.ExecRunner{}})}
	sch.RunOnce(context.Background())
	entries, err := db.Health().All()
	if err != nil {
		fmt.Fprintln(stderr, "check:", err)
		return 1
	}
	code := 0
	enc := json.NewEncoder(stdout)
	for _, e := range entries {
		if e.Status == "CRIT" {
			code = 1
		}
		enc.Encode(map[string]any{"service": e.ServiceKey, "status": e.Status, "payload": json.RawMessage(e.PayloadJSON)})
	}
	return code
}

func serve(ctx context.Context, cfg config) error {
	db, err := storage.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	sysd := &collector.Systemd{Runner: collector.ExecRunner{}}
	sch := &scheduler.Scheduler{Store: db.Health(), Probes: cfg.probes(db, sysd), Interval: cfg.RefreshInterval}
	go sch.Run(ctx)

	if cfg.AuditLog != "" {
		tail := &collector.AuditTailer{Path: cfg.AuditLog, State: db.AuditState(), Sink: db.Commands(), Failures: db.LoginFailures(), Users: &collector.SystemUsers{}}
		go tail.Run(ctx)
	}

	var box *secret.Box
	if cfg.SecretKey != "" {
		if box, err = secret.New(cfg.SecretKey); err != nil {
			return err
		}
	} else {
		log.Printf("SM_SECRET_KEY is not set: connector passwords cannot be stored (alerts via authenticated SMTP are unavailable)")
	}
	engine := &alerts.Engine{
		Sessions: db.Sessions(), Health: db.Health(), Settings: db.AlertSettings(), State: db.AlertState(),
		Outbox: db.Notifications(), Connectors: db.Connectors(), Secrets: box,
	}
	go engine.Run(ctx)

	handler := api.New(api.Config{
		Health: db.Health(), Services: db.Services(), Sessions: db.Sessions(), Commands: db.Commands(), LoginFailures: db.LoginFailures(), Units: sysd,
		Connectors: db.Connectors(), AlertSettings: db.AlertSettings(), Notifications: db.Notifications(), Secrets: box,
		TestConnector: engine.SendTest,
		Token:         cfg.Token, BasicUser: cfg.BasicUser, BasicPass: cfg.BasicPass,
		HealthPublic: cfg.HealthPublic,
		OnSelectionChanged: func() {
			rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			sch.Refresh(rctx, "systemd")
		},
		Static: staticHandler(),
	})
	srv := &http.Server{
		Addr: cfg.Listen, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("server-monitor listening on %s", cfg.Listen)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
