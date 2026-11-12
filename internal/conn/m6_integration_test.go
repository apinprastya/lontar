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
	"time"

	"dbedit/internal/driver"
	"dbedit/internal/driver/all"
	"dbedit/internal/sshtest"
	"dbedit/internal/sshtunnel"
	"dbedit/internal/store"
	"dbedit/internal/testutil"
	"dbedit/internal/tlstest"

	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// bastion starts the in-process SSH server, trusted through a fake user
// known_hosts, and returns a manager wired to it.
func bastion(t *testing.T, src *fakeSource) (*Manager, *sshtest.Server) {
	t.Helper()
	srv, err := sshtest.Start(sshtest.Options{User: "jump", Password: "sshpw"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	dir := t.TempDir()
	kh := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(kh, []byte(srv.KnownHostsLine()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(src, all.Registry())
	m.Tunnels = SSHTunnels(sshtunnel.New(sshtunnel.Options{
		HostKeys:  &sshtunnel.HostKeys{UserFiles: []string{kh}, AppFile: filepath.Join(dir, "app_known_hosts")},
		HomeDir:   func() (string, error) { return dir, nil },
		KeepAlive: -1,
	}))
	return m, srv
}

func withSSH(c store.Connection, srv *sshtest.Server) store.Connection {
	c.SSH = &store.SSHConfig{Enabled: true, Host: "127.0.0.1", Port: srv.Port(), User: "jump", AuthMethod: store.SSHAuthPassword}
	return c
}

func TestSSHTunnelToDatabases(t *testing.T) {
	cases := []struct {
		kind  driver.Kind
		start func(testing.TB) testutil.DB
	}{
		{driver.Postgres, testutil.SharedPostgres},
		{driver.MySQL, testutil.SharedMySQL},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			c, pw := fromDSN(t, tc.kind, tc.start(t).DSN)
			// Reach the database by a name only the bastion knows.
			real := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
			c.Host, c.Port = "db.internal", 5000
			src := newFakeSource()
			m, srv := bastion(t, src)
			srv.Rewrite["db.internal:5000"] = real
			c = withSSH(c, srv)
			src.conns[c.ID] = c
			src.passwords[c.ID] = pw
			src.passwords[c.ID+":ssh-password"] = "sshpw"
			ctx := context.Background()

			// Test goes through the tunnel and closes it.
			if _, err := m.Test(ctx, c, Secrets{Password: pw, SSHPassword: "sshpw"}); err != nil {
				t.Fatal(err)
			}
			if srv.Forwards() == 0 {
				t.Fatal("Test did not use the tunnel")
			}
			// Wrong SSH password.
			if _, err := m.Test(ctx, c, Secrets{Password: pw, SSHPassword: "nope"}); err == nil || !strings.Contains(err.Error(), "SSH authentication failed") {
				t.Fatalf("err = %v", err)
			}

			id, err := m.Connect(ctx, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			s, _ := m.Get(id)
			if !s.Tunneled {
				t.Fatal("not tunneled")
			}
			one := func() error {
				var n int
				return s.DB.QueryRowContext(ctx, "SELECT 1").Scan(&n)
			}
			if err := one(); err != nil {
				t.Fatal(err)
			}
			logins := srv.Logins()

			// Drop the SSH connection: an error surfaces, then the pool
			// reconnects the tunnel once and queries work again.
			srv.DropAll()
			var errs []error
			for i := 0; i < 3; i++ {
				if err := one(); err != nil {
					errs = append(errs, err)
					continue
				}
				break
			}
			if len(errs) == 3 {
				t.Fatalf("no recovery after the tunnel dropped: %v", errs)
			}
			if srv.Logins() != logins+1 {
				t.Fatalf("logins = %d, want %d (one reconnect)", srv.Logins(), logins+1)
			}

			// UseDatabase reuses the tunnel.
			if tc.kind == driver.Postgres {
				s2, err := m.UseDatabase(ctx, id, "postgres")
				if err != nil {
					t.Fatal(err)
				}
				var n int
				if err := s2.DB.QueryRowContext(ctx, "SELECT 1").Scan(&n); err != nil {
					t.Fatal(err)
				}
				if srv.Logins() != logins+1 {
					t.Fatal("UseDatabase opened another SSH connection")
				}
			}
			if err := m.Disconnect(id); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// pgTLS starts a PostgreSQL container with ssl=on and the given server
// certificate (copied in from the host).
func pgTLS(t *testing.T, f tlstest.Files) (host string, port int) {
	t.Helper()
	testutil.SharedPostgres(t) // skips without Docker
	ctx := context.Background()
	script := `cp /tls-in/* /var/lib/postgresql/ && chown postgres:postgres /var/lib/postgresql/*.pem && chmod 600 /var/lib/postgresql/server-key.pem && exec docker-entrypoint.sh "$@"`
	c, err := tcpostgres.Run(ctx, testutil.Images[testutil.Postgres],
		tcpostgres.WithDatabase("dbedit"), tcpostgres.WithUsername("dbedit"), tcpostgres.WithPassword("dbedit"),
		testcontainers.WithFiles(
			testcontainers.ContainerFile{HostFilePath: f.CA, ContainerFilePath: "/tls-in/ca.pem", FileMode: 0o644},
			testcontainers.ContainerFile{HostFilePath: f.ServerCert, ContainerFilePath: "/tls-in/server-cert.pem", FileMode: 0o644},
			testcontainers.ContainerFile{HostFilePath: f.ServerKey, ContainerFilePath: "/tls-in/server-key.pem", FileMode: 0o644},
		),
		testcontainers.WithEntrypoint("sh", "-c", script, "sh"),
		testcontainers.WithCmdArgs("-c", "ssl=on",
			"-c", "ssl_cert_file=/var/lib/postgresql/server-cert.pem",
			"-c", "ssl_key_file=/var/lib/postgresql/server-key.pem",
			"-c", "ssl_ca_file=/var/lib/postgresql/ca.pem"),
		tcpostgres.BasicWaitStrategies(),
	)
	if c != nil {
		t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	}
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return "localhost", int(p.Num())
}

// mysqlTLS starts a MySQL 8 container with the given server certificate.
func mysqlTLS(t *testing.T, f tlstest.Files) (host string, port int) {
	t.Helper()
	testutil.SharedMySQL(t) // skips without Docker
	ctx := context.Background()
	c, err := tcmysql.Run(ctx, testutil.Images[testutil.MySQL],
		tcmysql.WithDatabase("dbedit"), tcmysql.WithUsername("dbedit"), tcmysql.WithPassword("dbedit"),
		testcontainers.WithFiles(
			testcontainers.ContainerFile{HostFilePath: f.CA, ContainerFilePath: "/tls/ca.pem", FileMode: 0o644},
			testcontainers.ContainerFile{HostFilePath: f.ServerCert, ContainerFilePath: "/tls/server-cert.pem", FileMode: 0o644},
			testcontainers.ContainerFile{HostFilePath: f.ServerKey, ContainerFilePath: "/tls/server-key.pem", FileMode: 0o644},
		),
		testcontainers.WithCmdArgs("--ssl-ca=/tls/ca.pem", "--ssl-cert=/tls/server-cert.pem", "--ssl-key=/tls/server-key.pem"),
	)
	if c != nil {
		t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	}
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.MappedPort(ctx, "3306/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return "localhost", int(p.Num())
}

func TestTLS(t *testing.T) {
	ca, _ := tlstest.NewCA("dbedit test CA")
	dir := t.TempDir()
	files, err := ca.WriteFiles(dir, "dbedit", "localhost") // DNS name only
	if err != nil {
		t.Fatal(err)
	}
	wrong, _ := tlstest.NewCA("wrong CA")
	wrongDir := t.TempDir()
	wrongFiles, _ := wrong.WriteFiles(wrongDir, "x", "localhost")

	type check func(t *testing.T, s *Session) bool // reports whether TLS is in use
	pgCheck := func(t *testing.T, s *Session) bool {
		var on bool
		if err := s.DB.QueryRowContext(context.Background(), "SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()").Scan(&on); err != nil {
			t.Fatal(err)
		}
		return on
	}
	myCheck := func(t *testing.T, s *Session) bool {
		var name, cipher string
		if err := s.DB.QueryRowContext(context.Background(), "SHOW STATUS LIKE 'Ssl_cipher'").Scan(&name, &cipher); err != nil {
			t.Fatal(err)
		}
		return cipher != ""
	}
	engines := []struct {
		kind  driver.Kind
		start func(*testing.T, tlstest.Files) (string, int)
		check check
		modes []string
	}{
		{driver.Postgres, pgTLS, pgCheck, nil},
		{driver.MySQL, mysqlTLS, myCheck, []string{store.SSLPreferred}},
	}
	for _, e := range engines {
		t.Run(string(e.kind), func(t *testing.T) {
			host, port := e.start(t, files)
			base := store.Connection{ID: "tls", Name: "tls", Kind: e.kind, Host: host, Port: port, User: "dbedit", Database: "dbedit"}
			src := newFakeSource()
			m := NewManager(src, all.Registry())
			m.PingTimeout = 60 * time.Second
			sec := Secrets{Password: "dbedit"}
			ctx := context.Background()

			connect := func(t *testing.T, c store.Connection) (*Session, error) {
				t.Helper()
				src.mu.Lock()
				src.conns[c.ID], src.passwords[c.ID] = c, "dbedit"
				src.mu.Unlock()
				// The server may still be starting up (MySQL restarts once
				// during initialisation).
				var id string
				var err error
				for i := 0; i < 30; i++ {
					if id, err = m.Connect(ctx, c.ID); err == nil || !strings.Contains(err.Error(), "connection refused") && !strings.Contains(err.Error(), "EOF") && !strings.Contains(err.Error(), "bad connection") {
						break
					}
					time.Sleep(time.Second)
				}
				if err != nil {
					return nil, err
				}
				s, _ := m.Get(id)
				t.Cleanup(func() { _ = m.Disconnect(id) })
				return s, nil
			}
			with := func(mode, caFile, host string) store.Connection {
				c := base
				c.Host = host
				c.SSL = &store.SSLConfig{Mode: mode, CAFile: caFile}
				return c
			}
			ok := func(name string, c store.Connection, wantTLS bool) {
				t.Run(name, func(t *testing.T) {
					s, err := connect(t, c)
					if err != nil {
						t.Fatal(err)
					}
					if got := e.check(t, s); got != wantTLS {
						t.Fatalf("TLS in use = %v, want %v", got, wantTLS)
					}
				})
			}
			fail := func(name string, c store.Connection, want string) {
				t.Run(name, func(t *testing.T) {
					if _, err := connect(t, c); err == nil || !strings.Contains(strings.ToLower(err.Error()), want) {
						t.Fatalf("err = %v, want %q", err, want)
					}
				})
			}

			ok("disable", with(store.SSLDisable, "", "localhost"), false)
			ok("require", with(store.SSLRequire, "", "localhost"), true)
			ok("verify-ca", with(store.SSLVerifyCA, files.CA, "127.0.0.1"), true) // host name not checked
			ok("verify-full", with(store.SSLVerifyFull, files.CA, "localhost"), true)
			for _, mode := range e.modes {
				ok(mode, with(mode, "", "localhost"), true)
			}
			fail("verify-ca wrong CA", with(store.SSLVerifyCA, wrongFiles.CA, "localhost"), "certificate")
			fail("verify-full wrong CA", with(store.SSLVerifyFull, wrongFiles.CA, "localhost"), "certificate")
			fail("verify-full host mismatch", with(store.SSLVerifyFull, files.CA, "127.0.0.1"), "127.0.0.1")
			fail("require with wrong CA", with(store.SSLRequire, wrongFiles.CA, "localhost"), "certificate")

			// Through an SSH tunnel, verify-full still checks the
			// configured host name.
			t.Run("ssh+verify-full", func(t *testing.T) {
				src2 := newFakeSource()
				m2, srv := bastion(t, src2)
				c := withSSH(with(store.SSLVerifyFull, files.CA, "localhost"), srv)
				if _, err := m2.Test(ctx, c, Secrets{Password: "dbedit", SSHPassword: "sshpw"}); err != nil {
					t.Fatal(err)
				}
				if srv.Forwards() == 0 {
					t.Fatal("not tunneled")
				}
			})

			// The legacy driver options still work when the SSL tab is
			// unset; a set SSL mode wins over them.
			legacy := base
			if e.kind == driver.Postgres {
				legacy.Options = map[string]string{"sslmode": "require"}
			} else {
				legacy.Options = map[string]string{"tls": "skip-verify"}
			}
			legacy.ID = "legacy"
			ok("legacy option", legacy, true)
			legacy.ID = "override"
			legacy.SSL = &store.SSLConfig{Mode: store.SSLDisable}
			ok("ssl mode wins", legacy, false)
			_ = sec
			_ = fmt.Sprint
			_ = errors.New
		})
	}
}
