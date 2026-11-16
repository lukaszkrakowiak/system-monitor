package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"server-monitor/internal/collector"
	"server-monitor/internal/storage"
)

type memStore struct {
	mu      sync.Mutex
	entries map[string]storage.HealthEntry
	writes  map[string]int
	err     error
}

func newMemStore() *memStore {
	return &memStore{entries: map[string]storage.HealthEntry{}, writes: map[string]int{}}
}

func (m *memStore) Upsert(e storage.HealthEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.entries[e.ServiceKey] = e
	m.writes[e.ServiceKey]++
	return nil
}

func (m *memStore) get(k string) (storage.HealthEntry, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.entries[k], m.writes[k]
}

type fakeProbe struct {
	key   string
	calls atomic.Int32
	fn    func(ctx context.Context) collector.Result
}

func (f *fakeProbe) Key() string { return f.key }
func (f *fakeProbe) Check(ctx context.Context) collector.Result {
	f.calls.Add(1)
	return f.fn(ctx)
}

func ok(payload any) func(context.Context) collector.Result {
	return func(context.Context) collector.Result { return collector.Result{Status: "OK", Payload: payload} }
}

func quiet(string, ...any) {}

func TestRunOnceWritesAllProbes(t *testing.T) {
	st := newMemStore()
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	s := &Scheduler{Store: st, Now: func() time.Time { return now }, Logf: quiet, Probes: []collector.Probe{
		&fakeProbe{key: "a", fn: ok(map[string]int{"x": 1})},
		&fakeProbe{key: "b", fn: func(context.Context) collector.Result {
			return collector.Result{Status: "WARN", Payload: []string{"p"}}
		}},
	}}
	s.RunOnce(context.Background())
	a, _ := st.get("a")
	b, _ := st.get("b")
	if a.Status != "OK" || a.PayloadJSON != `{"x":1}` || !a.CheckedAt.Equal(now) || b.Status != "WARN" || b.PayloadJSON != `["p"]` {
		t.Fatalf("%+v %+v", a, b)
	}
}

func TestRunRefreshesPeriodicallyAndStops(t *testing.T) {
	st := newMemStore()
	p := &fakeProbe{key: "a", fn: ok(1)}
	s := &Scheduler{Store: st, Interval: 20 * time.Millisecond, Logf: quiet, Probes: []collector.Probe{p}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after cancel")
	}
	if n := p.calls.Load(); n < 3 {
		t.Fatalf("expected several refreshes, got %d", n)
	}
	_, w := st.get("a")
	after := p.calls.Load()
	time.Sleep(60 * time.Millisecond)
	if p.calls.Load() != after {
		t.Fatal("probe still running after Run returned")
	}
	if w < 3 {
		t.Fatalf("writes %d", w)
	}
}

func TestSlowProbeDoesNotBlockOthers(t *testing.T) {
	st := newMemStore()
	release := make(chan struct{})
	defer close(release)
	slow := &fakeProbe{key: "slow", fn: func(ctx context.Context) collector.Result {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return collector.Result{Status: "CRIT", Payload: "slow"}
	}}
	fast := &fakeProbe{key: "fast", fn: ok(1)}
	s := &Scheduler{Store: st, Interval: 10 * time.Millisecond, Timeout: 2 * time.Second, Logf: quiet, Probes: []collector.Probe{slow, fast}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, w := st.get("fast"); w >= 3 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("fast probe was starved by slow probe")
}

func TestHungProbeIgnoringContextIsCutOff(t *testing.T) {
	st := newMemStore()
	block := make(chan struct{})
	defer close(block)
	p := &fakeProbe{key: "hung", fn: func(context.Context) collector.Result { <-block; return collector.Result{Status: "OK"} }}
	s := &Scheduler{Store: st, Timeout: 50 * time.Millisecond, Logf: quiet, Probes: []collector.Probe{p}}
	start := time.Now()
	s.RunOnce(context.Background())
	e, _ := st.get("hung")
	if e.Status != "CRIT" || time.Since(start) > time.Second {
		t.Fatalf("%+v after %v", e, time.Since(start))
	}
	var pl map[string]string
	json.Unmarshal([]byte(e.PayloadJSON), &pl)
	if pl["error"] != "probe timed out" {
		t.Fatalf("%s", e.PayloadJSON)
	}
}

func TestPanicAndInvalidStatusBecomeCRIT(t *testing.T) {
	st := newMemStore()
	s := &Scheduler{Store: st, Logf: quiet, Probes: []collector.Probe{
		&fakeProbe{key: "panic", fn: func(context.Context) collector.Result { panic("kaboom") }},
		&fakeProbe{key: "bogus", fn: func(context.Context) collector.Result { return collector.Result{Status: "GREEN"} }},
	}}
	s.RunOnce(context.Background())
	for _, k := range []string{"panic", "bogus"} {
		if e, _ := st.get(k); e.Status != "CRIT" || e.PayloadJSON == "" {
			t.Fatalf("%s: %+v", k, e)
		}
	}
}

func TestStoreAndMarshalErrorsAreLoggedNotFatal(t *testing.T) {
	st := newMemStore()
	st.err = errors.New("disk full")
	var logged atomic.Int32
	s := &Scheduler{Store: st, Logf: func(string, ...any) { logged.Add(1) }, Probes: []collector.Probe{
		&fakeProbe{key: "a", fn: ok(1)},
		&fakeProbe{key: "b", fn: ok(make(chan int))}, // niemarshalowalny payload
	}}
	s.RunOnce(context.Background())
	if logged.Load() != 2 {
		t.Fatalf("logged %d", logged.Load())
	}
}

func TestCancelledContextSkipsWork(t *testing.T) {
	p := &fakeProbe{key: "a", fn: ok(1)}
	s := &Scheduler{Store: newMemStore(), Logf: quiet, Probes: []collector.Probe{p}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Run(ctx)
	if p.calls.Load() != 0 {
		t.Fatal("probe should not run with cancelled context")
	}
}

func TestDefaults(t *testing.T) {
	s := &Scheduler{}
	if s.interval() != DefaultInterval || s.timeout() != DefaultTimeout {
		t.Fatal("defaults")
	}
	if DefaultInterval < 10*time.Second || DefaultInterval > 15*time.Second {
		t.Fatal("default interval must be within 10–15 s")
	}
}

// Integracja z prawdziwym repozytorium SQLite (tymczasowy plik) pod -race.
func TestWithRealStorage(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Scheduler{Store: db.Health(), Interval: 10 * time.Millisecond, Logf: quiet, Probes: []collector.Probe{
		&fakeProbe{key: "redis", fn: ok(map[string]string{"k": "v"})},
		&fakeProbe{key: "kibana", fn: ok(map[string]string{"k": "v"})},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s.Run(ctx)
	all, err := db.Health().All()
	if err != nil || len(all) != 2 || all[0].Status != "OK" || all[0].PayloadJSON != `{"k":"v"}` {
		t.Fatalf("%+v %v", all, err)
	}
}
