// Package mysql implements the MySQL and MariaDB drivers using
// github.com/go-sql-driver/mysql. Both kinds share one implementation; the
// actual server flavour is detected from its version string, so a
// connection saved as "mysql" that points at a MariaDB server still reports
// MariaDB.
package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"

	"dbedit/internal/driver"

	gomysql "github.com/go-sql-driver/mysql"
)

// DefaultPort is the default TCP port of MySQL and MariaDB.
const DefaultPort = 3306

// reserved option keys: set through Config fields or credentials.
var reserved = []string{"user", "passwd", "password", "net", "addr", "dbname"}

// Driver handles MySQL or MariaDB, depending on kind.
type Driver struct {
	kind driver.Kind
}

// New returns the MySQL driver.
func New() *Driver { return &Driver{kind: driver.MySQL} }

// NewMariaDB returns the MariaDB driver.
func NewMariaDB() *Driver { return &Driver{kind: driver.MariaDB} }

// Kind implements driver.Driver.
func (d *Driver) Kind() driver.Kind { return d.kind }

// DisplayName implements driver.Driver.
func (d *Driver) DisplayName() string {
	if d.kind == driver.MariaDB {
		return "MariaDB"
	}
	return "MySQL"
}

// DefaultPort implements driver.Driver.
func (*Driver) DefaultPort() int { return DefaultPort }

// Config builds the go-sql-driver configuration for cfg.
func (d *Driver) Config(cfg driver.Config, password string) (*gomysql.Config, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("%s: host is required", d.kind)
	}
	if err := driver.CheckOptions(cfg.Options, reserved...); err != nil {
		return nil, err
	}
	mc := gomysql.NewConfig()
	mc.User = cfg.User
	mc.Passwd = password
	mc.Net = "tcp"
	mc.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.PortOr(DefaultPort)))
	mc.DBName = cfg.Database
	mc.ParseTime = true
	mc.Timeout = cfg.Timeout()
	if len(cfg.Options) == 0 {
		return mc, nil
	}
	// Route options through the driver's own DSN parser so known keys
	// (charset, tls, readTimeout, ...) are validated and applied exactly as
	// in a DSN; unknown keys become session variables. FormatDSN
	// query-escapes values and ParseDSN unescapes them.
	mc.Params = make(map[string]string, len(cfg.Options))
	for k, v := range cfg.Options {
		mc.Params[k] = v
	}
	parsed, err := gomysql.ParseDSN(mc.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("%s: invalid options: %w", d.kind, err)
	}
	// Never rely on the DSN round trip for the identity fields: keep them
	// exactly as given, whatever characters they contain.
	parsed.User, parsed.Passwd, parsed.DBName, parsed.Addr = mc.User, mc.Passwd, mc.DBName, mc.Addr
	return parsed, nil
}

// BuildDSN implements driver.Driver using mysql.Config.FormatDSN, which
// escapes the password and parameter values.
func (d *Driver) BuildDSN(cfg driver.Config, password string) (string, error) {
	mc, err := d.Config(cfg, password)
	if err != nil {
		return "", err
	}
	return mc.FormatDSN(), nil
}

// Open implements driver.Driver.
func (d *Driver) Open(_ context.Context, cfg driver.Config, password string) (*sql.DB, error) {
	mc, err := d.Config(cfg, password)
	if err != nil {
		return nil, err
	}
	connector, err := gomysql.NewConnector(mc)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid connection settings: %w", d.kind, err)
	}
	return sql.OpenDB(connector), nil
}

// ServerVersion implements driver.Driver. It reports "MariaDB x.y.z" or
// "MySQL x.y.z" based on the server's version string.
func (d *Driver) ServerVersion(ctx context.Context, db *sql.DB) (string, error) {
	var v string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&v); err != nil {
		return "", fmt.Errorf("%s: read server version: %w", d.kind, err)
	}
	return FormatVersion(v), nil
}

// IsMariaDB reports whether a VERSION() string comes from MariaDB.
func IsMariaDB(version string) bool {
	return strings.Contains(strings.ToLower(version), "mariadb")
}

// FormatVersion turns a raw VERSION() string ("11.4.2-MariaDB-ubu2404",
// "5.5.5-10.6.12-MariaDB", "8.0.36") into "MariaDB 11.4.2" / "MySQL 8.0.36".
func FormatVersion(raw string) string {
	v := raw
	if IsMariaDB(raw) {
		// Old replication-compat prefix used by MariaDB < 11.
		v = strings.TrimPrefix(v, "5.5.5-")
		if i := strings.Index(v, "-"); i >= 0 {
			v = v[:i]
		}
		return "MariaDB " + v
	}
	if i := strings.Index(v, "-"); i >= 0 {
		v = v[:i]
	}
	return "MySQL " + v
}
