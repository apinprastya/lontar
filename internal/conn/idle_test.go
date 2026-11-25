package conn

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"lontar/internal/driver"
	"lontar/internal/driver/all"
	"lontar/internal/driver/sqlite"
	"lontar/internal/sshtunnel"
	"lontar/internal/store"
)

// clock is a settable time source for Manager.now.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// eventLog records session events.
type eventLog struct {
	mu     sync.Mutex
	events []Event
}

func (l *eventLog) add(ev Event) {
	l.mu.Lock()
	l.events = append(l.events, ev)
	l.mu.Unlock()
}

func (l *eventLog) kinds(id string) []EventKind {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []EventKind
	for _, ev := range l.events {
		if ev.SessionID == id {
			out = append(out, ev.Kind)
		}
	}
	return out
}

func newIdleManager(t *testing.T, conns ...store.Connection) (*Manager, *fakeSource, *clock, *eventLog) {
	t.Helper()
	src := newFakeSource(conns...)
	m := NewManager(src, all.Registry())
	clk := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	m.now = clk.now
	log := &eventLog{}
	m.Subscribe(log.add)
	t.Cleanup(func() { _ = m.DisconnectAll() })
	return m, src, clk, log
}

func TestSuspendIdleAndResume(t *testing.T) {
	ctx := context.Background()
	m, _, clk, log := newIdleManager(t, sqliteConn(t, "a", true), sqliteConn(t, "b", true))
	a, err := m.Connect(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := m.Connect(ctx, "b")
	old, _ := m.Get(a)
	if _, err := old.DB.ExecContext(ctx, "CREATE TABLE t (x int)"); err != nil {
		t.Fatal(err)
	}

	if got := m.SuspendIdle(0, nil); got != nil {
		t.Fatalf("off: suspended %v", got)
	}
	clk.add(9 * time.Minute)
	if got := m.SuspendIdle(10*time.Minute, nil); got != nil {
		t.Fatalf("too early: suspended %v", got)
	}
	if _, err := m.Get(b); err != nil { // b stays in use
		t.Fatal(err)
	}
	clk.add(time.Minute)
	got := m.SuspendIdle(10*time.Minute, nil)
	if len(got) != 1 || got[0] != a {
		t.Fatalf("suspended %v, want [%s]", got, a)
	}
	if err := old.DB.PingContext(ctx); err == nil {
		t.Fatal("pool of a suspended session should be closed")
	}
	if s, err := m.Suspended(a); err != nil || !s {
		t.Fatalf("Suspended(a) = %v, %v", s, err)
	}
	if s, _ := m.Suspended(b); s {
		t.Fatal("b should stay open")
	}
	if _, err := m.Suspended("nope"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v", err)
	}
	if len(m.List()) != 2 {
		t.Fatal("a suspended session stays listed")
	}
	// Already suspended: not again.
	clk.add(time.Hour)
	if got := m.SuspendIdle(10*time.Minute, nil); len(got) != 1 || got[0] != b {
		t.Fatalf("second sweep suspended %v, want only b", got)
	}

	s, err := m.Get(a)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != a || s.DB == old.DB || s.ConnectionID != "a" {
		t.Fatalf("resumed session = %+v", s)
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatalf("resumed pool: %v", err)
	}
	if sus, _ := m.Suspended(a); sus {
		t.Fatal("still suspended after Get")
	}
	if again, _ := m.Get(a); again != s {
		t.Fatal("second Get should not reconnect")
	}
	if k := log.kinds(a); len(k) != 2 || k[0] != SessionSuspended || k[1] != SessionResumed {
		t.Fatalf("events = %v", k)
	}

	// A suspended session can be disconnected.
	if err := m.Disconnect(b); err != nil {
		t.Fatalf("disconnect suspended: %v", err)
	}
	if k := log.kinds(b); len(k) != 2 || k[1] != SessionClosed {
		t.Fatalf("events b = %v", k)
	}
}

func TestSuspendIdleKeepsBusySessions(t *testing.T) {
	ctx := context.Background()
	m, _, clk, _ := newIdleManager(t, sqliteConn(t, "a", true))
	id, _ := m.Connect(ctx, "a")
	s, _ := m.Get(id)
	clk.add(time.Hour)

	// keep says busy.
	if got := m.SuspendIdle(time.Minute, func(*Session) int { return -1 }); got != nil {
		t.Fatalf("busy: suspended %v", got)
	}
	// A connection in use (a running query) keeps it.
	c, err := s.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.SuspendIdle(time.Minute, nil); got != nil {
		t.Fatalf("in use: suspended %v", got)
	}
	// ... unless keep accounts for it (an idle editor connection).
	var seen *Session
	got := m.SuspendIdle(time.Minute, func(x *Session) int { seen = x; return 1 })
	if len(got) != 1 || seen != s {
		t.Fatalf("suspended %v (keep saw %p, want %p)", got, seen, s)
	}
	_ = c.Close()
}

func TestSuspendIdleSkipsSessionUsedDuringKeep(t *testing.T) {
	ctx := context.Background()
	m, _, clk, _ := newIdleManager(t, sqliteConn(t, "a", true))
	id, _ := m.Connect(ctx, "a")
	clk.add(time.Hour)
	got := m.SuspendIdle(time.Minute, func(*Session) int {
		_, _ = m.Get(id) // used while the sweep runs
		return 0
	})
	if got != nil {
		t.Fatalf("suspended %v", got)
	}
}

func TestResumeFailureKeepsSessionSuspended(t *testing.T) {
	ctx := context.Background()
	c := sqliteConn(t, "a", true)
	m, src, clk, _ := newIdleManager(t, c)
	id, _ := m.Connect(ctx, "a")
	clk.add(time.Hour)
	m.SuspendIdle(time.Minute, nil)

	// The saved connection is gone.
	delete(src.conns, "a")
	if _, err := m.Get(id); err == nil {
		t.Fatal("resume without the saved connection should fail")
	}
	if s, _ := m.Suspended(id); !s {
		t.Fatal("a failed resume keeps the session suspended")
	}
	// The connection cannot be opened (file removed, no create).
	broken := c
	broken.FilePath = filepath.Join(t.TempDir(), "missing.db")
	broken.CreateIfMissing = false
	src.conns["a"] = broken
	if _, err := m.Get(id); err == nil {
		t.Fatal("resume of an unreachable connection should fail")
	}
	// Fixed: resumes.
	src.conns["a"] = c
	if _, err := m.Get(id); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentGetResumesOnce(t *testing.T) {
	ctx := context.Background()
	m, _, clk, log := newIdleManager(t, sqliteConn(t, "a", true))
	id, _ := m.Connect(ctx, "a")
	clk.add(time.Hour)
	m.SuspendIdle(time.Minute, nil)

	var wg sync.WaitGroup
	dbs := make(chan *Session, 8)
	for range 8 {
		wg.Go(func() {
			s, err := m.Get(id)
			if err != nil {
				t.Error(err)
				return
			}
			dbs <- s
		})
	}
	wg.Wait()
	close(dbs)
	var first *Session
	for s := range dbs {
		if first == nil {
			first = s
		} else if s.DB != first.DB {
			t.Fatal("concurrent Gets opened several pools")
		}
	}
	resumed := 0
	for _, k := range log.kinds(id) {
		if k == SessionResumed {
			resumed++
		}
	}
	if resumed != 1 {
		t.Fatalf("resumed %d times", resumed)
	}
}

func TestResumeAfterDisconnectOrShutdown(t *testing.T) {
	ctx := context.Background()
	m, _, clk, _ := newIdleManager(t, sqliteConn(t, "a", true))
	id, _ := m.Connect(ctx, "a")
	clk.add(time.Hour)
	m.SuspendIdle(time.Minute, nil)

	if err := m.Disconnect(id); err != nil {
		t.Fatal(err)
	}
	if _, err := m.resume(id); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v", err)
	}

	// Shut down while reconnecting: the new pool is dropped.
	m2, _, clk2, _ := newIdleManager(t, sqliteConn(t, "b", true))
	id2, _ := m2.Connect(ctx, "b")
	clk2.add(time.Hour)
	m2.SuspendIdle(time.Minute, nil)
	m2.mu.Lock()
	m2.closed = true
	m2.mu.Unlock()
	if _, err := m2.Get(id2); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestSuspendIdleClosesAndReopensTunnel(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "x.db")
	var mu sync.Mutex
	var cfgs []driver.Config
	reg := driver.NewRegistry(dialRecorder{fakeServerOpen{fakeServer: fakeServer{Driver: sqlite.New(), dbs: []string{"one", "two"}}, path: path, lite: sqlite.New()}, &mu, &cfgs})
	c := store.Connection{ID: "p", Name: "pg", Kind: driver.Postgres, Host: "db", Database: "one", SafeMode: store.SafeConfirm,
		SSH: &store.SSHConfig{Enabled: true, Host: "bastion", Port: 22, User: "u", AuthMethod: "agent"}}
	m := NewManager(newFakeSource(c), reg)
	clk := &clock{t: time.Now()}
	m.now = clk.now
	var opened []*fakeTunnel
	m.Tunnels = func(context.Context, sshtunnel.Config) (Tunnel, error) {
		ft := &fakeTunnel{}
		opened = append(opened, ft)
		return ft, nil
	}
	t.Cleanup(func() { _ = m.DisconnectAll() })

	id, err := m.Connect(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UseDatabase(ctx, id, "two"); err != nil {
		t.Fatal(err)
	}
	clk.add(time.Hour)
	if got := m.SuspendIdle(time.Minute, nil); len(got) != 1 {
		t.Fatalf("suspended %v", got)
	}
	if opened[0].closed != 1 {
		t.Fatalf("tunnel closed %d times", opened[0].closed)
	}
	s, err := m.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(opened) != 2 || !s.Tunneled || s.Database != "two" || s.SafeMode != store.SafeConfirm {
		t.Fatalf("resumed = %+v (tunnels %d)", s, len(opened))
	}
	mu.Lock()
	last := cfgs[len(cfgs)-1]
	mu.Unlock()
	if last.Database != "two" || last.Dial == nil {
		t.Fatalf("reopened with %+v", last)
	}
	// Disconnect closes the new tunnel once; the old one is not closed again.
	if err := m.Disconnect(id); err != nil {
		t.Fatal(err)
	}
	if opened[0].closed != 1 || opened[1].closed != 1 {
		t.Fatalf("closed = %d, %d", opened[0].closed, opened[1].closed)
	}
}

func TestStartIdleSweep(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newFakeSource(sqliteConn(t, "a", true)), all.Registry())
	t.Cleanup(func() { _ = m.DisconnectAll() })
	id, _ := m.Connect(ctx, "a")
	done := make(chan struct{}, 1)
	m.Subscribe(func(ev Event) {
		if ev.Kind == SessionSuspended {
			select {
			case done <- struct{}{}:
			default:
			}
		}
	})
	stop := m.StartIdleSweep(5*time.Millisecond, func() time.Duration { return time.Nanosecond }, nil)
	defer stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sweep did not suspend the idle session")
	}
	stop()
	stop() // idempotent
	if s, _ := m.Suspended(id); !s {
		t.Fatal("not suspended")
	}
}

func TestSuspendIdleGivesSessionsAResumeLock(t *testing.T) {
	m, _, clk, _ := newIdleManager(t, sqliteConn(t, "a", true))
	id, _ := m.Connect(context.Background(), "a")
	m.mu.Lock()
	s := *m.sessions[id]
	s.resume = nil // a session built without Connect
	m.sessions[id] = &s
	m.mu.Unlock()
	clk.add(time.Hour)
	m.SuspendIdle(time.Minute, nil)
	if _, err := m.Get(id); err != nil {
		t.Fatal(err)
	}
}
