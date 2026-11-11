package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"dbedit/internal/driver"
)

// ListDatabases implements driver.Introspector using pg_database. Templates
// are listed only with includeSystem; databases that do not accept
// connections are never listed (they cannot be switched to).
func (*Driver) ListDatabases(ctx context.Context, db *sql.DB, includeSystem bool) ([]string, error) {
	out, err := driver.ScanStrings(ctx, db,
		`SELECT datname FROM pg_catalog.pg_database
		 WHERE datallowconn AND ($1 OR NOT datistemplate)
		 ORDER BY datname`, includeSystem)
	if err != nil {
		return nil, fmt.Errorf("postgres: list databases: %w", err)
	}
	return out, nil
}

// CurrentDatabase implements driver.Introspector.
func (*Driver) CurrentDatabase(ctx context.Context, db *sql.DB) (string, error) {
	return driver.ScanString(ctx, db, "SELECT current_database()")
}

// ListSchemas implements driver.Introspector. pg_* (pg_catalog, pg_toast,
// pg_temp_N, ...) and information_schema are hidden unless includeSystem.
func (*Driver) ListSchemas(ctx context.Context, db *sql.DB, includeSystem bool) ([]string, error) {
	out, err := driver.ScanStrings(ctx, db,
		`SELECT nspname FROM pg_catalog.pg_namespace
		 WHERE $1 OR (nspname NOT LIKE 'pg\_%' AND nspname <> 'information_schema')
		 ORDER BY nspname`, includeSystem)
	if err != nil {
		return nil, fmt.Errorf("postgres: list schemas: %w", err)
	}
	return out, nil
}

// CurrentSchema implements driver.Introspector ("" when the search_path
// resolves to no existing schema).
func (*Driver) CurrentSchema(ctx context.Context, db *sql.DB) (string, error) {
	return driver.ScanString(ctx, db, "SELECT current_schema()")
}

// ListTables implements driver.Introspector. Partitioned and foreign tables
// are reported as tables. The estimate comes from pg_class.reltuples, which
// is -1 for tables that were never vacuumed/analysed (reported as nil).
func (*Driver) ListTables(ctx context.Context, db *sql.DB, schema string) ([]driver.Table, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT c.relname, c.relkind::text, c.reltuples::bigint
		 FROM pg_catalog.pg_class c
		 JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'f', 'v', 'm')
		 ORDER BY c.relname`, schema)
	if err != nil {
		return nil, fmt.Errorf("postgres: list tables: %w", err)
	}
	defer rows.Close()
	out := []driver.Table{}
	for rows.Next() {
		var (
			t     = driver.Table{Schema: schema}
			kind  string
			tuple int64
		)
		if err := rows.Scan(&t.Name, &kind, &tuple); err != nil {
			return nil, fmt.Errorf("postgres: list tables: %w", err)
		}
		switch kind {
		case "v":
			t.Type = driver.TypeView
		case "m":
			t.Type = driver.TypeMaterializedView
		default:
			t.Type = driver.TypeTable
		}
		if t.Type == driver.TypeTable && tuple >= 0 {
			n := tuple
			t.EstimatedRows = &n
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Columns implements driver.Introspector. It reads pg_attribute rather than
// information_schema so that materialized views are covered too.
func (*Driver) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT a.attname,
		        pg_catalog.format_type(a.atttypid, a.atttypmod),
		        NOT a.attnotnull,
		        pg_catalog.pg_get_expr(d.adbin, d.adrelid),
		        COALESCE((SELECT k.ord FROM unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
		                  WHERE k.attnum = a.attnum), 0),
		        (SELECT array_to_json(array_agg(e.enumlabel ORDER BY e.enumsortorder))::text
		         FROM pg_catalog.pg_enum e WHERE e.enumtypid = a.atttypid)
		 FROM pg_catalog.pg_attribute a
		 JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
		 JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		 LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		 LEFT JOIN pg_catalog.pg_index i ON i.indrelid = c.oid AND i.indisprimary
		 WHERE n.nspname = $1 AND c.relname = $2
		   AND c.relkind IN ('r', 'p', 'f', 'v', 'm')
		   AND a.attnum > 0 AND NOT a.attisdropped
		 ORDER BY a.attnum`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("postgres: list columns: %w", err)
	}
	defer rows.Close()
	out := []driver.Column{}
	for rows.Next() {
		var (
			c         driver.Column
			def, enum sql.NullString
		)
		if err := rows.Scan(&c.Name, &c.DataType, &c.Nullable, &def, &c.PKPosition, &enum); err != nil {
			return nil, fmt.Errorf("postgres: list columns: %w", err)
		}
		if def.Valid {
			c.Default = &def.String
		}
		if enum.Valid {
			if err := json.Unmarshal([]byte(enum.String), &c.EnumValues); err != nil {
				return nil, fmt.Errorf("postgres: enum labels of %s: %w", c.Name, err)
			}
		}
		c.IsPrimaryKey = c.PKPosition > 0
		c.Ordinal = len(out) + 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// PrimaryKey implements driver.Introspector.
func (*Driver) PrimaryKey(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	out, err := driver.ScanStrings(ctx, db,
		`SELECT a.attname
		 FROM pg_catalog.pg_index i
		 JOIN pg_catalog.pg_class c ON c.oid = i.indrelid
		 JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		 CROSS JOIN LATERAL unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
		 JOIN pg_catalog.pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum
		 WHERE i.indisprimary AND n.nspname = $1 AND c.relname = $2
		 ORDER BY k.ord`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("postgres: primary key: %w", err)
	}
	return out, nil
}

// SchemaColumns implements driver.Introspector.
func (*Driver) SchemaColumns(ctx context.Context, db *sql.DB, schema string) ([]driver.TableColumn, error) {
	out, err := driver.ScanTableColumns(ctx, db,
		`SELECT c.relname, a.attname, pg_catalog.format_type(a.atttypid, a.atttypmod)
		 FROM pg_catalog.pg_attribute a
		 JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
		 JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND c.relkind IN ('r', 'p', 'f', 'v', 'm')
		   AND a.attnum > 0 AND NOT a.attisdropped
		 ORDER BY c.relname, a.attnum`, schema)
	if err != nil {
		return nil, fmt.Errorf("postgres: list schema columns: %w", err)
	}
	return out, nil
}
