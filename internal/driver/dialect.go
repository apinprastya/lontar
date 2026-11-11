package driver

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// Dialect holds the pure SQL-generation helpers of an engine. Implementations
// never touch a database, so they are trivially unit-testable.
//
// Identifiers are always passed through QuoteIdent, and callers must also
// check them against introspected metadata before use (see package query);
// values are always bound with Placeholder, never concatenated.
type Dialect interface {
	// QuoteIdent quotes an identifier (schema, table or column), doubling
	// any embedded quote characters: "x" on PostgreSQL/SQLite, `x` on
	// MySQL/MariaDB.
	QuoteIdent(name string) string
	// Placeholder returns the bind placeholder for the n-th (1-based)
	// argument: "$n" on PostgreSQL, "?" elsewhere.
	Placeholder(n int) string
	// LimitOffset returns a " LIMIT n OFFSET m" clause (with a leading
	// space). Negative values are clamped to zero.
	LimitOffset(limit, offset int) string
	// CaseInsensitiveLike is the operator for a case-insensitive pattern
	// match: ILIKE on PostgreSQL; LIKE on MySQL/MariaDB (case-insensitive
	// with the default *_ci collations) and SQLite (case-insensitive for
	// ASCII letters only).
	CaseInsensitiveLike() string
	// TextExpr converts a quoted column expression into one that can be
	// pattern-matched as text (PostgreSQL needs an explicit cast for
	// non-text columns; the others coerce implicitly).
	TextExpr(expr string) string
}

// TableType classifies a relation.
type TableType string

// Relation types returned by ListTables.
const (
	TypeTable            TableType = "table"
	TypeView             TableType = "view"
	TypeMaterializedView TableType = "materialized view"
)

// Table describes a table or view.
type Table struct {
	Schema string    `json:"schema"`
	Name   string    `json:"name"`
	Type   TableType `json:"type"`
	// EstimatedRows is a cheap, possibly stale estimate
	// (pg_class.reltuples, information_schema.TABLES.TABLE_ROWS); nil when
	// unknown (views, never-analysed tables, SQLite).
	EstimatedRows *int64 `json:"estimatedRows"`
}

// Column describes a column of a table or view.
type Column struct {
	Name     string `json:"name"`
	DataType string `json:"dataType"`
	Nullable bool   `json:"nullable"`
	// Default is the default expression as reported by the server; nil
	// when the column has none.
	Default      *string `json:"default"`
	Ordinal      int     `json:"ordinal"` // 1-based position
	IsPrimaryKey bool    `json:"isPrimaryKey"`
	// PKPosition is the 1-based position in the primary key (0 when the
	// column is not part of it).
	PKPosition int `json:"pkPosition"`
	// EnumValues lists the allowed values of an enum column in declaration
	// order (PostgreSQL enum types, MySQL/MariaDB ENUM); nil otherwise.
	EnumValues []string `json:"enumValues,omitempty"`
}

// Introspector reads database metadata. Every method takes the caller's
// context and the session's *sql.DB; all values are bound parameters.
type Introspector interface {
	// ListDatabases lists the databases on the server. System databases
	// (MySQL: mysql, sys, performance_schema, information_schema;
	// PostgreSQL: templates) are included only with includeSystem. SQLite
	// has a single database, "main".
	ListDatabases(ctx context.Context, db *sql.DB, includeSystem bool) ([]string, error)
	// CurrentDatabase returns the database the pool is connected to ("" on
	// MySQL when none is selected).
	CurrentDatabase(ctx context.Context, db *sql.DB) (string, error)
	// ListSchemas lists the schemas of the current database. PostgreSQL
	// hides pg_* and information_schema unless includeSystem; MySQL returns
	// the current database (database == schema); SQLite returns the
	// attached databases from PRAGMA database_list ("temp" only with
	// includeSystem).
	ListSchemas(ctx context.Context, db *sql.DB, includeSystem bool) ([]string, error)
	// CurrentSchema returns the default schema ("public", the MySQL
	// database, "main").
	CurrentSchema(ctx context.Context, db *sql.DB) (string, error)
	// ListTables lists tables and views in schema, ordered by name in the
	// server's collation (which differs between engines).
	ListTables(ctx context.Context, db *sql.DB, schema string) ([]Table, error)
	// Columns lists the columns of schema.table in ordinal order. It
	// returns an empty slice when the table does not exist.
	Columns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error)
	// PrimaryKey returns the primary key columns in key order (empty when
	// the table has none).
	PrimaryKey(ctx context.Context, db *sql.DB, schema, table string) ([]string, error)
	// SchemaColumns lists the columns of every table and view of schema
	// in one query, ordered by table and position (for SQL editor
	// autocompletion). It returns an empty slice for unknown schemas.
	SchemaColumns(ctx context.Context, db *sql.DB, schema string) ([]TableColumn, error)
}

// TableColumn is one column returned by SchemaColumns.
type TableColumn struct {
	Table    string `json:"table"`
	Name     string `json:"name"`
	DataType string `json:"dataType"`
}

// ScanTableColumns runs a (table, column, type) query.
func ScanTableColumns(ctx context.Context, db *sql.DB, query string, args ...any) ([]TableColumn, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TableColumn{}
	for rows.Next() {
		var c TableColumn
		if err := rows.Scan(&c.Table, &c.Name, &c.DataType); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func limitOffset(limit, offset int) string {
	if limit < 0 {
		limit = 0
	}
	if offset < 0 {
		offset = 0
	}
	return " LIMIT " + strconv.Itoa(limit) + " OFFSET " + strconv.Itoa(offset)
}

// PostgresDialect is the PostgreSQL dialect.
type PostgresDialect struct{}

// QuoteIdent implements Dialect.
func (PostgresDialect) QuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Placeholder implements Dialect.
func (PostgresDialect) Placeholder(n int) string { return "$" + strconv.Itoa(n) }

// LimitOffset implements Dialect.
func (PostgresDialect) LimitOffset(limit, offset int) string { return limitOffset(limit, offset) }

// CaseInsensitiveLike implements Dialect.
func (PostgresDialect) CaseInsensitiveLike() string { return "ILIKE" }

// TextExpr implements Dialect.
func (PostgresDialect) TextExpr(expr string) string { return "CAST(" + expr + " AS TEXT)" }

// MySQLDialect is the MySQL/MariaDB dialect.
type MySQLDialect struct{}

// QuoteIdent implements Dialect.
func (MySQLDialect) QuoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// Placeholder implements Dialect.
func (MySQLDialect) Placeholder(int) string { return "?" }

// LimitOffset implements Dialect.
func (MySQLDialect) LimitOffset(limit, offset int) string { return limitOffset(limit, offset) }

// CaseInsensitiveLike implements Dialect.
func (MySQLDialect) CaseInsensitiveLike() string { return "LIKE" }

// TextExpr implements Dialect.
func (MySQLDialect) TextExpr(expr string) string { return expr }

// SQLiteDialect is the SQLite dialect.
type SQLiteDialect struct{}

// QuoteIdent implements Dialect.
func (SQLiteDialect) QuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Placeholder implements Dialect.
func (SQLiteDialect) Placeholder(int) string { return "?" }

// LimitOffset implements Dialect.
func (SQLiteDialect) LimitOffset(limit, offset int) string { return limitOffset(limit, offset) }

// CaseInsensitiveLike implements Dialect. SQLite's LIKE folds ASCII letters
// only (unless the ICU extension is loaded).
func (SQLiteDialect) CaseInsensitiveLike() string { return "LIKE" }

// TextExpr implements Dialect.
func (SQLiteDialect) TextExpr(expr string) string { return expr }

// DialectFor returns the dialect for kind (nil for unknown kinds).
func DialectFor(kind Kind) Dialect {
	switch kind {
	case Postgres:
		return PostgresDialect{}
	case MySQL, MariaDB:
		return MySQLDialect{}
	case SQLite:
		return SQLiteDialect{}
	}
	return nil
}

// ScanStrings runs query and collects the first column of every row.
func ScanStrings(ctx context.Context, db *sql.DB, query string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ScanString runs a single-row, single-column query; NULL becomes "".
func ScanString(ctx context.Context, db *sql.DB, query string, args ...any) (string, error) {
	var s sql.NullString
	if err := db.QueryRowContext(ctx, query, args...).Scan(&s); err != nil {
		return "", err
	}
	return s.String, nil
}
