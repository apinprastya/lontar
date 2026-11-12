package conn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lontar/internal/driver"
	"lontar/internal/driver/all"
	"lontar/internal/driver/sqlite"
	"lontar/internal/store"
	"lontar/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

// fakeSource is an in-memory Source.
type fakeSource struct {
	mu        sync.Mutex
	conns     map[string]store.Connection
	passwords map[string]string
	pwErr     error
	sshErr    error
}

func newFakeSource(conns ...store.Connection) *fakeSource {
	f := &fakeSource{conns: map[string]store.Connection{}, passwords: map[string]string{}}
	for _, c := range conns {
		f.conns[c.ID] = c
	}
	return f
}

func (f *fakeSource) Get(id string) (store.Connection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.conns[id]
	if !ok {
		return store.Connection{}, fmt.Errorf("%w: %s", store.ErrNotFound, id)
	}
	return c, nil
}

func (f *fakeSource) Password(id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.passwords[id], f.pwErr
}

func (f *fakeSource) SSHSecrets(id string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.passwords[id+":ssh-password"], f.passwords[id+":ssh-passphrase"], f.sshErr
}

func sqliteConn(t *testing.T, id string, create bool) store.Connection {
	t.Helper()
	return store.Connection{ID: id, Name: "Local " + id, Kind: driver.SQLite, FilePath: filepath.Join(t.TempDir(), id+".db"), CreateIfMissing: create}
}

func TestConnectGetDisconnect(t *testing.T) {
	c := sqliteConn(t, "a", true)
	m := NewManager(newFakeSource(c), all.Registry())
	ctx := context.Background()

	id, err := m.Connect(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if s.ConnectionID != "a" || s.Name != "Local a" || s.Kind != driver.SQLite || !strings.HasPrefix(s.ServerVersion, "SQLite 3.") || s.ConnectedAt.IsZero() {
		t.Fatalf("session = %+v", s)
	}
	if s.DB.Stats().MaxOpenConnections != MaxOpenConns {
		t.Fatalf("max open = %d", s.DB.Stats().MaxOpenConnections)
	}
	if err := s.DB.PingContext(ctx); err != nil {
		t.Fatal(err)
	}

	// A second session for the same connection is independent.
	id2, err := m.Connect(ctx, "a")
	if err != nil || id2 == id {
		t.Fatalf("second connect = %q, %v", id2, err)
	}
	if got := m.List(); len(got) != 2 {
		t.Fatalf("List() = %d sessions", len(got))
	}

	if err := m.Disconnect(id); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(id); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v", err)
	}
	if err := m.Disconnect(id); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v", err)
	}
	if err := s.DB.PingContext(ctx); err == nil {
		t.Fatal("DB should be closed after Disconnect")
	}

	s2, _ := m.Get(id2)
	if err := m.DisconnectAll(); err != nil {
		t.Fatal(err)
	}
	if err := s2.DB.PingContext(ctx); err == nil {
		t.Fatal("DB should be closed after DisconnectAll")
	}
	if len(m.List()) != 0 {
		t.Fatal("sessions left after DisconnectAll")
	}
	if _, err := m.Connect(ctx, "a"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Connect after shutdown err = %v", err)
	}
}

func TestConnectErrors(t *testing.T) {
	ctx := context.Background()
	missing := sqliteConn(t, "missing", false)
	src := newFakeSource(missing)
	m := NewManager(src, all.Registry())

	if _, err := m.Connect(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := m.Connect(ctx, "missing"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v", err)
	}
	src.pwErr = errors.New("keychain locked")
	if _, err := m.Connect(ctx, "missing"); err == nil || !strings.Contains(err.Error(), "keychain locked") {
		t.Fatalf("err = %v", err)
	}
	if len(m.List()) != 0 {
		t.Fatal("failed connects must not leave sessions")
	}
}

type badVersion struct{ driver.Driver }

func (badVersion) ServerVersion(context.Context, *sql.DB) (string, error) {
	return "", errors.New("version unavailable")
}

func TestTest(t *testing.T) {
	ctx := context.Background()
	m := NewManager(newFakeSource(), all.Registry())

	v, err := m.Test(ctx, sqliteConn(t, "t", true), Secrets{Password: ""})
	if err != nil || !strings.HasPrefix(v, "SQLite ") {
		t.Fatalf("Test = %q, %v", v, err)
	}

	cases := []struct {
		name string
		c    store.Connection
		want string
	}{
		{"invalid", store.Connection{Kind: driver.Postgres, Host: "h"}, "name is required"},
		{"ssh invalid", store.Connection{Name: "x", Kind: driver.Postgres, Host: "h", SSH: &store.SSHConfig{Enabled: true}}, "SSH host is required"},
		{"ssh no tunnels", store.Connection{Name: "x", Kind: driver.Postgres, Host: "h", SSH: &store.SSHConfig{Enabled: true, Host: "b", User: "u"}}, "SSH tunnels are not available"},
		{"ssl", store.Connection{Name: "x", Kind: driver.Postgres, Host: "h", SSL: &store.SSLConfig{Mode: "preferred"}}, "not supported for postgres"},
		{"refused", store.Connection{Name: "x", Kind: driver.Postgres, Host: "127.0.0.1", Port: 1}, "127.0.0.1"},
		{"bad option", store.Connection{Name: "x", Kind: driver.Postgres, Host: "h", Options: map[string]string{"password": "x"}}, "not allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.Test(ctx, tc.c, Secrets{Password: ""})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}

	// Disabled SSH / SSL "disable" are fine.
	ok := sqliteConn(t, "ok", true)
	ok.SSH = &store.SSHConfig{Enabled: false}
	ok.SSL = &store.SSLConfig{Mode: "disable"}
	if _, err := m.Test(ctx, ok, Secrets{Password: ""}); err != nil {
		t.Fatal(err)
	}

	// Unregistered kind.
	m2 := NewManager(newFakeSource(), driver.NewRegistry(sqlite.New()))
	if _, err := m2.Test(ctx, store.Connection{Name: "x", Kind: driver.MySQL, Host: "h"}, Secrets{Password: ""}); !errors.Is(err, driver.ErrUnknownKind) {
		t.Fatalf("err = %v", err)
	}

	// Server version failure closes the DB and reports the error.
	m3 := NewManager(newFakeSource(), driver.NewRegistry(badVersion{sqlite.New()}))
	if _, err := m3.Test(ctx, sqliteConn(t, "v", true), Secrets{}); err == nil || !strings.Contains(err.Error(), "version unavailable") {
		t.Fatalf("err = %v", err)
	}
}

func TestFriendly(t *testing.T) {
	err := friendly(fmt.Errorf("dial: %w", context.DeadlineExceeded), 3*time.Second)
	if !strings.Contains(err.Error(), "timed out after 3s") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if err := friendly(errors.New("read tcp: i/o timeout"), time.Second); !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
	plain := errors.New("password authentication failed")
	if friendly(plain, time.Second) != plain {
		t.Fatal("other errors must pass through")
	}
}

func TestPingTimeout(t *testing.T) {
	m := NewManager(nil, nil)
	if m.pingTimeout() != DefaultPingTimeout {
		t.Fatal("default")
	}
	m.PingTimeout = time.Second
	if m.pingTimeout() != time.Second {
		t.Fatal("override")
	}
}

func TestConnectRaceWithShutdown(t *testing.T) {
	c := sqliteConn(t, "a", true)
	m := NewManager(newFakeSource(c), all.Registry())
	// Simulate DisconnectAll happening while Connect is opening the DB.
	m.newID = func() string {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		return "late"
	}
	if _, err := m.Connect(context.Background(), "a"); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
	if len(m.List()) != 0 {
		t.Fatal("session registered after shutdown")
	}
}

func TestConcurrentUse(t *testing.T) {
	c := sqliteConn(t, "a", true)
	m := NewManager(newFakeSource(c), all.Registry())
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := m.Connect(ctx, "a")
			if err != nil {
				errs <- err
				return
			}
			if _, err := m.Get(id); err != nil {
				errs <- err
			}
			_ = m.List()
			if err := m.Disconnect(id); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if len(m.List()) != 0 {
		t.Fatal("sessions leaked")
	}
}

func TestUseDatabaseErrors(t *testing.T) {
	ctx := context.Background()
	c := sqliteConn(t, "a", true)
	src := newFakeSource(c)
	m := NewManager(src, all.Registry())
	if _, err := m.UseDatabase(ctx, "nope", "x"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v", err)
	}
	id, err := m.Connect(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := m.Get(id)
	if s.Database != "main" {
		t.Fatalf("sqlite database = %q", s.Database)
	}
	if _, err := m.UseDatabase(ctx, id, "main"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
	_ = m.DisconnectAll()
}

// fakeServer pretends to be a server driver on top of SQLite so that
// UseDatabase's error paths can run without Docker.
type fakeServer struct {
	driver.Driver
	dbs     []string
	listErr error
}

func (f fakeServer) Kind() driver.Kind { return driver.Postgres }
func (f fakeServer) ListDatabases(context.Context, *sql.DB, bool) ([]string, error) {
	return f.dbs, f.listErr
}
func (f fakeServer) CurrentDatabase(context.Context, *sql.DB) (string, error) {
	return "", errors.New("unknown")
}

func TestUseDatabaseFakeServer(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "x.db")
	lite := sqlite.New()
	// Opening ignores Host/Database and always opens path.
	open := func(fs fakeServer) *Manager {
		reg := driver.NewRegistry(fakeServerOpen{fakeServer: fs, path: path, lite: lite})
		c := store.Connection{ID: "p", Name: "pg", Kind: driver.Postgres, Host: "h", Database: "one"}
		src := newFakeSource(c)
		return NewManager(src, reg)
	}

	m := open(fakeServer{Driver: lite, dbs: []string{"one", "two"}})
	id, err := m.Connect(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := m.Get(id)
	if s.Database != "one" { // CurrentDatabase failed: falls back to the config
		t.Fatalf("database = %q", s.Database)
	}
	s2, err := m.UseDatabase(ctx, id, "two")
	if err != nil || s2.ID != id || s2.Database != "two" {
		t.Fatalf("UseDatabase = %+v, %v", s2, err)
	}

	// Errors: listing databases, loading the connection, its password,
	// and the session disappearing while switching.
	m = open(fakeServer{Driver: lite, listErr: errors.New("list boom")})
	id, _ = m.Connect(ctx, "p")
	if _, err := m.UseDatabase(ctx, id, "two"); err == nil || !strings.Contains(err.Error(), "list boom") {
		t.Fatalf("err = %v", err)
	}

	m = open(fakeServer{Driver: lite, dbs: []string{"one", "two"}})
	id, _ = m.Connect(ctx, "p")
	m.src.(*fakeSource).pwErr = errors.New("keychain locked")
	if _, err := m.UseDatabase(ctx, id, "two"); err == nil || !strings.Contains(err.Error(), "keychain") {
		t.Fatalf("err = %v", err)
	}
	m.src.(*fakeSource).pwErr = nil
	delete(m.src.(*fakeSource).conns, "p")
	if _, err := m.UseDatabase(ctx, id, "two"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	_ = m.DisconnectAll()
	if _, err := m.UseDatabase(ctx, id, "two"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("err = %v", err)
	}
}

// fakeServerOpen opens an SQLite file for a "postgres" connection.
type fakeServerOpen struct {
	fakeServer
	path string
	lite *sqlite.Driver
}

func (f fakeServerOpen) Open(ctx context.Context, cfg driver.Config, pw string) (*sql.DB, error) {
	if cfg.Database == "bad" {
		return nil, errors.New("open boom")
	}
	return f.lite.Open(ctx, driver.Config{Kind: driver.SQLite, FilePath: f.path, CreateIfMissing: true}, pw)
}

func TestSessionEvents(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "x.db")
	reg := driver.NewRegistry(fakeServerOpen{fakeServer: fakeServer{Driver: sqlite.New(), dbs: []string{"one", "two"}}, path: path, lite: sqlite.New()})
	m := NewManager(newFakeSource(store.Connection{ID: "p", Name: "pg", Kind: driver.Postgres, Host: "h", Database: "one"}), reg)
	var (
		mu     sync.Mutex
		events []Event
	)
	m.Subscribe(func(ev Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	a, _ := m.Connect(ctx, "p")
	b, _ := m.Connect(ctx, "p")
	if _, err := m.UseDatabase(ctx, a, "two"); err != nil {
		t.Fatal(err)
	}
	if err := m.Disconnect(a); err != nil {
		t.Fatal(err)
	}
	if err := m.DisconnectAll(); err != nil {
		t.Fatal(err)
	}
	want := []Event{{a, DatabaseChanged}, {a, SessionClosed}, {b, SessionClosed}}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}
