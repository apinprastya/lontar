package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"lontar/internal/conn"
	"lontar/internal/store"
)

// errNotFound is returned for a connection that does not exist or is not
// enabled for AI access (the two are indistinguishable on purpose).
var errNotFound = errors.New("connection not found or not enabled for AI access")

// errNoWrite is returned by execute_write for a read-only connection.
var errNoWrite = errors.New("this connection does not allow AI writes (AI access is read-only, or the connection is in read-only safe mode)")

// poolKey identifies one of the server's sessions: per connection, one for
// reads (opened read-only) and one for writes, per database.
type poolKey struct {
	connectionID string
	write        bool
	database     string
}

// pool is an open session and when it was last used.
type pool struct {
	sessionID string
	// settings fingerprints the saved connection when the session was
	// opened: a later edit (safe mode, host, …) reconnects.
	settings string
	lastUsed time.Time
}

// visible returns the connections enabled for AI access, in saved order.
func (s *Server) visible() ([]store.Connection, error) {
	all, err := s.opts.Connections.List()
	if err != nil {
		return nil, err
	}
	out := make([]store.Connection, 0, len(all))
	for _, c := range all {
		c.Normalize()
		if c.EffectiveAIAccess() != store.AIOff {
			out = append(out, c)
		}
	}
	return out, nil
}

// connection resolves a connection reference: an ID, or a name (case
// insensitive) when it is unique among the visible connections.
func (s *Server) connection(ref string) (store.Connection, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return store.Connection{}, errors.New("connection is required (an id or name from list_connections)")
	}
	conns, err := s.visible()
	if err != nil {
		return store.Connection{}, err
	}
	if i := slices.IndexFunc(conns, func(c store.Connection) bool { return c.ID == ref }); i >= 0 {
		return conns[i], nil
	}
	var match []store.Connection
	for _, c := range conns {
		if strings.EqualFold(c.Name, ref) {
			match = append(match, c)
		}
	}
	switch len(match) {
	case 0:
		return store.Connection{}, fmt.Errorf("%w: %q (call list_connections)", errNotFound, ref)
	case 1:
		return match[0], nil
	}
	return store.Connection{}, fmt.Errorf("%d connections are named %q; use the id from list_connections", len(match), ref)
}

// session returns the server's session for c (read-only unless write) on
// database ("" for the connection's default), opening it when needed.
func (s *Server) session(ctx context.Context, c store.Connection, write bool, database string) (*conn.Session, error) {
	database = strings.TrimSpace(database)
	if c.Kind.IsFileBased() {
		if database != "" && database != "main" {
			return nil, fmt.Errorf("SQLite connections have a single database; omit database")
		}
		database = ""
	}
	key := poolKey{connectionID: c.ID, write: write, database: database}

	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.pools[key]; p != nil {
		if p.settings == fingerprint(c) {
			if sess, err := s.opts.Sessions.Get(p.sessionID); err == nil {
				p.lastUsed = s.now()
				return sess, nil
			}
		}
		// Edited since, or gone: reconnect.
		delete(s.pools, key)
		_ = s.opts.Sessions.Disconnect(p.sessionID)
	}

	connect := s.opts.Sessions.ConnectReadOnly
	if write {
		connect = s.opts.Sessions.Connect
	}
	id, err := connect(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	sess, err := s.opts.Sessions.Get(id)
	if err == nil && database != "" && database != sess.Database {
		sess, err = s.opts.Sessions.UseDatabase(ctx, id, database)
	}
	if err != nil {
		_ = s.opts.Sessions.Disconnect(id)
		return nil, err
	}
	s.pools[key] = &pool{sessionID: id, settings: fingerprint(c), lastUsed: s.now()}
	return sess, nil
}

// fingerprint identifies a connection's saved settings (secrets are not
// part of them; a changed password applies from the next reconnect).
func fingerprint(c store.Connection) string {
	c.CreatedAt, c.UpdatedAt = time.Time{}, time.Time{}
	data, _ := json.Marshal(c)
	return string(data)
}

// sweep closes sessions unused for IdleTimeout.
func (s *Server) sweep() {
	cutoff := s.now().Add(-s.opts.IdleTimeout)
	s.mu.Lock()
	var idle []string
	for k, p := range s.pools {
		if p.lastUsed.Before(cutoff) {
			idle = append(idle, p.sessionID)
			delete(s.pools, k)
		}
	}
	s.mu.Unlock()
	for _, id := range idle {
		_ = s.opts.Sessions.Disconnect(id)
	}
}
