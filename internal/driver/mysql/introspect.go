package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"dbedit/internal/driver"
)

// SystemDatabases are hidden by ListDatabases unless includeSystem is set.
var SystemDatabases = map[string]bool{
	"mysql":              true,
	"sys":                true,
	"performance_schema": true,
	"information_schema": true,
}

// ListDatabases implements driver.Introspector.
func (d *Driver) ListDatabases(ctx context.Context, db *sql.DB, includeSystem bool) ([]string, error) {
	all, err := driver.ScanStrings(ctx, db, "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME")
	if err != nil {
		return nil, fmt.Errorf("%s: list databases: %w", d.kind, err)
	}
	out := make([]string, 0, len(all))
	for _, name := range all {
		if includeSystem || !SystemDatabases[strings.ToLower(name)] {
			out = append(out, name)
		}
	}
	return out, nil
}

// CurrentDatabase implements driver.Introspector ("" when none is selected).
func (*Driver) CurrentDatabase(ctx context.Context, db *sql.DB) (string, error) {
	return driver.ScanString(ctx, db, "SELECT DATABASE()")
}

// ListSchemas implements driver.Introspector. In MySQL a database is a
// schema, so this is the current database (or nothing if none is selected);
// switch databases with conn.Manager.UseDatabase.
func (d *Driver) ListSchemas(ctx context.Context, db *sql.DB, _ bool) ([]string, error) {
	cur, err := d.CurrentDatabase(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("%s: list schemas: %w", d.kind, err)
	}
	if cur == "" {
		return []string{}, nil
	}
	return []string{cur}, nil
}

// CurrentSchema implements driver.Introspector; it is the current database.
func (d *Driver) CurrentSchema(ctx context.Context, db *sql.DB) (string, error) {
	return d.CurrentDatabase(ctx, db)
}

// ListTables implements driver.Introspector. TABLE_ROWS is an estimate for
// InnoDB (exact for MyISAM) and NULL for views. MariaDB sequences are
// skipped.
func (d *Driver) ListTables(ctx context.Context, db *sql.DB, schema string) ([]driver.Table, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT TABLE_NAME, TABLE_TYPE, TABLE_ROWS FROM information_schema.TABLES
		 WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME`, schema)
	if err != nil {
		return nil, fmt.Errorf("%s: list tables: %w", d.kind, err)
	}
	defer rows.Close()
	out := []driver.Table{}
	for rows.Next() {
		var (
			t     = driver.Table{Schema: schema}
			typ   string
			nrows sql.NullInt64
		)
		if err := rows.Scan(&t.Name, &typ, &nrows); err != nil {
			return nil, fmt.Errorf("%s: list tables: %w", d.kind, err)
		}
		switch typ {
		case "VIEW", "SYSTEM VIEW":
			t.Type = driver.TypeView
		case "BASE TABLE", "SYSTEM VERSIONED":
			t.Type = driver.TypeTable
			if nrows.Valid {
				n := nrows.Int64
				t.EstimatedRows = &n
			}
		default: // SEQUENCE, TEMPORARY, ...
			continue
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Columns implements driver.Introspector. IsPrimaryKey comes from
// KEY_COLUMN_USAGE, not COLUMN_KEY: MySQL reports a non-null UNIQUE column
// as "PRI" when the table has no primary key.
func (d *Driver) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	pk, err := d.PrimaryKey(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	pkPos := make(map[string]int, len(pk))
	for i, c := range pk {
		pkPos[c] = i + 1
	}
	rows, err := db.QueryContext(ctx,
		`SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT
		 FROM information_schema.COLUMNS
		 WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		 ORDER BY ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("%s: list columns: %w", d.kind, err)
	}
	defer rows.Close()
	out := []driver.Column{}
	for rows.Next() {
		var (
			c        driver.Column
			nullable string
			def      sql.NullString
		)
		if err := rows.Scan(&c.Name, &c.DataType, &nullable, &def); err != nil {
			return nil, fmt.Errorf("%s: list columns: %w", d.kind, err)
		}
		c.Nullable = nullable == "YES"
		c.EnumValues = ParseEnum(c.DataType)
		c.PKPosition = pkPos[c.Name]
		c.IsPrimaryKey = c.PKPosition > 0
		if def.Valid {
			c.Default = &def.String
		}
		c.Ordinal = len(out) + 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// PrimaryKey implements driver.Introspector.
func (d *Driver) PrimaryKey(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	out, err := driver.ScanStrings(ctx, db,
		`SELECT COLUMN_NAME FROM information_schema.KEY_COLUMN_USAGE
		 WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND CONSTRAINT_NAME = 'PRIMARY'
		 ORDER BY ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("%s: primary key: %w", d.kind, err)
	}
	return out, nil
}

// SchemaColumns implements driver.Introspector.
func (d *Driver) SchemaColumns(ctx context.Context, db *sql.DB, schema string) ([]driver.TableColumn, error) {
	out, err := driver.ScanTableColumns(ctx, db,
		`SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE FROM information_schema.COLUMNS
		 WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME, ORDINAL_POSITION`, schema)
	if err != nil {
		return nil, fmt.Errorf("%s: list schema columns: %w", d.kind, err)
	}
	return out, nil
}

// ParseEnum returns the values of an ENUM column type as reported by
// information_schema.COLUMNS.COLUMN_TYPE, for example enum('a','b'). Inside a
// value an embedded quote is doubled and a backslash is escaped. It returns
// nil for other types or malformed input.
func ParseEnum(columnType string) []string {
	t := strings.TrimSpace(columnType)
	if len(t) < 6 || !strings.EqualFold(t[:5], "enum(") || !strings.HasSuffix(t, ")") {
		return nil
	}
	body := t[5 : len(t)-1]
	out := []string{}
	for i := 0; i < len(body); {
		if body[i] != '\'' {
			return nil
		}
		var sb strings.Builder
		i++
		closed := false
		for i < len(body) {
			ch := body[i]
			switch {
			case ch == '\'' && i+1 < len(body) && body[i+1] == '\'':
				sb.WriteByte('\'')
				i += 2
			case ch == '\'':
				i++
				closed = true
			case ch == '\\' && i+1 < len(body):
				sb.WriteByte(body[i+1])
				i += 2
			default:
				sb.WriteByte(ch)
				i++
			}
			if closed {
				break
			}
		}
		if !closed {
			return nil
		}
		out = append(out, sb.String())
		if i < len(body) {
			if body[i] != ',' {
				return nil
			}
			i++
		}
	}
	return out
}
