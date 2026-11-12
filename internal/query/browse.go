// Package query builds and runs the SQL behind dbedit's table browser.
//
// # Safety
//
// Every user-supplied value (filter operands) is a bound parameter. Schema,
// table and column names are checked against introspected metadata
// (Introspector.Columns) and then quoted by the dialect. The single
// exception is Request.RawWhere: an explicit escape hatch that accepts a
// user-typed SQL condition verbatim (like the SQL editor does), because it
// is the user's own database. It is wrapped in parentheses and ANDed with
// the structured filters, but it is not validated.
package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"dbedit/internal/driver"
	"dbedit/internal/values"
)

// Engine is what the browser needs from a driver: SQL generation and
// metadata lookup. driver.Driver satisfies it.
type Engine interface {
	driver.Dialect
	driver.Introspector
}

// Paging limits.
const (
	DefaultLimit = 300
	MaxLimit     = 5000
)

// Op is a filter operator.
type Op string

// Filter operators.
const (
	OpEq          Op = "="
	OpNe          Op = "!="
	OpLt          Op = "<"
	OpGt          Op = ">"
	OpLe          Op = "<="
	OpGe          Op = ">="
	OpContains    Op = "contains"
	OpNotContains Op = "notContains"
	OpStartsWith  Op = "startsWith"
	OpEndsWith    Op = "endsWith"
	OpIn          Op = "in"
	OpNotIn       Op = "notIn"
	OpIsNull      Op = "isNull"
	OpIsNotNull   Op = "isNotNull"
	OpBetween     Op = "between"
)

// Ops lists every operator.
var Ops = []Op{OpEq, OpNe, OpLt, OpGt, OpLe, OpGe, OpContains, OpNotContains, OpStartsWith, OpEndsWith,
	OpIn, OpNotIn, OpIsNull, OpIsNotNull, OpBetween}

// Filter is one condition. Value is the operand; Value2 is the upper bound
// for between. For in/notIn, Value is a comma-separated list (items are
// trimmed).
type Filter struct {
	Column string `json:"column"`
	Op     Op     `json:"op"`
	Value  string `json:"value"`
	Value2 string `json:"value2"`
}

// Sort orders by one column.
type Sort struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc"`
}

// Request describes one page of a table.
type Request struct {
	Schema  string   `json:"schema"`
	Table   string   `json:"table"`
	Filters []Filter `json:"filters"`
	// RawWhere is an optional user-typed SQL condition, used verbatim (see
	// the package documentation). It is ANDed with Filters.
	RawWhere string `json:"rawWhere"`
	Sort     []Sort `json:"sort"`
	Offset   int    `json:"offset"`
	// Limit is the page size: 0 means DefaultLimit; capped at MaxLimit.
	Limit int `json:"limit"`
}

// ResultColumn describes a column of a page.
type ResultColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	IsPK     bool   `json:"isPK"`
	Nullable bool   `json:"nullable"`
	// HasDefault reports a column default (table pages only).
	HasDefault bool `json:"hasDefault,omitempty"`
	// EnumValues lists an enum column's values (table pages only).
	EnumValues []string `json:"enumValues,omitempty"`
}

// Page is one page of rows.
type Page struct {
	Columns []ResultColumn  `json:"columns"`
	Rows    [][]values.Cell `json:"rows"`
	Offset  int             `json:"offset"`
	Limit   int             `json:"limit"`
	HasMore bool            `json:"hasMore"`
	// TotalCount is set only when known without an extra query (a
	// partial last page); otherwise use CountRows.
	TotalCount *int64 `json:"totalCount"`
	ElapsedMs  int64  `json:"elapsedMs"`
}

// Errors.
var (
	ErrUnknownTable  = errors.New("table not found")
	ErrUnknownColumn = errors.New("unknown column")
	ErrBadFilter     = errors.New("invalid filter")
)

// Built is a generated statement and its arguments.
type Built struct {
	SQL  string
	Args []any
}

// builder accumulates SQL arguments and their placeholders.
type builder struct {
	d    driver.Dialect
	args []any
}

func (b *builder) arg(v any) string {
	b.args = append(b.args, v)
	return b.d.Placeholder(len(b.args))
}

// likeEscape is the escape character for LIKE patterns. '!' is not special
// in string literals on any engine (unlike '\' on MySQL).
const likeEscape = "!"

func escapeLike(s string) string {
	r := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	return r.Replace(s)
}

// condition renders one filter for the already-quoted column expression.
func (b *builder) condition(col string, f Filter) (string, error) {
	switch f.Op {
	case OpEq, OpLt, OpGt, OpLe, OpGe:
		return col + " " + string(f.Op) + " " + b.arg(f.Value), nil
	case OpNe:
		return col + " <> " + b.arg(f.Value), nil
	case OpContains, OpNotContains, OpStartsWith, OpEndsWith:
		pat := escapeLike(f.Value)
		switch f.Op {
		case OpStartsWith:
			pat += "%"
		case OpEndsWith:
			pat = "%" + pat
		default:
			pat = "%" + pat + "%"
		}
		not := ""
		if f.Op == OpNotContains {
			not = "NOT "
		}
		return b.d.TextExpr(col) + " " + not + b.d.CaseInsensitiveLike() + " " + b.arg(pat) + " ESCAPE '" + likeEscape + "'", nil
	case OpIn, OpNotIn:
		var ph []string
		for _, item := range strings.Split(f.Value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				ph = append(ph, b.arg(item))
			}
		}
		if len(ph) == 0 {
			return "", fmt.Errorf("%w: %s on %q needs at least one value", ErrBadFilter, f.Op, f.Column)
		}
		not := ""
		if f.Op == OpNotIn {
			not = "NOT "
		}
		return col + " " + not + "IN (" + strings.Join(ph, ", ") + ")", nil
	case OpIsNull:
		return col + " IS NULL", nil
	case OpIsNotNull:
		return col + " IS NOT NULL", nil
	case OpBetween:
		return col + " BETWEEN " + b.arg(f.Value) + " AND " + b.arg(f.Value2), nil
	}
	return "", fmt.Errorf("%w: unknown operator %q", ErrBadFilter, f.Op)
}

// columnSet indexes the introspected columns by exact name.
func columnSet(cols []driver.Column) map[string]bool {
	m := make(map[string]bool, len(cols))
	for _, c := range cols {
		m[c.Name] = true
	}
	return m
}

func (b *builder) checkColumn(known map[string]bool, name string) error {
	if !known[name] {
		return fmt.Errorf("%w %q", ErrUnknownColumn, name)
	}
	return nil
}

// from renders the quoted, schema-qualified table.
func from(d driver.Dialect, schema, table string) string {
	if schema == "" {
		return d.QuoteIdent(table)
	}
	return d.QuoteIdent(schema) + "." + d.QuoteIdent(table)
}

// where renders the WHERE clause (with a leading space) or "".
func (b *builder) where(known map[string]bool, req Request) (string, error) {
	var conds []string
	for _, f := range req.Filters {
		if err := b.checkColumn(known, f.Column); err != nil {
			return "", err
		}
		c, err := b.condition(b.d.QuoteIdent(f.Column), f)
		if err != nil {
			return "", err
		}
		conds = append(conds, c)
	}
	if raw := strings.TrimSpace(req.RawWhere); raw != "" {
		conds = append(conds, "("+raw+")")
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), nil
}

// NormalizeLimit applies the default and maximum page size.
func NormalizeLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultLimit
	case limit > MaxLimit:
		return MaxLimit
	}
	return limit
}

// BuildSelect generates the page query for req against the introspected
// columns cols. It fetches limit+1 rows so the caller can tell whether
// there is a next page. Without an explicit sort it orders by the primary
// key (when there is one) so that paging is stable.
func BuildSelect(d driver.Dialect, cols []driver.Column, req Request) (Built, error) {
	b, err := buildSelect(d, cols, req)
	if err != nil {
		return Built{}, err
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	b.SQL += d.LimitOffset(NormalizeLimit(req.Limit)+1, offset)
	return b, nil
}

// BuildSelectAll is BuildSelect without LIMIT/OFFSET: every row matching
// the filters, in the requested order (or primary-key order). Used to
// stream a whole table for export.
func BuildSelectAll(d driver.Dialect, cols []driver.Column, req Request) (Built, error) {
	return buildSelect(d, cols, req)
}

func buildSelect(d driver.Dialect, cols []driver.Column, req Request) (Built, error) {
	if len(cols) == 0 {
		return Built{}, fmt.Errorf("%w: %q", ErrUnknownTable, req.Table)
	}
	known := columnSet(cols)
	b := &builder{d: d}
	sel := make([]string, len(cols))
	for i, c := range cols {
		sel[i] = d.QuoteIdent(c.Name)
	}
	where, err := b.where(known, req)
	if err != nil {
		return Built{}, err
	}
	var order []string
	for _, s := range req.Sort {
		if err := b.checkColumn(known, s.Column); err != nil {
			return Built{}, err
		}
		dir := " ASC"
		if s.Desc {
			dir = " DESC"
		}
		order = append(order, d.QuoteIdent(s.Column)+dir)
	}
	if len(order) == 0 {
		order = pkOrder(d, cols)
	}
	q := "SELECT " + strings.Join(sel, ", ") + " FROM " + from(d, req.Schema, req.Table) + where
	if len(order) > 0 {
		q += " ORDER BY " + strings.Join(order, ", ")
	}
	return Built{SQL: q, Args: b.args}, nil
}

// LookupColumns introspects req's table and fails with ErrUnknownTable
// when it does not exist (see FetchTable).
func LookupColumns(ctx context.Context, db *sql.DB, e Engine, req Request) ([]driver.Column, error) {
	return lookupColumns(ctx, db, e, req)
}

// pkOrder returns the primary key columns in key order, ascending.
func pkOrder(d driver.Dialect, cols []driver.Column) []string {
	pk := slices.Clone(cols)
	pk = slices.DeleteFunc(pk, func(c driver.Column) bool { return !c.IsPrimaryKey })
	slices.SortStableFunc(pk, func(a, b driver.Column) int { return a.PKPosition - b.PKPosition })
	out := make([]string, len(pk))
	for i, c := range pk {
		out[i] = d.QuoteIdent(c.Name) + " ASC"
	}
	return out
}

// BuildCount generates SELECT COUNT(*) with req's filters.
func BuildCount(d driver.Dialect, cols []driver.Column, req Request) (Built, error) {
	if len(cols) == 0 {
		return Built{}, fmt.Errorf("%w: %q", ErrUnknownTable, req.Table)
	}
	b := &builder{d: d}
	where, err := b.where(columnSet(cols), req)
	if err != nil {
		return Built{}, err
	}
	return Built{SQL: "SELECT COUNT(*) FROM " + from(d, req.Schema, req.Table) + where, Args: b.args}, nil
}

// lookupColumns introspects the table; an empty result means it does not
// exist (or is not visible), which is what keeps unknown table names out
// of generated SQL.
func lookupColumns(ctx context.Context, db *sql.DB, e Engine, req Request) ([]driver.Column, error) {
	if req.Table == "" {
		return nil, fmt.Errorf("%w: no table given", ErrUnknownTable)
	}
	cols, err := e.Columns(ctx, db, req.Schema, req.Table)
	if err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTable, qualified(req.Schema, req.Table))
	}
	return cols, nil
}

func qualified(schema, table string) string {
	if schema == "" {
		return table
	}
	return schema + "." + table
}

// FetchTable returns one page of req.Table.
func FetchTable(ctx context.Context, db *sql.DB, e Engine, req Request) (*Page, error) {
	start := time.Now()
	cols, err := lookupColumns(ctx, db, e, req)
	if err != nil {
		return nil, err
	}
	q, err := BuildSelect(e, cols, req)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	limit := NormalizeLimit(req.Limit)
	offset := max(req.Offset, 0)
	page := &Page{Offset: offset, Limit: limit, Rows: [][]values.Cell{}}
	page.Columns = make([]ResultColumn, len(cols))
	cats := make([]values.Category, len(cols))
	types, _ := rows.ColumnTypes()
	for i, c := range cols {
		page.Columns[i] = ResultColumn{Name: c.Name, Type: c.DataType, IsPK: c.IsPrimaryKey, Nullable: c.Nullable,
			HasDefault: c.Default != nil, EnumValues: c.EnumValues}
		// The driver's wire type is the most precise hint for decoding;
		// fall back to the declared type (SQLite expressions, etc.).
		cats[i] = values.Categorize(c.DataType)
		if i < len(types) {
			if wire := values.Categorize(types[i].DatabaseTypeName()); wire != values.CatUnknown {
				cats[i] = wire
			}
		}
	}

	dest := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range dest {
		ptrs[i] = &dest[i]
	}
	for rows.Next() {
		if len(page.Rows) == limit {
			page.HasMore = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]values.Cell, len(cols))
		for i, v := range dest {
			row[i] = values.ConvertCat(v, cats[i])
		}
		page.Rows = append(page.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !page.HasMore && (len(page.Rows) > 0 || offset == 0) {
		total := int64(offset + len(page.Rows))
		page.TotalCount = &total
	}
	page.ElapsedMs = time.Since(start).Milliseconds()
	return page, nil
}

// CountRows returns the exact number of rows matching req's filters. It is
// a separate call so that a slow COUNT(*) never delays the first page.
func CountRows(ctx context.Context, db *sql.DB, e Engine, req Request) (int64, error) {
	cols, err := lookupColumns(ctx, db, e, req)
	if err != nil {
		return 0, err
	}
	q, err := BuildCount(e, cols, req)
	if err != nil {
		return 0, err
	}
	var n int64
	if err := db.QueryRowContext(ctx, q.SQL, q.Args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
