// Package driver defines the database driver abstraction used by Lontar and a
// registry of the available implementations.
//
// Each supported engine lives in its own subpackage (postgres, mysql, sqlite)
// and implements Driver. The subpackages do not register themselves; callers
// build a Registry explicitly (see the all subpackage), which keeps tests free
// of global state.
package driver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

// Kind identifies a database engine.
type Kind string

// Supported database kinds.
const (
	Postgres Kind = "postgres"
	MySQL    Kind = "mysql"
	MariaDB  Kind = "mariadb"
	SQLite   Kind = "sqlite"
)

// Kinds lists every supported kind in display order.
var Kinds = []Kind{Postgres, MySQL, MariaDB, SQLite}

// Valid reports whether k is a supported kind.
func (k Kind) Valid() bool {
	for _, v := range Kinds {
		if k == v {
			return true
		}
	}
	return false
}

// IsFileBased reports whether the kind opens a local file instead of
// connecting to a server.
func (k Kind) IsFileBased() bool { return k == SQLite }

// Config holds everything a driver needs to connect, except the password,
// which is passed separately so that it never ends up in a stored struct.
type Config struct {
	Kind     Kind
	Host     string
	Port     int // 0 means the driver's default port
	User     string
	Database string

	// FilePath is the database file for file-based kinds (SQLite).
	FilePath string
	// CreateIfMissing allows a file-based database to be created on open.
	CreateIfMissing bool
	// ReadOnly opens the database read-only, enforced by the server or
	// engine: SQLite mode=ro; PostgreSQL default_transaction_read_only=on;
	// MySQL transaction_read_only=1; MariaDB tx_read_only=1 (session
	// variables set on every new connection). Set by the connection's
	// read-only flag (SQLite) or its "readonly" safe mode.
	ReadOnly bool

	// Options are extra driver parameters (e.g. "sslmode" for PostgreSQL,
	// "charset" for MySQL, "_pragma" for SQLite). Keys that would override
	// the fields above or carry credentials are rejected by BuildDSN.
	Options map[string]string

	// ConnectTimeout bounds establishing a single connection. Zero means
	// DefaultConnectTimeout.
	ConnectTimeout time.Duration

	// TLS holds the connection's SSL/TLS settings. nil (or an empty Mode)
	// leaves TLS to the driver options ("sslmode" for PostgreSQL, "tls"
	// for MySQL/MariaDB) and the driver defaults. A non-empty Mode wins
	// over those options. Ignored for file-based kinds.
	TLS *TLSConfig

	// Dial, when set, opens every network connection of the pool instead
	// of a direct TCP dial (the SSH tunnel's in-process dialer). Host and
	// port are passed to it unresolved, so name resolution happens on the
	// far side of the tunnel.
	Dial DialFunc
}

// DialFunc opens a network connection to addr ("host:port").
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// DefaultConnectTimeout is used when Config.ConnectTimeout is zero.
const DefaultConnectTimeout = 10 * time.Second

// Timeout returns the effective connect timeout.
func (c Config) Timeout() time.Duration {
	if c.ConnectTimeout > 0 {
		return c.ConnectTimeout
	}
	return DefaultConnectTimeout
}

// PortOr returns the configured port or def when none is set.
func (c Config) PortOr(def int) int {
	if c.Port > 0 {
		return c.Port
	}
	return def
}

// Driver is implemented by each supported database engine.
//
// Besides opening connections, a driver is its engine's Dialect (pure SQL
// helpers) and Introspector (metadata queries). The engine packages embed
// one of the Dialect types from dialect.go.
type Driver interface {
	Dialect
	Introspector

	// Kind returns the kind this driver handles.
	Kind() Kind
	// DisplayName is a human readable engine name ("PostgreSQL").
	DisplayName() string
	// DefaultPort is the engine's default TCP port (0 for file-based kinds).
	DefaultPort() int
	// BuildDSN returns the data source name for cfg. Special characters in
	// password and other fields are escaped.
	BuildDSN(cfg Config, password string) (string, error)
	// Open returns a *sql.DB for cfg. It does not necessarily connect; use
	// PingContext to verify the connection.
	Open(ctx context.Context, cfg Config, password string) (*sql.DB, error)
	// ServerVersion returns a human readable server version, such as
	// "PostgreSQL 16.4" or "MariaDB 11.4.2".
	ServerVersion(ctx context.Context, db *sql.DB) (string, error)
}

// ErrUnknownKind is returned for kinds without a registered driver.
var ErrUnknownKind = errors.New("unknown database kind")

// ErrReservedOption is returned when Options contains a key that must be set
// through a dedicated field (host, user, password, ...).
var ErrReservedOption = errors.New("option is not allowed")

// CheckOptions returns ErrReservedOption if opts contains any reserved key.
func CheckOptions(opts map[string]string, reserved ...string) error {
	for _, r := range reserved {
		if _, ok := opts[r]; ok {
			return fmt.Errorf("%w: %q must be set with its own field", ErrReservedOption, r)
		}
	}
	return nil
}

// Info describes a driver for display purposes.
type Info struct {
	Kind        Kind   `json:"kind"`
	DisplayName string `json:"displayName"`
	DefaultPort int    `json:"defaultPort"`
	FileBased   bool   `json:"fileBased"`
}

// Registry maps kinds to drivers. It is safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	drivers map[Kind]Driver
}

// NewRegistry returns a registry containing drivers.
func NewRegistry(drivers ...Driver) *Registry {
	r := &Registry{drivers: map[Kind]Driver{}}
	for _, d := range drivers {
		r.Register(d)
	}
	return r
}

// Register adds or replaces the driver for d.Kind().
func (r *Registry) Register(d Driver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.drivers[d.Kind()] = d
}

// Get returns the driver for kind.
func (r *Registry) Get(kind Kind) (Driver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.drivers[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}
	return d, nil
}

// List describes all registered drivers in the order of Kinds, followed by
// any other kinds sorted by name.
func (r *Registry) List() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	order := map[Kind]int{}
	for i, k := range Kinds {
		order[k] = i
	}
	out := make([]Info, 0, len(r.drivers))
	for _, d := range r.drivers {
		out = append(out, Info{Kind: d.Kind(), DisplayName: d.DisplayName(), DefaultPort: d.DefaultPort(), FileBased: d.Kind().IsFileBased()})
	}
	sort.Slice(out, func(i, j int) bool {
		oi, iok := order[out[i].Kind]
		oj, jok := order[out[j].Kind]
		switch {
		case iok && jok:
			return oi < oj
		case iok != jok:
			return iok
		default:
			return out[i].Kind < out[j].Kind
		}
	})
	return out
}
