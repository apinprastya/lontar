package edits

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"lontar/internal/driver"
	"lontar/internal/query"
)

// SourceColumn maps one column of a query result to the table column it
// reads. Column is "" for an expression (or a repeat of a column), which
// stays read-only.
type SourceColumn struct {
	Column     string   `json:"column"`
	IsPK       bool     `json:"isPK"`
	Nullable   bool     `json:"nullable"`
	HasDefault bool     `json:"hasDefault"`
	EnumValues []string `json:"enumValues,omitempty"`
}

// Source says whether a SQL editor result can be edited like a table and,
// if so, which table and columns it maps to. Edits made through it are
// sent as an ordinary ChangeSet for Schema.Table and validated again by
// Preview/Apply.
type Source struct {
	Editable bool `json:"editable"`
	// Reason explains a read-only result (shown to the user).
	Reason string `json:"reason"`
	Schema string `json:"schema"`
	Table  string `json:"table"`
	// Columns has one entry per result column, in result order.
	Columns []SourceColumn `json:"columns"`
}

func readOnly(reason string) *Source {
	return &Source{Reason: reason, Columns: []SourceColumn{}}
}

// ResolveSource works out whether the result of text (a statement run in
// the SQL editor, with resultColumns columns) maps onto one table. The
// table is looked up in schema when text does not name one. Metadata
// errors are returned; everything else is a read-only Source with a
// reason.
func ResolveSource(ctx context.Context, db *sql.DB, e query.Engine, kind driver.Kind, text, schema string, resultColumns int) (*Source, error) {
	src, reason := query.ParseSelectSource(kind, text)
	if src == nil {
		return readOnly(reason), nil
	}
	if src.Schema != "" {
		schema = foldIdent(kind, src.Schema, src.SchemaQuoted)
	}
	if schema == "" && kind != driver.SQLite {
		return readOnly("No default schema is set, so the table cannot be found; qualify it with its schema."), nil
	}

	tables, err := e.ListTables(ctx, db, schema)
	if err != nil {
		return nil, err
	}
	name, ok := matchName(kind, src.Table, src.TableQuoted, len(tables), func(i int) string { return tables[i].Name })
	if !ok {
		return readOnly(fmt.Sprintf("The table %q was not found in %q, so the result is read-only.", src.Table, schema)), nil
	}
	t, err := LoadTable(ctx, db, e, schema, name)
	if err != nil {
		var ce *ChangeError
		if errors.As(err, &ce) {
			if errors.Is(err, ErrNotATable) {
				return readOnly(fmt.Sprintf("%s is a view; views are read-only.", name)), nil
			}
			return readOnly(ce.Error()), nil
		}
		return nil, err
	}
	if len(t.PrimaryKey()) == 0 {
		return readOnly(fmt.Sprintf("%s has no primary key, so its rows cannot be identified for editing.", name)), nil
	}

	// Expand the SELECT list to one table column (or "") per result column.
	var mapped []string
	for _, it := range src.Items {
		switch {
		case it.Star:
			for _, c := range t.Columns {
				mapped = append(mapped, c.Name)
			}
		case it.Column != "":
			col, ok := matchName(kind, it.Column, it.Quoted, len(t.Columns), func(i int) string { return t.Columns[i].Name })
			if !ok {
				// Not a column of the table (an alias of another column,
				// an outer reference, a function without parentheses).
				col = ""
			}
			mapped = append(mapped, col)
		default:
			mapped = append(mapped, "")
		}
	}
	if len(mapped) != resultColumns {
		return readOnly(query.ReasonUnsupported), nil
	}

	out := &Source{Editable: true, Schema: schema, Table: name, Columns: make([]SourceColumn, len(mapped))}
	seen := map[string]bool{}
	for i, m := range mapped {
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		c, _ := t.column(m)
		out.Columns[i] = SourceColumn{Column: c.Name, IsPK: c.IsPrimaryKey, Nullable: c.Nullable, HasDefault: c.Default != nil, EnumValues: c.EnumValues}
	}
	for _, pk := range t.PrimaryKey() {
		if !seen[pk.Name] {
			return readOnly(fmt.Sprintf("Add the primary key (%s) to the SELECT list to edit this result.", pkList(t))), nil
		}
	}
	return out, nil
}

func pkList(t Table) string {
	names := []string{}
	for _, c := range t.PrimaryKey() {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}

// foldIdent applies the engine's case rule to an identifier as written:
// PostgreSQL folds unquoted names to lower case.
func foldIdent(kind driver.Kind, name string, quoted bool) string {
	if kind == driver.Postgres && !quoted {
		return strings.ToLower(name)
	}
	return name
}

// matchName finds the catalog name an identifier refers to: an exact match
// (after PostgreSQL's folding), else, where the engine compares names
// without case (MySQL/MariaDB, SQLite, and unquoted PostgreSQL names that
// differ only in case are not equal there), one unique case-insensitive
// match.
func matchName(kind driver.Kind, written string, quoted bool, n int, at func(int) string) (string, bool) {
	want := foldIdent(kind, written, quoted)
	for i := range n {
		if at(i) == want {
			return at(i), true
		}
	}
	if kind == driver.Postgres {
		return "", false
	}
	found := ""
	for i := range n {
		if strings.EqualFold(at(i), want) {
			if found != "" {
				return "", false
			}
			found = at(i)
		}
	}
	return found, found != ""
}
