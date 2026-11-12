package testutil

import (
	"context"
	"database/sql"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"lontar/internal/driver"

	gomysql "github.com/go-sql-driver/mysql"
)

// DriverConfig converts a test database into a driver.Config and password,
// so tests can open it through Lontar's own drivers (with their DSN
// settings, e.g. MySQL clientFoundRows and read-only sessions).
func DriverConfig(t testing.TB, kind driver.Kind, db DB) (driver.Config, string) {
	t.Helper()
	cfg := driver.Config{Kind: kind}
	var pw, hostport string
	switch kind {
	case driver.SQLite:
		cfg.FilePath, _, _ = strings.Cut(db.DSN, "?")
		cfg.CreateIfMissing = true
		return cfg, ""
	case driver.Postgres:
		u, err := url.Parse(db.DSN)
		if err != nil {
			t.Fatalf("parse dsn: %v", err)
		}
		pw, _ = u.User.Password()
		cfg.User = u.User.Username()
		cfg.Database = strings.TrimPrefix(u.Path, "/")
		hostport = u.Host
		cfg.Options = map[string]string{"sslmode": "disable"}
	default:
		mc, err := gomysql.ParseDSN(db.DSN)
		if err != nil {
			t.Fatalf("parse dsn: %v", err)
		}
		pw, cfg.User, cfg.Database, hostport = mc.Passwd, mc.User, mc.DBName, mc.Addr
	}
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		t.Fatalf("split host: %v", err)
	}
	cfg.Host = host
	cfg.Port, _ = strconv.Atoi(port)
	return cfg, pw
}

// OpenDriver opens db with d (read-only when readOnly), pings it and closes
// it when the test ends.
func OpenDriver(t testing.TB, d driver.Driver, db DB, readOnly bool) *sql.DB {
	t.Helper()
	cfg, pw := DriverConfig(t, d.Kind(), db)
	cfg.ReadOnly = readOnly
	if readOnly {
		cfg.CreateIfMissing = false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := d.Open(ctx, cfg, pw)
	if err != nil {
		t.Fatalf("open %s: %v", d.Kind(), err)
		return nil
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.PingContext(ctx); err != nil {
		t.Fatalf("ping %s: %v", d.Kind(), err)
		return nil
	}
	return conn
}
