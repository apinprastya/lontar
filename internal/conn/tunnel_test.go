package conn

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"dbedit/internal/driver"
	"dbedit/internal/driver/sqlite"
	"dbedit/internal/sshtunnel"
	"dbedit/internal/store"
)

type fakeTunnel struct {
	mu     sync.Mutex
	closed int
}

func (f *fakeTunnel) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("fake")
}
func (f *fakeTunnel) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

// dialRecorder is a fake server driver that records the config it opens.
type dialRecorder struct {
	fakeServerOpen
	mu   *sync.Mutex
	cfgs *[]driver.Config
}

func (d dialRecorder) Open(ctx context.Context, cfg driver.Config, pw string) (*sql.DB, error) {
	d.mu.Lock()
	*d.cfgs = append(*d.cfgs, cfg)
	d.mu.Unlock()
	return d.fakeServerOpen.Open(ctx, cfg, pw)
}

func TestTunnelLifecycle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "x.db")
	var mu sync.Mutex
	var cfgs []driver.Config
	reg := driver.NewRegistry(dialRecorder{fakeServerOpen{fakeServer: fakeServer{Driver: sqlite.New(), dbs: []string{"one", "two"}}, path: path, lite: sqlite.New()}, &mu, &cfgs})
	c := store.Connection{ID: "p", Name: "pg", Kind: driver.Postgres, Host: "db", Database: "one",
		SSH: &store.SSHConfig{Enabled: true, Host: "bastion", Port: 2222, User: "jump", AuthMethod: "key", KeyPath: "~/id"},
		SSL: &store.SSLConfig{Mode: store.SSLVerifyFull, CAFile: "~/ca.pem"}}
	src := newFakeSource(c)
	src.passwords["p"] = "dbpw"
	src.passwords["p:ssh-password"] = "sshpw"
	src.passwords["p:ssh-passphrase"] = "phrase"
	m := NewManager(src, reg)
	m.HomeDir = func() (string, error) { return "/home/test", nil }

	// No opener: refused.
	if _, err := m.Connect(ctx, "p"); !errors.Is(err, ErrNoTunnels) {
		t.Fatalf("err = %v", err)
	}
	var opened []*fakeTunnel
	var gotCfg sshtunnel.Config
	m.Tunnels = func(_ context.Context, cfg sshtunnel.Config) (Tunnel, error) {
		gotCfg = cfg
		ft := &fakeTunnel{}
		opened = append(opened, ft)
		return ft, nil
	}
	id, err := m.Connect(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	want := sshtunnel.Config{Host: "bastion", Port: 2222, User: "jump", Auth: "key", KeyPath: "~/id", Password: "sshpw", Passphrase: "phrase"}
	if gotCfg != want {
		t.Fatalf("ssh cfg = %+v", gotCfg)
	}
	s, _ := m.Get(id)
	if !s.Tunneled || len(opened) != 1 {
		t.Fatal("session not tunneled")
	}
	mu.Lock()
	last := cfgs[len(cfgs)-1]
	mu.Unlock()
	if last.Dial == nil || last.TLS == nil || last.TLS.CAFile != filepath.Join("/home/test", "ca.pem") {
		t.Fatalf("driver cfg = %+v", last)
	}

	// UseDatabase reuses the tunnel.
	if _, err := m.UseDatabase(ctx, id, "two"); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0].closed != 0 {
		t.Fatalf("tunnels = %d, closed = %d", len(opened), opened[0].closed)
	}
	mu.Lock()
	last = cfgs[len(cfgs)-1]
	mu.Unlock()
	if last.Dial == nil {
		t.Fatal("UseDatabase did not use the tunnel")
	}
	// Disconnect closes it.
	if err := m.Disconnect(id); err != nil {
		t.Fatal(err)
	}
	if opened[0].closed != 1 {
		t.Fatal("tunnel not closed on disconnect")
	}
	// DisconnectAll closes every tunnel.
	id2, _ := m.Connect(ctx, "p")
	_ = id2
	if err := m.DisconnectAll(); err != nil {
		t.Fatal(err)
	}
	if opened[1].closed != 1 {
		t.Fatal("tunnel not closed on DisconnectAll")
	}

	// Test opens and closes its own tunnel.
	m2 := NewManager(src, reg)
	m2.Tunnels = m.Tunnels
	if _, err := m2.Test(ctx, c, Secrets{}); err != nil {
		t.Fatal(err)
	}
	if n := len(opened); opened[n-1].closed != 1 {
		t.Fatal("Test left the tunnel open")
	}
	// A failure after the tunnel opened closes it.
	bad := c
	bad.Database = "bad"
	if _, err := m2.Test(ctx, bad, Secrets{}); err == nil || !strings.Contains(err.Error(), "open boom") {
		t.Fatalf("err = %v", err)
	}
	if n := len(opened); opened[n-1].closed != 1 {
		t.Fatal("failed open left the tunnel open")
	}
	// Tunnel errors are returned as is.
	m2.Tunnels = func(context.Context, sshtunnel.Config) (Tunnel, error) {
		return nil, &sshtunnel.HostKeyUnknownError{Message: "unknown host"}
	}
	var hk *sshtunnel.HostKeyUnknownError
	if _, err := m2.Test(ctx, c, Secrets{}); !errors.As(err, &hk) {
		t.Fatalf("err = %v", err)
	}
	// SSH secrets errors.
	src.sshErr = errors.New("keychain")
	if _, err := m2.Connect(ctx, "p"); err == nil || !strings.Contains(err.Error(), "keychain") {
		t.Fatalf("err = %v", err)
	}
	src.sshErr = nil
	// A bad home dir for certificate paths.
	m2.HomeDir = func() (string, error) { return "", errors.New("no home") }
	if _, err := m2.Test(ctx, c, Secrets{}); err == nil || !strings.Contains(err.Error(), "no home") {
		t.Fatalf("err = %v", err)
	}
	// The real adapter: a refused SSH connection.
	d := sshtunnel.New(sshtunnel.Options{HostKeys: &sshtunnel.HostKeys{}})
	if _, err := SSHTunnels(d)(ctx, sshtunnel.Config{Host: "127.0.0.1", Port: 1, User: "u"}); err == nil {
		t.Fatal("expected dial error")
	}
}
