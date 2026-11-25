package ddl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"lontar/internal/driver"
)

// Engine is what ddl needs from a driver (driver.Driver satisfies it).
type Engine interface {
	driver.Dialect
	Kind() driver.Kind
	ListTables(ctx context.Context, db *sql.DB, schema string) ([]driver.Table, error)
	Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error)
	TableInfo(ctx context.Context, db *sql.DB, schema, table string) (*driver.TableDetail, error)
}

// Options are the session settings of a change.
type Options struct {
	// ServerVersion selects the capabilities (conn.Session.ServerVersion).
	ServerVersion string
	// ReadOnly refuses Apply (read-only safe mode).
	ReadOnly bool
}

// prepare re-introspects the table, refuses a stale change set and
// gathers what Build needs.
func prepare(ctx context.Context, db *sql.DB, e Engine, tc TableChange, opts Options) (Input, error) {
	kind := e.Kind()
	caps := Capabilities(kind, opts.ServerVersion)
	in := Input{Kind: kind, Dialect: e, Caps: caps, Change: tc, Lit: Literals{Flavor: Flavor(caps.Flavor)}}
	if tc.Create {
		if _, err := driver.FindTable(ctx, e, db, tc.Schema, tc.Table); err == nil {
			return in, invalid(SectionTable, -1, "name", fmt.Sprintf("A table or view named %q already exists.", tc.Table))
		} else if !errors.Is(err, driver.ErrTableNotFound) {
			return in, err
		}
	} else {
		if _, err := driver.FindTable(ctx, e, db, tc.Schema, tc.Table); err != nil {
			if errors.Is(err, driver.ErrTableNotFound) {
				return in, notFound(SectionTable, -1, "", fmt.Sprintf("Table %s.%s was not found.", tc.Schema, tc.Table))
			}
			return in, err
		}
		cur, err := e.TableInfo(ctx, db, tc.Schema, tc.Table)
		if err != nil {
			return in, err
		}
		if tc.Base == nil || Fingerprint(cur) != Fingerprint(tc.Base) {
			return in, &ChangeError{Kind: FailStructureChanged, Section: SectionTable, Index: -1,
				Message: "The table was changed since its structure was loaded. Reload it and make the changes again."}
		}
		in.Current = cur
	}
	if tc.NewName != nil && *tc.NewName != tc.Table {
		if _, err := driver.FindTable(ctx, e, db, tc.Schema, *tc.NewName); err == nil {
			return in, invalid(SectionTable, -1, "name", fmt.Sprintf("A table or view named %q already exists.", *tc.NewName))
		}
	}
	var err error
	switch Flavor(caps.Flavor) {
	case Postgres:
		if in.Current != nil {
			in.Extra, err = postgresExtra(ctx, db, e, tc.Schema, tc.Table)
		}
	case MySQL, MariaDB:
		in.Lit.NoBackslashEscapes, err = noBackslashEscapes(ctx, db)
		if err == nil && in.Current != nil {
			in.Extra.MySQLColumns, err = mysqlColumns(ctx, db, tc.Schema, tc.Table)
		}
	}
	if err != nil {
		return in, err
	}
	in.Refs = func(schema, table string) ([]string, error) {
		if _, err := driver.FindTable(ctx, e, db, schema, table); err != nil {
			return nil, err
		}
		cols, err := e.Columns(ctx, db, schema, table)
		if err != nil {
			return nil, err
		}
		names := make([]string, len(cols))
		for i, c := range cols {
			names[i] = c.Name
		}
		return names, nil
	}
	return in, nil
}

// Preview returns the statements Apply would run (the exact SQL: DDL has
// no bound parameters).
func Preview(ctx context.Context, db *sql.DB, e Engine, tc TableChange, opts Options) ([]Statement, error) {
	in, err := prepare(ctx, db, e, tc, opts)
	if err != nil {
		return nil, err
	}
	return Build(in)
}

// Apply validates and runs a change set. PostgreSQL and SQLite run it in
// one transaction; MySQL and MariaDB commit each DDL statement, so a
// failure reports how many statements were applied.
func Apply(ctx context.Context, db *sql.DB, e Engine, tc TableChange, opts Options) (*Result, error) {
	if opts.ReadOnly {
		return nil, &ChangeError{Kind: FailReadOnly, Section: SectionTable, Index: -1,
			Message: "This connection is in read-only safe mode. Change it in the connection settings to change the structure."}
	}
	in, err := prepare(ctx, db, e, tc, opts)
	if err != nil {
		return nil, err
	}
	stmts, err := Build(in)
	if err != nil {
		return nil, err
	}
	if err := run(ctx, db, in.Caps.TransactionalDDL, stmts); err != nil {
		return nil, err
	}
	return &Result{Statements: stmts}, nil
}

// DropTableStatement returns the DROP TABLE statement of a table
// (cascade: also drop what depends on it, PostgreSQL).
func DropTableStatement(ctx context.Context, db *sql.DB, e Engine, schema, table string, cascade bool, opts Options) (Statement, error) {
	caps := Capabilities(e.Kind(), opts.ServerVersion)
	t, err := driver.FindTable(ctx, e, db, schema, table)
	if errors.Is(err, driver.ErrTableNotFound) {
		return Statement{}, notFound(SectionTable, -1, "", fmt.Sprintf("Table %s.%s was not found.", schema, table))
	}
	if err != nil {
		return Statement{}, err
	}
	if t.Type != driver.TypeTable {
		return Statement{}, unsupported(SectionTable, -1, "", fmt.Sprintf("%s is a %s, not a table.", table, t.Type))
	}
	if cascade && !caps.DropCascade {
		return Statement{}, unsupported(SectionTable, -1, "cascade", "CASCADE is only supported on PostgreSQL.")
	}
	s := "DROP TABLE " + e.QuoteIdent(schema) + "." + e.QuoteIdent(t.Name)
	if cascade {
		s += " CASCADE"
	}
	return stmt(SectionTable, -1, s), nil
}

// DropTable drops a table.
func DropTable(ctx context.Context, db *sql.DB, e Engine, schema, table string, cascade bool, opts Options) (*Result, error) {
	if opts.ReadOnly {
		return nil, &ChangeError{Kind: FailReadOnly, Section: SectionTable, Index: -1,
			Message: "This connection is in read-only safe mode. Change it in the connection settings to drop tables."}
	}
	st, err := DropTableStatement(ctx, db, e, schema, table, cascade, opts)
	if err != nil {
		return nil, err
	}
	stmts := []Statement{st}
	if err := run(ctx, db, Capabilities(e.Kind(), opts.ServerVersion).TransactionalDDL, stmts); err != nil {
		return nil, err
	}
	return &Result{Statements: stmts}, nil
}

// run executes stmts on one connection: in a transaction when the engine
// has transactional DDL, otherwise in order.
func run(ctx context.Context, db *sql.DB, transactional bool, stmts []Statement) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return dbError(Statement{Section: SectionTable, Index: -1}, err, 0, transactional)
	}
	defer conn.Close()
	if !transactional {
		for i, st := range stmts {
			if _, err := conn.ExecContext(ctx, st.SQL); err != nil {
				return dbError(st, err, i, false)
			}
		}
		return nil
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return dbError(Statement{Section: SectionTable, Index: -1}, err, 0, true)
	}
	for _, st := range stmts {
		if _, err := tx.ExecContext(ctx, st.SQL); err != nil {
			_ = tx.Rollback()
			return dbError(st, err, 0, true)
		}
	}
	if err := tx.Commit(); err != nil {
		return dbError(Statement{Section: SectionTable, Index: -1}, err, 0, true)
	}
	return nil
}

func dbError(st Statement, err error, applied int, transactional bool) *ChangeError {
	msg := err.Error()
	if transactional {
		msg += ". Nothing was changed."
	} else if applied > 0 {
		msg += fmt.Sprintf(". %d statement(s) before it were applied (MySQL commits each DDL statement); reload the structure.", applied)
	} else {
		msg += ". Nothing was changed."
	}
	return &ChangeError{Kind: FailDatabase, Section: st.Section, Index: st.Index, SQL: st.SQL, Applied: applied, Message: msg, Err: err}
}

// postgresExtra reads the primary key constraint and the indexes that back
// constraints. The table is passed as a bound, quoted regclass literal.
func postgresExtra(ctx context.Context, db *sql.DB, e Engine, schema, table string) (Extra, error) {
	ex := Extra{ConstraintIndexes: map[string]string{}}
	rel := e.QuoteIdent(schema) + "." + e.QuoteIdent(table)
	rows, err := db.QueryContext(ctx, `
SELECT k.conname, k.contype::text, COALESCE(i.relname, '')
FROM pg_catalog.pg_constraint k
LEFT JOIN pg_catalog.pg_class i ON i.oid = k.conindid
WHERE k.conrelid = $1::regclass AND k.contype IN ('p', 'u', 'x')`, rel)
	if err != nil {
		return ex, fmt.Errorf("postgres: constraints: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, typ, idx string
		if err := rows.Scan(&name, &typ, &idx); err != nil {
			return ex, err
		}
		if typ == "p" {
			ex.PKName = name
		} else if idx != "" {
			ex.ConstraintIndexes[idx] = name
		}
	}
	return ex, rows.Err()
}

// noBackslashEscapes reports whether the session's sql_mode treats
// backslashes in strings as ordinary characters.
func noBackslashEscapes(ctx context.Context, db *sql.DB) (bool, error) {
	var mode string
	if err := db.QueryRowContext(ctx, "SELECT @@SESSION.sql_mode").Scan(&mode); err != nil {
		return false, fmt.Errorf("mysql: sql_mode: %w", err)
	}
	return strings.Contains(strings.ToUpper(mode), "NO_BACKSLASH_ESCAPES"), nil
}

// mysqlColumns reads the raw column definitions of a table.
func mysqlColumns(ctx context.Context, db *sql.DB, schema, table string) (map[string]MySQLColumn, error) {
	rows, err := db.QueryContext(ctx, `
SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA, COALESCE(COLLATION_NAME, ''),
       COLUMN_COMMENT, COALESCE(GENERATION_EXPRESSION, '')
FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("mysql: columns: %w", err)
	}
	defer rows.Close()
	out := map[string]MySQLColumn{}
	for rows.Next() {
		var (
			name, nullable string
			c              MySQLColumn
			def            sql.NullString
		)
		if err := rows.Scan(&name, &c.Type, &nullable, &def, &c.Extra, &c.Collation, &c.Comment, &c.GenExpr); err != nil {
			return nil, err
		}
		c.Nullable = nullable == "YES"
		if def.Valid {
			v := def.String
			c.Default = &v
		}
		out[name] = c
	}
	return out, rows.Err()
}
