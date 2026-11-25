package query

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	dbdriver "lontar/internal/driver"
	"lontar/internal/sqlsplit"

	"github.com/jackc/pgx/v5"
)

// Parameter value types.
const (
	ParamText   = "text"
	ParamNumber = "number"
)

// ParamValue is the value of one :name placeholder of the SQL editor. It
// is always bound as a parameter, never written into the SQL.
type ParamValue struct {
	Value string `json:"value"`
	// Null binds NULL (Value is ignored).
	Null bool `json:"null"`
	// Type is ParamText (the default: a string, cast by the database) or
	// ParamNumber (an integer or a float, e.g. for LIMIT).
	Type string `json:"type"`
}

// ParamNames returns the distinct :name placeholders of a script, in order
// of first use.
func ParamNames(kind dbdriver.Kind, sqlText string) []string {
	return sqlsplit.ParamNames(kind, sqlText)
}

// MissingParams returns the :name placeholders of a script that have no
// value in values, in order of first use.
func MissingParams(kind dbdriver.Kind, sqlText string, values map[string]ParamValue) []string {
	missing := []string{}
	for _, st := range sqlsplit.Split(kind, sqlText) {
		if st.Meta {
			continue
		}
		for _, p := range sqlsplit.Params(kind, st.Text) {
			if _, ok := values[p.Name]; !ok && !slices.Contains(missing, p.Name) {
				missing = append(missing, p.Name)
			}
		}
	}
	return missing
}

// checkParams returns the placeholders of stmts that have no value, and an
// error for a value that cannot be bound (a Number that is not a number).
func checkParams(kind dbdriver.Kind, stmts []sqlsplit.Statement, values map[string]ParamValue) ([]string, error) {
	missing := []string{}
	seen := map[string]bool{}
	for _, st := range stmts {
		if st.Meta {
			continue
		}
		for _, p := range sqlsplit.Params(kind, st.Text) {
			if seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			v, ok := values[p.Name]
			if !ok {
				missing = append(missing, p.Name)
				continue
			}
			if _, err := paramArg(p.Name, v); err != nil {
				return nil, err
			}
		}
	}
	return missing, nil
}

// paramArg converts a value to the Go value that is bound.
func paramArg(name string, v ParamValue) (any, error) {
	switch {
	case v.Null:
		return nil, nil
	case v.Type == ParamNumber:
		s := strings.TrimSpace(v.Value)
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, nil
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, nil
		}
		return nil, fmt.Errorf("parameter :%s: %q is not a number", name, v.Value)
	case v.Type == "" || v.Type == ParamText:
		return v.Value, nil
	}
	return nil, fmt.Errorf("parameter :%s: unknown type %q", name, v.Type)
}

// boundStatement is a statement with its :name placeholders replaced by
// the engine's bind placeholders.
type boundStatement struct {
	SQL string
	// Args are the values to bind, without driver options (nil: the
	// statement has no placeholders).
	Args []any
	// repl are the replaced ranges, in order.
	repl []replacement
}

type replacement struct{ newStart, newEnd, oldStart, oldEnd int }

// Pos maps a byte offset in SQL back to one in the original statement; an
// offset inside a bind placeholder points at its :name.
func (b boundStatement) Pos(off int) int {
	i := sort.Search(len(b.repl), func(i int) bool { return b.repl[i].newStart > off }) - 1
	if i < 0 {
		return off
	}
	r := b.repl[i]
	if off < r.newEnd {
		return r.oldStart
	}
	return off - r.newEnd + r.oldEnd
}

// driverArgs returns the arguments for QueryContext/ExecContext: the
// engine's driver options and the bound values. With values, PostgreSQL
// describes the statement first so the server infers each parameter's
// type from its context (an int column compared with a text value works),
// still without caching anything on the connection.
func (b boundStatement) driverArgs(kind dbdriver.Kind) []any {
	if b.Args == nil {
		return statementArgs(kind)
	}
	if kind == dbdriver.Postgres {
		return append([]any{pgx.QueryExecModeDescribeExec}, b.Args...)
	}
	return b.Args
}

// bindParams replaces the :name placeholders of text: PostgreSQL gets $n
// (one number per name), MySQL/MariaDB and SQLite one ? per occurrence.
// Every placeholder must have a value (see checkParams).
func bindParams(kind dbdriver.Kind, text string, values map[string]ParamValue) (boundStatement, error) {
	params := sqlsplit.Params(kind, text)
	if len(params) == 0 {
		return boundStatement{SQL: text}, nil
	}
	dialect := dbdriver.DialectFor(kind)
	var sb strings.Builder
	out := boundStatement{Args: []any{}}
	numbers := map[string]int{}
	last := 0
	for _, p := range params {
		v, ok := values[p.Name]
		if !ok {
			return boundStatement{}, fmt.Errorf("parameter :%s has no value", p.Name)
		}
		var ph string
		if kind == dbdriver.Postgres {
			n, seen := numbers[p.Name]
			if !seen {
				arg, err := paramArg(p.Name, v)
				if err != nil {
					return boundStatement{}, err
				}
				out.Args = append(out.Args, arg)
				n = len(out.Args)
				numbers[p.Name] = n
			}
			ph = dialect.Placeholder(n)
		} else {
			arg, err := paramArg(p.Name, v)
			if err != nil {
				return boundStatement{}, err
			}
			out.Args = append(out.Args, arg)
			ph = dialect.Placeholder(len(out.Args))
		}
		sb.WriteString(text[last:p.Start])
		start := sb.Len()
		sb.WriteString(ph)
		out.repl = append(out.repl, replacement{newStart: start, newEnd: sb.Len(), oldStart: p.Start, oldEnd: p.End})
		last = p.End
	}
	sb.WriteString(text[last:])
	out.SQL = sb.String()
	return out, nil
}
