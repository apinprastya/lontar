// Package sqlite implements the SQLite driver using the pure-Go
// modernc.org/sqlite (no CGO).
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"dbedit/internal/driver"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// driverName is the database/sql name registered by modernc.org/sqlite.
const driverName = "sqlite"

// BusyTimeoutMS is how long a connection waits for a lock before failing.
const BusyTimeoutMS = 5000

// reserved option keys: these are controlled by Config fields.
var reserved = []string{"mode", "vfs"}

// Driver is the SQLite driver.
type Driver struct {
	homeDir func() (string, error)
}

// New returns the SQLite driver.
func New() *Driver { return &Driver{homeDir: os.UserHomeDir} }

// Kind implements driver.Driver.
func (*Driver) Kind() driver.Kind { return driver.SQLite }

// DisplayName implements driver.Driver.
func (*Driver) DisplayName() string { return "SQLite" }

// DefaultPort implements driver.Driver; SQLite has no port.
func (*Driver) DefaultPort() int { return 0 }

// ResolvePath expands a leading "~" and makes p absolute.
func (d *Driver) ResolvePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("sqlite: database file path is required")
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := d.homeDir()
		if err != nil {
			return "", fmt.Errorf("sqlite: expand ~: %w", err)
		}
		p = filepath.Join(home, p[1:])
	}
	return filepath.Abs(p)
}

// BuildDSN returns a "file:" URI so that SQLite applies the mode parameter:
// mode=ro (read-only), mode=rw (must exist) or mode=rwc (create if missing).
// The path is percent-encoded, so "?", "#" and "%" in file names are safe.
// The password is ignored: SQLite has no authentication.
func (d *Driver) BuildDSN(cfg driver.Config, _ string) (string, error) {
	path, err := d.ResolvePath(cfg.FilePath)
	if err != nil {
		return "", err
	}
	if err := driver.CheckOptions(cfg.Options, reserved...); err != nil {
		return "", err
	}
	mode := "rw"
	switch {
	case cfg.ReadOnly:
		mode = "ro"
	case cfg.CreateIfMissing:
		mode = "rwc"
	}
	q := url.Values{}
	q.Set("mode", mode)
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", BusyTimeoutMS))
	q.Add("_pragma", "foreign_keys(1)")
	for k, v := range cfg.Options {
		q.Add(k, v)
	}
	return fileURI(path) + "?" + q.Encode(), nil
}

// fileURI converts an absolute OS path into an SQLite "file:" URI. Windows
// paths become "file:///C:/dir/db.sqlite".
func fileURI(abs string) string {
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// Open implements driver.Driver. Unless CreateIfMissing is set, a missing
// file is reported as an error instead of silently creating an empty
// database.
func (d *Driver) Open(_ context.Context, cfg driver.Config, password string) (*sql.DB, error) {
	dsn, err := d.BuildDSN(cfg, password)
	if err != nil {
		return nil, err
	}
	if !cfg.CreateIfMissing || cfg.ReadOnly {
		path, _ := d.ResolvePath(cfg.FilePath) // already validated by BuildDSN
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("sqlite: database file %s does not exist", path)
		}
	}
	return sql.Open(driverName, dsn)
}

// ServerVersion implements driver.Driver.
func (*Driver) ServerVersion(ctx context.Context, db *sql.DB) (string, error) {
	var v string
	if err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&v); err != nil {
		return "", fmt.Errorf("sqlite: read version: %w", err)
	}
	return "SQLite " + v, nil
}
