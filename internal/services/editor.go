package services

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	"dbedit/internal/conn"
	"dbedit/internal/driver"
	"dbedit/internal/history"
	"dbedit/internal/query"
	"dbedit/internal/sqlsplit"
)

// EditorSessions is the subset of *conn.Manager used by EditorService.
type EditorSessions interface {
	Get(sessionID string) (*conn.Session, error)
	Subscribe(fn func(conn.Event))
}

// CompletionTableLimit is the number of tables above which
// CompletionSchema returns names only; columns are then loaded per table
// with CompletionColumns.
var CompletionTableLimit = 2000

// janitorInterval is how often idle editor connections and cursors are
// closed.
const janitorInterval = time.Minute

// Statement is a statement of an editor script. Offsets are UTF-16 code
// units (JavaScript string indices) into the script.
type Statement struct {
	Text  string `json:"text"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	// DelimEnd is the offset after the statement's delimiter.
	DelimEnd int `json:"delimEnd"`
}

// CompletionColumn is a column offered by the editor's autocompletion.
type CompletionColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// CompletionTable is a table or view offered by autocompletion. Columns
// is null when not loaded (see CompletionSchema.ColumnsLoaded).
type CompletionTable struct {
	Name    string             `json:"name"`
	Type    driver.TableType   `json:"type"`
	Columns []CompletionColumn `json:"columns"`
}

// CompletionSchema is what the SQL editor autocompletes: the tables (with
// their columns) of the current schema, plus the schema names.
type CompletionSchema struct {
	Database string            `json:"database"`
	Schema   string            `json:"schema"`
	Schemas  []string          `json:"schemas"`
	Tables   []CompletionTable `json:"tables"`
	// ColumnsLoaded is false for schemas with more than
	// CompletionTableLimit tables: columns are then fetched per table.
	ColumnsLoaded bool `json:"columnsLoaded"`
}

// EditorService runs SQL editor tabs (see query.Editors), splits scripts
// for the editor, feeds autocompletion and records query history.
type EditorService struct {
	sessions EditorSessions
	drivers  DriverGetter
	editors  *query.Editors
	history  *history.Store

	janitor   sync.Once
	stopSweep func()

	mu    sync.Mutex
	cache map[string]*CompletionSchema // sessionID + "\x00" + schema
}

// NewEditorService returns an EditorService. hist may be nil (no
// history). It subscribes to session events: editors are closed when their
// session disconnects and reconnect after a database switch.
func NewEditorService(sessions EditorSessions, drivers DriverGetter, hist *history.Store) *EditorService {
	s := &EditorService{sessions: sessions, drivers: drivers, history: hist, cache: map[string]*CompletionSchema{}}
	s.editors = query.NewEditors(s.resolve, &query.Runner{Timeout: -1})
	s.editors.BasePoolSize = conn.MaxOpenConns
	sessions.Subscribe(s.onSessionEvent)
	return s
}

func (s *EditorService) resolve(sessionID string) (*sql.DB, driver.Kind, error) {
	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		return nil, "", err
	}
	return sess.DB, sess.Kind, nil
}

func (s *EditorService) onSessionEvent(ev conn.Event) {
	s.invalidate(ev.SessionID)
	switch ev.Kind {
	case conn.SessionClosed:
		s.editors.SessionClosed(ev.SessionID)
	case conn.DatabaseChanged:
		s.editors.SessionChanged(ev.SessionID)
	}
}

// OpenEditor creates an editor for a session and returns its ID. Its
// connection is taken from the pool on the first run.
func (s *EditorService) OpenEditor(sessionID string) (string, error) {
	s.janitor.Do(func() { s.stopSweep = s.editors.StartJanitor(janitorInterval) })
	return s.editors.Open(sessionID)
}

// CloseEditor closes an editor: a running query is cancelled, an open
// transaction is rolled back and its connection is closed.
func (s *EditorService) CloseEditor(editorID string) error {
	return s.editors.Close(editorID)
}

// Execute runs sql (one or more statements) in the editor; see
// query.Editors.Execute. requestID can be passed to Cancel. The run is
// recorded in the query history.
func (s *EditorService) Execute(ctx context.Context, editorID, requestID, sql string, opts query.ExecOptions) (*query.ExecResult, error) {
	started := time.Now()
	res, err := s.editors.Execute(ctx, editorID, requestID, sql, opts)
	if err != nil {
		return nil, err
	}
	s.record(editorID, sql, started, res)
	return res, nil
}

// record adds a run to the history (errors are logged, never shown).
func (s *EditorService) record(editorID, sql string, started time.Time, res *query.ExecResult) {
	if s.history == nil || len(res.Statements) == 0 {
		return
	}
	sess, err := s.sessions.Get(s.editors.SessionOf(editorID))
	if err != nil {
		return
	}
	e := history.Entry{ConnectionID: sess.ConnectionID, Database: sess.Database, SQL: sql, StartedAt: started, DurationMs: res.DurationMs}
	var rows, affected int64
	var hasRows, hasAffected bool
	for _, st := range res.Statements {
		for _, rs := range st.ResultSets {
			rows += int64(len(rs.Rows))
			hasRows = true
		}
		if st.RowsAffected != nil {
			affected += *st.RowsAffected
			hasAffected = true
		}
	}
	if hasRows {
		e.RowCount = &rows
	}
	if hasAffected {
		e.RowsAffected = &affected
	}
	if fe := res.FirstError(); fe != nil {
		e.IsError, e.Error = true, fe.Message
	}
	if _, err := s.history.Add(context.Background(), e); err != nil {
		log.Printf("query history: %v", err)
	}
}

// Cancel cancels a running Execute or FetchMore.
func (s *EditorService) Cancel(requestID string) bool {
	return s.editors.Cancel(requestID)
}

// FetchMore returns the next n rows of a truncated result that is still
// open (ResultSet.CanLoadMore).
func (s *EditorService) FetchMore(ctx context.Context, editorID, requestID, resultID string, n int) (*query.ResultSet, error) {
	return s.editors.FetchMore(ctx, editorID, requestID, resultID, n)
}

// TxState returns the editor's transaction state as of its last run.
func (s *EditorService) TxState(editorID string) (query.TxInfo, error) {
	return s.editors.TxState(editorID)
}

func toStatement(st sqlsplit.Statement, m sqlsplit.OffsetMap) Statement {
	return Statement{Text: st.Text, Start: m.UTF16(st.Start), End: m.UTF16(st.End), DelimEnd: m.UTF16(st.DelimEnd)}
}

// Split splits a script into statements with the rules of kind.
func (s *EditorService) Split(kind driver.Kind, sql string) []Statement {
	m := sqlsplit.NewOffsetMap(sql, 0)
	stmts := sqlsplit.Split(kind, sql)
	out := make([]Statement, len(stmts))
	for i, st := range stmts {
		out[i] = toStatement(st, m)
	}
	return out
}

// StatementAt returns the statement at a cursor position (UTF-16 offset)
// of a script, or null when the script has no statements. See
// sqlsplit.StatementAt for how a cursor between statements is resolved.
func (s *EditorService) StatementAt(kind driver.Kind, sql string, cursor int) *Statement {
	st, ok := sqlsplit.StatementAt(kind, sql, sqlsplit.ByteOffset(sql, cursor))
	if !ok {
		return nil
	}
	out := toStatement(st, sqlsplit.NewOffsetMap(sql, 0))
	return &out
}

func cacheKey(sessionID, schema string) string { return sessionID + "\x00" + schema }

func (s *EditorService) invalidate(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.cache {
		if len(k) > len(sessionID) && k[:len(sessionID)+1] == sessionID+"\x00" {
			delete(s.cache, k)
		}
	}
}

// CompletionSchema returns the tables and columns of a schema for
// autocompletion. It is cached per session and schema until refresh is
// set, the database is switched or the session closes.
func (s *EditorService) CompletionSchema(ctx context.Context, sessionID, schema string, refresh bool) (*CompletionSchema, error) {
	key := cacheKey(sessionID, schema)
	if !refresh {
		s.mu.Lock()
		cached := s.cache[key]
		s.mu.Unlock()
		if cached != nil {
			return cached, nil
		}
	}
	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		return nil, err
	}
	d, err := s.drivers.Get(sess.Kind)
	if err != nil {
		return nil, err
	}
	schemas, err := d.ListSchemas(ctx, sess.DB, false)
	if err != nil {
		return nil, err
	}
	tables, err := d.ListTables(ctx, sess.DB, schema)
	if err != nil {
		return nil, err
	}
	out := &CompletionSchema{Database: sess.Database, Schema: schema, Schemas: schemas, Tables: make([]CompletionTable, len(tables))}
	index := make(map[string]int, len(tables))
	for i, t := range tables {
		out.Tables[i] = CompletionTable{Name: t.Name, Type: t.Type}
		index[t.Name] = i
	}
	if len(tables) <= CompletionTableLimit {
		cols, err := d.SchemaColumns(ctx, sess.DB, schema)
		if err != nil {
			return nil, err
		}
		for i := range out.Tables {
			out.Tables[i].Columns = []CompletionColumn{}
		}
		for _, c := range cols {
			if i, ok := index[c.Table]; ok {
				out.Tables[i].Columns = append(out.Tables[i].Columns, CompletionColumn{Name: c.Name, Type: c.DataType})
			}
		}
		out.ColumnsLoaded = true
	}
	s.mu.Lock()
	s.cache[key] = out
	s.mu.Unlock()
	return out, nil
}

// CompletionColumns returns one table's columns (for schemas too big for
// CompletionSchema to include them).
func (s *EditorService) CompletionColumns(ctx context.Context, sessionID, schema, table string) ([]CompletionColumn, error) {
	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		return nil, err
	}
	d, err := s.drivers.Get(sess.Kind)
	if err != nil {
		return nil, err
	}
	cols, err := d.Columns(ctx, sess.DB, schema, table)
	if err != nil {
		return nil, err
	}
	out := make([]CompletionColumn, len(cols))
	for i, c := range cols {
		out[i] = CompletionColumn{Name: c.Name, Type: c.DataType}
	}
	return out, nil
}

// ServiceShutdown is called by Wails when the application quits: editors
// are closed (open transactions rolled back) and the history is closed.
func (s *EditorService) ServiceShutdown() error {
	if s.stopSweep != nil {
		s.stopSweep()
	}
	s.editors.CloseAll()
	if s.history != nil {
		return s.history.Close()
	}
	return nil
}

// HistoryService exposes the SQL editor's query history.
type HistoryService struct {
	store *history.Store
}

// NewHistoryService returns a HistoryService.
func NewHistoryService(store *history.Store) *HistoryService {
	return &HistoryService{store: store}
}

// List returns a connection's history, newest first, optionally filtered
// by a substring of the SQL.
func (s *HistoryService) List(ctx context.Context, connectionID, search string, limit, offset int) ([]history.Entry, error) {
	return s.store.List(ctx, connectionID, search, limit, offset)
}

// Delete removes one entry.
func (s *HistoryService) Delete(ctx context.Context, id int64) error {
	return s.store.Delete(ctx, id)
}

// Clear removes a connection's history and returns how many entries were
// removed.
func (s *HistoryService) Clear(ctx context.Context, connectionID string) (int64, error) {
	return s.store.Clear(ctx, connectionID)
}
