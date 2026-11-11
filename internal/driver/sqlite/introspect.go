package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"dbedit/internal/driver"
)

// MainDatabase is the only database of an SQLite connection.
const MainDatabase = "main"

// ListDatabases implements driver.Introspector: an SQLite connection has a
// single database. Attached databases are exposed as schemas.
func (*Driver) ListDatabases(context.Context, *sql.DB, bool) ([]string, error) {
	return []string{MainDatabase}, nil
}

// CurrentDatabase implements driver.Introspector.
func (*Driver) CurrentDatabase(context.Context, *sql.DB) (string, error) {
	return MainDatabase, nil
}

// ListSchemas implements driver.Introspector using PRAGMA database_list
// (normally just "main"; plus attached databases). "temp" is listed only
// with includeSystem.
func (*Driver) ListSchemas(ctx context.Context, db *sql.DB, includeSystem bool) ([]string, error) {
	out, err := driver.ScanStrings(ctx, db,
		`SELECT name FROM pragma_database_list WHERE ? OR name <> 'temp' ORDER BY seq`, includeSystem)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list schemas: %w", err)
	}
	return out, nil
}

// CurrentSchema implements driver.Introspector.
func (*Driver) CurrentSchema(context.Context, *sql.DB) (string, error) {
	return MainDatabase, nil
}

// ListTables implements driver.Introspector using pragma_table_list (SQLite
// 3.37+), whose schema argument is a bound parameter. Internal sqlite_*
// tables and FTS shadow tables are skipped. SQLite keeps no cheap row
// estimate, so EstimatedRows is always nil.
func (*Driver) ListTables(ctx context.Context, db *sql.DB, schema string) ([]driver.Table, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT name, type FROM pragma_table_list
		 WHERE schema = ? AND type IN ('table', 'view', 'virtual')
		   AND name NOT LIKE 'sqlite\_%' ESCAPE '\'
		 ORDER BY name`, schema)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list tables: %w", err)
	}
	defer rows.Close()
	out := []driver.Table{}
	for rows.Next() {
		var (
			t   = driver.Table{Schema: schema}
			typ string
		)
		if err := rows.Scan(&t.Name, &typ); err != nil {
			return nil, fmt.Errorf("sqlite: list tables: %w", err)
		}
		t.Type = driver.TypeTable
		if typ == "view" {
			t.Type = driver.TypeView
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// schemaExists reports whether schema is an attached database;
// pragma_table_info fails (rather than returning nothing) for unknown ones.
func schemaExists(ctx context.Context, db *sql.DB, schema string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_database_list WHERE name = ?`, schema).Scan(&n)
	return n > 0, err
}

// Columns implements driver.Introspector using pragma_table_info(table,
// schema), with both names bound.
func (*Driver) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	if ok, err := schemaExists(ctx, db, schema); err != nil || !ok {
		if err != nil {
			return nil, fmt.Errorf("sqlite: list columns: %w", err)
		}
		return []driver.Column{}, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_info(?, ?) ORDER BY cid`, table, schema)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list columns: %w", err)
	}
	defer rows.Close()
	out := []driver.Column{}
	for rows.Next() {
		var (
			c       driver.Column
			notNull bool
			def     sql.NullString
			pk      int
		)
		if err := rows.Scan(&c.Name, &c.DataType, &notNull, &def, &pk); err != nil {
			return nil, fmt.Errorf("sqlite: list columns: %w", err)
		}
		c.Nullable = !notNull
		c.PKPosition = pk
		c.IsPrimaryKey = pk > 0
		if def.Valid {
			c.Default = &def.String
		}
		c.Ordinal = len(out) + 1
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	markRowidAlias(out)
	return out, nil
}

// markRowidAlias marks an INTEGER PRIMARY KEY column as not nullable: it
// aliases the rowid and can never be NULL, although table_info reports
// notnull = 0 for it.
func markRowidAlias(cols []driver.Column) {
	pk := -1
	for i, c := range cols {
		if c.IsPrimaryKey {
			if pk >= 0 {
				return // composite key
			}
			pk = i
		}
	}
	if pk >= 0 && strings.EqualFold(cols[pk].DataType, "INTEGER") {
		cols[pk].Nullable = false
	}
}

// PrimaryKey implements driver.Introspector.
func (*Driver) PrimaryKey(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	if ok, err := schemaExists(ctx, db, schema); err != nil || !ok {
		if err != nil {
			return nil, fmt.Errorf("sqlite: primary key: %w", err)
		}
		return []string{}, nil
	}
	out, err := driver.ScanStrings(ctx, db,
		`SELECT name FROM pragma_table_info(?, ?) WHERE pk > 0 ORDER BY pk`, table, schema)
	if err != nil {
		return nil, fmt.Errorf("sqlite: primary key: %w", err)
	}
	return out, nil
}
