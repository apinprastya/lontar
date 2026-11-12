// Package postgres implements the PostgreSQL driver using pgx (database/sql
// via pgx/v5/stdlib).
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"lontar/internal/driver"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/stdlib"
)

// CancelDeadline is how long a cancelled query may take to stop after the
// server-side cancel request before the connection is closed.
const CancelDeadline = 5 * time.Second

// cancelHandler makes a cancelled context send a PostgreSQL cancel request
// (like pressing Ctrl+C in psql) instead of pgx's default of closing the
// connection, so a cancelled query keeps its connection and session (the
// SQL editor's pinned connection with its transaction and settings).
func cancelHandler(c *pgconn.PgConn) ctxwatch.Handler {
	return &pgconn.CancelRequestContextWatcherHandler{Conn: c, DeadlineDelay: CancelDeadline}
}

// DefaultPort is PostgreSQL's default TCP port.
const DefaultPort = 5432

// ApplicationName is reported to the server (visible in pg_stat_activity).
const ApplicationName = "lontar"

// ReadOnlyParam is the run-time parameter that makes sessions read-only.
const ReadOnlyParam = "default_transaction_read_only"

// reserved option keys: these are set from dedicated Config fields, and
// credentials must never be stored as plain options.
var reserved = []string{"host", "hostaddr", "port", "user", "password", "passfile", "dbname", "database"}

// Driver is the PostgreSQL driver. Introspection lives in introspect.go.
type Driver struct {
	driver.PostgresDialect
}

// New returns a PostgreSQL driver.
func New() *Driver { return &Driver{} }

// Kind implements driver.Driver.
func (*Driver) Kind() driver.Kind { return driver.Postgres }

// DisplayName implements driver.Driver.
func (*Driver) DisplayName() string { return "PostgreSQL" }

// DefaultPort implements driver.Driver.
func (*Driver) DefaultPort() int { return DefaultPort }

// BuildDSN returns a postgres:// URL. User, password and database are
// percent-encoded by net/url, so any characters are safe.
func (d *Driver) BuildDSN(cfg driver.Config, password string) (string, error) {
	if cfg.Host == "" {
		return "", fmt.Errorf("postgres: host is required")
	}
	if err := driver.CheckOptions(cfg.Options, reserved...); err != nil {
		return "", err
	}
	u := url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.PortOr(DefaultPort))),
	}
	switch {
	case cfg.User != "" && password != "":
		u.User = url.UserPassword(cfg.User, password)
	case cfg.User != "":
		u.User = url.User(cfg.User)
	case password != "":
		u.User = url.UserPassword("", password)
	}
	if cfg.Database != "" {
		u.Path = "/" + cfg.Database
	}
	q := url.Values{}
	q.Set("connect_timeout", strconv.Itoa(int(cfg.Timeout().Seconds())))
	q.Set("application_name", ApplicationName)
	for k, v := range cfg.Options {
		q.Set(k, v)
	}
	if cfg.TLS.Active() {
		// The SSL settings win over any ssl* driver options.
		for _, k := range sslParams {
			q.Del(k)
		}
		q.Set("sslmode", cfg.TLS.Mode)
		setIf(q, "sslrootcert", cfg.TLS.CAFile)
		setIf(q, "sslcert", cfg.TLS.CertFile)
		setIf(q, "sslkey", cfg.TLS.KeyFile)
	}
	if cfg.ReadOnly {
		// A runtime parameter sent at connection start: every transaction
		// of every pooled connection is read-only, so the server rejects
		// writes ("cannot execute INSERT in a read-only transaction"). Set
		// after Options so that they cannot turn it off.
		q.Set(ReadOnlyParam, "on")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// sslParams are the libpq TLS parameters replaced by Config.TLS.
var sslParams = []string{"sslmode", "sslrootcert", "sslcert", "sslkey", "sslpassword", "sslsni"}

func setIf(q url.Values, k, v string) {
	if v != "" {
		q.Set(k, v)
	}
}

// Open implements driver.Driver. TLS is configured by pgx from the libpq
// parameters (sslmode, sslrootcert, sslcert, sslkey): verify-ca checks the
// chain without the host name, verify-full also the host name, and require
// with a CA file behaves like verify-ca. With cfg.Dial (SSH tunnel) every
// connection is opened through it and the host name is not resolved
// locally; TLS still verifies against the configured host.
func (d *Driver) Open(_ context.Context, cfg driver.Config, password string) (*sql.DB, error) {
	if err := driver.CheckTLS(driver.Postgres, cfg.TLS); err != nil {
		return nil, err
	}
	dsn, err := d.BuildDSN(cfg, password)
	if err != nil {
		return nil, err
	}
	pc, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: invalid connection settings: %w", err)
	}
	if cfg.Dial != nil {
		applyDial(pc, cfg.Dial)
	}
	return OpenConfig(pc), nil
}

// applyDial routes every connection attempt (including TLS fallbacks)
// through dial and skips local DNS resolution.
func applyDial(pc *pgx.ConnConfig, dial driver.DialFunc) {
	pc.DialFunc = pgconn.DialFunc(dial)
	pc.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
}

// OpenConfig opens a pool for a parsed pgx configuration, with cancelled
// contexts sending a server-side cancel request (see cancelHandler).
func OpenConfig(pc *pgx.ConnConfig) *sql.DB {
	pc.BuildContextWatcherHandler = cancelHandler
	return stdlib.OpenDB(*pc)
}

// ServerVersion implements driver.Driver.
func (*Driver) ServerVersion(ctx context.Context, db *sql.DB) (string, error) {
	var v string
	if err := db.QueryRowContext(ctx, "SHOW server_version").Scan(&v); err != nil {
		return "", fmt.Errorf("postgres: read server version: %w", err)
	}
	return "PostgreSQL " + v, nil
}
