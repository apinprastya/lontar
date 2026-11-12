package services

import (
	"context"
	"errors"
	"time"

	"dbedit/internal/conn"
	"dbedit/internal/driver"
	"dbedit/internal/sshtunnel"
	"dbedit/internal/store"
)

// ConnectionStore is the subset of *store.Store used by ConnectionService.
type ConnectionStore interface {
	List() ([]store.Connection, error)
	Get(id string) (store.Connection, error)
	SaveSecrets(c store.Connection, sec store.Secrets) (store.Connection, error)
	Delete(id string) error
	Duplicate(id string) (store.Connection, error)
	Password(id string) (string, error)
	SSHSecrets(id string) (password, passphrase string, err error)
}

// HostKeyAccepter records an SSH host key the user accepted
// (*sshtunnel.HostKeys).
type HostKeyAccepter interface {
	Accept(host, fingerprint string) error
}

// SessionManager is the subset of *conn.Manager used by ConnectionService.
type SessionManager interface {
	Connect(ctx context.Context, connectionID string) (string, error)
	Disconnect(sessionID string) error
	Get(sessionID string) (*conn.Session, error)
	Test(ctx context.Context, c store.Connection, sec conn.Secrets) (string, error)
	DisconnectAll() error
}

// DriverLister describes the available drivers (*driver.Registry).
type DriverLister interface {
	List() []driver.Info
}

// SessionInfo describes an open session to the frontend.
type SessionInfo struct {
	ID            string      `json:"id"`
	ConnectionID  string      `json:"connectionId"`
	Name          string      `json:"name"`
	Kind          driver.Kind `json:"kind"`
	ServerVersion string      `json:"serverVersion"`
	ConnectedAt   time.Time   `json:"connectedAt"`
	// SafeMode is the session's safe mode (fixed until reconnect).
	SafeMode store.SafeMode `json:"safeMode"`
	// Tunneled reports a connection through an SSH tunnel.
	Tunneled bool `json:"tunneled"`
}

// ConnectionService exposes saved connections and sessions to the frontend.
//
// Passwords flow only from the frontend to Go (Save, Test); they are never
// returned. Connections carry HasPassword instead.
type ConnectionService struct {
	store    ConnectionStore
	sessions SessionManager
	drivers  DriverLister

	// HostKeys records accepted SSH host keys (AcceptHostKey).
	HostKeys HostKeyAccepter
}

// NewConnectionService returns a ConnectionService.
func NewConnectionService(st ConnectionStore, sessions SessionManager, drivers DriverLister) *ConnectionService {
	return &ConnectionService{store: st, sessions: sessions, drivers: drivers}
}

// Drivers lists the supported database types with their default ports.
func (s *ConnectionService) Drivers() []driver.Info {
	return s.drivers.List()
}

// List returns all saved connections sorted by name.
func (s *ConnectionService) List() ([]store.Connection, error) {
	return s.store.List()
}

// Get returns one saved connection.
func (s *ConnectionService) Get(id string) (store.Connection, error) {
	return s.store.Get(id)
}

// Save creates (empty ID) or updates a connection. For each secret (the
// database password, the SSH password and the SSH key passphrase): null
// keeps the stored value, "" removes it, anything else replaces it.
func (s *ConnectionService) Save(c store.Connection, secrets store.Secrets) (store.Connection, error) {
	return s.store.SaveSecrets(c, secrets)
}

// Delete removes a connection and its stored password.
func (s *ConnectionService) Delete(id string) error {
	return s.store.Delete(id)
}

// Duplicate copies a connection (and its password) under a new name.
func (s *ConnectionService) Duplicate(id string) (store.Connection, error) {
	return s.store.Duplicate(id)
}

// Test connects with the given settings (through the SSH tunnel and with
// the TLS settings) without saving them and returns the server version. A
// null secret on an existing connection (non-empty ID) uses the stored
// one. An unknown or changed SSH host key is returned as a structured
// error (see hostKeyError).
func (s *ConnectionService) Test(ctx context.Context, c store.Connection, secrets store.Secrets) (string, error) {
	var sec conn.Secrets
	var err error
	sec.Password = orStored(secrets.Password, "")
	if secrets.Password == nil && c.ID != "" {
		if sec.Password, err = s.store.Password(c.ID); err != nil {
			return "", err
		}
	}
	if c.SSH != nil && c.SSH.Enabled {
		var storedPw, storedPhrase string
		if c.ID != "" && (secrets.SSHPassword == nil || secrets.SSHPassphrase == nil) {
			if storedPw, storedPhrase, err = s.store.SSHSecrets(c.ID); err != nil {
				return "", err
			}
		}
		sec.SSHPassword = orStored(secrets.SSHPassword, storedPw)
		sec.SSHPassphrase = orStored(secrets.SSHPassphrase, storedPhrase)
	}
	v, err := s.sessions.Test(ctx, c, sec)
	return v, hostKeyError(err)
}

func orStored(v *string, stored string) string {
	if v != nil {
		return *v
	}
	return stored
}

// hostKeyError returns the structured SSH host key error inside err (so
// that Wails sends its fields to the frontend as the error's cause), or
// err itself.
func hostKeyError(err error) error {
	var unknown *sshtunnel.HostKeyUnknownError
	var changed *sshtunnel.HostKeyChangedError
	switch {
	case errors.As(err, &unknown):
		return unknown
	case errors.As(err, &changed):
		return changed
	}
	return err
}

// AcceptHostKey trusts the SSH host key with the given SHA256 fingerprint
// that host presented in the last refused attempt (see
// sshtunnel.HostKeyUnknownError). It is appended to dbedit's own
// known_hosts file; the user's ~/.ssh/known_hosts is never written. A
// changed key cannot be accepted here.
func (s *ConnectionService) AcceptHostKey(host, fingerprint string) error {
	if s.HostKeys == nil {
		return conn.ErrNoTunnels
	}
	return s.HostKeys.Accept(host, fingerprint)
}

// Connect opens a session for a saved connection.
func (s *ConnectionService) Connect(ctx context.Context, connectionID string) (SessionInfo, error) {
	id, err := s.sessions.Connect(ctx, connectionID)
	if err != nil {
		return SessionInfo{}, hostKeyError(err)
	}
	sess, err := s.sessions.Get(id)
	if err != nil {
		return SessionInfo{}, err
	}
	return SessionInfo{
		ID:            sess.ID,
		ConnectionID:  sess.ConnectionID,
		Name:          sess.Name,
		Kind:          sess.Kind,
		ServerVersion: sess.ServerVersion,
		ConnectedAt:   sess.ConnectedAt,
		SafeMode:      sess.SafeMode,
		Tunneled:      sess.Tunneled,
	}, nil
}

// Disconnect closes a session.
func (s *ConnectionService) Disconnect(sessionID string) error {
	return s.sessions.Disconnect(sessionID)
}

// ServiceShutdown is called by Wails when the application quits; it closes
// every open session.
func (s *ConnectionService) ServiceShutdown() error {
	return s.sessions.DisconnectAll()
}
