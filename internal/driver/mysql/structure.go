package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"dbedit/internal/driver"
)

// hasColumn reports whether information_schema.<table> has column (feature
// probe: STATISTICS.EXPRESSION exists from MySQL 8.0.13, CHECK_CONSTRAINTS
// from 8.0.16 and in MariaDB, where it also has TABLE_NAME).
func hasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM information_schema.COLUMNS
		 WHERE TABLE_SCHEMA = 'information_schema' AND TABLE_NAME = ? AND COLUMN_NAME = ?`, table, column).Scan(&n)
	return n > 0, err
}

// TableInfo implements driver.Introspector from information_schema; every
// name is a bound parameter.
func (d *Driver) TableInfo(ctx context.Context, db *sql.DB, schema, table string) (*driver.TableDetail, error) {
	wrap := func(err error) error { return fmt.Errorf("%s: table info: %w", d.kind, err) }
	var (
		typ                        string
		engine, collation, comment sql.NullString
		tableRows, dataLen, idxLen sql.NullInt64
	)
	err := db.QueryRowContext(ctx,
		`SELECT TABLE_TYPE, ENGINE, TABLE_COLLATION, TABLE_COMMENT, TABLE_ROWS, DATA_LENGTH, INDEX_LENGTH
		 FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, schema, table).
		Scan(&typ, &engine, &collation, &comment, &tableRows, &dataLen, &idxLen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, wrap(fmt.Errorf("%w: %s.%s", driver.ErrTableNotFound, schema, table))
	}
	if err != nil {
		return nil, wrap(err)
	}
	t := driver.Table{Schema: schema, Name: table, Type: driver.TypeTable}
	if strings.Contains(typ, "VIEW") {
		t.Type = driver.TypeView
	}
	info := driver.NewTableDetail(t)
	if t.Type == driver.TypeTable {
		info.Comment = comment.String
		info.Engine = engine.String
		info.Collation = collation.String
		if tableRows.Valid {
			n := tableRows.Int64
			info.EstimatedRows = &n
		}
		if dataLen.Valid || idxLen.Valid {
			n := dataLen.Int64 + idxLen.Int64
			info.SizeBytes = &n
		}
	}
	if info.Columns, err = d.columnDetails(ctx, db, schema, table); err != nil {
		return nil, wrap(err)
	}
	if info.Indexes, err = d.indexes(ctx, db, schema, table); err != nil {
		return nil, wrap(err)
	}
	if info.ForeignKeys, err = foreignKeys(ctx, db, false, schema, table); err != nil {
		return nil, wrap(err)
	}
	if info.ReferencedBy, err = foreignKeys(ctx, db, true, schema, table); err != nil {
		return nil, wrap(err)
	}
	if info.Checks, info.ChecksSupported, err = checks(ctx, db, schema, table); err != nil {
		return nil, wrap(err)
	}
	if info.Triggers, err = d.triggers(ctx, db, schema, table); err != nil {
		return nil, wrap(err)
	}
	return info, nil
}

func (d *Driver) columnDetails(ctx context.Context, db *sql.DB, schema, table string) ([]driver.ColumnDetail, error) {
	pk, err := d.PrimaryKey(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	pkPos := map[string]int{}
	for i, c := range pk {
		pkPos[c] = i + 1
	}
	rows, err := db.QueryContext(ctx,
		`SELECT COLUMN_NAME, ORDINAL_POSITION, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA,
		        COLLATION_NAME, COLUMN_COMMENT, GENERATION_EXPRESSION
		 FROM information_schema.COLUMNS
		 WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		 ORDER BY ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.ColumnDetail{}
	for rows.Next() {
		var (
			c                    driver.ColumnDetail
			nullable, extra      string
			def, coll, comm, gen sql.NullString
		)
		if err := rows.Scan(&c.Name, &c.Ordinal, &c.DataType, &nullable, &def, &extra, &coll, &comm, &gen); err != nil {
			return nil, err
		}
		c.Nullable = nullable == "YES"
		c.Collation = coll.String
		c.Comment = comm.String
		c.EnumValues = ParseEnum(c.DataType)
		c.PKPosition = pkPos[c.Name]
		c.IsPrimaryKey = c.PKPosition > 0
		applyExtra(&c, extra)
		if c.Generated != "" {
			if gen.String != "" {
				g := gen.String
				c.GenerationExpr = &g
			}
		} else if def.Valid {
			v := def.String
			c.Default = &v
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// applyExtra reads information_schema.COLUMNS.EXTRA: auto_increment,
// VIRTUAL/STORED GENERATED, on update <expr> (DEFAULT_GENERATED is noise).
func applyExtra(c *driver.ColumnDetail, extra string) {
	low := strings.ToLower(extra)
	c.AutoIncrement = strings.Contains(low, "auto_increment")
	switch {
	case strings.Contains(low, "stored generated"), strings.Contains(low, "persistent generated"):
		c.Generated = "STORED"
	case strings.Contains(low, "virtual generated"):
		c.Generated = "VIRTUAL"
	}
	if i := strings.Index(low, "on update "); i >= 0 {
		c.OnUpdate = strings.TrimSpace(extra[i+len("on update "):])
	}
}

func (d *Driver) indexes(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Index, error) {
	hasExpr, err := hasColumn(ctx, db, "STATISTICS", "EXPRESSION")
	if err != nil {
		return nil, err
	}
	expr := "NULL"
	if hasExpr {
		expr = "EXPRESSION"
	}
	rows, err := db.QueryContext(ctx,
		`SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME, SUB_PART, INDEX_TYPE, `+expr+`
		 FROM information_schema.STATISTICS
		 WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		 ORDER BY INDEX_NAME = 'PRIMARY' DESC, INDEX_NAME, SEQ_IN_INDEX`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Index{}
	var quoted [][]string
	for rows.Next() {
		var (
			name, method string
			nonUnique    int
			col, ex      sql.NullString
			sub          sql.NullInt64
		)
		if err := rows.Scan(&name, &nonUnique, &col, &sub, &method, &ex); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Name != name {
			out = append(out, driver.Index{Name: name, Unique: nonUnique == 0, Primary: name == "PRIMARY", Method: method, Columns: []string{}})
			quoted = append(quoted, nil)
		}
		ix := &out[len(out)-1]
		var shown, q string
		switch {
		case col.Valid:
			shown, q = col.String, d.QuoteIdent(col.String)
		default:
			shown, q = "("+ex.String+")", "("+ex.String+")"
		}
		if sub.Valid {
			n := "(" + strconv.FormatInt(sub.Int64, 10) + ")"
			shown, q = shown+n, q+n
		}
		ix.Columns = append(ix.Columns, shown)
		quoted[len(quoted)-1] = append(quoted[len(quoted)-1], q)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Definition = d.indexDefinition(out[i], quoted[i])
	}
	return out, nil
}

// indexDefinition describes an index the way SHOW CREATE TABLE lists it.
func (d *Driver) indexDefinition(ix driver.Index, cols []string) string {
	var kind string
	switch {
	case ix.Primary:
		kind = "PRIMARY KEY"
	case ix.Method == "FULLTEXT" || ix.Method == "SPATIAL":
		kind = ix.Method + " KEY " + d.QuoteIdent(ix.Name)
	case ix.Unique:
		kind = "UNIQUE KEY " + d.QuoteIdent(ix.Name)
	default:
		kind = "KEY " + d.QuoteIdent(ix.Name)
	}
	def := kind + " (" + strings.Join(cols, ",") + ")"
	if ix.Method != "" && ix.Method != "FULLTEXT" && ix.Method != "SPATIAL" {
		def += " USING " + ix.Method
	}
	return def
}

// foreignKeys lists outgoing (or incoming) foreign keys from
// KEY_COLUMN_USAGE and REFERENTIAL_CONSTRAINTS.
func foreignKeys(ctx context.Context, db *sql.DB, incoming bool, schema, table string) ([]driver.ForeignKey, error) {
	cond := "k.TABLE_SCHEMA = ? AND k.TABLE_NAME = ?"
	if incoming {
		cond = "k.REFERENCED_TABLE_SCHEMA = ? AND k.REFERENCED_TABLE_NAME = ?"
	}
	rows, err := db.QueryContext(ctx,
		`SELECT k.CONSTRAINT_NAME, k.TABLE_SCHEMA, k.TABLE_NAME, k.COLUMN_NAME,
		        k.REFERENCED_TABLE_SCHEMA, k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME,
		        r.UPDATE_RULE, r.DELETE_RULE
		 FROM information_schema.KEY_COLUMN_USAGE k
		 JOIN information_schema.REFERENTIAL_CONSTRAINTS r
		   ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME
		  AND r.TABLE_NAME = k.TABLE_NAME
		 WHERE `+cond+` AND k.REFERENCED_TABLE_NAME IS NOT NULL
		 ORDER BY k.TABLE_SCHEMA, k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.ForeignKey{}
	for rows.Next() {
		var fk driver.ForeignKey
		var col, ref string
		if err := rows.Scan(&fk.Name, &fk.Schema, &fk.Table, &col, &fk.RefSchema, &fk.RefTable, &ref, &fk.OnUpdate, &fk.OnDelete); err != nil {
			return nil, err
		}
		if n := len(out); n > 0 && out[n-1].Name == fk.Name && out[n-1].Schema == fk.Schema && out[n-1].Table == fk.Table {
			out[n-1].Columns = append(out[n-1].Columns, col)
			out[n-1].RefColumns = append(out[n-1].RefColumns, ref)
			continue
		}
		fk.Columns, fk.RefColumns = []string{col}, []string{ref}
		out = append(out, fk)
	}
	return out, rows.Err()
}

// checks reads CHECK constraints: MariaDB's CHECK_CONSTRAINTS has
// TABLE_NAME; MySQL (8.0.16+) joins TABLE_CONSTRAINTS. Older servers do
// not report them (supported = false).
func checks(ctx context.Context, db *sql.DB, schema, table string) (out []driver.Check, supported bool, err error) {
	out = []driver.Check{}
	maria, err := hasColumn(ctx, db, "CHECK_CONSTRAINTS", "TABLE_NAME")
	if err != nil {
		return nil, false, err
	}
	var q string
	switch {
	case maria:
		q = `SELECT CONSTRAINT_NAME, CHECK_CLAUSE FROM information_schema.CHECK_CONSTRAINTS
		     WHERE CONSTRAINT_SCHEMA = ? AND TABLE_NAME = ? ORDER BY CONSTRAINT_NAME`
	default:
		ok, err := hasColumn(ctx, db, "CHECK_CONSTRAINTS", "CHECK_CLAUSE")
		if err != nil || !ok {
			return out, false, err
		}
		q = `SELECT tc.CONSTRAINT_NAME, cc.CHECK_CLAUSE
		     FROM information_schema.TABLE_CONSTRAINTS tc
		     JOIN information_schema.CHECK_CONSTRAINTS cc
		       ON cc.CONSTRAINT_SCHEMA = tc.CONSTRAINT_SCHEMA AND cc.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
		     WHERE tc.TABLE_SCHEMA = ? AND tc.TABLE_NAME = ? AND tc.CONSTRAINT_TYPE = 'CHECK'
		     ORDER BY tc.CONSTRAINT_NAME`
	}
	rows, err := db.QueryContext(ctx, q, schema, table)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var c driver.Check
		if err := rows.Scan(&c.Name, &c.Expression); err != nil {
			return nil, false, err
		}
		out = append(out, c)
	}
	return out, true, rows.Err()
}

func (d *Driver) triggers(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Trigger, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT TRIGGER_NAME, ACTION_TIMING, EVENT_MANIPULATION, ACTION_ORIENTATION, ACTION_STATEMENT
		 FROM information_schema.TRIGGERS
		 WHERE EVENT_OBJECT_SCHEMA = ? AND EVENT_OBJECT_TABLE = ?
		 ORDER BY ACTION_TIMING, EVENT_MANIPULATION, ACTION_ORDER`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Trigger{}
	for rows.Next() {
		var t driver.Trigger
		var orient, stmt string
		if err := rows.Scan(&t.Name, &t.Timing, &t.Event, &orient, &stmt); err != nil {
			return nil, err
		}
		t.Definition = "CREATE TRIGGER " + d.QuoteIdent(t.Name) + " " + t.Timing + " " + t.Event +
			" ON " + d.QuoteIdent(table) + " FOR EACH " + orient + " " + stmt
		out = append(out, t)
	}
	return out, rows.Err()
}

// TableDDL implements driver.Introspector with SHOW CREATE TABLE / VIEW
// (plus SHOW CREATE TRIGGER for the table's triggers). The names are
// checked against ListTables and the trigger list first, then quoted:
// SHOW statements take no bind parameters.
func (d *Driver) TableDDL(ctx context.Context, db *sql.DB, schema, table string) (string, error) {
	wrap := func(err error) error { return fmt.Errorf("%s: table DDL: %w", d.kind, err) }
	t, err := driver.FindTable(ctx, d, db, schema, table)
	if err != nil {
		return "", wrap(err)
	}
	name := d.QuoteIdent(t.Schema) + "." + d.QuoteIdent(t.Name)
	stmt := "SHOW CREATE TABLE " + name
	if t.Type == driver.TypeView {
		stmt = "SHOW CREATE VIEW " + name
	}
	ddl, err := showCreate(ctx, db, stmt, 1)
	if err != nil {
		return "", wrap(err)
	}
	out := strings.TrimRight(ddl, "; \n") + ";\n"
	if t.Type != driver.TypeTable {
		return out, nil
	}
	trg, err := driver.ScanStrings(ctx, db,
		`SELECT TRIGGER_NAME FROM information_schema.TRIGGERS
		 WHERE EVENT_OBJECT_SCHEMA = ? AND EVENT_OBJECT_TABLE = ? ORDER BY ACTION_TIMING, EVENT_MANIPULATION, ACTION_ORDER`, schema, table)
	if err != nil {
		return "", wrap(err)
	}
	for _, name := range trg {
		def, err := showCreate(ctx, db, "SHOW CREATE TRIGGER "+d.QuoteIdent(schema)+"."+d.QuoteIdent(name), 2)
		if err != nil {
			return "", wrap(err)
		}
		out += "\n" + delimited(def)
	}
	return out, nil
}

// delimited terminates a statement; one whose body contains ";" is
// wrapped in DELIMITER commands (understood by the SQL editor's splitter
// and the mysql client).
func delimited(stmt string) string {
	stmt = strings.TrimRight(stmt, "; \n")
	if !strings.Contains(stmt, ";") {
		return stmt + ";\n"
	}
	return "DELIMITER ;;\n" + stmt + ";;\nDELIMITER ;\n"
}

// showCreate runs a SHOW CREATE statement and returns column col of its
// single row.
func showCreate(ctx context.Context, db *sql.DB, stmt string, col int) (string, error) {
	rows, err := db.QueryContext(ctx, stmt)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("%s returned no rows", stmt)
	}
	vals := make([]sql.NullString, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return "", err
	}
	if col >= len(vals) {
		return "", fmt.Errorf("%s returned %d columns", stmt, len(vals))
	}
	return vals[col].String, rows.Err()
}
