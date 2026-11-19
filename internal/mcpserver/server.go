// Package mcpserver is Lontar's Model Context Protocol server (`lontar
// mcp`). It lets an AI assistant such as Claude list the saved connections
// the user opted in (store.Connection.AIAccess), read their schema, run
// read-only queries and, where allowed, change data after the user
// approves each change in the MCP client.
//
// # Safety model
//
//   - A connection is invisible unless its AIAccess is "read" or "write"
//     (store.Connection.EffectiveAIAccess, read again on every call).
//   - Reads run on a session opened read-only at the database level
//     (conn.Manager.ConnectReadOnly), inside a READ ONLY transaction that is
//     always rolled back, after a statement check that refuses writes,
//     transaction control, USE and a list of side-effect functions.
//   - Writes (execute_write) are limited to INSERT/UPDATE/DELETE/MERGE/
//     REPLACE, need AIAccess "write" (capped to "read" by a read-only safe
//     mode), are shown to the user through MCP elicitation and run only
//     when approved, in one transaction. Clients without elicitation cannot
//     write.
//   - Secrets and connection details (host, user, SSH) are never returned.
//   - Every executed statement is recorded in the query history with
//     source "mcp".
package mcpserver

import (
	"context"
	"crypto/rand"
	"database/sql"
	"log"
	"sync"
	"time"

	"lontar/internal/conn"
	"lontar/internal/driver"
	"lontar/internal/history"
	"lontar/internal/query"
	"lontar/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Limits and defaults.
const (
	// DefaultMaxRows is how many rows run_query returns per statement by
	// default; MaxMaxRows is the most a caller may ask for.
	DefaultMaxRows = 200
	MaxMaxRows     = 1000
	// MaxOutputBytes bounds the text of one tool result (rows beyond it
	// are dropped and the result says so).
	MaxOutputBytes = 64 << 10
	// MaxCellBytes bounds one cell's text.
	MaxCellBytes = 2000
	// MaxWriteSQLBytes bounds execute_write's SQL: the user must be able
	// to read what they approve.
	MaxWriteSQLBytes = 8 << 10
	// DefaultQueryTimeout bounds one tool call's database work.
	DefaultQueryTimeout = 60 * time.Second
	// DefaultIdleTimeout closes sessions unused for this long.
	DefaultIdleTimeout = 10 * time.Minute
)

// Connections provides the saved connections (*store.Store). It is read
// on every call, so changes made in the app apply immediately.
type Connections interface {
	List() ([]store.Connection, error)
}

// Sessions opens and closes database sessions (*conn.Manager).
type Sessions interface {
	Connect(ctx context.Context, connectionID string) (string, error)
	ConnectReadOnly(ctx context.Context, connectionID string) (string, error)
	Get(sessionID string) (*conn.Session, error)
	UseDatabase(ctx context.Context, sessionID, database string) (*conn.Session, error)
	Disconnect(sessionID string) error
}

// Drivers returns the driver of a kind (*driver.Registry).
type Drivers interface {
	Get(kind driver.Kind) (driver.Driver, error)
}

// History records executed statements (*history.Store).
type History interface {
	Add(ctx context.Context, e history.Entry) (int64, error)
}

// Options configures a Server.
type Options struct {
	Connections Connections
	Sessions    Sessions
	Drivers     Drivers
	// History is optional.
	History History
	// Version is reported to the client.
	Version string
	// QueryTimeout bounds the database work of one call (default
	// DefaultQueryTimeout). The user's approval of a write does not count.
	QueryTimeout time.Duration
	// IdleTimeout closes sessions unused for this long (default
	// DefaultIdleTimeout).
	IdleTimeout time.Duration
	// Logf logs diagnostics (default log.Printf). It must never write to
	// stdout, which carries the protocol.
	Logf func(format string, args ...any)
}

// Server is the MCP server. Create it with New.
type Server struct {
	opts    Options
	editors *query.Editors
	now     func() time.Time
	// key signs write approvals (see approvalState).
	key []byte

	// mu guards pools; it is held while connecting so that concurrent
	// calls share one session per key.
	mu    sync.Mutex
	pools map[poolKey]*pool
}

// New returns a server for opts.
func New(opts Options) *Server {
	if opts.QueryTimeout <= 0 {
		opts.QueryTimeout = DefaultQueryTimeout
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = DefaultIdleTimeout
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	s := &Server{opts: opts, now: time.Now, key: key, pools: map[poolKey]*pool{}}
	s.editors = query.NewEditors(s.resolveEditor, nil)
	return s
}

// resolveEditor lets the query runtime (used by explain_query) find a
// session's pool.
func (s *Server) resolveEditor(sessionID string) (*sql.DB, driver.Kind, error) {
	sess, err := s.opts.Sessions.Get(sessionID)
	if err != nil {
		return nil, "", err
	}
	return sess.DB, sess.Kind, nil
}

// instructions are sent to the client when it connects.
const instructions = `Lontar gives you access to the user's saved database connections (PostgreSQL, MySQL/MariaDB, SQLite) that they enabled for AI access.

Start with list_connections, then explore with list_tables and describe_table (or read the lontar://{connection}/schema resource) before writing queries. run_query is read-only and returns at most max_rows rows per statement as CSV (NULL is written as NULL); aggregate or add LIMIT/WHERE instead of fetching large tables. Use the engine's own SQL dialect.

execute_write exists only for connections with write access: it runs INSERT/UPDATE/DELETE/MERGE statements in one transaction after the user approves them, and never runs DDL. Prefer showing the user what you intend to change first; connections tagged prod deserve extra care.`

// MCP returns a new *mcp.Server with Lontar's tools and resources.
func (s *Server) MCP() *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "lontar", Title: "Lontar", Version: s.opts.Version}, &mcp.ServerOptions{
		Instructions: instructions,
	})
	s.addTools(srv)
	s.addResources(srv)
	return srv
}

// Run serves one client on t until it disconnects or ctx ends, then closes
// every session.
func (s *Server) Run(ctx context.Context, t mcp.Transport) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer s.Close()
	go s.sweepLoop(ctx, time.Minute)
	return s.MCP().Run(ctx, t)
}

// sweepLoop closes idle sessions every interval until ctx ends.
func (s *Server) sweepLoop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweep()
		}
	}
}

// Close closes every session the server opened.
func (s *Server) Close() {
	s.editors.CloseAll()
	s.mu.Lock()
	pools := s.pools
	s.pools = map[poolKey]*pool{}
	s.mu.Unlock()
	for _, p := range pools {
		_ = s.opts.Sessions.Disconnect(p.sessionID)
	}
}
