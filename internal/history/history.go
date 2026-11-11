// Package history stores the SQL editor's query history in an SQLite
// database (history.db in the app's config directory), using the pure-Go
// modernc driver.
//
// Entries are keyed by the saved connection's ID; connection settings and
// passwords are never stored. Each connection keeps at most Limit entries
// (the newest win).
package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

// FileName is the history database's file name inside the config dir.
const FileName = "history.db"

// DefaultLimit is how many entries are kept per connection.
const DefaultLimit = 10000

// MaxSQLBytes caps the stored SQL text of one entry; longer scripts are
// cut (on a UTF-8 boundary) and flagged.
const MaxSQLBytes = 256 << 10

// ErrNotFound is returned by Delete for an unknown ID.
var ErrNotFound = errors.New("history entry not found")

// Entry is one executed script.
type Entry struct {
	ID           int64     `json:"id"`
	ConnectionID string    `json:"connectionId"`
	Database     string    `json:"database"`
	SQL          string    `json:"sql"`
	StartedAt    time.Time `json:"startedAt"`
	DurationMs   float64   `json:"durationMs"`
	// RowCount is the number of rows returned (nil: no result sets).
	RowCount *int64 `json:"rowCount"`
	// RowsAffected is the sum of rows affected (nil: unknown/none).
	RowsAffected *int64 `json:"rowsAffected"`
	IsError      bool   `json:"isError"`
	Error        string `json:"error"`
	// Truncated reports that SQL was cut to MaxSQLBytes.
	Truncated bool `json:"truncated"`
}

// Store is the history database. It opens lazily on first use and is safe
// for concurrent use.
type Store struct {
	dir string
	// Limit is the number of entries kept per connection; zero means
	// DefaultLimit.
	Limit int

	mu sync.Mutex
	db *sql.DB
}

// New returns a store that keeps its database in dir (created on first
// use with mode 0700).
func New(dir string) *Store { return &Store{dir: dir} }

func (s *Store) limit() int {
	if s.Limit > 0 {
		return s.Limit
	}
	return DefaultLimit
}

const schema = `
CREATE TABLE IF NOT EXISTS history (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	connection_id TEXT    NOT NULL,
	database      TEXT    NOT NULL DEFAULT '',
	sql           TEXT    NOT NULL,
	started_at    INTEGER NOT NULL,
	duration_ms   REAL    NOT NULL DEFAULT 0,
	row_count     INTEGER,
	rows_affected INTEGER,
	is_error      INTEGER NOT NULL DEFAULT 0,
	error         TEXT    NOT NULL DEFAULT '',
	truncated     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS history_conn ON history (connection_id, id DESC);
`

// open returns the database, creating it on first use.
func (s *Store) open() (*sql.DB, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return s.db, nil
	}
	// A failure is not cached: a later call may succeed (e.g. once the
	// directory is writable).
	db, err := s.openDB()
	if err != nil {
		return nil, err
	}
	s.db = db
	return db, nil
}

func (s *Store) openDB() (*sql.DB, error) {
	if s.dir == "" {
		return nil, errors.New("history: no directory configured")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	path := filepath.Join(s.dir, FileName)
	// Create the file private before SQLite opens it: SQLite gives the
	// -wal and -shm files the database file's permissions.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	_ = f.Close()
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") { // Windows: file:///C:/…
		uriPath = "/" + uriPath
	}
	dsn := (&url.URL{Scheme: "file", Path: uriPath}).String()
	db, err := sql.Open("sqlite", dsn+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	// One writer at a time keeps SQLite simple; the history is tiny.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("history: create schema: %w", err)
	}
	return db, nil
}

// Close closes the database (it reopens on the next call).
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	db := s.db
	s.db = nil
	if db == nil {
		return nil
	}
	return db.Close()
}

// cut returns sql limited to MaxSQLBytes on a UTF-8 boundary.
func cut(text string) (string, bool) {
	if len(text) <= MaxSQLBytes {
		return text, false
	}
	n := MaxSQLBytes
	for n > 0 && text[n]&0xC0 == 0x80 {
		n--
	}
	return text[:n], true
}

// Add records e (its ID is ignored) and prunes the connection's oldest
// entries beyond the limit. It returns the new entry's ID.
func (s *Store) Add(ctx context.Context, e Entry) (int64, error) {
	db, err := s.open()
	if err != nil {
		return 0, err
	}
	if e.ConnectionID == "" {
		return 0, errors.New("history: connection ID is required")
	}
	text, truncated := cut(e.SQL)
	if e.StartedAt.IsZero() {
		e.StartedAt = time.Now()
	}
	res, err := db.ExecContext(ctx,
		`INSERT INTO history (connection_id, database, sql, started_at, duration_ms, row_count, rows_affected, is_error, error, truncated)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ConnectionID, e.Database, text, e.StartedAt.UnixMilli(), e.DurationMs, e.RowCount, e.RowsAffected, e.IsError, e.Error, truncated || e.Truncated)
	if err != nil {
		return 0, fmt.Errorf("history: add: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("history: add: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		`DELETE FROM history WHERE connection_id = ? AND id <= (
		   SELECT id FROM history WHERE connection_id = ? ORDER BY id DESC LIMIT 1 OFFSET ?)`,
		e.ConnectionID, e.ConnectionID, s.limit()); err != nil {
		return id, fmt.Errorf("history: prune: %w", err)
	}
	return id, nil
}

// escapeLike escapes LIKE wildcards with '!' (used as the ESCAPE char).
func escapeLike(s string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s)
}

// List returns a connection's entries, newest first. search, when not
// empty, matches SQL text case-insensitively (ASCII) as a substring; it is
// bound as a parameter. An empty connectionID lists every connection.
func (s *Store) List(ctx context.Context, connectionID, search string, limit, offset int) ([]Entry, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	offset = max(offset, 0)
	rows, err := db.QueryContext(ctx,
		`SELECT id, connection_id, database, sql, started_at, duration_ms, row_count, rows_affected, is_error, error, truncated
		 FROM history
		 WHERE (? = '' OR connection_id = ?) AND (? = '' OR sql LIKE ? ESCAPE '!')
		 ORDER BY id DESC LIMIT ? OFFSET ?`,
		connectionID, connectionID, search, "%"+escapeLike(search)+"%", limit, offset)
	if err != nil {
		return nil, fmt.Errorf("history: list: %w", err)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var (
			e         Entry
			startedMs int64
		)
		if err := rows.Scan(&e.ID, &e.ConnectionID, &e.Database, &e.SQL, &startedMs, &e.DurationMs,
			&e.RowCount, &e.RowsAffected, &e.IsError, &e.Error, &e.Truncated); err != nil {
			return nil, fmt.Errorf("history: list: %w", err)
		}
		e.StartedAt = time.UnixMilli(startedMs)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Delete removes one entry.
func (s *Store) Delete(ctx context.Context, id int64) error {
	db, err := s.open()
	if err != nil {
		return err
	}
	res, err := db.ExecContext(ctx, `DELETE FROM history WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("history: delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Clear removes every entry of a connection (all entries when
// connectionID is empty) and returns how many were removed.
func (s *Store) Clear(ctx context.Context, connectionID string) (int64, error) {
	db, err := s.open()
	if err != nil {
		return 0, err
	}
	res, err := db.ExecContext(ctx, `DELETE FROM history WHERE ? = '' OR connection_id = ?`, connectionID, connectionID)
	if err != nil {
		return 0, fmt.Errorf("history: clear: %w", err)
	}
	return res.RowsAffected()
}
