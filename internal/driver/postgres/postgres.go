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

	"dbedit/internal/driver"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// DefaultPort is PostgreSQL's default TCP port.
const DefaultPort = 5432

// ApplicationName is reported to the server (visible in pg_stat_activity).
const ApplicationName = "dbedit"

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
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Open implements driver.Driver.
func (d *Driver) Open(_ context.Context, cfg driver.Config, password string) (*sql.DB, error) {
	dsn, err := d.BuildDSN(cfg, password)
	if err != nil {
		return nil, err
	}
	pc, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: invalid connection settings: %w", err)
	}
	return stdlib.OpenDB(*pc), nil
}

// ServerVersion implements driver.Driver.
func (*Driver) ServerVersion(ctx context.Context, db *sql.DB) (string, error) {
	var v string
	if err := db.QueryRowContext(ctx, "SHOW server_version").Scan(&v); err != nil {
		return "", fmt.Errorf("postgres: read server version: %w", err)
	}
	return "PostgreSQL " + v, nil
}
