package conn

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"dbedit/internal/driver"
	"dbedit/internal/driver/all"
	"dbedit/internal/store"
	"dbedit/internal/testutil"

	gomysql "github.com/go-sql-driver/mysql"
)

// fromDSN converts a testutil DSN into a saved connection and its password.
func fromDSN(t *testing.T, kind driver.Kind, dsn string) (store.Connection, string) {
	t.Helper()
	c := store.Connection{ID: string(kind), Name: "it-" + string(kind), Kind: kind}
	var pw, hostport string
	if kind == driver.Postgres {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		pw, _ = u.User.Password()
		c.User = u.User.Username()
		c.Database = strings.TrimPrefix(u.Path, "/")
		hostport = u.Host
		c.Options = map[string]string{"sslmode": "disable"}
	} else {
		mc, err := gomysql.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		pw, c.User, c.Database, hostport = mc.Passwd, mc.User, mc.DBName, mc.Addr
	}
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		t.Fatal(err)
	}
	c.Host = host
	c.Port, _ = strconv.Atoi(port)
	return c, pw
}

func TestServersIntegration(t *testing.T) {
	cases := []struct {
		kind       driver.Kind
		start      func(testing.TB) testutil.DB
		prefix     string
		badPwMatch string
	}{
		{driver.Postgres, testutil.SharedPostgres, "PostgreSQL 16.", "password authentication failed"},
		{driver.MySQL, testutil.SharedMySQL, "MySQL 8.", "Access denied"},
		{driver.MariaDB, testutil.SharedMariaDB, "MariaDB 11.", "Access denied"},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			c, pw := fromDSN(t, tc.kind, tc.start(t).DSN)
			src := newFakeSource(c)
			src.passwords[c.ID] = pw
			m := NewManager(src, all.Registry())
			ctx := context.Background()

			// Test: opens, pings, reports the version, keeps nothing open.
			v, err := m.Test(ctx, c, Secrets{Password: pw})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(v, tc.prefix) {
				t.Fatalf("Test version = %q, want %q", v, tc.prefix)
			}
			if len(m.List()) != 0 {
				t.Fatal("Test must not create a session")
			}

			// Wrong password.
			if _, err := m.Test(ctx, c, Secrets{Password: "definitely-wrong"}); err == nil || !strings.Contains(err.Error(), tc.badPwMatch) {
				t.Fatalf("wrong password err = %v, want %q", err, tc.badPwMatch)
			}

			// Connect with the stored password.
			id, err := m.Connect(ctx, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			s, _ := m.Get(id)
			if !strings.HasPrefix(s.ServerVersion, tc.prefix) {
				t.Fatalf("session version = %q", s.ServerVersion)
			}
			var one int
			if err := s.DB.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
				t.Fatalf("SELECT 1 = %d, %v", one, err)
			}
			if err := m.Disconnect(id); err != nil {
				t.Fatal(err)
			}
		})
	}

	// MariaDB saved as "mysql" is detected from the version string.
	t.Run("mariadb-as-mysql", func(t *testing.T) {
		c, pw := fromDSN(t, driver.MySQL, testutil.SharedMariaDB(t).DSN)
		v, err := NewManager(newFakeSource(), all.Registry()).Test(context.Background(), c, Secrets{Password: pw})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(v, "MariaDB 11.") {
			t.Fatalf("version = %q", v)
		}
	})
}

func TestUseDatabaseIntegration(t *testing.T) {
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
			target := tc.start(t)
			ctx := context.Background()
			if tc.kind != driver.Postgres {
				// Only root may create databases; browse as root too.
				root, err := testutil.AsRoot(target)
				if err != nil {
					t.Fatal(err)
				}
				target = root
			}
			admin := testutil.Open(t, target)
			for _, q := range []string{"DROP DATABASE IF EXISTS second", "CREATE DATABASE second"} {
				if _, err := admin.ExecContext(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			c, pw := fromDSN(t, tc.kind, target.DSN)
			src := newFakeSource(c)
			src.passwords[c.ID] = pw
			m := NewManager(src, all.Registry())
			t.Cleanup(func() { _ = m.DisconnectAll() })

			id, err := m.Connect(ctx, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := m.Get(id)
			wantBefore := c.Database
			if before.Database != wantBefore {
				t.Fatalf("initial database = %q, want %q", before.Database, wantBefore)
			}

			s, err := m.UseDatabase(ctx, id, "second")
			if err != nil {
				t.Fatal(err)
			}
			if s.ID != id || s.Database != "second" || s.DB == before.DB {
				t.Fatalf("after switch: %+v", s)
			}
			got, _ := m.Get(id)
			if got != s {
				t.Fatal("Get should return the new session")
			}
			d, _ := all.Registry().Get(tc.kind)
			cur, err := d.CurrentDatabase(ctx, got.DB)
			if err != nil || cur != "second" {
				t.Fatalf("current database = %q, %v", cur, err)
			}
			if err := before.DB.PingContext(ctx); err == nil {
				t.Fatal("old pool should be closed")
			}
			if _, err := m.UseDatabase(ctx, id, "no_such_db"); !errors.Is(err, ErrUnknownDatabase) {
				t.Fatalf("err = %v", err)
			}
			if _, err := m.UseDatabase(ctx, id, "second; DROP DATABASE second"); !errors.Is(err, ErrUnknownDatabase) {
				t.Fatalf("err = %v", err)
			}
			// Switch back (root on MySQL starts without a database).
			back := wantBefore
			if back == "" {
				back = "dbedit"
			}
			if s, err = m.UseDatabase(ctx, id, back); err != nil || s.Database != back {
				t.Fatalf("switch back = %+v, %v", s, err)
			}
		})
	}
}
