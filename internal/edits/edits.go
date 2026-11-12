// Package edits applies the table grid's staged changes: a ChangeSet of
// row updates, inserts and deletes, keyed by primary key.
//
// # Safety
//
// The frontend never sends SQL. It sends a structured ChangeSet: column
// names, the original primary-key cells of each changed row and the new
// values as typed text (EditValue). This package
//
//   - checks the table, every column and the primary key against
//     introspected metadata (LoadTable) before anything runs;
//   - quotes every identifier with the dialect;
//   - binds every value as a parameter (NULL and DEFAULT are keywords, not
//     values); no value is ever concatenated into executed SQL.
//
// Preview renders the same statements with literals instead of
// placeholders. That display SQL is for the preview dialog only and is
// NEVER executed.
//
// # Type handling
//
// PostgreSQL: values are bound as Go strings with pgx's DescribeExec mode,
// so the server infers each parameter's type from its context (the target
// column of SET/VALUES, the key column of WHERE) and pgx sends strings in
// text format. The server then parses the text with the column type's own
// input function, exactly like a typed literal: big integers and numerics
// stay exact, and enums, arrays, json/jsonb, uuid, interval and
// date/time types all work without dbedit naming a type. Bytes are bound
// as []byte.
//
// MySQL/MariaDB and SQLite: strings, with conversions where implicit
// conversion would be lossy. Integer columns get int64/uint64 (MySQL
// compares an integer column with a string as a double, which cannot tell
// 2^53+1 from 2^53), true/false become 1/0, DECIMAL keys are compared via
// CAST(? AS DECIMAL(65,30)) and MySQL date-times use a space separator.
package edits

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"dbedit/internal/driver"
	"dbedit/internal/query"
	"dbedit/internal/values"

	"github.com/jackc/pgx/v5"
)

// ValueKind says what an EditValue holds.
type ValueKind string

// Value kinds.
const (
	// ValNull sets SQL NULL.
	ValNull ValueKind = "null"
	// ValDefault sets the column default (DEFAULT; omitted on SQLite
	// inserts, refused for SQLite updates).
	ValDefault ValueKind = "default"
	// ValText is what the user typed; it is converted per column type.
	ValText ValueKind = "value"
	// ValBytes is binary data given as hex.
	ValBytes ValueKind = "bytes"
)

// EditValue is a new cell value.
type EditValue struct {
	Kind ValueKind `json:"kind"`
	Text string    `json:"text,omitempty"`
	Hex  string    `json:"hex,omitempty"`
}

// Update changes columns of one existing row.
type Update struct {
	// PK holds the row's original primary-key cells, exactly as FetchTable
	// returned them (so an edit to a key column still finds the row).
	PK map[string]values.Cell `json:"pk"`
	// Set maps column names to new values.
	Set map[string]EditValue `json:"set"`
	// Old holds the original cell of every column in Set. A truncated
	// original (Cell.Trunc) is refused: the user never saw the full value.
	Old map[string]values.Cell `json:"old"`
}

// Insert adds a row. Columns not in Values get their default.
type Insert struct {
	Values map[string]EditValue `json:"values"`
}

// Delete removes one row by its original primary key.
type Delete struct {
	PK map[string]values.Cell `json:"pk"`
}

// ChangeSet is everything staged for one table.
type ChangeSet struct {
	Schema  string   `json:"schema"`
	Table   string   `json:"table"`
	Updates []Update `json:"updates"`
	Inserts []Insert `json:"inserts"`
	Deletes []Delete `json:"deletes"`
}

// Len returns the number of changes.
func (cs ChangeSet) Len() int { return len(cs.Updates) + len(cs.Inserts) + len(cs.Deletes) }

// Op names the kind of a change.
type Op string

// Change kinds.
const (
	OpUpdate Op = "update"
	OpInsert Op = "insert"
	OpDelete Op = "delete"
)

// Errors (wrapped in *ChangeError).
var (
	ErrUnknownTable  = query.ErrUnknownTable
	ErrUnknownColumn = query.ErrUnknownColumn
	ErrNotATable     = errors.New("views are read-only")
	ErrNoPrimaryKey  = errors.New("the table has no primary key, so rows cannot be identified for editing")
	ErrTruncated     = errors.New("the value was truncated for display and cannot be edited")
	ErrInvalidValue  = errors.New("invalid value")
	ErrEmpty         = errors.New("there are no changes")
	ErrReadOnly      = errors.New("the connection is in read-only safe mode")
	ErrStale         = errors.New("the row was changed or deleted since it was loaded")
	ErrDuplicate     = errors.New("the primary key matched more than one row")
)

// FailureKind classifies a ChangeError for the UI.
type FailureKind string

// Failure kinds.
const (
	FailInvalid   FailureKind = "invalid"   // refused before running anything
	FailReadOnly  FailureKind = "readonly"  // safe mode
	FailStale     FailureKind = "stale"     // UPDATE/DELETE matched no row
	FailDuplicate FailureKind = "duplicate" // UPDATE/DELETE matched several rows
	FailDatabase  FailureKind = "database"  // the database reported an error
)

// ChangeError describes why a change set was refused or rolled back. Op and
// Index name the failing change (Index is its position in Updates, Inserts
// or Deletes; -1 when the whole set failed).
type ChangeError struct {
	Op      Op          `json:"op,omitempty"`
	Index   int         `json:"index"`
	Kind    FailureKind `json:"kind"`
	Column  string      `json:"column,omitempty"`
	Message string      `json:"message"`
	// SQL is the display SQL of the failing statement.
	SQL string `json:"sql,omitempty"`
	Err error  `json:"-"`
}

func (e *ChangeError) Error() string { return e.Message }

// Unwrap returns the underlying error.
func (e *ChangeError) Unwrap() error { return e.Err }

func setError(kind FailureKind, err error) *ChangeError {
	return &ChangeError{Index: -1, Kind: kind, Message: err.Error(), Err: err}
}

func changeError(op Op, index int, column string, err error) *ChangeError {
	msg := fmt.Sprintf("%s #%d", op, index+1)
	if column != "" {
		msg += fmt.Sprintf(", column %q", column)
	}
	return &ChangeError{Op: op, Index: index, Kind: FailInvalid, Column: column, Message: msg + ": " + err.Error(), Err: err}
}

// Table is the introspected metadata a change set is checked against.
type Table struct {
	Schema  string
	Name    string
	Type    driver.TableType
	Columns []driver.Column
}

// Engine is what edits needs from a driver (driver.Driver satisfies it).
type Engine interface {
	query.Engine
	Kind() driver.Kind
}

// LoadTable introspects schema.name. It fails for unknown tables and for
// views; a missing primary key is reported by Build.
func LoadTable(ctx context.Context, db *sql.DB, e query.Engine, schema, name string) (Table, error) {
	if name == "" {
		return Table{}, setError(FailInvalid, fmt.Errorf("%w: no table given", ErrUnknownTable))
	}
	cols, err := e.Columns(ctx, db, schema, name)
	if err != nil {
		return Table{}, err
	}
	tables, err := e.ListTables(ctx, db, schema)
	if err != nil {
		return Table{}, err
	}
	i := slices.IndexFunc(tables, func(t driver.Table) bool { return t.Name == name })
	if len(cols) == 0 || i < 0 {
		return Table{}, setError(FailInvalid, fmt.Errorf("%w: %q", ErrUnknownTable, name))
	}
	if tables[i].Type != driver.TypeTable {
		return Table{}, setError(FailInvalid, fmt.Errorf("%w (%s is a %s)", ErrNotATable, name, tables[i].Type))
	}
	return Table{Schema: schema, Name: name, Type: tables[i].Type, Columns: cols}, nil
}

func (t Table) column(name string) (driver.Column, bool) {
	for _, c := range t.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return driver.Column{}, false
}

// PrimaryKey returns the key columns in key order.
func (t Table) PrimaryKey() []driver.Column {
	pk := slices.Clone(t.Columns)
	pk = slices.DeleteFunc(pk, func(c driver.Column) bool { return !c.IsPrimaryKey })
	slices.SortStableFunc(pk, func(a, b driver.Column) int { return a.PKPosition - b.PKPosition })
	return pk
}

// Statement is one generated statement.
type Statement struct {
	Op    Op
	Index int
	// SQL is executed with Args (placeholders only).
	SQL  string
	Args []any
	// Display is SQL with dialect-quoted literals for the preview dialog.
	// It is never executed.
	Display string

	// returning lists the key columns a PostgreSQL INSERT returns.
	returning []driver.Column
	// rowidKey is the key column filled from LastInsertId (MySQL
	// auto-increment, SQLite rowid alias).
	rowidKey string
}

// sqlb writes the executed and the display SQL side by side.
type sqlb struct {
	kind       driver.Kind
	d          driver.Dialect
	exec, disp strings.Builder
	args       []any
}

func (b *sqlb) raw(s string) {
	b.exec.WriteString(s)
	b.disp.WriteString(s)
}

func (b *sqlb) ident(name string) { b.raw(b.d.QuoteIdent(name)) }

// param binds p.bound and writes its placeholder (and p.display).
func (b *sqlb) param(p param) {
	b.args = append(b.args, p.bound)
	ph, lit := b.d.Placeholder(len(b.args)), p.display
	if p.cast != "" {
		ph, lit = "CAST("+ph+" AS "+p.cast+")", "CAST("+lit+" AS "+p.cast+")"
	}
	b.exec.WriteString(ph)
	b.disp.WriteString(lit)
}

func (b *sqlb) table(t Table) {
	if t.Schema != "" {
		b.ident(t.Schema)
		b.raw(".")
	}
	b.ident(t.Name)
}

// param is a bound value with its display literal. cast is a fixed type
// name (never user input) to wrap the placeholder in.
type param struct {
	bound   any
	display string
	cast    string
}

// Build validates cs against t and generates its statements in execution
// order: deletes, updates, inserts.
func Build(kind driver.Kind, d driver.Dialect, t Table, cs ChangeSet) ([]Statement, error) {
	if cs.Table != t.Name || cs.Schema != t.Schema {
		return nil, setError(FailInvalid, fmt.Errorf("%w: change set is for %q, not %q", ErrUnknownTable, cs.Table, t.Name))
	}
	pk := t.PrimaryKey()
	if len(pk) == 0 {
		return nil, setError(FailInvalid, ErrNoPrimaryKey)
	}
	if cs.Len() == 0 {
		return nil, setError(FailInvalid, ErrEmpty)
	}
	out := make([]Statement, 0, cs.Len())
	seen := map[string]string{} // key → "delete #n" / "update #n"
	for i, del := range cs.Deletes {
		b := &sqlb{kind: kind, d: d}
		b.raw("DELETE FROM ")
		b.table(t)
		key, err := b.where(t, pk, del.PK, OpDelete, i)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[key]; dup {
			return nil, changeError(OpDelete, i, "", fmt.Errorf("%w: the row is already changed by %s", ErrInvalidValue, prev))
		}
		seen[key] = fmt.Sprintf("delete #%d", i+1)
		out = append(out, b.statement(OpDelete, i))
	}
	for i, up := range cs.Updates {
		st, key, err := buildUpdate(kind, d, t, pk, up, i)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[key]; dup {
			return nil, changeError(OpUpdate, i, "", fmt.Errorf("%w: the row is already changed by %s", ErrInvalidValue, prev))
		}
		seen[key] = fmt.Sprintf("update #%d", i+1)
		out = append(out, st)
	}
	for i, in := range cs.Inserts {
		st, err := buildInsert(kind, d, t, pk, in, i)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

func (b *sqlb) statement(op Op, index int) Statement {
	return Statement{Op: op, Index: index, SQL: b.exec.String(), Args: b.args, Display: b.disp.String()}
}

// where writes " WHERE k1 = ? AND k2 = ?" for the original key cells and
// returns a key identifying the row (for duplicate detection).
func (b *sqlb) where(t Table, pk []driver.Column, cells map[string]values.Cell, op Op, index int) (string, error) {
	for name := range cells {
		if _, ok := t.column(name); !ok {
			return "", changeError(op, index, name, ErrUnknownColumn)
		}
	}
	if len(cells) > len(pk) {
		return "", changeError(op, index, "", fmt.Errorf("%w: the key must give exactly the primary key columns", ErrInvalidValue))
	}
	b.raw(" WHERE ")
	var key strings.Builder
	for i, c := range pk {
		cell, ok := cells[c.Name]
		if !ok {
			return "", changeError(op, index, c.Name, fmt.Errorf("%w: missing primary key value", ErrInvalidValue))
		}
		p, err := keyParam(b.kind, c, cell)
		if err != nil {
			return "", changeError(op, index, c.Name, err)
		}
		if i > 0 {
			b.raw(" AND ")
		}
		b.ident(c.Name)
		b.raw(" = ")
		b.param(p)
		key.WriteString(p.display)
		key.WriteByte(0)
	}
	return key.String(), nil
}

// sortedColumns returns the columns named in m in table order, or an error
// for an unknown name.
func sortedColumns[V any](t Table, m map[string]V, op Op, index int) ([]driver.Column, error) {
	cols := make([]driver.Column, 0, len(m))
	for name := range m {
		c, ok := t.column(name)
		if !ok {
			return nil, changeError(op, index, name, ErrUnknownColumn)
		}
		cols = append(cols, c)
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].Ordinal < cols[j].Ordinal })
	return cols, nil
}

func buildUpdate(kind driver.Kind, d driver.Dialect, t Table, pk []driver.Column, up Update, i int) (Statement, string, error) {
	if len(up.Set) == 0 {
		return Statement{}, "", changeError(OpUpdate, i, "", fmt.Errorf("%w: nothing to set", ErrInvalidValue))
	}
	cols, err := sortedColumns(t, up.Set, OpUpdate, i)
	if err != nil {
		return Statement{}, "", err
	}
	b := &sqlb{kind: kind, d: d}
	b.raw("UPDATE ")
	b.table(t)
	b.raw(" SET ")
	for n, c := range cols {
		old, ok := up.Old[c.Name]
		switch {
		case !ok:
			return Statement{}, "", changeError(OpUpdate, i, c.Name, fmt.Errorf("%w: the original value is missing", ErrInvalidValue))
		case old.Trunc:
			return Statement{}, "", changeError(OpUpdate, i, c.Name, ErrTruncated)
		}
		ev := up.Set[c.Name]
		if ev.Kind == ValDefault && kind == driver.SQLite {
			return Statement{}, "", changeError(OpUpdate, i, c.Name, fmt.Errorf("%w: SQLite cannot set a column to DEFAULT in an UPDATE", ErrInvalidValue))
		}
		if n > 0 {
			b.raw(", ")
		}
		b.ident(c.Name)
		b.raw(" = ")
		if err := b.value(c, ev); err != nil {
			return Statement{}, "", changeError(OpUpdate, i, c.Name, err)
		}
	}
	key, err := b.where(t, pk, up.PK, OpUpdate, i)
	if err != nil {
		return Statement{}, "", err
	}
	return b.statement(OpUpdate, i), key, nil
}

// value writes NULL, DEFAULT or a bound parameter for ev.
func (b *sqlb) value(c driver.Column, ev EditValue) error {
	switch ev.Kind {
	case ValNull:
		b.raw("NULL")
	case ValDefault:
		b.raw("DEFAULT")
	case ValBytes:
		raw, err := decodeHex(ev.Hex)
		if err != nil {
			return err
		}
		b.param(param{bound: raw, display: bytesLiteral(b.kind, raw)})
	case ValText:
		b.param(textParam(b.kind, c, ev.Text))
	default:
		return fmt.Errorf("%w: unknown value kind %q", ErrInvalidValue, ev.Kind)
	}
	return nil
}

func buildInsert(kind driver.Kind, d driver.Dialect, t Table, pk []driver.Column, in Insert, i int) (Statement, error) {
	cols, err := sortedColumns(t, in.Values, OpInsert, i)
	if err != nil {
		return Statement{}, err
	}
	if kind == driver.SQLite {
		// SQLite has no DEFAULT keyword in VALUES: leave the column out.
		cols = slices.DeleteFunc(cols, func(c driver.Column) bool { return in.Values[c.Name].Kind == ValDefault })
	}
	b := &sqlb{kind: kind, d: d}
	b.raw("INSERT INTO ")
	b.table(t)
	switch {
	case len(cols) == 0 && kind == driver.MySQL, len(cols) == 0 && kind == driver.MariaDB:
		b.raw(" () VALUES ()")
	case len(cols) == 0:
		b.raw(" DEFAULT VALUES")
	default:
		b.raw(" (")
		for n, c := range cols {
			if n > 0 {
				b.raw(", ")
			}
			b.ident(c.Name)
		}
		b.raw(") VALUES (")
		for n, c := range cols {
			if n > 0 {
				b.raw(", ")
			}
			if err := b.value(c, in.Values[c.Name]); err != nil {
				return Statement{}, changeError(OpInsert, i, c.Name, err)
			}
		}
		b.raw(")")
	}
	st := b.statement(OpInsert, i)
	switch {
	case kind == driver.Postgres:
		names := make([]string, len(pk))
		for n, c := range pk {
			names[n] = d.QuoteIdent(c.Name)
		}
		ret := " RETURNING " + strings.Join(names, ", ")
		st.SQL += ret
		st.Display += ret
		st.returning = pk
	case len(pk) == 1 && values.Categorize(pk[0].DataType) == values.CatInt:
		// MySQL: the auto-increment value; SQLite: the rowid (it is the
		// key when the key is an INTEGER PRIMARY KEY).
		if kind != driver.SQLite || strings.EqualFold(pk[0].DataType, "INTEGER") {
			st.rowidKey = pk[0].Name
		}
	}
	return st, nil
}

// ---- Value conversion ---------------------------------------------------

func decodeHex(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	s = strings.ReplaceAll(s, " ", "")
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: not valid hex", ErrInvalidValue)
	}
	return b, nil
}

func boolWord(s string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// mysqlTime turns an ISO "2024-01-02T03:04:05" into MySQL's preferred
// "2024-01-02 03:04:05" (MariaDB and MySQL both accept the space).
func mysqlTime(s string) string {
	if len(s) > 10 && s[10] == 'T' && s[4] == '-' {
		return s[:10] + " " + s[11:]
	}
	return s
}

func isTimeCat(cat values.Category) bool {
	return cat == values.CatDate || cat == values.CatTimestamp || cat == values.CatTimestampTZ
}

// intParam converts integer text exactly; ok is false when it is not one.
func intParam(s string) (param, bool) {
	t := strings.TrimSpace(s)
	if i, err := strconv.ParseInt(t, 10, 64); err == nil {
		return param{bound: i, display: strconv.FormatInt(i, 10)}, true
	}
	if u, err := strconv.ParseUint(t, 10, 64); err == nil {
		return param{bound: u, display: strconv.FormatUint(u, 10)}, true
	}
	return param{}, false
}

// BindText converts text typed (or imported) for column c into the value to
// bind, with the same rules as grid edits (see the package docs): a Go
// string on PostgreSQL (the server parses it with the column type's input
// function; run with ExecArgs), integers and booleans as int64/uint64 on
// MySQL/MariaDB/SQLite, MySQL date-times with a space separator.
func BindText(kind driver.Kind, c driver.Column, text string) any {
	return textParam(kind, c, text).bound
}

// ExecArgs prepends what the engine needs to bind BindText values: on
// PostgreSQL pgx's QueryExecModeDescribeExec, so the server infers every
// parameter's type from its context and no statement is cached.
func ExecArgs(kind driver.Kind, args []any) []any {
	if kind == driver.Postgres {
		return append([]any{pgx.QueryExecModeDescribeExec}, args...)
	}
	return args
}

// DecodeHex decodes binary data given as hex (an optional 0x or \x prefix
// and spaces are allowed).
func DecodeHex(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, `\x`)
	return decodeHex(s)
}

// textParam converts typed text for a column (see the package docs).
func textParam(kind driver.Kind, c driver.Column, text string) param {
	cat := values.Categorize(c.DataType)
	switch kind {
	case driver.Postgres:
		return param{bound: text, display: pgTextLiteral(cat, text)}
	case driver.MySQL, driver.MariaDB, driver.SQLite:
		switch {
		case cat == values.CatInt || cat == values.CatBool:
			if b, ok := boolWord(text); ok {
				return param{bound: b2i(b), display: strconv.FormatInt(b2i(b), 10)}
			}
			if p, ok := intParam(text); ok {
				return p
			}
		case kind != driver.SQLite && isTimeCat(cat):
			text = mysqlTime(text)
		}
	}
	return param{bound: text, display: quote(kind, text)}
}

// looksNumeric reports a plain decimal number ("-12", "3.50", "1e5"), safe
// to show unquoted. NaN, Infinity, hex and digit separators are quoted.
func looksNumeric(s string) bool {
	if s == "" || strings.ContainsAny(s, "xXpP_iInN") {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// pgTextLiteral renders text for a PostgreSQL column: numbers and booleans
// bare when they are valid, everything else as a string literal (which
// PostgreSQL coerces to the column type, like the bound text).
func pgTextLiteral(cat values.Category, text string) string {
	switch cat {
	case values.CatInt, values.CatDecimal, values.CatFloat:
		if looksNumeric(text) {
			return text
		}
	case values.CatBool:
		if b, ok := boolWord(text); ok {
			return strings.ToUpper(strconv.FormatBool(b))
		}
	}
	return quote(driver.Postgres, text)
}

// numberText returns the exact text of a num cell's value.
func numberText(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e21 {
			return strconv.FormatFloat(x, 'f', -1, 64), nil
		}
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 32), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case int:
		return strconv.Itoa(x), nil
	case int32:
		return strconv.FormatInt(int64(x), 10), nil
	case uint64:
		return strconv.FormatUint(x, 10), nil
	case interface{ String() string }: // json.Number
		return x.String(), nil
	}
	return "", fmt.Errorf("%w: unexpected number %T", ErrInvalidValue, v)
}

// keyParam converts an original key cell (as FetchTable returned it) back
// into a value that matches the stored key exactly.
func keyParam(kind driver.Kind, c driver.Column, cell values.Cell) (param, error) {
	if cell.Trunc {
		return param{}, ErrTruncated
	}
	cat := values.Categorize(c.DataType)
	switch cell.K {
	case values.Null:
		return param{}, fmt.Errorf("%w: a NULL key cannot identify a row", ErrInvalidValue)
	case values.Bytes:
		s, _ := cell.V.(string)
		raw, err := decodeHex(s)
		if err != nil {
			return param{}, err
		}
		return param{bound: raw, display: bytesLiteral(kind, raw)}, nil
	case values.Bool:
		b, _ := cell.V.(bool)
		if kind == driver.Postgres {
			return param{bound: strconv.FormatBool(b), display: strings.ToUpper(strconv.FormatBool(b))}, nil
		}
		return param{bound: b2i(b), display: strconv.FormatInt(b2i(b), 10)}, nil
	case values.Num:
		s, err := numberText(cell.V)
		if err != nil {
			return param{}, err
		}
		return numKeyParam(kind, cat, s), nil
	case values.Str, values.JSON, values.Time:
		s, ok := cell.V.(string)
		if !ok {
			return param{}, fmt.Errorf("%w: expected text, got %T", ErrInvalidValue, cell.V)
		}
		if kind != driver.Postgres && kind != driver.SQLite && isTimeCat(cat) {
			s = mysqlTime(s)
		}
		return param{bound: s, display: quote(kind, s)}, nil
	}
	return param{}, fmt.Errorf("%w: unknown cell kind %q", ErrInvalidValue, cell.K)
}

func numKeyParam(kind driver.Kind, cat values.Category, s string) param {
	switch kind {
	case driver.Postgres:
		return param{bound: s, display: s}
	case driver.MySQL, driver.MariaDB:
		switch cat {
		case values.CatDecimal:
			// A string compared with a DECIMAL column is compared as a
			// double; compare as a decimal instead.
			return param{bound: s, display: quote(kind, s), cast: "DECIMAL(65,30)"}
		case values.CatFloat:
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return param{bound: f, display: s}
			}
		}
		if p, ok := intParam(s); ok {
			return p
		}
	case driver.SQLite:
		if p, ok := intParam(s); ok {
			return p
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return param{bound: f, display: s}
		}
	}
	return param{bound: s, display: quote(kind, s)}
}

// ---- Display literals (preview only; never executed) --------------------

// quote renders s as a string literal of kind's dialect.
//
//   - PostgreSQL (standard_conforming_strings on): '…' with embedded quotes
//     doubled; strings with control characters use E'…' with backslash
//     escapes.
//   - MySQL/MariaDB (default sql_mode, without NO_BACKSLASH_ESCAPES):
//     '…' with \\, \', \0, \n, \r, \Z escaped.
//   - SQLite: '…' with embedded quotes doubled; a string containing NUL is
//     shown as CAST(X'…' AS TEXT).
func quote(kind driver.Kind, s string) string {
	switch kind {
	case driver.MySQL, driver.MariaDB:
		var b strings.Builder
		b.WriteByte('\'')
		for i := 0; i < len(s); i++ {
			switch ch := s[i]; ch {
			case 0:
				b.WriteString(`\0`)
			case '\'':
				b.WriteString(`\'`)
			case '\\':
				b.WriteString(`\\`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case 0x1a:
				b.WriteString(`\Z`)
			default:
				b.WriteByte(ch)
			}
		}
		b.WriteByte('\'')
		return b.String()
	case driver.SQLite:
		if strings.IndexByte(s, 0) >= 0 {
			return "CAST(X'" + hex.EncodeToString([]byte(s)) + "' AS TEXT)"
		}
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	if !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	var b strings.Builder
	b.WriteString("E'")
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; {
		case ch == '\\':
			b.WriteString(`\\`)
		case ch == '\'':
			b.WriteString(`\'`)
		case ch == '\n':
			b.WriteString(`\n`)
		case ch == '\r':
			b.WriteString(`\r`)
		case ch == '\t':
			b.WriteString(`\t`)
		case ch < 0x20 || ch == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, ch)
		default:
			b.WriteByte(ch)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// bytesLiteral renders binary data: '\x…'::bytea on PostgreSQL, X'…'
// elsewhere.
func bytesLiteral(kind driver.Kind, b []byte) string {
	h := hex.EncodeToString(b)
	if kind == driver.Postgres {
		return `'\x` + h + `'::bytea`
	}
	return "X'" + h + "'"
}

// ---- Preview and Apply --------------------------------------------------

// PreviewStatement is one statement of a preview.
type PreviewStatement struct {
	Op    Op  `json:"op"`
	Index int `json:"index"`
	// SQL is display SQL with literals. It is NEVER executed: Apply runs
	// the same statement with bound parameters.
	SQL string `json:"sql"`
}

// Preview validates cs against the live table and returns the statements
// Apply would run, rendered as display SQL. Nothing is executed.
func Preview(ctx context.Context, db *sql.DB, e Engine, cs ChangeSet) ([]PreviewStatement, error) {
	t, err := LoadTable(ctx, db, e, cs.Schema, cs.Table)
	if err != nil {
		return nil, err
	}
	stmts, err := Build(e.Kind(), e, t, cs)
	if err != nil {
		return nil, err
	}
	out := make([]PreviewStatement, len(stmts))
	for i, st := range stmts {
		out[i] = PreviewStatement{Op: st.Op, Index: st.Index, SQL: st.Display + ";"}
	}
	return out, nil
}

// Options controls Apply.
type Options struct {
	// ReadOnly refuses to apply anything (the connection's "readonly"
	// safe mode; the database would reject the writes too).
	ReadOnly bool
}

// Result reports what Apply changed.
type Result struct {
	Deleted  int `json:"deleted"`
	Updated  int `json:"updated"`
	Inserted int `json:"inserted"`
	// InsertedKeys has one entry per insert: the new row's primary key
	// when it is cheap to know (PostgreSQL RETURNING, MySQL
	// auto-increment, SQLite rowid), otherwise null.
	InsertedKeys []map[string]values.Cell `json:"insertedKeys"`
}

// Apply validates cs against the live table and runs it in one
// transaction: deletes, then updates, then inserts. Every UPDATE and
// DELETE must affect exactly one row. Any failure rolls everything back
// and returns a *ChangeError naming the failing change.
func Apply(ctx context.Context, db *sql.DB, e Engine, cs ChangeSet, opts Options) (*Result, error) {
	if opts.ReadOnly {
		return nil, setError(FailReadOnly, ErrReadOnly)
	}
	kind := e.Kind()
	t, err := LoadTable(ctx, db, e, cs.Schema, cs.Table)
	if err != nil {
		return nil, err
	}
	stmts, err := Build(kind, e, t, cs)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, &ChangeError{Index: -1, Kind: FailDatabase, Message: err.Error(), Err: err}
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	res := &Result{InsertedKeys: make([]map[string]values.Cell, len(cs.Inserts))}
	for _, st := range stmts {
		if err := run(ctx, tx, kind, st, res); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, &ChangeError{Index: -1, Kind: FailDatabase, Message: "commit failed: " + err.Error(), Err: err}
	}
	committed = true
	return res, nil
}

func failure(st Statement, kind FailureKind, err error, detail string) *ChangeError {
	return &ChangeError{
		Op: st.Op, Index: st.Index, Kind: kind, SQL: st.Display, Err: err,
		Message: fmt.Sprintf("%s #%d failed: %s. Nothing was saved.", st.Op, st.Index+1, detail),
	}
}

func run(ctx context.Context, tx *sql.Tx, kind driver.Kind, st Statement, res *Result) error {
	// PostgreSQL: describe first so the server infers parameter types; no
	// statement cache, so schema changes never break a commit.
	args := ExecArgs(kind, st.Args)
	if len(st.returning) > 0 {
		dest := make([]any, len(st.returning))
		ptrs := make([]any, len(dest))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := tx.QueryRowContext(ctx, st.SQL, args...).Scan(ptrs...); err != nil {
			return failure(st, FailDatabase, err, err.Error())
		}
		key := make(map[string]values.Cell, len(dest))
		for i, c := range st.returning {
			key[c.Name] = values.Convert(dest[i], c.DataType)
		}
		res.InsertedKeys[st.Index] = key
		res.Inserted++
		return nil
	}
	r, err := tx.ExecContext(ctx, st.SQL, args...)
	if err != nil {
		return failure(st, FailDatabase, err, err.Error())
	}
	if st.Op == OpInsert {
		res.Inserted++
		if st.rowidKey != "" {
			if id, err := r.LastInsertId(); err == nil && id != 0 {
				res.InsertedKeys[st.Index] = map[string]values.Cell{st.rowidKey: values.ConvertCat(id, values.CatInt)}
			}
		}
		return nil
	}
	n, err := r.RowsAffected()
	if err != nil {
		return failure(st, FailDatabase, err, err.Error())
	}
	switch {
	case n == 0:
		return failure(st, FailStale, ErrStale, ErrStale.Error())
	case n > 1:
		return failure(st, FailDuplicate, ErrDuplicate, fmt.Sprintf("%s (%d rows)", ErrDuplicate, n))
	}
	if st.Op == OpDelete {
		res.Deleted++
	} else {
		res.Updated++
	}
	return nil
}
