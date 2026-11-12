package query

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	dbdriver "lontar/internal/driver"
	"lontar/internal/sqlsplit"
	"lontar/internal/values"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SQL editor errors.
var (
	ErrEditorNotFound = errors.New("editor not found (it was closed)")
	ErrEditorBusy     = errors.New("a query is already running in this editor")
	ErrNoCursor       = errors.New("this result is closed; run the query again to load more rows")
)

// Idle limits of the SQL editor.
const (
	// DefaultEditorIdle is how long an editor keeps its connection
	// without a run; then the connection is rolled back and closed.
	DefaultEditorIdle = 30 * time.Minute
	// DefaultCursorIdle is how long a truncated result keeps its cursor
	// open for "Load more" without a fetch.
	DefaultCursorIdle = 5 * time.Minute
	// rollbackTimeout bounds the ROLLBACK sent when an editor closes.
	rollbackTimeout = 5 * time.Second
	// pingTimeout bounds the connection check after a cancel.
	pingTimeout = 3 * time.Second
)

// Messages shown when an editor's session state was lost.
const (
	msgLost      = "The connection to the server was lost. The editor reconnected, so its open transaction, temporary tables and session settings (SET, USE, search_path) are gone."
	msgCancelled = "Cancelling closed the connection (MySQL and MariaDB drop it). The next run opens a new connection, so this editor's open transaction, temporary tables and session settings are gone."
	msgReconnect = "This editor's previous connection was closed, so this run used a new one: its open transaction, temporary tables and session settings (SET, USE, search_path) are gone."
	msgIdle      = "The editor's connection was closed after 30 minutes without a run (any open transaction was rolled back)."
	msgSwitched  = "The database was switched in the sidebar, so the editor reconnected (any open transaction was rolled back)."
)

// Resolver returns a session's pool and engine kind.
type Resolver func(sessionID string) (*sql.DB, dbdriver.Kind, error)

// Editors runs SQL editor tabs. Each editor pins one connection (a
// *sql.Conn) from its session's pool on its first run and keeps it until
// the editor closes, its session disconnects or switches databases, or it
// has been idle for IdleTimeout. Session state therefore persists across
// runs: transactions, SET/USE/search_path and temporary tables.
//
// A pinned connection is never returned to the pool (it may carry session
// state): it is rolled back and closed. While pinned it does not count
// against the session's browse capacity: the pool's MaxOpenConns is raised
// by one per pinned connection (BasePoolSize + pinned).
//
// Editors is safe for concurrent use; runs of one editor are serialised
// (a second concurrent run fails with ErrEditorBusy).
type Editors struct {
	resolve Resolver
	runner  *Runner

	// BasePoolSize is the session pools' MaxOpenConns without pinned
	// connections (conn.MaxOpenConns). Zero leaves the pools alone.
	BasePoolSize int
	// IdleTimeout and CursorIdleTimeout default to DefaultEditorIdle and
	// DefaultCursorIdle.
	IdleTimeout       time.Duration
	CursorIdleTimeout time.Duration

	now   func() time.Time
	newID func() string

	mu      sync.Mutex
	editors map[string]*Editor
	pinned  map[*sql.DB]int
}

// NewEditors returns an editor manager. runner cancels runs by request ID;
// for the editor it should have no timeout (Timeout < 0).
func NewEditors(resolve Resolver, runner *Runner) *Editors {
	if runner == nil {
		runner = &Runner{Timeout: -1}
	}
	return &Editors{
		resolve: resolve,
		runner:  runner,
		now:     time.Now,
		newID:   uuid.NewString,
		editors: map[string]*Editor{},
		pinned:  map[*sql.DB]int{},
	}
}

// Editor is one SQL editor tab.
type Editor struct {
	id, sessionID string

	// run serialises runs, fetches and closing, and guards these fields.
	run    sync.Mutex
	db     *sql.DB
	kind   dbdriver.Kind
	conn   *sql.Conn
	cursor *cursor
	lost   string // why the session was reset; reported by the next run
	track  txTracker

	// mu guards the fields below, which are read without holding run.
	mu        sync.Mutex
	tx        TxInfo
	lastUsed  time.Time
	requestID string
	closed    bool
}

// cursor is a truncated result whose rows are still open.
type cursor struct {
	id       string
	rows     *sql.Rows
	cancel   context.CancelFunc
	cats     []values.Category
	next     []values.Cell // the row read ahead
	pgIdle   bool          // PostgreSQL outside a transaction: cancel, do not drain
	lastUsed time.Time
}

func (ed *Editor) setRequest(id string) {
	ed.mu.Lock()
	ed.requestID = id
	ed.mu.Unlock()
}

func (ed *Editor) request() string {
	ed.mu.Lock()
	defer ed.mu.Unlock()
	return ed.requestID
}

func (ed *Editor) isClosed() bool {
	ed.mu.Lock()
	defer ed.mu.Unlock()
	return ed.closed
}

func (ed *Editor) touch(t time.Time) {
	ed.mu.Lock()
	ed.lastUsed = t
	ed.mu.Unlock()
}

func (ed *Editor) idleSince() time.Time {
	ed.mu.Lock()
	defer ed.mu.Unlock()
	return ed.lastUsed
}

func (ed *Editor) setTx(info TxInfo) {
	ed.mu.Lock()
	ed.tx = info
	ed.mu.Unlock()
}

func (ed *Editor) txInfo() TxInfo {
	ed.mu.Lock()
	defer ed.mu.Unlock()
	return ed.tx
}

func (e *Editors) idle() time.Duration {
	if e.IdleTimeout > 0 {
		return e.IdleTimeout
	}
	return DefaultEditorIdle
}

func (e *Editors) cursorIdle() time.Duration {
	if e.CursorIdleTimeout > 0 {
		return e.CursorIdleTimeout
	}
	return DefaultCursorIdle
}

func txSource(kind dbdriver.Kind) string {
	if kind == dbdriver.Postgres {
		return TxSourceServer
	}
	return TxSourceHeuristic
}

// Open creates an editor for a session. No connection is taken until the
// first run.
func (e *Editors) Open(sessionID string) (string, error) {
	_, kind, err := e.resolve(sessionID)
	if err != nil {
		return "", err
	}
	ed := &Editor{id: e.newID(), sessionID: sessionID, kind: kind}
	ed.tx = TxInfo{State: TxIdle, Source: txSource(kind)}
	ed.lastUsed = e.now()
	e.mu.Lock()
	e.editors[ed.id] = ed
	e.mu.Unlock()
	return ed.id, nil
}

func (e *Editors) get(id string) (*Editor, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ed, ok := e.editors[id]
	if !ok {
		return nil, ErrEditorNotFound
	}
	return ed, nil
}

// SessionOf returns an editor's session ID ("" for unknown editors).
func (e *Editors) SessionOf(editorID string) string {
	ed, err := e.get(editorID)
	if err != nil {
		return ""
	}
	return ed.sessionID
}

// Len returns the number of open editors.
func (e *Editors) Len() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.editors)
}

// TxState returns an editor's transaction state as of its last run.
func (e *Editors) TxState(editorID string) (TxInfo, error) {
	ed, err := e.get(editorID)
	if err != nil {
		return TxInfo{}, err
	}
	return ed.txInfo(), nil
}

// Cancel cancels a running Execute or FetchMore by request ID.
func (e *Editors) Cancel(requestID string) bool { return e.runner.Cancel(requestID) }

// lock takes an editor's run lock without waiting.
func (e *Editors) lock(editorID string) (*Editor, error) {
	ed, err := e.get(editorID)
	if err != nil {
		return nil, err
	}
	if !ed.run.TryLock() {
		return nil, ErrEditorBusy
	}
	if ed.isClosed() {
		ed.run.Unlock()
		return nil, ErrEditorNotFound
	}
	return ed, nil
}

// Execute splits sqlText and runs its statements in order on the editor's
// connection. A failing statement stops the run unless
// opts.ContinueOnError; a cancel (Cancel(requestID)) always stops it. The
// returned error is only for failures before any statement ran (unknown
// editor or session, no connection); statement errors are in the result.
func (e *Editors) Execute(ctx context.Context, editorID, requestID, sqlText string, opts ExecOptions) (*ExecResult, error) {
	ed, err := e.lock(editorID)
	if err != nil {
		return nil, err
	}
	defer ed.run.Unlock()
	if requestID == "" {
		requestID = e.newID()
	}
	ed.setRequest(requestID)
	defer ed.setRequest("")

	var res *ExecResult
	err = e.runner.Run(ctx, requestID, func(ctx context.Context) error {
		var err error
		res, err = e.execute(ctx, ed, sqlText, opts)
		return err
	})
	if res == nil {
		return nil, err
	}
	return res, nil
}

func (e *Editors) execute(ctx context.Context, ed *Editor, sqlText string, opts ExecOptions) (*ExecResult, error) {
	start := time.Now()
	db, kind, err := e.resolve(ed.sessionID)
	if err != nil {
		return nil, err
	}
	res := &ExecResult{Statements: []StatementResult{}, Notices: []string{}}
	e.closeCursor(ed)
	if ed.conn != nil && ed.db != db {
		e.release(ed, msgSwitched, true)
	}
	if ed.conn == nil {
		if err := e.pin(ctx, ed, db, kind); err != nil {
			return nil, err
		}
	}
	e.reportLost(ed, res)

	maxRows := NormalizeMaxRows(opts.MaxRows)
	stmts := sqlsplit.Split(ed.kind, sqlText)
	om := sqlsplit.NewOffsetMap(sqlText, opts.BaseOffset)
	stopped := false
	for i, st := range stmts {
		sr := StatementResult{Index: i, SQL: st.Text, Start: om.UTF16(st.Start), End: om.UTF16(st.End), ResultSets: []ResultSet{}}
		if stopped {
			sr.Status = StatusSkipped
		} else {
			e.runStatement(ctx, ed, &sr, st, i == len(stmts)-1, maxRows, om, res)
			switch {
			case sr.Status == StatusCancelled:
				res.Cancelled, stopped = true, true
			case sr.Status == StatusError && !opts.ContinueOnError:
				stopped = true
			}
		}
		res.Statements = append(res.Statements, sr)
	}
	e.refreshTx(ed)
	res.Tx = ed.txInfo()
	res.DurationMs = durationMs(time.Since(start))
	ed.touch(e.now())
	return res, nil
}

// reportLost moves a pending "session lost" message into res.
func (e *Editors) reportLost(ed *Editor, res *ExecResult) {
	if ed.lost == "" {
		return
	}
	res.Notices = append(res.Notices, ed.lost)
	res.SessionReset = true
	ed.lost = ""
}

func (e *Editors) runStatement(ctx context.Context, ed *Editor, sr *StatementResult, st sqlsplit.Statement, last bool, maxRows int, om sqlsplit.OffsetMap, res *ExecResult) {
	t0 := time.Now()
	defer func() { sr.DurationMs = durationMs(time.Since(t0)) }()
	words := sqlsplit.Words(ed.kind, st.Text, 0)

	err := ctx.Err()
	if err == nil {
		err = e.runOnce(ctx, ed, sr, st.Text, words, last, maxRows)
	}
	if err != nil && ctx.Err() == nil && (errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone)) {
		// The connection died while idle and nothing was sent: replace
		// it and run the statement once more.
		e.drop(ed, msgReconnect)
		if err = e.pin(ctx, ed, ed.db, ed.kind); err == nil {
			e.reportLost(ed, res)
			sr.ResultSets = sr.ResultSets[:0]
			err = e.runOnce(ctx, ed, sr, st.Text, words, last, maxRows)
		}
	}
	switch {
	case err == nil:
		sr.Status = StatusOK
		ed.track.observe(ed.kind, st.Text, words, nil)
	case ctx.Err() != nil:
		sr.Status = StatusCancelled
		sr.Error = &ExecError{Message: "Query cancelled"}
		e.checkAlive(ed, res, msgCancelled)
	default:
		sr.Status = StatusError
		sr.Error = toExecError(err, st.Text, func(b int) int { return om.UTF16(st.Start + b) })
		ed.track.observe(ed.kind, st.Text, words, err)
		if errors.Is(err, gomysql.ErrInvalidConn) || errors.Is(err, driver.ErrBadConn) {
			e.checkAlive(ed, res, msgLost)
		}
	}
}

// runOnce runs one statement on the pinned connection. The statement runs
// under its own context, detached from ctx but cancelled with it, so that
// the rows of a truncated last result can stay open after the call.
func (e *Editors) runOnce(ctx context.Context, ed *Editor, sr *StatementResult, text string, words []string, last bool, maxRows int) error {
	if ed.conn == nil {
		return sql.ErrConnDone
	}
	sctx, scancel := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(ctx, scancel)
	keep := false
	defer func() {
		stop()
		if !keep {
			scancel()
		}
	}()
	args := statementArgs(ed.kind)
	if !returnsRows(words) {
		r, err := ed.conn.ExecContext(sctx, text, args...)
		if err != nil {
			return err
		}
		if n, err := r.RowsAffected(); err == nil && isDML(words) {
			sr.RowsAffected = &n
		}
		return nil
	}

	pgIdle := ed.kind == dbdriver.Postgres && ed.pgTxStatus() == 'I'
	rows, err := ed.conn.QueryContext(sctx, text, args...)
	if err != nil {
		return err
	}
	// Only MySQL's CALL returns several result sets; a truncated set of
	// a CALL is skipped past (its rest is discarded), never kept open.
	multi := len(words) > 0 && words[0] == "CALL"
	for {
		types, err := rows.ColumnTypes()
		if err != nil {
			_ = rows.Close()
			return err
		}
		if len(types) > 0 {
			rs, cur, err := e.readSet(rows, types, maxRows)
			if err != nil {
				closeRows(rows, scancel, pgIdle)
				return err
			}
			if rs.Truncated && !multi {
				if last {
					cur.cancel, cur.pgIdle, cur.lastUsed = scancel, pgIdle, e.now()
					ed.cursor, rs.CanLoadMore, keep = cur, true, true
				} else {
					closeRows(rows, scancel, pgIdle)
				}
				sr.ResultSets = append(sr.ResultSets, rs)
				return nil
			}
			sr.ResultSets = append(sr.ResultSets, rs)
		}
		if !rows.NextResultSet() {
			break
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	return rows.Close()
}

// readSet reads up to maxRows rows of the current result set. When more
// rows exist, the set is Truncated and the returned cursor holds the next
// row (read ahead).
func (e *Editors) readSet(rows *sql.Rows, types []*sql.ColumnType, maxRows int) (ResultSet, *cursor, error) {
	rs := ResultSet{ID: e.newID(), Columns: make([]ResultColumn, len(types)), Rows: [][]values.Cell{}}
	cats := make([]values.Category, len(types))
	for i, t := range types {
		nullable, ok := t.Nullable()
		rs.Columns[i] = ResultColumn{Name: t.Name(), Type: strings.ToLower(t.DatabaseTypeName()), Nullable: nullable || !ok}
		cats[i] = values.Categorize(t.DatabaseTypeName())
	}
	cur := &cursor{id: rs.ID, rows: rows, cats: cats}
	for rows.Next() {
		row, err := scanRow(rows, cats)
		if err != nil {
			return rs, nil, err
		}
		if len(rs.Rows) == maxRows {
			rs.Truncated, cur.next = true, row
			return rs, cur, nil
		}
		rs.Rows = append(rs.Rows, row)
	}
	return rs, nil, rows.Err()
}

func scanRow(rows *sql.Rows, cats []values.Category) ([]values.Cell, error) {
	dest := make([]any, len(cats))
	ptrs := make([]any, len(cats))
	for i := range dest {
		ptrs[i] = &dest[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	row := make([]values.Cell, len(cats))
	for i, v := range dest {
		row[i] = values.ConvertCat(v, cats[i])
	}
	return row, nil
}

// closeRows stops reading a result early. Closing rows normally reads the
// rest of the result (pgx and the MySQL driver drain the connection),
// which can take long for a huge table. On PostgreSQL outside a
// transaction the statement is cancelled instead (a server-side cancel
// request; harmless there). Inside a transaction a cancel would abort it,
// so the rows are drained, as on MySQL (where a cancel closes the
// connection) and SQLite (where closing is immediate).
func closeRows(rows *sql.Rows, cancel context.CancelFunc, pgIdle bool) {
	if pgIdle {
		cancel()
	}
	_ = rows.Close()
}

// FetchMore returns up to n (NormalizeMaxRows) further rows of a
// truncated result whose cursor is still open (ResultSet.CanLoadMore).
// The returned set has no columns. When it is not Truncated, the cursor
// is closed.
func (e *Editors) FetchMore(ctx context.Context, editorID, requestID, resultID string, n int) (*ResultSet, error) {
	ed, err := e.lock(editorID)
	if err != nil {
		return nil, err
	}
	defer ed.run.Unlock()
	cur := ed.cursor
	if cur == nil || cur.id != resultID {
		return nil, ErrNoCursor
	}
	if requestID == "" {
		requestID = e.newID()
	}
	ed.setRequest(requestID)
	defer ed.setRequest("")
	n = NormalizeMaxRows(n)

	var rs *ResultSet
	err = e.runner.Run(ctx, requestID, func(ctx context.Context) error {
		stop := context.AfterFunc(ctx, cur.cancel)
		defer stop()
		out := [][]values.Cell{cur.next}
		cur.next = nil
		for cur.rows.Next() {
			row, err := scanRow(cur.rows, cur.cats)
			if err != nil {
				return err
			}
			if len(out) == n {
				cur.next = row
				break
			}
			out = append(out, row)
		}
		if cur.next == nil {
			if err := cur.rows.Err(); err != nil {
				return err
			}
		}
		more := cur.next != nil
		rs = &ResultSet{ID: cur.id, Columns: []ResultColumn{}, Rows: out, Truncated: more, CanLoadMore: more}
		return nil
	})
	ed.touch(e.now())
	if err != nil || !rs.Truncated {
		e.closeCursor(ed)
	} else {
		cur.lastUsed = e.now()
	}
	if err != nil {
		if errors.Is(err, ErrCanceled) {
			e.checkAlive(ed, &ExecResult{}, msgCancelled)
		}
		e.refreshTx(ed)
		return nil, err
	}
	return rs, nil
}

// closeCursor closes an open truncated result.
func (e *Editors) closeCursor(ed *Editor) {
	cur := ed.cursor
	if cur == nil {
		return
	}
	ed.cursor = nil
	closeRows(cur.rows, cur.cancel, cur.pgIdle)
	cur.cancel()
}

// pin takes a connection from db's pool for the editor.
func (e *Editors) pin(ctx context.Context, ed *Editor, db *sql.DB, kind dbdriver.Kind) error {
	e.adjustPool(db, 1)
	c, err := db.Conn(ctx)
	if err != nil {
		e.adjustPool(db, -1)
		return err
	}
	ed.db, ed.kind, ed.conn = db, kind, c
	ed.track = txTracker{}
	return nil
}

// adjustPool tracks pinned connections per pool and raises the pool's
// limit by that number, so browsing never waits for editors.
func (e *Editors) adjustPool(db *sql.DB, delta int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := e.pinned[db] + delta
	if n <= 0 {
		delete(e.pinned, db)
		n = 0
	} else {
		e.pinned[db] = n
	}
	if e.BasePoolSize > 0 {
		db.SetMaxOpenConns(e.BasePoolSize + n)
	}
}

// Pinned returns the number of connections pinned from db (for tests).
func (e *Editors) Pinned(db *sql.DB) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pinned[db]
}

// release gives up the editor's connection: the open result is closed, an
// open transaction is rolled back (when rollback is set) and the
// connection is closed rather than returned to the pool, since it may
// carry session state. reason is reported by the next run ("" for none).
func (e *Editors) release(ed *Editor, reason string, rollback bool) {
	e.closeCursor(ed)
	if ed.conn == nil {
		return
	}
	if rollback && e.txOpen(ed) {
		ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		_, _ = ed.conn.ExecContext(ctx, "ROLLBACK", statementArgs(ed.kind)...)
		cancel()
	}
	e.drop(ed, reason)
}

// drop discards the editor's connection without a rollback (closing a
// connection rolls back on every engine).
func (e *Editors) drop(ed *Editor, reason string) {
	e.closeCursor(ed)
	if ed.conn != nil {
		// Returning ErrBadConn from Raw makes database/sql close the
		// connection instead of putting it back into the pool.
		_ = ed.conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = ed.conn.Close()
		e.adjustPool(ed.db, -1)
		ed.conn = nil
		if reason != "" {
			ed.lost = reason
		}
	}
	ed.track = txTracker{}
	ed.setTx(TxInfo{State: TxIdle, Source: txSource(ed.kind)})
}

// txOpen reports whether the editor's connection may be in a transaction.
// Without a server-reported state, a ROLLBACK is always sent (harmless
// outside a transaction).
func (e *Editors) txOpen(ed *Editor) bool {
	if ed.kind == dbdriver.Postgres {
		st := ed.pgTxStatus()
		return st != 'I' && st != 0
	}
	return true
}

// checkAlive pings the connection after a cancel or a connection error.
// MySQL and MariaDB close the connection on cancel; it is dropped (notice
// goes into res) so that the next run reconnects and says so.
func (e *Editors) checkAlive(ed *Editor, res *ExecResult, notice string) {
	if ed.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	if err := ed.conn.PingContext(ctx); err != nil {
		e.drop(ed, msgReconnect)
		res.Notices = append(res.Notices, notice)
	}
}

// pgTxStatus reads PostgreSQL's transaction status of the pinned
// connection ('I' idle, 'T' in a transaction, 'E' failed transaction; 0
// when unknown). It reads the status from the last ReadyForQuery message,
// so it costs no round trip.
func (ed *Editor) pgTxStatus() byte {
	if ed.conn == nil {
		return 0
	}
	var st byte
	_ = ed.conn.Raw(func(dc any) error {
		if c, ok := dc.(interface{ Conn() *pgx.Conn }); ok {
			st = c.Conn().PgConn().TxStatus()
		}
		return nil
	})
	return st
}

// refreshTx updates the editor's transaction state after a run.
func (e *Editors) refreshTx(ed *Editor) {
	info := TxInfo{State: TxIdle, Source: txSource(ed.kind)}
	switch {
	case ed.conn == nil:
	case ed.kind == dbdriver.Postgres:
		switch ed.pgTxStatus() {
		case 'I':
		case 'T':
			info.State = TxActive
		case 'E':
			info.State = TxFailed
		default:
			info.State = TxUnknown
		}
	case ed.track.open:
		info.State = TxActive
	}
	ed.setTx(info)
}

// Close closes an editor: a running query is cancelled, an open
// transaction is rolled back and the connection is closed.
func (e *Editors) Close(editorID string) error {
	e.mu.Lock()
	ed, ok := e.editors[editorID]
	delete(e.editors, editorID)
	e.mu.Unlock()
	if !ok {
		return ErrEditorNotFound
	}
	e.shutdown(ed)
	return nil
}

func (e *Editors) shutdown(ed *Editor) {
	ed.mu.Lock()
	ed.closed = true
	ed.mu.Unlock()
	if rid := ed.request(); rid != "" {
		e.runner.Cancel(rid)
	}
	ed.run.Lock()
	defer ed.run.Unlock()
	e.release(ed, "", true)
}

// remove removes and returns the editors matching keep.
func (e *Editors) remove(match func(*Editor) bool) []*Editor {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []*Editor
	for id, ed := range e.editors {
		if match(ed) {
			out = append(out, ed)
			delete(e.editors, id)
		}
	}
	return out
}

// SessionClosed closes every editor of a session (it disconnected).
func (e *Editors) SessionClosed(sessionID string) {
	for _, ed := range e.remove(func(ed *Editor) bool { return ed.sessionID == sessionID }) {
		e.shutdown(ed)
	}
}

// SessionChanged releases the connections of a session's editors after
// the session switched databases (its pool was replaced). The editors stay
// open and reconnect on their next run, which reports the reset.
func (e *Editors) SessionChanged(sessionID string) {
	e.mu.Lock()
	var eds []*Editor
	for _, ed := range e.editors {
		if ed.sessionID == sessionID {
			eds = append(eds, ed)
		}
	}
	e.mu.Unlock()
	for _, ed := range eds {
		if rid := ed.request(); rid != "" {
			e.runner.Cancel(rid)
		}
		ed.run.Lock()
		if ed.conn != nil {
			e.release(ed, msgSwitched, true)
		}
		ed.run.Unlock()
	}
}

// CloseAll closes every editor (application shutdown).
func (e *Editors) CloseAll() {
	for _, ed := range e.remove(func(*Editor) bool { return true }) {
		e.shutdown(ed)
	}
}

// Sweep closes idle cursors (CursorIdleTimeout) and idle connections
// (IdleTimeout). Editors that are running are skipped.
func (e *Editors) Sweep() {
	e.mu.Lock()
	eds := make([]*Editor, 0, len(e.editors))
	for _, ed := range e.editors {
		eds = append(eds, ed)
	}
	e.mu.Unlock()
	now := e.now()
	for _, ed := range eds {
		if !ed.run.TryLock() {
			continue
		}
		if cur := ed.cursor; cur != nil && now.Sub(cur.lastUsed) >= e.cursorIdle() {
			e.closeCursor(ed)
		}
		if ed.conn != nil && now.Sub(ed.idleSince()) >= e.idle() {
			e.release(ed, msgIdle, true)
		}
		ed.run.Unlock()
	}
}

// StartJanitor runs Sweep every interval until the returned stop function
// is called.
func (e *Editors) StartJanitor(interval time.Duration) (stop func()) {
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
				e.Sweep()
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

// txTracker follows the transaction state from the statements run, for
// engines whose driver does not expose it (MySQL/MariaDB, SQLite):
//
//   - BEGIN, START TRANSACTION (and SQLite SAVEPOINT) open a transaction;
//   - COMMIT, END (SQLite), ROLLBACK (but not ROLLBACK TO) close it;
//   - on MySQL, statements that commit implicitly (CREATE, ALTER, DROP,
//     RENAME, TRUNCATE, GRANT, REVOKE, LOCK/UNLOCK, but not CREATE/DROP
//     TEMPORARY) close it, SET autocommit = 0 makes every following
//     statement open one, SET autocommit = 1 commits, and a deadlock
//     (error 1213) rolls back.
//
// It can be wrong (e.g. a transaction started inside a procedure); the UI
// labels it as a guess.
type txTracker struct {
	open   bool
	manual bool // MySQL autocommit = 0
}

var autocommitRe = regexp.MustCompile(`(?is)^SET\s+(?:(?:SESSION|LOCAL)\s+|@@(?:SESSION\.|LOCAL\.)?)?autocommit\s*:?=\s*'?(\w+)'?$`)

var mysqlImplicitCommit = map[string]bool{
	"CREATE": true, "ALTER": true, "DROP": true, "RENAME": true, "TRUNCATE": true,
	"GRANT": true, "REVOKE": true, "LOCK": true, "UNLOCK": true,
}

func (t *txTracker) observe(kind dbdriver.Kind, text string, words []string, err error) {
	mysql := kind == dbdriver.MySQL || kind == dbdriver.MariaDB
	if err != nil {
		var me *gomysql.MySQLError
		if mysql && errors.As(err, &me) && me.Number == 1213 {
			t.open = false
		}
		return
	}
	if len(words) == 0 {
		return
	}
	second := ""
	if len(words) > 1 {
		second = words[1]
	}
	switch first := words[0]; {
	case first == "BEGIN", first == "START" && second == "TRANSACTION":
		t.open = true
	case first == "SAVEPOINT" && !mysql:
		t.open = true
	case first == "COMMIT", first == "END" && !mysql:
		t.open = false
	case first == "ROLLBACK":
		if second != "TO" && !(len(words) > 2 && words[2] == "TO") {
			t.open = false
		}
	case first == "SET" && mysql:
		if m := autocommitRe.FindStringSubmatch(strings.TrimSpace(text)); m != nil {
			switch strings.ToUpper(m[1]) {
			case "0", "OFF", "FALSE":
				t.manual = true
			default:
				t.manual, t.open = false, false
			}
		}
	case mysql && mysqlImplicitCommit[first] && second != "TEMPORARY":
		t.open = false
	case t.manual:
		t.open = true
	}
}
