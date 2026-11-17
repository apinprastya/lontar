package conn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"lontar/internal/driver"
	"lontar/internal/driver/all"
	"lontar/internal/sshtest"
	"lontar/internal/sshtunnel"
	"lontar/internal/store"
	"lontar/internal/testutil"
)

// jumpChain starts two jump hosts and a final SSH server ("pw1".."pw3"),
// trusts all but the servers at the indexes in untrusted, and returns a
// manager wired to them.
func jumpChain(t *testing.T, src *fakeSource, untrusted ...int) (*Manager, []*sshtest.Server) {
	t.Helper()
	servers, err := sshtest.StartChain(
		sshtest.Options{User: "u1", Password: "pw1"},
		sshtest.Options{User: "u2", Password: "pw2"},
		sshtest.Options{User: "u3", Password: "pw3"},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sshtest.CloseAll(servers) })
	dir := t.TempDir()
	var lines []string
	for i, s := range servers {
		skip := false
		for _, u := range untrusted {
			skip = skip || u == i
		}
		if !skip {
			lines = append(lines, s.KnownHostsLine())
		}
	}
	kh := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(kh, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(src, all.Registry())
	m.Tunnels = SSHTunnels(sshtunnel.New(sshtunnel.Options{
		HostKeys:  &sshtunnel.HostKeys{UserFiles: []string{kh}, AppFile: filepath.Join(dir, "app_known_hosts")},
		HomeDir:   func() (string, error) { return dir, nil },
		KeepAlive: -1,
	}))
	return m, servers
}

func withJumps(c store.Connection, servers []*sshtest.Server) store.Connection {
	last := servers[len(servers)-1]
	c.SSH = &store.SSHConfig{Enabled: true, Host: "127.0.0.1", Port: last.Port(), User: fmt.Sprintf("u%d", len(servers)), AuthMethod: store.SSHAuthPassword}
	for i, s := range servers[:len(servers)-1] {
		c.SSH.Jumps = append(c.SSH.Jumps, store.SSHHop{ID: fmt.Sprintf("hop%d", i+1), Host: "127.0.0.1", Port: s.Port(),
			User: fmt.Sprintf("u%d", i+1), AuthMethod: store.SSHAuthPassword})
	}
	return c
}

func TestSSHJumpHostsToDatabases(t *testing.T) {
	cases := []struct {
		kind  driver.Kind
		start func(testing.TB) testutil.DB
	}{
		{driver.Postgres, testutil.SharedPostgres},
		{driver.MySQL, testutil.SharedMySQL},
		{driver.MariaDB, testutil.SharedMariaDB},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			c, pw := fromDSN(t, tc.kind, tc.start(t).DSN)
			real := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
			c.Host, c.Port = "db.internal", 5000
			src := newFakeSource()
			m, servers := jumpChain(t, src)
			servers[2].Rewrite["db.internal:5000"] = real
			c = withJumps(c, servers)
			src.conns[c.ID] = c
			src.passwords[c.ID] = pw
			src.passwords[c.ID+":ssh-password"] = "pw3"
			for i := 1; i <= 2; i++ {
				src.passwords[store.SecretKey(c.ID, store.JumpSecretPurpose(fmt.Sprintf("hop%d", i), "password"))] = fmt.Sprintf("pw%d", i)
			}
			ctx := context.Background()

			// Test goes through the whole chain.
			hops := []store.HopSecret{{Password: "pw1"}, {Password: "pw2"}}
			if _, err := m.Test(ctx, c, Secrets{Password: pw, SSHPassword: "pw3", Jumps: hops}); err != nil {
				t.Fatal(err)
			}
			for i, s := range servers {
				if s.Forwards() == 0 {
					t.Fatalf("server %d not used", i)
				}
			}
			// A wrong password at jump host 1 names the hop.
			bad := []store.HopSecret{{Password: "nope"}, {Password: "pw2"}}
			if _, err := m.Test(ctx, c, Secrets{Password: pw, SSHPassword: "pw3", Jumps: bad}); err == nil ||
				!strings.Contains(err.Error(), "jump host 1 (127.0.0.1): SSH authentication failed") || strings.Contains(err.Error(), "nope") {
				t.Fatalf("err = %v", err)
			}

			// Connect uses the stored hop secrets.
			id, err := m.Connect(ctx, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			s, _ := m.Get(id)
			var n int
			if err := s.DB.QueryRowContext(ctx, "SELECT 1").Scan(&n); err != nil {
				t.Fatal(err)
			}
			// The middle jump host drops everything: the pool recovers
			// after rebuilding the chain.
			servers[1].DropAll()
			var errs []error
			for i := 0; i < 3; i++ {
				if err := s.DB.QueryRowContext(ctx, "SELECT 1").Scan(&n); err != nil {
					errs = append(errs, err)
					continue
				}
				break
			}
			if len(errs) == 3 {
				t.Fatalf("no recovery after the chain dropped: %v", errs)
			}
			if err := m.Disconnect(id); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSSHJumpHostUnknownKeyAtHop2(t *testing.T) {
	// No database needed: the chain stops at the unknown key.
	src := newFakeSource()
	m, servers := jumpChain(t, src, 1)
	c := withJumps(store.Connection{ID: "x", Name: "x", Kind: driver.Postgres, Host: "db", Port: 5432}, servers)
	_, err := m.Test(context.Background(), c, Secrets{SSHPassword: "pw3", Jumps: []store.HopSecret{{Password: "pw1"}, {Password: "pw2"}}})
	var unknown *sshtunnel.HostKeyUnknownError
	if !errors.As(err, &unknown) || unknown.Hop != 2 || unknown.HopLabel != "jump host 2 (127.0.0.1)" {
		t.Fatalf("err = %v", err)
	}
	if servers[2].Logins() != 0 {
		t.Fatal("reached the final server past an unknown key")
	}
}
