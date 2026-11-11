package driver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
)

// ErrTableNotFound is returned by TableInfo and TableDDL for a table or view
// that ListTables does not report.
var ErrTableNotFound = errors.New("table not found")

// TableDetail is the full, read-only structure of a table or view, as shown
// by the Structure view. Slices are never nil.
type TableDetail struct {
	Schema string    `json:"schema"`
	Name   string    `json:"name"`
	Type   TableType `json:"type"`
	// Comment is the table comment ("" when none).
	Comment string `json:"comment"`
	// Engine and Collation are the storage engine and default collation
	// (MySQL/MariaDB only).
	Engine    string `json:"engine"`
	Collation string `json:"collation"`
	// EstimatedRows is the server's cheap row estimate (nil when unknown).
	EstimatedRows *int64 `json:"estimatedRows"`
	// SizeBytes is the on-disk size including indexes where cheap
	// (PostgreSQL pg_total_relation_size, MySQL DATA_LENGTH+INDEX_LENGTH);
	// nil when unknown.
	SizeBytes *int64 `json:"sizeBytes"`

	Columns      []ColumnDetail `json:"columns"`
	Indexes      []Index        `json:"indexes"`
	ForeignKeys  []ForeignKey   `json:"foreignKeys"`
	ReferencedBy []ForeignKey   `json:"referencedBy"`
	Checks       []Check        `json:"checks"`
	Triggers     []Trigger      `json:"triggers"`
	// ChecksSupported is false when the engine does not expose check
	// constraints (SQLite, MySQL before 8.0.16).
	ChecksSupported bool `json:"checksSupported"`
}

// ColumnDetail is a column with everything the Structure view shows.
type ColumnDetail struct {
	Name    string `json:"name"`
	Ordinal int    `json:"ordinal"`
	// DataType is the full declared type: varchar(255), numeric(10,2),
	// int unsigned, text[].
	DataType string  `json:"dataType"`
	Nullable bool    `json:"nullable"`
	Default  *string `json:"default"`
	// PKPosition is the 1-based position in the primary key (0: none).
	IsPrimaryKey bool `json:"isPrimaryKey"`
	PKPosition   int  `json:"pkPosition"`
	// AutoIncrement: MySQL auto_increment, PostgreSQL serial or identity,
	// SQLite INTEGER PRIMARY KEY (rowid alias).
	AutoIncrement bool `json:"autoIncrement"`
	// Identity is "ALWAYS" or "BY DEFAULT" for PostgreSQL identity columns.
	Identity string `json:"identity"`
	// Generated is "STORED" or "VIRTUAL" for generated columns, with the
	// expression in GenerationExpr where the engine exposes it.
	Generated      string  `json:"generated"`
	GenerationExpr *string `json:"generationExpr"`
	// OnUpdate is MySQL's ON UPDATE expression (e.g. CURRENT_TIMESTAMP).
	OnUpdate   string   `json:"onUpdate"`
	Collation  string   `json:"collation"`
	Comment    string   `json:"comment"`
	EnumValues []string `json:"enumValues,omitempty"`
}

// Index describes an index (including those backing PRIMARY KEY and UNIQUE
// constraints).
type Index struct {
	Name string `json:"name"`
	// Columns are the key columns in order; expressions are shown in
	// parentheses, prefix lengths as name(n).
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary"`
	// Method is the access method (btree, hash, gin, FULLTEXT, ...); ""
	// when the engine does not report one (SQLite).
	Method string `json:"method"`
	// Predicate is the WHERE clause of a partial index ("" when none).
	Predicate string `json:"predicate"`
	// Definition is the CREATE INDEX statement or an equivalent
	// description.
	Definition string `json:"definition"`
}

// ForeignKey is a foreign key from Schema.Table(Columns) to
// RefSchema.RefTable(RefColumns); the column lists are in key order.
type ForeignKey struct {
	Name       string   `json:"name"`
	Schema     string   `json:"schema"`
	Table      string   `json:"table"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"refSchema"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
	// OnUpdate and OnDelete are the referential actions: NO ACTION,
	// RESTRICT, CASCADE, SET NULL, SET DEFAULT.
	OnUpdate string `json:"onUpdate"`
	OnDelete string `json:"onDelete"`
}

// Check is a check constraint.
type Check struct {
	Name       string `json:"name"`
	Expression string `json:"expression"`
}

// Trigger is a trigger on the table.
type Trigger struct {
	Name string `json:"name"`
	// Timing is BEFORE, AFTER or INSTEAD OF; Event is INSERT, UPDATE,
	// DELETE or TRUNCATE (several joined with " OR ").
	Timing     string `json:"timing"`
	Event      string `json:"event"`
	Definition string `json:"definition"`
}

// NewTableDetail returns an empty detail for t with non-nil slices.
func NewTableDetail(t Table) *TableDetail {
	return &TableDetail{
		Schema: t.Schema, Name: t.Name, Type: t.Type, EstimatedRows: t.EstimatedRows,
		Columns: []ColumnDetail{}, Indexes: []Index{}, ForeignKeys: []ForeignKey{},
		ReferencedBy: []ForeignKey{}, Checks: []Check{}, Triggers: []Trigger{},
	}
}

// Structure is implemented by every driver: the Structure view's metadata.
type Structure interface {
	// TableInfo returns the structure of schema.table: detailed columns,
	// indexes, foreign keys in both directions, checks, triggers and
	// table metadata. ErrTableNotFound for unknown tables.
	TableInfo(ctx context.Context, db *sql.DB, schema, table string) (*TableDetail, error)
	// TableDDL returns the CREATE statement(s) of schema.table (with its
	// indexes, triggers and comments where the engine keeps them
	// separately). ErrTableNotFound for unknown tables.
	TableDDL(ctx context.Context, db *sql.DB, schema, table string) (string, error)
}

// TableLister is the part of Introspector that FindTable needs.
type TableLister interface {
	ListTables(ctx context.Context, db *sql.DB, schema string) ([]Table, error)
}

// FindTable returns the table or view named exactly name in schema, or
// ErrTableNotFound. Callers use it to validate an identifier before it is
// quoted into SQL (SHOW CREATE TABLE, sqlite_master).
func FindTable(ctx context.Context, l TableLister, db *sql.DB, schema, name string) (Table, error) {
	tables, err := l.ListTables(ctx, db, schema)
	if err != nil {
		return Table{}, err
	}
	return LookupTable(tables, schema, name)
}

// LookupTable finds name in tables (exact match) or returns
// ErrTableNotFound.
func LookupTable(tables []Table, schema, name string) (Table, error) {
	i := slices.IndexFunc(tables, func(t Table) bool { return t.Name == name })
	if name == "" || i < 0 {
		return Table{}, fmt.Errorf("%w: %s.%s", ErrTableNotFound, schema, name)
	}
	return tables[i], nil
}

// ReferentialAction maps a PostgreSQL confupdtype/confdeltype code (a, r,
// c, n, d) or an engine's action text to the SQL action name.
func ReferentialAction(code string) string {
	switch code {
	case "a", "":
		return "NO ACTION"
	case "r":
		return "RESTRICT"
	case "c":
		return "CASCADE"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	}
	return code
}
