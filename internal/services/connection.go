package services

import (
	"context"
	"time"

	"dbedit/internal/conn"
	"dbedit/internal/driver"
	"dbedit/internal/store"
)

// ConnectionStore is the subset of *store.Store used by ConnectionService.
type ConnectionStore interface {
	List() ([]store.Connection, error)
	Get(id string) (store.Connection, error)
	Save(c store.Connection, password *string) (store.Connection, error)
	Delete(id string) error
	Duplicate(id string) (store.Connection, error)
	Password(id string) (string, error)
}

// SessionManager is the subset of *conn.Manager used by ConnectionService.
type SessionManager interface {
	Connect(ctx context.Context, connectionID string) (string, error)
	Disconnect(sessionID string) error
	Get(sessionID string) (*conn.Session, error)
	Test(ctx context.Context, c store.Connection, password string) (string, error)
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
}

// ConnectionService exposes saved connections and sessions to the frontend.
//
// Passwords flow only from the frontend to Go (Save, Test); they are never
// returned. Connections carry HasPassword instead.
type ConnectionService struct {
	store    ConnectionStore
	sessions SessionManager
	drivers  DriverLister
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

// Save creates (empty ID) or updates a connection. password: null keeps the
// stored password, "" removes it, anything else replaces it.
func (s *ConnectionService) Save(c store.Connection, password *string) (store.Connection, error) {
	return s.store.Save(c, password)
}

// Delete removes a connection and its stored password.
func (s *ConnectionService) Delete(id string) error {
	return s.store.Delete(id)
}

// Duplicate copies a connection (and its password) under a new name.
func (s *ConnectionService) Duplicate(id string) (store.Connection, error) {
	return s.store.Duplicate(id)
}

// Test connects with the given settings without saving them and returns
// the server version. A null password on an existing connection (non-empty
// ID) uses the stored password.
func (s *ConnectionService) Test(ctx context.Context, c store.Connection, password *string) (string, error) {
	pw := ""
	switch {
	case password != nil:
		pw = *password
	case c.ID != "":
		stored, err := s.store.Password(c.ID)
		if err != nil {
			return "", err
		}
		pw = stored
	}
	return s.sessions.Test(ctx, c, pw)
}

// Connect opens a session for a saved connection.
func (s *ConnectionService) Connect(ctx context.Context, connectionID string) (SessionInfo, error) {
	id, err := s.sessions.Connect(ctx, connectionID)
	if err != nil {
		return SessionInfo{}, err
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
