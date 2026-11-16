// Package scheduler uruchamia pętle tła odświeżające health_checks_cache.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"server-monitor/internal/collector"
	"server-monitor/internal/storage"
)

const (
	// DefaultInterval mieści się w wymaganym przedziale 10–15 s.
	DefaultInterval = 12 * time.Second
	// DefaultTimeout to twardy budżet jednej sondy.
	DefaultTimeout = 5 * time.Second
)

// Store to wycinek repozytorium health_checks_cache.
type Store interface {
	Upsert(storage.HealthEntry) error
}

// Scheduler odświeża cache niezależnie od żądań HTTP.
type Scheduler struct {
	Store    Store
	Probes   []collector.Probe
	Interval time.Duration // domyślnie DefaultInterval
	Timeout  time.Duration // domyślnie DefaultTimeout
	Now      func() time.Time
	Logf     func(format string, args ...any)
}

func (s *Scheduler) interval() time.Duration {
	if s.Interval > 0 {
		return s.Interval
	}
	return DefaultInterval
}

func (s *Scheduler) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return DefaultTimeout
}

func (s *Scheduler) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// Run odświeża każdą sondę natychmiast, potem co Interval, w osobnej goroutine na sondę
// (wolna sonda nie opóźnia pozostałych). Blokuje do anulowania ctx.
func (s *Scheduler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, p := range s.Probes {
		wg.Add(1)
		go func(p collector.Probe) {
			defer wg.Done()
			s.tick(ctx, p)
			t := time.NewTicker(s.interval())
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					s.tick(ctx, p)
				}
			}
		}(p)
	}
	wg.Wait()
}

// RunOnce odświeża wszystkie sondy równolegle i czeka na zakończenie.
func (s *Scheduler) RunOnce(ctx context.Context) {
	var wg sync.WaitGroup
	for _, p := range s.Probes {
		wg.Add(1)
		go func(p collector.Probe) {
			defer wg.Done()
			s.tick(ctx, p)
		}(p)
	}
	wg.Wait()
}

// Refresh natychmiast odświeża jedną sondę o danym kluczu (np. po zmianie wyboru usług).
// Zwraca false, gdy nie ma takiej sondy.
func (s *Scheduler) Refresh(ctx context.Context, key string) bool {
	for _, p := range s.Probes {
		if p.Key() == key {
			s.tick(ctx, p)
			return true
		}
	}
	return false
}

func (s *Scheduler) tick(ctx context.Context, p collector.Probe) {
	if ctx.Err() != nil {
		return
	}
	res := s.check(ctx, p)
	switch res.Status {
	case "OK", "WARN", "CRIT":
	default:
		res = collector.Result{Status: "CRIT", Payload: map[string]string{"error": "probe returned invalid status " + res.Status}}
	}
	payload, err := json.Marshal(res.Payload)
	if err != nil {
		s.logf("scheduler: %s: marshal payload: %v", p.Key(), err)
		return
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	if err := s.Store.Upsert(storage.HealthEntry{ServiceKey: p.Key(), Status: res.Status,
		PayloadJSON: string(payload), CheckedAt: now}); err != nil {
		s.logf("scheduler: %s: store: %v", p.Key(), err)
	}
}

// check uruchamia sondę z budżetem czasu; panikę i zawieszenie zamienia na CRIT.
func (s *Scheduler) check(ctx context.Context, p collector.Probe) collector.Result {
	cctx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	ch := make(chan collector.Result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- collector.Result{Status: "CRIT", Payload: map[string]string{"error": fmt.Sprint("panic: ", r)}}
			}
		}()
		ch <- p.Check(cctx)
	}()
	select {
	case r := <-ch:
		return r
	case <-cctx.Done():
		return collector.Result{Status: "CRIT", Payload: map[string]string{"error": "probe timed out"}}
	}
}
