// Package sqlfile runs SQL script files (dumps) against a database,
// streaming them statement by statement (see sqlsplit.Scanner), so files of
// any size run with bounded memory.
//
// A run uses one dedicated connection, which is discarded afterwards
// (dumps change session settings such as the search path or foreign key
// checks). PostgreSQL COPY … FROM stdin blocks (pg_dump plain format) are
// sent with the COPY protocol on that connection. Options choose between
// one transaction for the whole file (rolled back on an error or on cancel)
// and continuing after errors; by default the run stops at the first error
// and keeps what ran before it.
//
// Safe mode is enforced by CheckSafeMode before a run: it reads the file
// without running anything and classifies every statement with
// query.IsWrite.
package sqlfile

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lontar/internal/driver"
	"lontar/internal/query"
	"lontar/internal/sqlsplit"
	"lontar/internal/store"

	"github.com/jackc/pgx/v5"
)

// Options control a run.
type Options struct {
	// SingleTransaction runs the whole file in one transaction: an error
	// or a cancel rolls everything back. The file's own transaction
	// statements (BEGIN, COMMIT, …) are skipped.
	SingleTransaction bool `json:"singleTransaction"`
	// ContinueOnError runs every statement and reports the failures.
	// It cannot be combined with SingleTransaction.
	ContinueOnError bool `json:"continueOnError"`
}

// Limits.
const (
	// PreviewChars is the length of a statement preview.
	PreviewChars = 200
	// MaxErrors is how many errors a result lists (all are counted).
	MaxErrors = 100
	// MaxErrorSQL is the largest statement text kept with an error (to
	// open it in a query tab); longer ones keep only the preview.
	MaxErrorSQL = 64 << 10
)

// Errors.
var (
	ErrOptions    = errors.New("single transaction and continue on error cannot be combined")
	ErrCompressed = errors.New("compressed files are not supported; decompress the file first")
)

// StatementInfo locates a statement of the file.
type StatementInfo struct {
	// Line is the 1-based line where the statement starts.
	Line int `json:"line"`
	// Preview is the start of the statement (at most PreviewChars
	// characters, whitespace runs collapsed).
	Preview string `json:"preview"`
}

// StatementError is a statement that failed.
type StatementError struct {
	StatementInfo
	// Message is the database's error message.
	Message string `json:"message"`
	// SQL is the whole statement when it is at most MaxErrorSQL bytes
	// (without COPY data), else "".
	SQL string `json:"sql"`
}

// Result reports a run.
type Result struct {
	// Run is the number of statements that ran successfully (in single
	// transaction mode they are rolled back when RolledBack is set).
	Run int64 `json:"run"`
	// Failed is the number of statements that failed.
	Failed int64 `json:"failed"`
	// Skipped is the number of statements that were deliberately not
	// run: the file's own transaction statements in single transaction
	// mode, and psql meta-commands that only matter to psql (\restrict,
	// \unrestrict, \encoding, \set, …).
	Skipped int64 `json:"skipped"`
	// RowsCopied is the number of rows loaded by COPY … FROM stdin.
	RowsCopied int64 `json:"rowsCopied"`
	// Errors are the first MaxErrors failures.
	Errors []StatementError `json:"errors"`
	// Cancelled: the run was cancelled.
	Cancelled bool `json:"cancelled"`
	// Stopped: the run ended at an error before the end of the file.
	Stopped bool `json:"stopped"`
	// RolledBack: the single transaction was rolled back.
	RolledBack bool `json:"rolledBack"`
	// DurationMs is how long the run took.
	DurationMs int64 `json:"durationMs"`
}

// Progress is a snapshot of a run or scan.
type Progress struct {
	BytesRead  int64 `json:"bytesRead"`
	TotalBytes int64 `json:"totalBytes"`
	Statements int64 `json:"statements"`
	RowsCopied int64 `json:"rowsCopied"`
}

// ProgressFunc receives progress (often: callers throttle).
type ProgressFunc func(Progress)

// Preview shortens a statement for display: whitespace runs become one
// space and the text is cut after PreviewChars characters ("…" added).
func Preview(text string) string {
	var b strings.Builder
	n := 0
	space := false
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
			n++
		}
		space = false
		if n >= PreviewChars {
			return strings.TrimRight(b.String(), " ") + "…"
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

func info(st sqlsplit.Statement) StatementInfo {
	return StatementInfo{Line: st.Line, Preview: Preview(st.Text)}
}

// open opens path and rejects compressed files.
func open(path string) (*os.File, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	if fi.IsDir() {
		f.Close()
		return nil, 0, fmt.Errorf("%s is a directory", filepath.Base(path))
	}
	head := make([]byte, 2)
	if n, _ := io.ReadFull(f, head); n == 2 && head[0] == 0x1f && head[1] == 0x8b {
		f.Close()
		return nil, 0, ErrCompressed
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, fi.Size(), nil
}

// newScanner returns a scanner over f, skipping a UTF-8 byte order mark.
func newScanner(kind driver.Kind, f io.Reader) *sqlsplit.Scanner {
	br := bufio.NewReaderSize(f, 256<<10)
	if b, err := br.Peek(3); err == nil && bytes.Equal(b, []byte{0xef, 0xbb, 0xbf}) {
		_, _ = br.Discard(3)
	}
	return sqlsplit.NewScanner(kind, br)
}

// ---- Inspect ------------------------------------------------------------------

// Dump types.
const (
	DumpPostgres = "pg_dump"
	DumpMySQL    = "mysqldump"
	DumpMariaDB  = "mariadb-dump"
	DumpSQLite   = "sqlite"
	DumpPlain    = "sql"
)

// Info describes a file before it runs.
type Info struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	// DumpType is the tool that wrote the file (DumpPostgres, …) or
	// DumpPlain.
	DumpType string `json:"dumpType"`
	// HasCopy: COPY … FROM stdin blocks were seen (PostgreSQL).
	HasCopy bool `json:"hasCopy"`
	// Statements is the number of statements: exact when Exact, else
	// extrapolated from the first InspectBytes.
	Statements int64 `json:"statements"`
	Exact      bool  `json:"exact"`
	// Warning explains a likely mismatch (a dump of another engine).
	Warning string `json:"warning"`
}

// InspectBytes is how much of a file Inspect scans.
const InspectBytes = 8 << 20

// Inspect reads the start of path: its dump type and a statement count
// (exact for files up to InspectBytes).
func Inspect(ctx context.Context, kind driver.Kind, path string) (*Info, error) {
	f, size, err := open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	out := &Info{Path: path, Name: filepath.Base(path), Size: size, DumpType: DetectDump(head)}
	out.Warning = mismatch(out.DumpType, kind)
	if bytes.IndexByte(head, 0) >= 0 {
		out.Warning = "The file does not look like a text file."
	}
	sc := newScanner(kind, io.LimitReader(f, InspectBytes))
	var count int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		st, err := sc.Next()
		if err != nil {
			break
		}
		if size > InspectBytes && int64(st.DelimEnd) >= InspectBytes-1 {
			break // possibly cut off by the limit
		}
		if st.CopyData != nil {
			out.HasCopy = true
		}
		count++
	}
	out.Exact = size <= InspectBytes
	out.Statements = count
	if !out.Exact && sc.Offset() > 0 {
		out.Statements = count * size / sc.Offset()
	}
	return out, nil
}

// DetectDump names the tool that wrote a script from its first bytes.
func DetectDump(head []byte) string {
	h := string(head)
	switch {
	case strings.Contains(h, "-- PostgreSQL database dump"):
		return DumpPostgres
	case strings.Contains(h, "-- MariaDB dump"):
		return DumpMariaDB
	case strings.Contains(h, "-- MySQL dump"):
		return DumpMySQL
	case strings.HasPrefix(strings.TrimLeft(strings.TrimPrefix(h, "\xef\xbb\xbf"), " \r\n"), "PRAGMA foreign_keys=OFF;") && strings.Contains(h, "BEGIN TRANSACTION;"):
		return DumpSQLite
	}
	return DumpPlain
}

func mismatch(dump string, kind driver.Kind) string {
	var want []driver.Kind
	switch dump {
	case DumpPostgres:
		want = []driver.Kind{driver.Postgres}
	case DumpMySQL, DumpMariaDB:
		want = []driver.Kind{driver.MySQL, driver.MariaDB}
	case DumpSQLite:
		want = []driver.Kind{driver.SQLite}
	default:
		return ""
	}
	for _, k := range want {
		if k == kind {
			return ""
		}
	}
	return fmt.Sprintf("This looks like a %s file, but the connection is %s; it will probably fail.", dump, kindName(kind))
}

func kindName(k driver.Kind) string {
	switch k {
	case driver.Postgres:
		return "PostgreSQL"
	case driver.MySQL:
		return "MySQL"
	case driver.MariaDB:
		return "MariaDB"
	case driver.SQLite:
		return "SQLite"
	}
	return string(k)
}

// ---- Scan (safe mode) ---------------------------------------------------------

// ScanResult is what Scan found.
type ScanResult struct {
	Statements int64 `json:"statements"`
	// Writes is the number of statements query.IsWrite flags.
	Writes int64 `json:"writes"`
	// First are the first writes (up to the limit given to Scan).
	First []StatementInfo `json:"first"`
}

// Scan reads path without running it and classifies every statement.
// With stopAtWrite it stops at the first write. limit is how many writes
// First keeps.
func Scan(ctx context.Context, kind driver.Kind, path string, limit int, stopAtWrite bool, progress ProgressFunc) (*ScanResult, error) {
	f, size, err := open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := newScanner(kind, f)
	out := &ScanResult{First: []StatementInfo{}}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		st, err := sc.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out.Statements++
		if !st.Meta && query.IsWrite(kind, st.Text) {
			out.Writes++
			if len(out.First) < limit {
				out.First = append(out.First, info(st))
			}
			if stopAtWrite {
				return out, nil
			}
		}
		if progress != nil {
			progress(Progress{BytesRead: sc.Offset(), TotalBytes: size, Statements: out.Statements})
		}
	}
}

// ReadOnlyError refuses a file with a write statement on a read-only
// connection; nothing ran.
type ReadOnlyError struct {
	StatementInfo
}

func (e *ReadOnlyError) Error() string {
	return fmt.Sprintf("This connection is in read-only safe mode and the file writes (line %d: %s). Nothing was run.", e.Line, e.Preview)
}

// CheckSafeMode applies a connection's safe mode before a run. Read-only:
// the file is read up to its first write, which refuses it
// (*ReadOnlyError). Confirm, unless confirmed: the whole file is read and
// the writes are returned (Writes > 0 means the user must confirm; First
// holds up to limit of them). Otherwise (and when nothing needs
// confirming) the result is nil.
func CheckSafeMode(ctx context.Context, kind driver.Kind, path string, mode store.SafeMode, confirmed bool, limit int, progress ProgressFunc) (*ScanResult, error) {
	switch {
	case mode == store.SafeReadOnly:
		sc, err := Scan(ctx, kind, path, 1, true, progress)
		if err != nil {
			return nil, err
		}
		if sc.Writes > 0 {
			return nil, &ReadOnlyError{StatementInfo: sc.First[0]}
		}
	case mode == store.SafeConfirm && !confirmed:
		sc, err := Scan(ctx, kind, path, limit, false, progress)
		if err != nil {
			return nil, err
		}
		if sc.Writes > 0 {
			return sc, nil
		}
	}
	return nil, nil
}

// ---- Run ----------------------------------------------------------------------

// runner holds the state of one Run.
type runner struct {
	ctx      context.Context
	conn     *sql.Conn
	kind     driver.Kind
	sc       *sqlsplit.Scanner
	size     int64
	opts     Options
	res      *Result
	progress ProgressFunc
	inTx     bool
}

// Run runs the file at path on a dedicated connection of db (see the
// package documentation). Statement failures and cancelling are reported in
// the Result; an error is returned only when the run could not start.
func Run(ctx context.Context, db *sql.DB, kind driver.Kind, path string, opts Options, progress ProgressFunc) (*Result, error) {
	if opts.SingleTransaction && opts.ContinueOnError {
		return nil, ErrOptions
	}
	started := time.Now()
	f, size, err := open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	// The dump may leave session state behind (search_path, foreign key
	// checks, an open transaction): never hand the connection back.
	defer func() {
		_ = conn.Raw(func(any) error { return sqldriver.ErrBadConn })
		_ = conn.Close()
	}()
	r := &runner{ctx: ctx, conn: conn, kind: kind, sc: newScanner(kind, f), size: size, opts: opts,
		res: &Result{Errors: []StatementError{}}, progress: progress}
	r.run()
	r.res.DurationMs = time.Since(started).Milliseconds()
	r.report()
	return r.res, nil
}

func (r *runner) report() {
	if r.progress != nil {
		r.progress(Progress{BytesRead: r.sc.Offset(), TotalBytes: r.size, Statements: r.res.Run + r.res.Failed, RowsCopied: r.res.RowsCopied})
	}
}

// cancelled records a cancel (rolling the transaction back).
func (r *runner) cancelled() {
	r.res.Cancelled = true
	r.rollback()
}

func (r *runner) rollback() {
	if !r.inTx {
		return
	}
	// The run's context may be cancelled: roll back regardless.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 30*time.Second)
	defer cancel()
	_, _ = r.conn.ExecContext(ctx, "ROLLBACK")
	r.inTx = false
	r.res.RolledBack = true
}

func (r *runner) run() {
	if r.opts.SingleTransaction {
		if _, err := r.conn.ExecContext(r.ctx, "BEGIN"); err != nil {
			if r.ctx.Err() != nil {
				r.res.Cancelled = true
				return
			}
			r.fail(StatementInfo{Line: 0, Preview: "BEGIN"}, "", err)
			r.res.Stopped = true
			return
		}
		r.inTx = true
	}
	for {
		if r.ctx.Err() != nil {
			r.cancelled()
			return
		}
		st, err := r.sc.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// The file cannot be read on (too large a statement, a read
			// error): stop in every mode.
			r.fail(StatementInfo{Line: 0}, "", err)
			r.res.Stopped = true
			r.rollback()
			return
		}
		if st.Meta && ignoredMeta(st.Text) || r.opts.SingleTransaction && !st.Meta && isTxControl(r.kind, st.Text) {
			r.res.Skipped++
			continue
		}
		if err := r.exec(st); err != nil {
			if r.ctx.Err() != nil {
				r.cancelled()
				return
			}
			sqlText := ""
			if len(st.Text) <= MaxErrorSQL {
				sqlText = st.Text
			}
			r.fail(info(st), sqlText, err)
			if !r.opts.ContinueOnError {
				r.res.Stopped = true
				r.rollback()
				return
			}
		} else {
			r.res.Run++
		}
		r.report()
	}
	if r.inTx {
		if _, err := r.conn.ExecContext(r.ctx, "COMMIT"); err != nil {
			if r.ctx.Err() != nil {
				r.cancelled()
				return
			}
			r.fail(StatementInfo{Preview: "COMMIT"}, "", err)
			r.res.Stopped = true
			r.rollback()
			return
		}
		r.inTx = false
	}
}

func (r *runner) fail(at StatementInfo, sqlText string, err error) {
	r.res.Failed++
	if len(r.res.Errors) < MaxErrors {
		r.res.Errors = append(r.res.Errors, StatementError{StatementInfo: at, Message: errorText(err), SQL: sqlText})
	}
}

// errorText is the message of a database error (without the statement).
func errorText(err error) string {
	return strings.TrimSpace(err.Error())
}

// exec runs one statement (a COPY with its data block on PostgreSQL).
func (r *runner) exec(st sqlsplit.Statement) error {
	if st.Meta {
		name, _, _ := strings.Cut(st.Text, " ")
		return fmt.Errorf("the psql meta-command %s is not supported (only psql runs it)", name)
	}
	if st.CopyData != nil && r.kind == driver.Postgres {
		return r.copyFrom(st)
	}
	_, err := r.conn.ExecContext(r.ctx, st.Text)
	return err
}

// pgxConn is what pgx's database/sql connection offers.
type pgxConn interface{ Conn() *pgx.Conn }

// copyFrom streams a COPY data block through the COPY protocol.
func (r *runner) copyFrom(st sqlsplit.Statement) error {
	data := &progressReader{r: st.CopyData, fn: func() {
		if r.progress != nil {
			r.progress(Progress{BytesRead: r.sc.Offset(), TotalBytes: r.size, Statements: r.res.Run + r.res.Failed, RowsCopied: r.res.RowsCopied})
		}
	}}
	return r.conn.Raw(func(dc any) error {
		pc, ok := dc.(pgxConn)
		if !ok {
			return fmt.Errorf("COPY FROM stdin needs a PostgreSQL connection")
		}
		tag, err := pc.Conn().PgConn().CopyFrom(r.ctx, data, st.Text)
		if err != nil {
			return err
		}
		r.res.RowsCopied += tag.RowsAffected()
		return nil
	})
}

// progressReader calls fn every progressEvery bytes.
type progressReader struct {
	r    io.Reader
	fn   func()
	read int
}

const progressEvery = 1 << 20

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += n
	if p.read >= progressEvery {
		p.read = 0
		p.fn()
	}
	return n, err
}

// ignoredMeta reports psql meta-commands that only change psql itself and
// are skipped (pg_dump writes \restrict and \unrestrict).
func ignoredMeta(text string) bool {
	name, _, _ := strings.Cut(text, " ")
	switch strings.ToLower(name) {
	case `\restrict`, `\unrestrict`, `\encoding`, `\set`, `\unset`, `\echo`, `\qecho`, `\warn`, `\timing`, `\pset`, `\a`, `\t`, `\x`:
		return true
	}
	return false
}

// isTxControl reports the transaction statements a single-transaction run
// skips: BEGIN [TRANSACTION|WORK], START TRANSACTION, COMMIT, END and
// ROLLBACK (not ROLLBACK TO a savepoint).
func isTxControl(kind driver.Kind, text string) bool {
	w := sqlsplit.Words(kind, text, 3)
	if len(w) == 0 {
		return false
	}
	switch w[0] {
	case "BEGIN":
		// BEGIN; BEGIN TRANSACTION/WORK/DEFERRED/IMMEDIATE/EXCLUSIVE (not
		// a PL block, which never stands alone in a dump).
		return true
	case "START":
		return len(w) > 1 && w[1] == "TRANSACTION"
	case "COMMIT", "END":
		return true
	case "ROLLBACK":
		return !(len(w) > 1 && w[1] == "TO") && !(len(w) > 2 && w[2] == "TO")
	}
	return false
}
