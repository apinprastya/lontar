// Package export writes table data and query results to CSV, JSON or SQL
// INSERT files.
//
// # Sources
//
//   - Table: every row of a table with the tab's filters and sort, streamed
//     from one SELECT without LIMIT on a dedicated connection. Rows are read
//     from the driver's cursor one at a time and written straight to the
//     file, so memory does not grow with the table.
//   - Query: a query re-run for "export all rows". It must be a single
//     statement that the editor's classifier calls a read that returns rows
//     (CheckRerunnable); it then runs inside a READ ONLY transaction
//     (PostgreSQL, MySQL/MariaDB) or with PRAGMA query_only (SQLite) as a
//     second guard. It runs on a fresh pooled connection, not the editor
//     tab's own one, so session state (temporary tables, SET, an open
//     transaction) does not apply.
//   - Cells: rows already loaded in the UI (values.Cell); values cut for
//     display are refused.
//
// # Files
//
// ToFile streams into a temporary file next to the target and renames it
// into place on success; on error or cancellation the temporary file is
// removed and the target is untouched. Files are created with mode 0600.
package export

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"dbedit/internal/driver"
	"dbedit/internal/query"
	"dbedit/internal/sqlsplit"
	"dbedit/internal/values"
)

func errorsAs(err error, target any) bool { return errors.As(err, target) }

// Source yields rows to export.
type Source interface {
	Columns() []Column
	// Next returns the next row, or io.EOF after the last.
	Next() ([]Value, error)
}

// Progress is called with the number of rows written so far.
type Progress func(rows int64)

// ProgressEvery is the minimum interval between progress calls (the last
// call, with the final count, is always made).
var ProgressEvery = 200 * time.Millisecond

// Write writes every row of src to w in opts' format and returns the
// number of rows written. It stops with ctx's error when ctx is cancelled.
func Write(ctx context.Context, src Source, w io.Writer, kind driver.Kind, d driver.Dialect, opts Options, progress Progress) (int64, error) {
	bw := bufferedWriter(w)
	wr, err := NewWriter(bw, kind, d, opts)
	if err != nil {
		return 0, err
	}
	if err := wr.Begin(src.Columns()); err != nil {
		return 0, err
	}
	var n int64
	last := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		row, err := src.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return n, err
		}
		if err := wr.Row(row); err != nil {
			return n, err
		}
		n++
		if progress != nil && n%256 == 0 && time.Since(last) >= ProgressEvery {
			last = time.Now()
			progress(n)
		}
	}
	if err := wr.End(); err != nil {
		return n, err
	}
	if err := bw.Flush(); err != nil {
		return n, err
	}
	if progress != nil {
		progress(n)
	}
	return n, nil
}

// ToFile calls fn with a temporary file in path's directory and renames it
// to path when fn succeeds. On failure (including cancellation) the
// temporary file is removed and path is left as it was.
func ToFile(path string, fn func(w io.Writer) error) (err error) {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: no file chosen", ErrOptions)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".part-*")
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	name := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if err = fn(tmp); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = os.Rename(name, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// ---- Sources --------------------------------------------------------------

// rowsSource reads *sql.Rows.
type rowsSource struct {
	rows *sql.Rows
	cols []Column
	cats []values.Category
	dest []any
	ptrs []any
}

// newRowsSource wraps rows. declared gives the introspected column types
// (table exports); the driver's wire type wins when it is known.
func newRowsSource(rows *sql.Rows, declared []driver.Column) (*rowsSource, error) {
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	s := &rowsSource{rows: rows, cols: make([]Column, len(types)), cats: make([]values.Category, len(types))}
	for i, ct := range types {
		typ := strings.ToLower(ct.DatabaseTypeName())
		s.cats[i] = values.Categorize(typ)
		if i < len(declared) {
			if s.cats[i] == values.CatUnknown {
				s.cats[i] = values.Categorize(declared[i].DataType)
			}
			typ = declared[i].DataType
		}
		s.cols[i] = Column{Name: ct.Name(), Type: typ}
	}
	s.dest = make([]any, len(types))
	s.ptrs = make([]any, len(types))
	for i := range s.dest {
		s.ptrs[i] = &s.dest[i]
	}
	return s, nil
}

func (s *rowsSource) Columns() []Column { return s.cols }

func (s *rowsSource) Next() ([]Value, error) {
	if !s.rows.Next() {
		if err := s.rows.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	if err := s.rows.Scan(s.ptrs...); err != nil {
		return nil, err
	}
	out := make([]Value, len(s.dest))
	for i, v := range s.dest {
		out[i] = FromDriver(v, s.cats[i])
		s.dest[i] = nil
	}
	return out, nil
}

// CellSource exports rows already loaded in the UI.
type CellSource struct {
	Cols []Column
	Rows [][]values.Cell
	i    int
}

// Columns implements Source.
func (c *CellSource) Columns() []Column { return c.Cols }

// Next implements Source.
func (c *CellSource) Next() ([]Value, error) {
	if c.i >= len(c.Rows) {
		return nil, io.EOF
	}
	row := c.Rows[c.i]
	c.i++
	if len(row) != len(c.Cols) {
		return nil, fmt.Errorf("row %d has %d values for %d columns", c.i, len(row), len(c.Cols))
	}
	out := make([]Value, len(row))
	for i, cell := range row {
		v, err := FromCell(cell)
		if err != nil {
			return nil, fmt.Errorf("row %d, column %s: %w", c.i, c.Cols[i].Name, err)
		}
		out[i] = v
	}
	return out, nil
}

// ---- Table and query exports ---------------------------------------------

// Engine is what exports need from a driver (driver.Driver satisfies it).
type Engine interface {
	query.Engine
	Kind() driver.Kind
}

// Table streams every row of req's table matching its filters, in its sort
// order (Offset and Limit are ignored), to w. Columns named in
// opts.SQL.SkipColumns are left out (generated columns, which an INSERT
// cannot set).
func Table(ctx context.Context, db *sql.DB, e Engine, req query.Request, w io.Writer, opts Options, progress Progress) (int64, error) {
	cols, err := query.LookupColumns(ctx, db, e, req)
	if err != nil {
		return 0, err
	}
	b, err := query.BuildSelectAll(e, cols, req)
	if err != nil {
		return 0, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, b.SQL, b.Args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	src, err := newRowsSource(rows, cols)
	if err != nil {
		return 0, err
	}
	if len(opts.SQL.SkipColumns) > 0 {
		return Write(ctx, skipColumns(src, opts.SQL.SkipColumns), w, e.Kind(), e, opts, progress)
	}
	return Write(ctx, src, w, e.Kind(), e, opts, progress)
}

// skipSource drops some columns of another source.
type skipSource struct {
	src  Source
	keep []int
	cols []Column
}

func skipColumns(src Source, names []string) Source {
	s := &skipSource{src: src}
	for i, c := range src.Columns() {
		if !slices.Contains(names, c.Name) {
			s.keep = append(s.keep, i)
			s.cols = append(s.cols, c)
		}
	}
	return s
}

func (s *skipSource) Columns() []Column { return s.cols }

func (s *skipSource) Next() ([]Value, error) {
	row, err := s.src.Next()
	if err != nil {
		return nil, err
	}
	out := make([]Value, len(s.keep))
	for i, k := range s.keep {
		out[i] = row[k]
	}
	return out, nil
}

// ErrNotRerunnable is returned by CheckRerunnable.
var ErrNotRerunnable = errors.New("only a single read-only statement that returns rows can be run again for export")

// CheckRerunnable returns the single statement of script if it is a read
// that returns rows (query.ReturnsRows and not query.IsWrite); otherwise
// ErrNotRerunnable. Writes are never run again.
func CheckRerunnable(kind driver.Kind, script string) (string, error) {
	var stmts []string
	for _, st := range sqlsplit.Split(kind, script) {
		if t := strings.TrimSpace(st.Text); t != "" {
			stmts = append(stmts, t)
		}
	}
	if len(stmts) != 1 {
		return "", fmt.Errorf("%w (found %d statements)", ErrNotRerunnable, len(stmts))
	}
	st := stmts[0]
	if query.IsWrite(kind, st) || !query.ReturnsRows(kind, st) {
		return "", ErrNotRerunnable
	}
	return st, nil
}

// Query runs script again (see CheckRerunnable) in a read-only transaction
// and streams all its rows to w.
func Query(ctx context.Context, db *sql.DB, e Engine, script string, w io.Writer, opts Options, progress Progress) (int64, error) {
	kind := e.Kind()
	stmt, err := CheckRerunnable(kind, script)
	if err != nil {
		return 0, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	var rows *sql.Rows
	if kind == driver.SQLite {
		if _, err := conn.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
			return 0, err
		}
		defer func() { _, _ = conn.ExecContext(context.Background(), "PRAGMA query_only = OFF") }()
		rows, err = conn.QueryContext(ctx, stmt)
	} else {
		tx, terr := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if terr != nil {
			return 0, terr
		}
		defer func() { _ = tx.Rollback() }()
		rows, err = tx.QueryContext(ctx, stmt)
	}
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	src, err := newRowsSource(rows, nil)
	if err != nil {
		return 0, err
	}
	return Write(ctx, src, w, kind, e, opts, progress)
}
