package query

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	dbdriver "lontar/internal/driver"
	"lontar/internal/values"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"modernc.org/sqlite"
)

// Result size limits of the SQL editor.
const (
	// DefaultMaxRows is how many rows a result set returns by default.
	DefaultMaxRows = 1000
	// MaxMaxRows is the largest allowed MaxRows (and FetchMore batch).
	MaxMaxRows = 50000
)

// ExecOptions controls Execute.
type ExecOptions struct {
	// MaxRows caps the rows returned per result set (0: DefaultMaxRows;
	// at most MaxMaxRows).
	MaxRows int `json:"maxRows"`
	// ContinueOnError runs the remaining statements after one fails.
	ContinueOnError bool `json:"continueOnError"`
	// BaseOffset is the position of the SQL text inside the editor (in
	// UTF-16 code units, i.e. JavaScript string indices); it is added to
	// every reported offset.
	BaseOffset int `json:"baseOffset"`
	// Confirmed is the user's confirmation of the write statements of a
	// run on a connection in "confirm" safe mode (see
	// ExecResult.NeedsConfirm). It is ignored in other modes.
	Confirmed bool `json:"confirmed"`
}

// NormalizeMaxRows applies the default and the upper bound.
func NormalizeMaxRows(n int) int {
	switch {
	case n <= 0:
		return DefaultMaxRows
	case n > MaxMaxRows:
		return MaxMaxRows
	}
	return n
}

// Statement statuses.
const (
	StatusOK        = "ok"
	StatusError     = "error"
	StatusCancelled = "cancelled"
	StatusSkipped   = "skipped" // not run (an earlier statement failed)
)

// ResultSet is one set of rows returned by a statement (a MySQL CALL can
// return several).
type ResultSet struct {
	ID      string          `json:"id"`
	Columns []ResultColumn  `json:"columns"`
	Rows    [][]values.Cell `json:"rows"`
	// Truncated reports that more rows exist than were returned.
	Truncated bool `json:"truncated"`
	// CanLoadMore reports that the cursor is still open, so FetchMore can
	// return the next rows (only for the last result of a run).
	CanLoadMore bool `json:"canLoadMore"`
}

// ExecError describes a failed statement.
type ExecError struct {
	Message string `json:"message"`
	// Code is the engine's error code: the SQLSTATE on PostgreSQL, the
	// error number on MySQL/MariaDB, the result code name on SQLite.
	Code string `json:"code"`
	// SQLState is MySQL's SQLSTATE (PostgreSQL's is in Code).
	SQLState string `json:"sqlState,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Hint     string `json:"hint,omitempty"`
	// Position is the error position in the editor (UTF-16 offset,
	// BaseOffset applied), when the engine reports one.
	Position *int `json:"position"`
}

// StatementResult is the outcome of one statement.
type StatementResult struct {
	Index int    `json:"index"`
	SQL   string `json:"sql"`
	// Start and End delimit the statement in the editor (UTF-16 offsets,
	// BaseOffset applied).
	Start      int         `json:"start"`
	End        int         `json:"end"`
	Status     string      `json:"status"`
	ResultSets []ResultSet `json:"resultSets"`
	// RowsAffected is set for statements that do not return rows.
	RowsAffected *int64     `json:"rowsAffected"`
	DurationMs   float64    `json:"durationMs"`
	Error        *ExecError `json:"error"`
}

// TxState is the transaction state of an editor's connection.
type TxState string

// Transaction states.
const (
	TxIdle    TxState = "idle"
	TxActive  TxState = "active"
	TxFailed  TxState = "failed" // PostgreSQL: aborted, needs ROLLBACK
	TxUnknown TxState = "unknown"
)

// Transaction state sources.
const (
	TxSourceServer    = "server"    // read from the connection (PostgreSQL)
	TxSourceHeuristic = "heuristic" // tracked from BEGIN/COMMIT/… statements
)

// TxInfo is an editor's transaction state and how it was determined.
type TxInfo struct {
	State  TxState `json:"state"`
	Source string  `json:"source"`
}

// ExecResult is the outcome of Execute.
type ExecResult struct {
	Statements []StatementResult `json:"statements"`
	DurationMs float64           `json:"durationMs"`
	Tx         TxInfo            `json:"tx"`
	// Notices are messages for the user (e.g. the session was reset).
	Notices []string `json:"notices"`
	// SessionReset reports that the editor's connection had to be
	// replaced, so transactions, temporary tables and session settings
	// from earlier runs are gone.
	SessionReset bool `json:"sessionReset"`
	Cancelled    bool `json:"cancelled"`
	// NeedsConfirm reports that nothing was run: the connection is in
	// "confirm" safe mode and the script contains the WriteStatements.
	// Run it again with ExecOptions.Confirmed to execute it.
	NeedsConfirm    bool     `json:"needsConfirm"`
	WriteStatements []string `json:"writeStatements"`
}

// FirstError returns the first failed statement's error (nil if none).
func (r *ExecResult) FirstError() *ExecError {
	for _, s := range r.Statements {
		if s.Error != nil {
			return s.Error
		}
	}
	return nil
}

// rowKeywords start statements that return rows. Everything else runs
// with ExecContext (which reports rows affected) unless it contains
// RETURNING. A wrong guess never runs a statement twice: a row-returning
// statement run with Exec loses its rows; a statement run with Query that
// returns no columns is reported without a row count.
var rowKeywords = map[string]bool{
	"SELECT": true, "WITH": true, "VALUES": true, "TABLE": true, "SHOW": true,
	"EXPLAIN": true, "DESCRIBE": true, "DESC": true, "PRAGMA": true, "CALL": true,
	"FETCH": true, "EXECUTE": true, "HELP": true, "CHECK": true, "ANALYZE": true,
	"OPTIMIZE": true, "REPAIR": true, "CHECKSUM": true, "XA": true,
}

// dmlKeywords start statements whose rows-affected count is meaningful;
// other statements (DDL, BEGIN, SET, ...) are reported without a count.
var dmlKeywords = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "REPLACE": true, "MERGE": true,
	"UPSERT": true, "COPY": true, "LOAD": true, "WITH": true,
}

func isDML(words []string) bool { return len(words) > 0 && dmlKeywords[words[0]] }

// returnsRows guesses whether a statement returns rows from its words.
func returnsRows(words []string) bool {
	if len(words) == 0 {
		return true // e.g. "(1)"; Query copes with both
	}
	if rowKeywords[words[0]] {
		return true
	}
	for _, w := range words[1:] {
		if w == "RETURNING" {
			return true
		}
	}
	return false
}

var (
	mysqlLineRe = regexp.MustCompile(`near '((?s).*)' at line (\d+)$`)
	// SQLite messages that name the offending token or identifier.
	sqliteNear = []*regexp.Regexp{
		regexp.MustCompile(`near "([^"]+)"`),
		regexp.MustCompile(`no such (?:column|table|function): ([^\s(]+)`),
	}
	sqliteCodeRe = regexp.MustCompile(`\((SQLITE_\w+)\)`)
)

// errorPosition returns the byte offset of an error inside stmt (relative
// to the statement), if the engine reports one.
func errorPosition(err error, stmt string) (int, bool) {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Position > 0 {
		// Position counts characters (1-based) of the statement text.
		chars := int(pg.Position) - 1
		for i := range stmt {
			if chars == 0 {
				return i, true
			}
			chars--
		}
		return len(stmt), true
	}
	var my *gomysql.MySQLError
	if errors.As(err, &my) {
		m := mysqlLineRe.FindStringSubmatch(my.Message)
		if m == nil {
			return 0, false
		}
		line, _ := strconv.Atoi(m[2])
		start := 0
		for l := 1; l < line; l++ {
			i := strings.IndexByte(stmt[start:], '\n')
			if i < 0 {
				break
			}
			start += i + 1
		}
		// The message quotes the rest of the statement from the error.
		near := m[1]
		if near == "" {
			return len(stmt), true
		}
		if len(near) > 40 {
			near = near[:40]
		}
		if i := strings.Index(stmt[start:], near); i >= 0 {
			return start + i, true
		}
		return start, true
	}
	var lite *sqlite.Error
	if errors.As(err, &lite) {
		for _, re := range sqliteNear {
			if m := re.FindStringSubmatch(lite.Error()); m != nil {
				if i := indexWord(stmt, m[1]); i >= 0 {
					return i, true
				}
			}
		}
	}
	return 0, false
}

// indexWord finds name in stmt as a whole word (case-insensitively), or
// its last dotted part ("main.t" → "t"); -1 when absent.
func indexWord(stmt, name string) int {
	for _, n := range []string{name, name[strings.LastIndexByte(name, '.')+1:]} {
		re, err := regexp.Compile(`(?i)(^|[^\w$])(` + regexp.QuoteMeta(n) + `)($|[^\w$])`)
		if err != nil || n == "" {
			continue
		}
		if m := re.FindStringSubmatchIndex(stmt); m != nil {
			return m[4]
		}
	}
	return -1
}

// toExecError converts a driver error. pos maps statement-relative byte
// offsets to editor offsets.
func toExecError(err error, stmt string, pos func(int) int) *ExecError {
	e := &ExecError{Message: err.Error()}
	var (
		pg   *pgconn.PgError
		my   *gomysql.MySQLError
		lite *sqlite.Error
	)
	switch {
	case errors.As(err, &pg):
		e.Message, e.Code, e.Detail, e.Hint = pg.Message, pg.Code, pg.Detail, pg.Hint
	case errors.As(err, &my):
		e.Message, e.Code, e.SQLState = my.Message, strconv.Itoa(int(my.Number)), strings.TrimRight(string(my.SQLState[:]), "\x00")
	case errors.As(err, &lite):
		// "Generic error (SQLITE_ERROR)" → "SQLITE_ERROR".
		e.Code = strconv.Itoa(lite.Code())
		if m := sqliteCodeRe.FindStringSubmatch(sqlite.ErrorCodeString[lite.Code()]); m != nil {
			e.Code = m[1]
		}
	}
	if p, ok := errorPosition(err, stmt); ok {
		at := pos(p)
		e.Position = &at
	}
	return e
}

// pgExecMode runs editor statements on PostgreSQL with the extended
// protocol and an unnamed statement: nothing is cached on the connection
// (no "cached plan must not change result type" after DDL), and each
// statement is still a single command, so the server reports error
// positions relative to it.
var pgExecMode = pgx.QueryExecModeExec

// statementArgs returns the driver options passed with each statement.
func statementArgs(kind dbdriver.Kind) []any {
	if kind == dbdriver.Postgres {
		return []any{pgExecMode}
	}
	return nil
}

// isBadConn reports a connection that must be replaced.
func isBadConn(err error) bool {
	return errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) || errors.Is(err, gomysql.ErrInvalidConn)
}

// durationMs is d in fractional milliseconds.
func durationMs(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
