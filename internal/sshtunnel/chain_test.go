package sshtunnel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lontar/internal/sshtest"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// chainEnv is a chain of n test SSH servers (jump hosts, then the final
// one) with distinct passwords "pw1".."pwN".
type chainEnv struct {
	*env
	servers []*sshtest.Server
}

func newChainEnv(t *testing.T, n int) *chainEnv {
	t.Helper()
	e := newEnv(t)
	opts := make([]sshtest.Options, n)
	for i := range opts {
		opts[i] = sshtest.Options{User: fmt.Sprintf("u%d", i+1), Password: fmt.Sprintf("pw%d", i+1),
			AuthorizedKeys: []ssh.PublicKey{e.signer.PublicKey()}}
	}
	servers, err := sshtest.StartChain(opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sshtest.CloseAll(servers) })
	return &chainEnv{env: e, servers: servers}
}

// trustAll writes every server's key to the user known_hosts, except the
// servers at the given indexes.
func (c *chainEnv) trustAll(t *testing.T, except ...int) {
	t.Helper()
	var lines []string
	for i, s := range c.servers {
		skip := false
		for _, x := range except {
			skip = skip || x == i
		}
		if !skip {
			lines = append(lines, s.KnownHostsLine())
		}
	}
	if err := os.MkdirAll(filepath.Dir(c.userKH), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.userKH, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (c *chainEnv) hop(i int) Config {
	s := c.servers[i]
	return Config{Host: "127.0.0.1", Port: s.Port(), User: fmt.Sprintf("u%d", i+1), Auth: AuthPassword, Password: fmt.Sprintf("pw%d", i+1)}
}

// config returns the chain: every server but the last as a jump host.
func (c *chainEnv) config() Config {
	last := len(c.servers) - 1
	cfg := c.hop(last)
	for i := 0; i < last; i++ {
		cfg.Jumps = append(cfg.Jumps, c.hop(i))
	}
	return cfg
}

// recordCloses makes d record the clients it closes and returns them in
// order.
func recordCloses(d *Dialer) func() []*ssh.Client {
	var mu sync.Mutex
	var closed []*ssh.Client
	d.onClose = func(c *ssh.Client) {
		mu.Lock()
		closed = append(closed, c)
		mu.Unlock()
	}
	return func() []*ssh.Client {
		mu.Lock()
		defer mu.Unlock()
		return append([]*ssh.Client{}, closed...)
	}
}

func TestChainReachesTarget(t *testing.T) {
	for _, n := range []int{2, 3} {
		t.Run(fmt.Sprintf("%d hops", n), func(t *testing.T) {
			c := newChainEnv(t, n)
			c.trustAll(t)
			ctx := context.Background()
			tun, err := c.dialer(Options{KeepAlive: -1}).Open(ctx, c.config())
			if err != nil {
				t.Fatal(err)
			}
			defer tun.Close()
			// The final server resolves the database host name.
			last := c.servers[n-1]
			last.Rewrite["db.internal:5432"] = c.echo
			conn, err := tun.Dial(ctx, "tcp", "db.internal:5432")
			if err != nil {
				t.Fatal(err)
			}
			roundTrip(t, conn)
			_ = conn.Close()
			for i, s := range c.servers {
				if s.Logins() != 1 {
					t.Errorf("server %d logins = %d", i, s.Logins())
				}
				// Every jump host forwarded once (to the next hop); the
				// final server once (to the database).
				if s.Forwards() != 1 {
					t.Errorf("server %d forwards = %d", i, s.Forwards())
				}
			}
			tun.mu.Lock()
			chainLen := len(tun.chain)
			tun.mu.Unlock()
			if chainLen != n {
				t.Fatalf("chain = %d clients", chainLen)
			}
		})
	}
}

func TestChainHostKeyAtHop2(t *testing.T) {
	c := newChainEnv(t, 3)
	c.trustAll(t, 1) // the second jump host is unknown
	ctx := context.Background()
	d := c.dialer(Options{KeepAlive: -1})
	closed := recordCloses(d)
	_, err := d.Open(ctx, c.config())
	var unknown *HostKeyUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want HostKeyUnknownError", err)
	}
	wantHost := knownhosts.Normalize(c.servers[1].Addr)
	if unknown.Hop != 2 || unknown.HopLabel != "jump host 2 (127.0.0.1)" || unknown.Host != wantHost ||
		!strings.HasPrefix(unknown.Error(), "jump host 2 (127.0.0.1): the authenticity") {
		t.Fatalf("unknown = %+v", unknown)
	}
	// Hop 1 was connected and is closed again; hop 3 was never reached.
	if got := closed(); len(got) != 1 {
		t.Fatalf("closed = %d clients", len(got))
	}
	if c.servers[2].Logins() != 0 {
		t.Fatal("the final server was reached past an unknown key")
	}
	// Accepting the key of hop 2 lets the chain through.
	if err := d.HostKeys().Accept(unknown.Host, unknown.FingerprintSHA256); err != nil {
		t.Fatal(err)
	}
	tun, err := d.Open(ctx, c.config())
	if err != nil {
		t.Fatal(err)
	}
	tun.Close()

	// A changed key at hop 2 is a hard error naming the hop.
	c2 := newChainEnv(t, 3)
	c2.trustAll(t, 1)
	other := knownhosts.Line([]string{c2.servers[1].Addr}, sshtest.NewHostKey().PublicKey())
	f, _ := os.OpenFile(c2.userKH, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(other + "\n")
	_ = f.Close()
	_, err = c2.dialer(Options{KeepAlive: -1}).Open(ctx, c2.config())
	var changed *HostKeyChangedError
	if !errors.As(err, &changed) {
		t.Fatalf("err = %v, want HostKeyChangedError", err)
	}
	if changed.Hop != 2 || changed.HopLabel != "jump host 2 (127.0.0.1)" || !strings.Contains(changed.Error(), "jump host 2") {
		t.Fatalf("changed = %+v", changed)
	}

	// An unknown final server is "SSH host", hop 0.
	c3 := newChainEnv(t, 2)
	c3.trustAll(t, 1)
	_, err = c3.dialer(Options{KeepAlive: -1}).Open(ctx, c3.config())
	if !errors.As(err, &unknown) || unknown.Hop != 0 || unknown.HopLabel != "SSH host (127.0.0.1)" {
		t.Fatalf("final unknown = %v", err)
	}
	// Without jump hosts the error has no label (as before).
	_, err = c3.dialer(Options{KeepAlive: -1}).Open(ctx, c3.hop(1))
	if !errors.As(err, &unknown) || unknown.HopLabel != "" || strings.Contains(unknown.Error(), "SSH host (") {
		t.Fatalf("single unknown = %+v", unknown)
	}
}

func TestChainAuthFailureAtHop1(t *testing.T) {
	c := newChainEnv(t, 2)
	c.trustAll(t)
	cfg := c.config()
	cfg.Jumps[0].Password = "wrong-secret-pw"
	_, err := c.dialer(Options{KeepAlive: -1}).Open(context.Background(), cfg)
	var he *HopError
	if !errors.As(err, &he) || he.Hop != 1 || he.Host != "127.0.0.1" {
		t.Fatalf("err = %v, want HopError at hop 1", err)
	}
	if !strings.HasPrefix(err.Error(), "jump host 1 (127.0.0.1): SSH authentication failed for u1@") {
		t.Fatalf("message = %q", err)
	}
	if c.servers[1].Logins() != 0 {
		t.Fatal("the final server was reached")
	}

	// Key auth at hop 2 (the final one) with a wrong passphrase: the hop is
	// named, the sentinel survives, and no secret is in the message.
	writeKey(t, filepath.Join(c.dir, "home", ".ssh", "id_enc"), c.clientK, "right-phrase")
	cfg = c.config()
	cfg.Jumps[0].Password = "pw1"
	cfg.Auth, cfg.KeyPath, cfg.Passphrase, cfg.Password = AuthKey, "~/.ssh/id_enc", "wrong-secret-phrase", "pw2"
	_, err = c.dialer(Options{KeepAlive: -1}).Open(context.Background(), cfg)
	if !errors.Is(err, ErrBadPassphrase) || !errors.As(err, &he) || he.Hop != 0 ||
		!strings.HasPrefix(err.Error(), "SSH host (127.0.0.1): ") {
		t.Fatalf("err = %v", err)
	}
	for _, secret := range []string{"wrong-secret-pw", "wrong-secret-phrase", "right-phrase", "pw1", "pw2"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("secret %q in error %q", secret, err)
		}
	}
	// The right passphrase works through the jump host.
	cfg.Passphrase = "right-phrase"
	tun, err := c.dialer(Options{KeepAlive: -1}).Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	tun.Close()
}

func TestChainReconnect(t *testing.T) {
	c := newChainEnv(t, 3)
	c.trustAll(t)
	ctx := context.Background()
	d := c.dialer(Options{KeepAlive: -1})
	closed := recordCloses(d)
	tun, err := d.Open(ctx, c.config())
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	tun.mu.Lock()
	first := append([]*ssh.Client{}, tun.chain...)
	tun.mu.Unlock()

	// The final server drops the client: the rest of the old chain is
	// closed and the next dial rebuilds the whole chain.
	c.servers[2].DropAll()
	waitFor(t, func() bool { return tun.Err() != nil })
	waitFor(t, func() bool { return len(closed()) == 2 })
	if got := closed(); got[0] != first[1] || got[1] != first[0] {
		t.Fatal("old jump host clients not closed in reverse order")
	}
	conn, err := tun.Dial(ctx, "tcp", c.echo)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, conn)
	_ = conn.Close()
	for i, s := range c.servers {
		if s.Logins() != 2 {
			t.Errorf("server %d logins = %d, want 2", i, s.Logins())
		}
	}

	// A dead last client noticed only by Dial (reconnect with the dead
	// client) closes its whole chain before rebuilding.
	tun.mu.Lock()
	dead, chain := tun.client, append([]*ssh.Client{}, tun.chain...)
	tun.mu.Unlock()
	before := len(closed())
	if _, err := tun.reconnect(ctx, dead); err != nil {
		t.Fatal(err)
	}
	got := closed()[before:]
	if len(got) < 3 || got[0] != chain[2] || got[1] != chain[1] || got[2] != chain[0] {
		t.Fatalf("closed %d clients, want the dead chain in reverse order", len(got))
	}

	// A jump host that goes away takes the chain down; reconnecting
	// through it fails and says so.
	_ = c.servers[0].Close()
	waitFor(t, func() bool {
		tun.mu.Lock()
		defer tun.mu.Unlock()
		return tun.client == nil
	})
	if _, err := tun.Dial(ctx, "tcp", c.echo); err == nil || !strings.Contains(err.Error(), "reconnecting failed") ||
		!strings.Contains(err.Error(), "jump host 1") {
		t.Fatalf("err = %v", err)
	}
}

func TestChainCloseOrder(t *testing.T) {
	c := newChainEnv(t, 3)
	c.trustAll(t)
	d := c.dialer(Options{KeepAlive: -1})
	closed := recordCloses(d)
	tun, err := d.Open(context.Background(), c.config())
	if err != nil {
		t.Fatal(err)
	}
	tun.mu.Lock()
	chain := append([]*ssh.Client{}, tun.chain...)
	tun.mu.Unlock()
	if err := tun.Close(); err != nil {
		t.Fatal(err)
	}
	got := closed()
	if len(got) != 3 || got[0] != chain[2] || got[1] != chain[1] || got[2] != chain[0] {
		t.Fatalf("close order wrong (%d closed)", len(got))
	}
	// The Wait watcher does not close anything twice.
	time.Sleep(20 * time.Millisecond)
	if len(closed()) != 3 {
		t.Fatalf("closed %d times", len(closed()))
	}
	// Closing already closed clients reports the (first) error.
	if err := d.closeChain(chain); err == nil {
		t.Fatal("closing a closed chain reported no error")
	}
}

func TestChainTimeoutCoversWholeChain(t *testing.T) {
	c := newChainEnv(t, 2)
	c.trustAll(t)
	hole, stop, err := sshtest.Blackhole()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	// Hop 2 is a server that accepts the connection and never answers.
	cfg := c.hop(1)
	cfg.Jumps = []Config{c.hop(0)}
	cfg.Host, cfg.Port = "127.0.0.1", portOf(hole)
	timeout := 300 * time.Millisecond
	d := c.dialer(Options{KeepAlive: -1, Timeout: timeout})
	closed := recordCloses(d)
	start := time.Now()
	_, err = d.Open(context.Background(), cfg)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	var he *HopError
	if !errors.As(err, &he) || he.Hop != 0 {
		t.Fatalf("err = %v", err)
	}
	if elapsed > 5*timeout {
		t.Fatalf("took %s with a %s timeout", elapsed, timeout)
	}
	if len(closed()) != 1 {
		t.Fatalf("jump host client not closed (%d)", len(closed()))
	}

	// TimeoutFunc replaces Timeout (re-read for every connect).
	d = c.dialer(Options{KeepAlive: -1, Timeout: time.Hour, TimeoutFunc: func() time.Duration { return timeout }})
	start = time.Now()
	if _, err = d.Open(context.Background(), cfg); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("TimeoutFunc: err = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*timeout {
		t.Fatalf("TimeoutFunc: took %s with a %s timeout", elapsed, timeout)
	}

	// A parent context that is already done stops at hop 1.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.dialer(Options{KeepAlive: -1}).Open(ctx, c.config()); err == nil || !strings.Contains(err.Error(), "jump host 1") {
		t.Fatalf("err = %v", err)
	}
	// A jump host that cannot reach the next hop.
	cfg.Port = 1
	if _, err := c.dialer(Options{KeepAlive: -1}).Open(context.Background(), cfg); err == nil ||
		!strings.Contains(err.Error(), "SSH host (127.0.0.1): SSH: connect to 127.0.0.1:1") {
		t.Fatalf("err = %v", err)
	}
}

func TestChainKeepAlive(t *testing.T) {
	c := newChainEnv(t, 2)
	c.trustAll(t)
	d := c.dialer(Options{KeepAlive: 10 * time.Millisecond})
	closed := recordCloses(d)
	tun, err := d.Open(context.Background(), c.config())
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	time.Sleep(50 * time.Millisecond)
	if tun.Err() != nil {
		t.Fatalf("keepalive broke a healthy chain: %v", tun.Err())
	}
	// The jump host drops: the final client goes with it and the jump
	// client is closed.
	c.servers[0].DropAll()
	waitFor(t, func() bool { return tun.Err() != nil })
	waitFor(t, func() bool { return len(closed()) >= 1 })
}

func TestHopLabel(t *testing.T) {
	if HopLabel(3, "b") != "jump host 3 (b)" || HopLabel(0, "h") != "SSH host (h)" {
		t.Fatal("HopLabel")
	}
	e := &HopError{Hop: 1, Host: "b", Err: ErrNoAgent}
	if !errors.Is(e, ErrNoAgent) || e.Error() != "jump host 1 (b): no SSH agent is running" {
		t.Fatalf("HopError = %v", e)
	}
}
