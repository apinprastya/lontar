// Package conn manages open database sessions. A session is one opened
// *sql.DB for a saved connection; the frontend refers to it by session ID.
package conn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"lontar/internal/driver"
	"lontar/internal/sshtunnel"
	"lontar/internal/store"

	"github.com/google/uuid"
)

// Pool limits applied to every session. Lontar is an interactive client:
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
	// ErrUnsupported is returned for operations a kind does not support
	// (switching databases on SQLite).
	ErrUnsupported = errors.New("not supported")
	// ErrNoTunnels is returned when a connection uses SSH but the manager
	// has no tunnel opener.
	ErrNoTunnels = errors.New("SSH tunnels are not available")
	// ErrUnknownDatabase is returned by UseDatabase for a database that is
	// not in the server's database list.
	ErrUnknownDatabase = errors.New("database not found")
)

// Source provides saved connections and their secrets (store.Store).
type Source interface {
	Get(id string) (store.Connection, error)
	Password(id string) (string, error)
	SSHSecrets(id string) (password, passphrase string, err error)
	// JumpSecrets returns the jump host secrets by hop ID.
	JumpSecrets(id string) (map[string]store.HopSecret, error)
}

// Secrets are the secrets used to open a connection.
type Secrets struct {
	Password      string
	SSHPassword   string
	SSHPassphrase string
	// Jumps are the jump host secrets, in the order of the connection's
	// SSH.Jumps (missing entries mean none).
	Jumps []store.HopSecret
}

// Tunnel is an open SSH tunnel (*sshtunnel.Tunnel).
type Tunnel interface {
	Dial(ctx context.Context, network, addr string) (net.Conn, error)
	Close() error
}

// TunnelOpener opens an SSH tunnel.
type TunnelOpener func(ctx context.Context, cfg sshtunnel.Config) (Tunnel, error)

// SSHTunnels adapts an *sshtunnel.Dialer to a TunnelOpener.
func SSHTunnels(d *sshtunnel.Dialer) TunnelOpener {
	return func(ctx context.Context, cfg sshtunnel.Config) (Tunnel, error) {
		t, err := d.Open(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return t, nil
	}
}

// Session is an open connection. Its fields are immutable; DB is safe for
// concurrent use. UseDatabase replaces the session (same ID) with a new
// value, so callers should Get it again rather than keep a pointer.
type Session struct {
	ID            string
	ConnectionID  string
	Name          string
	Kind          driver.Kind
	ServerVersion string
	ConnectedAt   time.Time
	DB            *sql.DB
	// Database is the database the pool is connected to (as reported by
	// the server; "" on MySQL when none is selected).
	Database string
	// SafeMode is the connection's safe mode when it was opened (a change
	// to the saved connection applies from the next connect). In
	// SafeReadOnly the pool itself is read-only (driver.Config.ReadOnly).
	SafeMode store.SafeMode
	// Tunneled reports a connection through an SSH tunnel.
	Tunneled bool

	// tunnel is the session's SSH tunnel (nil without SSH). It lives as
	// long as the session: UseDatabase reuses it, Disconnect closes it.
	tunnel Tunnel
	// suspended is set on the copy SuspendIdle stores: its pool (and
	// tunnel) are closed and the next Get reopens them.
	suspended bool
	// resume serialises reopening the session; copies share it.
	resume *sync.Mutex
}

// close closes the pool, then the tunnel. Closing a suspended session's
// already closed pool again is a no-op (sql.DB.Close is idempotent) and its
// tunnel is nil.
func (s *Session) close() error {
	err := s.DB.Close()
	if s.tunnel != nil {
		if terr := s.tunnel.Close(); err == nil {
			err = terr
		}
	}
	return err
}

// Manager owns all open sessions. It is safe for concurrent use.
type Manager struct {
	src     Source
	drivers *driver.Registry

	// PingTimeout bounds Connect and Test. Zero means DefaultPingTimeout.
	PingTimeout time.Duration
	// Tunnels opens SSH tunnels; nil refuses SSH connections.
	Tunnels TunnelOpener
	// HomeDir expands "~" in certificate paths (default os.UserHomeDir).
	HomeDir func() (string, error)

	mu        sync.Mutex
	sessions  map[string]*Session
	used      map[string]time.Time // last Get per session ID
	closed    bool
	listeners []func(Event)
	newID     func() string
	now       func() time.Time
}

// NewManager returns a Manager that loads connections from src and opens
// them with drivers.
func NewManager(src Source, drivers *driver.Registry) *Manager {
	return &Manager{
		src:      src,
		drivers:  drivers,
		sessions: map[string]*Session{},
		used:     map[string]time.Time{},
		newID:    uuid.NewString,
		now:      time.Now,
	}
}

// EventKind says what happened to a session.
type EventKind int

// Session events.
const (
	// SessionClosed: the session was disconnected (its pool is about to
	// be closed).
	SessionClosed EventKind = iota + 1
	// DatabaseChanged: UseDatabase replaced the session's pool.
	DatabaseChanged
	// SessionSuspended: SuspendIdle is about to close the session's pool
	// (and tunnel) because it was idle; the session stays open.
	SessionSuspended
	// SessionResumed: Get reopened a suspended session's pool.
	SessionResumed
)

// Event is delivered to Subscribe listeners.
type Event struct {
	SessionID string
	Kind      EventKind
	// DB is the pool being closed (SessionSuspended only).
	DB *sql.DB
}

// Subscribe registers fn to be called (synchronously, without the
// manager's lock) for every session event. Listeners use it to release
// resources tied to a session, such as SQL editor connections.
func (m *Manager) Subscribe(fn func(Event)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listeners = append(m.listeners, fn)
}

func (m *Manager) emit(ev Event) {
	m.mu.Lock()
	ls := append([]func(Event){}, m.listeners...)
	m.mu.Unlock()
	for _, fn := range ls {
		fn(ev)
	}
}

func (m *Manager) pingTimeout() time.Duration {
	if m.PingTimeout > 0 {
		return m.PingTimeout
	}
	return DefaultPingTimeout
}

// opened is the result of openDB.
type opened struct {
	db      *sql.DB
	version string
	current string
	tunnel  Tunnel
}

// openDB validates c, opens it (through tun, or a new SSH tunnel when c
// uses SSH and tun is nil), pings it and reads the server version and
// current database. The returned DB is configured with the pool limits. A
// tunnel opened here is closed again on failure.
func (m *Manager) openDB(ctx context.Context, c store.Connection, sec Secrets, tun Tunnel) (_ *opened, err error) {
	c.Normalize()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	d, err := m.drivers.Get(c.Kind)
	if err != nil {
		return nil, err
	}
	timeout := m.pingTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cfg := c.DriverConfig()
	cfg.ConnectTimeout = timeout
	if cfg.TLS != nil {
		if err := m.expandTLS(cfg.TLS); err != nil {
			return nil, err
		}
	}
	switch {
	case tun != nil:
		cfg.Dial = tun.Dial
	case c.SSHEnabled():
		if m.Tunnels == nil {
			return nil, ErrNoTunnels
		}
		tun, err = m.Tunnels(ctx, sshConfig(c, sec))
		if err != nil {
			return nil, err
		}
		opened := tun
		defer func() {
			if err != nil {
				_ = opened.Close()
			}
		}()
		cfg.Dial = tun.Dial
	}
	db, err := d.Open(ctx, cfg, sec.Password)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(MaxOpenConns)
	db.SetMaxIdleConns(MaxIdleConns)
	db.SetConnMaxIdleTime(ConnMaxIdleTime)
	db.SetConnMaxLifetime(ConnMaxLifetime)

	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, friendly(err, timeout)
	}
	version, err := d.ServerVersion(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, friendly(err, timeout)
	}
	current, cerr := d.CurrentDatabase(ctx, db)
	if cerr != nil {
		current = c.Database
	}
	return &opened{db: db, version: version, current: current, tunnel: tun}, nil
}

// sshConfig builds the tunnel settings of c, with its jump hosts.
func sshConfig(c store.Connection, sec Secrets) sshtunnel.Config {
	cfg := sshtunnel.Config{
		Host:       c.SSH.Host,
		Port:       c.SSH.Port,
		User:       c.SSH.User,
		Auth:       sshtunnel.AuthMethod(c.SSH.AuthMethod),
		KeyPath:    c.SSH.KeyPath,
		Password:   sec.SSHPassword,
		Passphrase: sec.SSHPassphrase,
	}
	for i, h := range c.SSH.Jumps {
		var hs store.HopSecret
		if i < len(sec.Jumps) {
			hs = sec.Jumps[i]
		}
		cfg.Jumps = append(cfg.Jumps, sshtunnel.Config{
			Host:       h.Host,
			Port:       h.Port,
			User:       h.User,
			Auth:       sshtunnel.AuthMethod(h.AuthMethod),
			KeyPath:    h.KeyPath,
			Password:   hs.Password,
			Passphrase: hs.Passphrase,
		})
	}
	return cfg
}

// expandTLS expands "~" in the certificate paths.
func (m *Manager) expandTLS(t *driver.TLSConfig) error {
	home := m.HomeDir
	if home == nil {
		home = os.UserHomeDir
	}
	for _, p := range []*string{&t.CAFile, &t.CertFile, &t.KeyFile} {
		v, err := sshtunnel.ExpandHome(*p, home)
		if err != nil {
			return err
		}
		*p = v
	}
	return nil
}

// friendly turns timeouts into a readable message; other driver errors are
// already descriptive ("password authentication failed for user ...").
func friendly(err error, timeout time.Duration) error {
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "i/o timeout") {
		return fmt.Errorf("connection timed out after %s: %w", timeout, err)
	}
	return err
}

// Test opens c with the given secrets (through its SSH tunnel, with its
// TLS settings), pings it and returns the server version. The connection
// and tunnel are closed afterwards and nothing is stored.
func (m *Manager) Test(ctx context.Context, c store.Connection, sec Secrets) (string, error) {
	o, err := m.openDB(ctx, c, sec, nil)
	if err != nil {
		return "", err
	}
	s := &Session{DB: o.db, tunnel: o.tunnel}
	_ = s.close()
	return o.version, nil
}

// secrets loads the stored secrets of a connection.
func (m *Manager) secrets(c store.Connection) (Secrets, error) {
	var sec Secrets
	var err error
	if sec.Password, err = m.src.Password(c.ID); err != nil {
		return Secrets{}, err
	}
	if c.SSHEnabled() {
		if sec.SSHPassword, sec.SSHPassphrase, err = m.src.SSHSecrets(c.ID); err != nil {
			return Secrets{}, err
		}
		if len(c.SSH.Jumps) > 0 {
			byID, err := m.src.JumpSecrets(c.ID)
			if err != nil {
				return Secrets{}, err
			}
			for _, h := range c.SSH.Jumps {
				sec.Jumps = append(sec.Jumps, byID[h.ID])
			}
		}
	}
	return sec, nil
}

// Connect opens the saved connection connectionID and returns the new
// session's ID.
func (m *Manager) Connect(ctx context.Context, connectionID string) (string, error) {
	return m.connect(ctx, connectionID, false)
}

// ConnectReadOnly is Connect with the session forced into
// store.SafeReadOnly, whatever the saved safe mode: the pool is opened
// read-only at the database level (driver.Config.ReadOnly). The MCP server
// runs AI queries on such sessions.
func (m *Manager) ConnectReadOnly(ctx context.Context, connectionID string) (string, error) {
	return m.connect(ctx, connectionID, true)
}

func (m *Manager) connect(ctx context.Context, connectionID string, readOnly bool) (string, error) {
	if m.isClosed() {
		return "", ErrClosed
	}
	c, err := m.src.Get(connectionID)
	if err != nil {
		return "", err
	}
	c.Normalize()
	if readOnly {
		c.SafeMode = store.SafeReadOnly
	}
	sec, err := m.secrets(c)
	if err != nil {
		return "", err
	}
	o, err := m.openDB(ctx, c, sec, nil)
	if err != nil {
		return "", err
	}
	s := &Session{
		SafeMode:      c.SafeMode,
		Database:      o.current,
		ID:            m.newID(),
		ConnectionID:  c.ID,
		Name:          c.Name,
		Kind:          c.Kind,
		ServerVersion: o.version,
		ConnectedAt:   m.now(),
		DB:            o.db,
		Tunneled:      o.tunnel != nil,
		tunnel:        o.tunnel,
		resume:        &sync.Mutex{},
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed { // shut down while we were connecting
		_ = s.close()
		return "", ErrClosed
	}
	m.sessions[s.ID] = s
	m.used[s.ID] = m.now()
	return s.ID, nil
}

func (m *Manager) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// Get returns the session with the given ID and marks it as used. A
// session suspended by SuspendIdle is reopened first (see resume), so the
// returned session's pool is always open.
func (m *Manager) Get(sessionID string) (*Session, error) {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	if !ok {
		m.mu.Unlock()
		return nil, ErrSessionNotFound
	}
	m.used[sessionID] = m.now()
	m.mu.Unlock()
	if s.suspended {
		return m.resume(sessionID)
	}
	return s, nil
}

// Suspended reports whether the session is suspended (idle, pool closed),
// without reopening it.
func (m *Manager) Suspended(sessionID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		return false, ErrSessionNotFound
	}
	return s.suspended, nil
}

// resume reopens a suspended session from the saved connection (current
// SSH and TLS settings and secrets), keeping its ID, safe mode and
// database. Concurrent calls for one session wait for a single reconnect.
// On failure the session stays suspended, so a later Get retries.
func (m *Manager) resume(sessionID string) (*Session, error) {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return nil, ErrSessionNotFound
	}
	s.resume.Lock()
	defer s.resume.Unlock()
	m.mu.Lock()
	s, ok = m.sessions[sessionID] // another call may have resumed it
	m.mu.Unlock()
	switch {
	case !ok:
		return nil, ErrSessionNotFound
	case !s.suspended:
		return s, nil
	}

	c, err := m.src.Get(s.ConnectionID)
	if err != nil {
		return nil, err
	}
	c.Normalize()
	sec, err := m.secrets(c)
	if err != nil {
		return nil, err
	}
	if s.Database != "" {
		c.Database = s.Database
	}
	c.SafeMode = s.SafeMode
	o, err := m.openDB(context.Background(), c, sec, nil)
	if err != nil {
		return nil, err
	}
	next := *s
	next.DB, next.ServerVersion, next.tunnel, next.Tunneled = o.db, o.version, o.tunnel, o.tunnel != nil
	if o.current != "" {
		next.Database = o.current
	}
	next.suspended = false

	m.mu.Lock()
	if cur, ok := m.sessions[sessionID]; !ok || cur != s || m.closed {
		m.mu.Unlock()
		_ = next.close()
		return nil, ErrSessionNotFound
	}
	m.sessions[sessionID] = &next
	m.used[sessionID] = m.now()
	m.mu.Unlock()
	m.emit(Event{SessionID: sessionID, Kind: SessionResumed})
	return &next, nil
}

// SuspendIdle suspends every open session that has not been used (Get)
// for at least idle: its pool and SSH tunnel are closed but the session
// stays, and the next Get reopens it. A session is kept open while its
// pool has connections in use beyond those keep allows for, or when keep
// returns -1 (busy). keep returns how many in-use connections belong to
// idle holders that SessionSuspended listeners release (SQL editors); nil
// means none. idle <= 0 does nothing. It returns the suspended IDs.
func (m *Manager) SuspendIdle(idle time.Duration, keep func(*Session) int) []string {
	if idle <= 0 {
		return nil
	}
	now := m.now()
	m.mu.Lock()
	var cands []*Session
	for id, s := range m.sessions {
		if !s.suspended && now.Sub(m.used[id]) >= idle {
			cands = append(cands, s)
		}
	}
	m.mu.Unlock()

	var out []string
	for _, s := range cands {
		allowed := 0
		if keep != nil {
			if allowed = keep(s); allowed < 0 {
				continue
			}
		}
		if s.DB.Stats().InUse > allowed {
			continue
		}
		next := *s
		next.suspended, next.tunnel = true, nil
		if next.resume == nil {
			next.resume = &sync.Mutex{}
		}
		m.mu.Lock()
		// Skip sessions used, replaced or closed since the scan.
		if cur, ok := m.sessions[s.ID]; !ok || cur != s || m.closed || now.Sub(m.used[s.ID]) < idle {
			m.mu.Unlock()
			continue
		}
		m.sessions[s.ID] = &next
		m.mu.Unlock()
		m.emit(Event{SessionID: s.ID, Kind: SessionSuspended, DB: s.DB})
		_ = s.close()
		out = append(out, s.ID)
	}
	sort.Strings(out)
	return out
}

// StartIdleSweep runs SuspendIdle every interval with the timeout idle()
// returns (re-read each time, so a settings change applies on the next
// tick) until the returned stop function is called.
func (m *Manager) StartIdleSweep(interval time.Duration, idle func() time.Duration, keep func(*Session) int) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				m.SuspendIdle(idle(), keep)
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
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
	delete(m.used, sessionID)
	m.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	m.emit(Event{SessionID: sessionID, Kind: SessionClosed})
	return s.close()
}

// DisconnectAll closes every session and refuses new ones. It is called on
// application shutdown.
func (m *Manager) DisconnectAll() error {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = map[string]*Session{}
	m.used = map[string]time.Time{}
	m.closed = true
	m.mu.Unlock()

	var errs []error
	for _, s := range sessions {
		m.emit(Event{SessionID: s.ID, Kind: SessionClosed})
		if err := s.close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", s.Name, err))
		}
	}
	return errors.Join(errs...)
}

// UseDatabase reconnects the session to another database on the same
// server, keeping its ID. A new pool is opened from the stored connection
// settings and secret with only the database replaced; this is required on
// PostgreSQL (a connection is bound to one database) and is simpler and
// safer than USE on MySQL/MariaDB, where USE would affect only one pooled
// connection. The name must appear in the server's database list. The old
// pool is closed after the swap, so queries still running on it fail.
// SQLite has a single database and returns ErrUnsupported.
func (m *Manager) UseDatabase(ctx context.Context, sessionID, database string) (*Session, error) {
	old, err := m.Get(sessionID)
	if err != nil {
		return nil, err
	}
	if old.Kind.IsFileBased() {
		return nil, fmt.Errorf("switching databases is %w for %s", ErrUnsupported, old.Kind)
	}
	d, err := m.drivers.Get(old.Kind)
	if err != nil {
		return nil, err
	}
	known, err := d.ListDatabases(ctx, old.DB, true)
	if err != nil {
		return nil, err
	}
	if !contains(known, database) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownDatabase, database)
	}
	c, err := m.src.Get(old.ConnectionID)
	if err != nil {
		return nil, err
	}
	c.Normalize()
	sec, err := m.secrets(c)
	if err != nil {
		return nil, err
	}
	c.Database = database
	// Keep the session's safe mode (and so its read-only pool) even if the
	// saved connection changed since it was opened.
	c.SafeMode = old.SafeMode
	// Reuse the session's tunnel, if any. A change to the saved SSH
	// settings applies from the next connect, like safe mode.
	c.SSH = nil
	o, err := m.openDB(ctx, c, sec, old.tunnel)
	if err != nil {
		return nil, err
	}
	next := *old
	next.DB, next.ServerVersion, next.Database = o.db, o.version, o.current

	m.mu.Lock()
	cur, ok := m.sessions[sessionID]
	if !ok || m.closed {
		m.mu.Unlock()
		_ = o.db.Close()
		return nil, ErrSessionNotFound
	}
	m.sessions[sessionID] = &next
	m.mu.Unlock()
	m.emit(Event{SessionID: sessionID, Kind: DatabaseChanged})
	_ = cur.DB.Close()
	return &next, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
