package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"dbedit/internal/driver"
)

// master returns the quoted sqlite_master of schema. The schema must be an
// attached database (checked with PRAGMA database_list), because a schema
// prefix cannot be a bound parameter.
func (d *Driver) master(ctx context.Context, db *sql.DB, schema string) (string, error) {
	ok, err := schemaExists(ctx, db, schema)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: unknown schema %q", driver.ErrTableNotFound, schema)
	}
	return d.QuoteIdent(schema) + ".sqlite_master", nil
}

// TableInfo implements driver.Introspector with the pragma table-valued
// functions (names bound) and sqlite_master (for index and trigger SQL).
// SQLite does not expose check constraints, comments, sizes or row
// estimates.
func (d *Driver) TableInfo(ctx context.Context, db *sql.DB, schema, table string) (*driver.TableDetail, error) {
	wrap := func(err error) error { return fmt.Errorf("sqlite: table info: %w", err) }
	master, err := d.master(ctx, db, schema)
	if err != nil {
		return nil, wrap(err)
	}
	t, err := driver.FindTable(ctx, d, db, schema, table)
	if err != nil {
		return nil, wrap(err)
	}
	info := driver.NewTableDetail(t)
	var tableSQL sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT sql FROM `+master+` WHERE type IN ('table', 'view') AND name = ?`, t.Name).Scan(&tableSQL); err != nil {
		return nil, wrap(err)
	}
	if info.Columns, err = columnDetails(ctx, db, schema, t.Name, tableSQL.String); err != nil {
		return nil, wrap(err)
	}
	if info.Indexes, err = indexes(ctx, db, master, schema, t.Name); err != nil {
		return nil, wrap(err)
	}
	if info.ForeignKeys, err = d.foreignKeys(ctx, db, schema, t.Name); err != nil {
		return nil, wrap(err)
	}
	if info.ReferencedBy, err = d.referencedBy(ctx, db, schema, t.Name); err != nil {
		return nil, wrap(err)
	}
	if info.Triggers, err = triggers(ctx, db, master, t.Name); err != nil {
		return nil, wrap(err)
	}
	return info, nil
}

var withoutRowidRE = regexp.MustCompile(`(?i)\bWITHOUT\s+ROWID\b`)

func columnDetails(ctx context.Context, db *sql.DB, schema, table, tableSQL string) ([]driver.ColumnDetail, error) {
	// table_xinfo also lists generated columns: hidden = 2 (virtual) or 3
	// (stored); 1 is a hidden column of a virtual table.
	rows, err := db.QueryContext(ctx,
		`SELECT name, type, "notnull", dflt_value, pk, hidden FROM pragma_table_xinfo(?, ?) WHERE hidden <> 1 ORDER BY cid`, table, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.ColumnDetail{}
	for rows.Next() {
		var (
			c       driver.ColumnDetail
			notNull bool
			def     sql.NullString
			hidden  int
		)
		if err := rows.Scan(&c.Name, &c.DataType, &notNull, &def, &c.PKPosition, &hidden); err != nil {
			return nil, err
		}
		c.Nullable = !notNull
		c.IsPrimaryKey = c.PKPosition > 0
		if def.Valid {
			v := def.String
			c.Default = &v
		}
		switch hidden {
		case 2:
			c.Generated = "VIRTUAL"
		case 3:
			c.Generated = "STORED"
		}
		c.Ordinal = len(out) + 1
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// A single INTEGER PRIMARY KEY of a rowid table aliases the rowid: never
	// NULL, and assigned automatically (AUTOINCREMENT only changes how).
	pk := -1
	for i, c := range out {
		if c.IsPrimaryKey {
			if pk >= 0 {
				return out, nil // composite key
			}
			pk = i
		}
	}
	if pk >= 0 && strings.EqualFold(out[pk].DataType, "INTEGER") && !withoutRowidRE.MatchString(tableSQL) {
		out[pk].Nullable = false
		out[pk].AutoIncrement = true
	}
	return out, nil
}

func indexes(ctx context.Context, db *sql.DB, master, schema, table string) ([]driver.Index, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT l.name, l."unique", l.origin, COALESCE(m.sql, '')
		 FROM pragma_index_list(?, ?) AS l
		 LEFT JOIN `+master+` AS m ON m.type = 'index' AND m.name = l.name
		 ORDER BY l.origin = 'pk' DESC, l.name`, table, schema)
	if err != nil {
		return nil, err
	}
	var out []driver.Index
	for rows.Next() {
		var ix driver.Index
		var origin string
		if err := rows.Scan(&ix.Name, &ix.Unique, &origin, &ix.Definition); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ix.Primary = origin == "pk"
		ix.Predicate = partialPredicate(ix.Definition)
		out = append(out, ix)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		// cid -2 is an expression (its text is only in the index SQL),
		// -1 the rowid.
		cols, err := driver.ScanStrings(ctx, db,
			`SELECT CASE WHEN cid = -2 THEN '(expression)' WHEN cid = -1 THEN 'rowid' ELSE name END
			 FROM pragma_index_xinfo(?, ?) WHERE key = 1 ORDER BY seqno`, out[i].Name, schema)
		if err != nil {
			return nil, err
		}
		out[i].Columns = cols
		if out[i].Definition == "" {
			kind := "UNIQUE"
			if out[i].Primary {
				kind = "PRIMARY KEY"
			}
			out[i].Definition = kind + " (" + strings.Join(cols, ", ") + ")"
		}
	}
	if out == nil {
		out = []driver.Index{}
	}
	return out, nil
}

var whereRE = regexp.MustCompile(`(?is)\)\s*WHERE\s+(.+?)\s*;?\s*$`)

// partialPredicate extracts the WHERE clause of a partial CREATE INDEX.
func partialPredicate(createSQL string) string {
	if m := whereRE.FindStringSubmatch(createSQL); m != nil {
		return m[1]
	}
	return ""
}

// fkRow is one row of pragma_foreign_key_list.
type fkRow struct {
	table    string // the referencing table (referencedBy only)
	id       int
	ref      string
	from     string
	to       sql.NullString
	onUpdate string
	onDelete string
}

func (d *Driver) groupFKs(ctx context.Context, db *sql.DB, schema string, rows []fkRow, srcTable string, incoming bool, target string) ([]driver.ForeignKey, error) {
	out := []driver.ForeignKey{}
	for i, r := range rows {
		src := srcTable
		if incoming {
			src = r.table
		}
		if i > 0 && rows[i-1].id == r.id && (!incoming || rows[i-1].table == r.table) {
			fk := &out[len(out)-1]
			fk.Columns = append(fk.Columns, r.from)
			fk.RefColumns = append(fk.RefColumns, r.to.String)
			continue
		}
		ref := r.ref
		if incoming {
			ref = target
		}
		out = append(out, driver.ForeignKey{
			Schema: schema, Table: src, Columns: []string{r.from},
			RefSchema: schema, RefTable: ref, RefColumns: []string{r.to.String},
			OnUpdate: r.onUpdate, OnDelete: r.onDelete,
		})
	}
	// "REFERENCES parent" without columns means the parent's primary key.
	for i := range out {
		if !slicesAllEmpty(out[i].RefColumns) {
			continue
		}
		pk, err := d.PrimaryKey(ctx, db, schema, out[i].RefTable)
		if err != nil {
			return nil, err
		}
		if len(pk) == len(out[i].Columns) {
			out[i].RefColumns = pk
		}
	}
	return out, nil
}

func slicesAllEmpty(s []string) bool {
	for _, v := range s {
		if v != "" {
			return false
		}
	}
	return true
}

func (d *Driver) foreignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]driver.ForeignKey, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, "table", "from", "to", on_update, on_delete
		 FROM pragma_foreign_key_list(?, ?) ORDER BY id, seq`, table, schema)
	if err != nil {
		return nil, err
	}
	var list []fkRow
	for rows.Next() {
		var r fkRow
		if err := rows.Scan(&r.id, &r.ref, &r.from, &r.to, &r.onUpdate, &r.onDelete); err != nil {
			_ = rows.Close()
			return nil, err
		}
		list = append(list, r)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// pragma_foreign_key_list reports the parent as written in the DDL;
	// map it to the table's real name where the case differs.
	tables, err := d.ListTables(ctx, db, schema)
	if err != nil {
		return nil, err
	}
	for i := range list {
		for _, t := range tables {
			if strings.EqualFold(t.Name, list[i].ref) {
				list[i].ref = t.Name
				break
			}
		}
	}
	return d.groupFKs(ctx, db, schema, list, table, false, "")
}

func (d *Driver) referencedBy(ctx context.Context, db *sql.DB, schema, table string) ([]driver.ForeignKey, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT t.name, f.id, f."from", f."to", f.on_update, f.on_delete
		 FROM pragma_table_list AS t, pragma_foreign_key_list(t.name, t.schema) AS f
		 WHERE t.schema = ? AND t.type = 'table' AND f."table" = ? COLLATE NOCASE
		 ORDER BY t.name, f.id, f.seq`, schema, table)
	if err != nil {
		return nil, err
	}
	var list []fkRow
	for rows.Next() {
		var r fkRow
		if err := rows.Scan(&r.table, &r.id, &r.from, &r.to, &r.onUpdate, &r.onDelete); err != nil {
			_ = rows.Close()
			return nil, err
		}
		list = append(list, r)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return d.groupFKs(ctx, db, schema, list, "", true, table)
}

var (
	timingRE = regexp.MustCompile(`(?i)\b(BEFORE|AFTER|INSTEAD\s+OF)\s+(INSERT|UPDATE|DELETE)\b`)
	eventRE  = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE)\s+ON\b`)
)

// triggerKind reads the timing and event of a CREATE TRIGGER statement
// (SQLite keeps no catalog columns for them). The default timing is BEFORE.
func triggerKind(createSQL string) (timing, event string) {
	if m := timingRE.FindStringSubmatch(createSQL); m != nil {
		return strings.ToUpper(strings.Join(strings.Fields(m[1]), " ")), strings.ToUpper(m[2])
	}
	if m := eventRE.FindStringSubmatch(createSQL); m != nil {
		return "BEFORE", strings.ToUpper(m[1])
	}
	return "", ""
}

func triggers(ctx context.Context, db *sql.DB, master, table string) ([]driver.Trigger, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT name, sql FROM `+master+` WHERE type = 'trigger' AND tbl_name = ? COLLATE NOCASE ORDER BY name`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Trigger{}
	for rows.Next() {
		var t driver.Trigger
		if err := rows.Scan(&t.Name, &t.Definition); err != nil {
			return nil, err
		}
		t.Timing, t.Event = triggerKind(t.Definition)
		out = append(out, t)
	}
	return out, rows.Err()
}

// TableDDL implements driver.Introspector: the sqlite_master SQL of the
// table or view, then of its indexes and triggers. The table must be
// listed by ListTables and the schema attached.
func (d *Driver) TableDDL(ctx context.Context, db *sql.DB, schema, table string) (string, error) {
	wrap := func(err error) error { return fmt.Errorf("sqlite: table DDL: %w", err) }
	master, err := d.master(ctx, db, schema)
	if err != nil {
		return "", wrap(err)
	}
	t, err := driver.FindTable(ctx, d, db, schema, table)
	if err != nil {
		return "", wrap(err)
	}
	stmts, err := driver.ScanStrings(ctx, db,
		`SELECT sql FROM `+master+`
		 WHERE sql IS NOT NULL
		   AND ((type IN ('table', 'view') AND name = ?) OR (type IN ('index', 'trigger') AND tbl_name = ? COLLATE NOCASE))
		 ORDER BY CASE type WHEN 'table' THEN 0 WHEN 'view' THEN 0 WHEN 'index' THEN 1 ELSE 2 END, name`, t.Name, t.Name)
	if err != nil {
		return "", wrap(err)
	}
	var b strings.Builder
	for i, s := range stmts {
		if i == 1 {
			b.WriteString("\n")
		}
		b.WriteString(strings.TrimRight(strings.TrimSpace(s), ";") + ";\n")
	}
	return b.String(), nil
}
