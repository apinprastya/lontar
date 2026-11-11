// Package conn manages open database sessions. A session is one opened
// *sql.DB for a saved connection; the frontend refers to it by session ID.
package conn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"dbedit/internal/driver"
	"dbedit/internal/store"

	"github.com/google/uuid"
)

// Pool limits applied to every session. dbedit is an interactive client:
// a few connections are enough for browsing plus a running query, and idle
// connections are released so servers are not held open needlessly.
const (
	MaxOpenConns    = 8
	MaxIdleConns    = 2
	ConnMaxIdleTime = 5 * time.Minute
	ConnMaxLifetime = 30 * time.Minute
)

// DefaultPingTimeout bounds opening + pinging a connection.
const DefaultPingTimeout = 15 * time.Second

// Errors returned by Manager.
var (
	ErrSessionNotFound = errors.New("session not found (it may have been disconnected)")
	ErrClosed          = errors.New("session manager is shut down")
	// ErrUnsupported is returned for settings that are stored but not
	// implemented yet (SSH tunnels, SSL options). Connecting without them
	// would silently bypass what the user configured.
	ErrUnsupported = errors.New("not supported yet")
)

// Source provides saved connections and their passwords (store.Store).
type Source interface {
	Get(id string) (store.Connection, error)
	Password(id string) (string, error)
}

// Session is an open connection. Its fields are immutable; DB is safe for
// concurrent use.
type Session struct {
	ID            string
	ConnectionID  string
	Name          string
	Kind          driver.Kind
	ServerVersion string
	ConnectedAt   time.Time
	DB            *sql.DB
}

// Manager owns all open sessions. It is safe for concurrent use.
type Manager struct {
	src     Source
	drivers *driver.Registry

	// PingTimeout bounds Connect and Test. Zero means DefaultPingTimeout.
	PingTimeout time.Duration

	mu       sync.Mutex
	sessions map[string]*Session
	closed   bool
	newID    func() string
	now      func() time.Time
}

// NewManager returns a Manager that loads connections from src and opens
// them with drivers.
func NewManager(src Source, drivers *driver.Registry) *Manager {
	return &Manager{
		src:      src,
		drivers:  drivers,
		sessions: map[string]*Session{},
		newID:    uuid.NewString,
		now:      time.Now,
	}
}

func (m *Manager) pingTimeout() time.Duration {
	if m.PingTimeout > 0 {
		return m.PingTimeout
	}
	return DefaultPingTimeout
}

// checkSupported rejects settings that later milestones implement.
func checkSupported(c store.Connection) error {
	if c.SSH != nil && c.SSH.Enabled {
		return fmt.Errorf("SSH tunnels are %w", ErrUnsupported)
	}
	if c.SSL != nil && c.SSL.Mode != "" && c.SSL.Mode != "disable" {
		return fmt.Errorf("SSL options are %w; set sslmode/tls in the connection options instead", ErrUnsupported)
	}
	return nil
}

// open validates c, opens it, pings it and reads the server version. The
// returned DB is configured with the pool limits.
func (m *Manager) open(ctx context.Context, c store.Connection, password string) (*sql.DB, string, error) {
	c.Normalize()
	if err := c.Validate(); err != nil {
		return nil, "", err
	}
	if err := checkSupported(c); err != nil {
		return nil, "", err
	}
	d, err := m.drivers.Get(c.Kind)
	if err != nil {
		return nil, "", err
	}
	timeout := m.pingTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cfg := c.DriverConfig()
	cfg.ConnectTimeout = timeout
	db, err := d.Open(ctx, cfg, password)
	if err != nil {
		return nil, "", err
	}
	db.SetMaxOpenConns(MaxOpenConns)
	db.SetMaxIdleConns(MaxIdleConns)
	db.SetConnMaxIdleTime(ConnMaxIdleTime)
	db.SetConnMaxLifetime(ConnMaxLifetime)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, "", friendly(err, timeout)
	}
	version, err := d.ServerVersion(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, "", friendly(err, timeout)
	}
	return db, version, nil
}

// friendly turns timeouts into a readable message; other driver errors are
// already descriptive ("password authentication failed for user ...").
func friendly(err error, timeout time.Duration) error {
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "i/o timeout") {
		return fmt.Errorf("connection timed out after %s: %w", timeout, err)
	}
	return err
}

// Test opens c with password, pings it and returns the server version. The
// connection is closed afterwards and nothing is stored.
func (m *Manager) Test(ctx context.Context, c store.Connection, password string) (string, error) {
	db, version, err := m.open(ctx, c, password)
	if err != nil {
		return "", err
	}
	_ = db.Close()
	return version, nil
}

// Connect opens the saved connection connectionID and returns the new
// session's ID.
func (m *Manager) Connect(ctx context.Context, connectionID string) (string, error) {
	if m.isClosed() {
		return "", ErrClosed
	}
	c, err := m.src.Get(connectionID)
	if err != nil {
		return "", err
	}
	password, err := m.src.Password(connectionID)
	if err != nil {
		return "", err
	}
	db, version, err := m.open(ctx, c, password)
	if err != nil {
		return "", err
	}
	s := &Session{
		ID:            m.newID(),
		ConnectionID:  c.ID,
		Name:          c.Name,
		Kind:          c.Kind,
		ServerVersion: version,
		ConnectedAt:   m.now(),
		DB:            db,
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed { // shut down while we were connecting
		_ = db.Close()
		return "", ErrClosed
	}
	m.sessions[s.ID] = s
	return s.ID, nil
}

func (m *Manager) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// Get returns the session with the given ID.
func (m *Manager) Get(sessionID string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return s, nil
}

// List returns all open sessions ordered by connect time.
func (m *Manager) List() []*Session {
	m.mu.Lock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ConnectedAt.Before(out[j].ConnectedAt) })
	return out
}

// Disconnect closes and forgets the session.
func (m *Manager) Disconnect(sessionID string) error {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	return s.DB.Close()
}

// DisconnectAll closes every session and refuses new ones. It is called on
// application shutdown.
func (m *Manager) DisconnectAll() error {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = map[string]*Session{}
	m.closed = true
	m.mu.Unlock()

	var errs []error
	for _, s := range sessions {
		if err := s.DB.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", s.Name, err))
		}
	}
	return errors.Join(errs...)
}
