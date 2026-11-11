package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"dbedit/internal/driver"
)

// pgRel is the pg_class row of a table or view. Q* fields are quoted with
// quote_ident by the server.
type pgRel struct {
	OID         int64
	Kind        string // r, p, f, v, m
	Persistence string // p, u, t
	QSchema     string
	QName       string
	Comment     string
	Tuples      int64
	Size        sql.NullInt64
	PartKey     string // PARTITION BY clause body (partitioned tables)
	ViewDef     string // pg_get_viewdef (views, materialized views)
}

// relationSQL reads one relation; both names are bound.
const relationSQL = `
SELECT c.oid::int8, c.relkind::text, c.relpersistence::text,
       quote_ident(n.nspname), quote_ident(c.relname),
       COALESCE(pg_catalog.obj_description(c.oid, 'pg_class'), ''),
       c.reltuples::int8,
       CASE WHEN c.relkind IN ('r', 'p', 'm') THEN pg_catalog.pg_total_relation_size(c.oid) END,
       COALESCE(CASE WHEN c.relkind = 'p' THEN pg_catalog.pg_get_partkeydef(c.oid) END, ''),
       COALESCE(CASE WHEN c.relkind IN ('v', 'm') THEN pg_catalog.pg_get_viewdef(c.oid, true) END, '')
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p', 'f', 'v', 'm')`

func loadRel(ctx context.Context, db *sql.DB, schema, table string) (pgRel, error) {
	var r pgRel
	err := db.QueryRowContext(ctx, relationSQL, schema, table).Scan(
		&r.OID, &r.Kind, &r.Persistence, &r.QSchema, &r.QName, &r.Comment, &r.Tuples, &r.Size, &r.PartKey, &r.ViewDef)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("%w: %s.%s", driver.ErrTableNotFound, schema, table)
	}
	return r, err
}

// tableType maps a relkind.
func tableType(kind string) driver.TableType {
	switch kind {
	case "v":
		return driver.TypeView
	case "m":
		return driver.TypeMaterializedView
	}
	return driver.TypeTable
}

// pgColumn is one pg_attribute row with everything TableInfo and the DDL
// builder need.
type pgColumn struct {
	Name       string
	QName      string
	Num        int
	Type       string // format_type
	TypName    string // pg_type.typname (int4, int8, ...)
	Nullable   bool
	Default    sql.NullString // pg_attrdef expression (also the generation expression)
	Identity   string         // "", a (ALWAYS), d (BY DEFAULT)
	Generated  string         // "", s (STORED), v (VIRTUAL)
	Collation  string         // non-default collation name
	QCollation string         // its schema-qualified, quoted name
	Comment    sql.NullString
	PKPos      int
	Enum       sql.NullString // JSON array of labels
	OwnedSeq   bool           // owns a sequence (serial)
}

const columnsSQL = `
SELECT a.attname, quote_ident(a.attname), a.attnum,
       pg_catalog.format_type(a.atttypid, a.atttypmod), t.typname,
       NOT a.attnotnull,
       pg_catalog.pg_get_expr(d.adbin, d.adrelid),
       a.attidentity::text, a.attgenerated::text,
       COALESCE(CASE WHEN a.attcollation <> 0 AND a.attcollation <> t.typcollation THEN co.collname END, ''),
       COALESCE(CASE WHEN a.attcollation <> 0 AND a.attcollation <> t.typcollation
                     THEN quote_ident(cn.nspname) || '.' || quote_ident(co.collname) END, ''),
       pg_catalog.col_description(a.attrelid, a.attnum),
       COALESCE((SELECT k.ord FROM pg_catalog.pg_index i,
                        unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
                 WHERE i.indrelid = a.attrelid AND i.indisprimary AND k.attnum = a.attnum), 0),
       (SELECT array_to_json(array_agg(e.enumlabel ORDER BY e.enumsortorder))::text
        FROM pg_catalog.pg_enum e WHERE e.enumtypid = a.atttypid),
       EXISTS (SELECT 1 FROM pg_catalog.pg_depend dep
               JOIN pg_catalog.pg_class s ON s.oid = dep.objid AND s.relkind = 'S'
               WHERE dep.classid = 'pg_catalog.pg_class'::regclass
                 AND dep.refobjid = a.attrelid AND dep.refobjsubid = a.attnum AND dep.deptype = 'a')
FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_type t ON t.oid = a.atttypid
LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
LEFT JOIN pg_catalog.pg_collation co ON co.oid = a.attcollation
LEFT JOIN pg_catalog.pg_namespace cn ON cn.oid = co.collnamespace
WHERE a.attrelid = $1::int8::oid AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`

func loadColumns(ctx context.Context, db *sql.DB, oid int64) ([]pgColumn, error) {
	rows, err := db.QueryContext(ctx, columnsSQL, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pgColumn
	for rows.Next() {
		var c pgColumn
		if err := rows.Scan(&c.Name, &c.QName, &c.Num, &c.Type, &c.TypName, &c.Nullable, &c.Default,
			&c.Identity, &c.Generated, &c.Collation, &c.QCollation, &c.Comment, &c.PKPos, &c.Enum, &c.OwnedSeq); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// serialType is the serial pseudo-type of a column whose default is
// nextval() of a sequence it owns ("" otherwise).
func (c pgColumn) serialType() string {
	if !c.OwnedSeq || c.Identity != "" || !c.Default.Valid || !strings.HasPrefix(c.Default.String, "nextval(") {
		return ""
	}
	switch c.TypName {
	case "int2":
		return "smallserial"
	case "int4":
		return "serial"
	case "int8":
		return "bigserial"
	}
	return ""
}

func (c pgColumn) detail() (driver.ColumnDetail, error) {
	d := driver.ColumnDetail{
		Name: c.Name, Ordinal: c.Num, DataType: c.Type, Nullable: c.Nullable,
		PKPosition: c.PKPos, IsPrimaryKey: c.PKPos > 0, Collation: c.Collation, Comment: c.Comment.String,
	}
	switch c.Identity {
	case "a":
		d.Identity = "ALWAYS"
	case "d":
		d.Identity = "BY DEFAULT"
	}
	switch c.Generated {
	case "s":
		d.Generated = "STORED"
	case "v":
		d.Generated = "VIRTUAL"
	}
	if c.Default.Valid {
		expr := c.Default.String
		if d.Generated != "" {
			d.GenerationExpr = &expr
		} else {
			d.Default = &expr
		}
	}
	d.AutoIncrement = d.Identity != "" || (c.OwnedSeq && d.Default != nil && strings.HasPrefix(*d.Default, "nextval("))
	if c.Enum.Valid {
		if err := json.Unmarshal([]byte(c.Enum.String), &d.EnumValues); err != nil {
			return d, fmt.Errorf("postgres: enum labels of %s: %w", c.Name, err)
		}
	}
	return d, nil
}

// pgIndex is an index with whether a constraint (PK, UNIQUE, EXCLUDE)
// owns it.
type pgIndex struct {
	driver.Index
	Constraint bool
}

const indexesSQL = `
SELECT ic.relname, i.indisunique, i.indisprimary, am.amname,
       pg_catalog.pg_get_indexdef(i.indexrelid),
       COALESCE(pg_catalog.pg_get_expr(i.indpred, i.indrelid, true), ''),
       COALESCE((SELECT json_agg(CASE WHEN i.indkey[k.n - 1] <> 0 THEN a.attname
                                      ELSE '(' || pg_catalog.pg_get_indexdef(i.indexrelid, k.n, true) || ')' END
                                 ORDER BY k.n)
                 FROM generate_series(1, i.indnkeyatts) AS k(n)
                 LEFT JOIN pg_catalog.pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = i.indkey[k.n - 1]), '[]')::text,
       EXISTS (SELECT 1 FROM pg_catalog.pg_constraint con
               WHERE con.conindid = i.indexrelid AND con.conrelid = i.indrelid AND con.contype IN ('p', 'u', 'x'))
FROM pg_catalog.pg_index i
JOIN pg_catalog.pg_class ic ON ic.oid = i.indexrelid
JOIN pg_catalog.pg_am am ON am.oid = ic.relam
WHERE i.indrelid = $1::int8::oid
ORDER BY i.indisprimary DESC, ic.relname`

func loadIndexes(ctx context.Context, db *sql.DB, oid int64) ([]pgIndex, error) {
	rows, err := db.QueryContext(ctx, indexesSQL, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pgIndex
	for rows.Next() {
		var (
			ix   pgIndex
			cols string
		)
		if err := rows.Scan(&ix.Name, &ix.Unique, &ix.Primary, &ix.Method, &ix.Definition, &ix.Predicate, &cols, &ix.Constraint); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(cols), &ix.Columns); err != nil {
			return nil, err
		}
		out = append(out, ix)
	}
	return out, rows.Err()
}

// fkSQL lists foreign keys; %s is conrelid (outgoing) or confrelid
// (incoming, without the clones on partitions).
const fkSQL = `
SELECT con.conname, sn.nspname, sc.relname, rn.nspname, rc.relname,
       (SELECT json_agg(a.attname ORDER BY k.ord)
        FROM unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord)
        JOIN pg_catalog.pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum)::text,
       (SELECT json_agg(a.attname ORDER BY k.ord)
        FROM unnest(con.confkey) WITH ORDINALITY AS k(attnum, ord)
        JOIN pg_catalog.pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.attnum)::text,
       con.confupdtype::text, con.confdeltype::text
FROM pg_catalog.pg_constraint con
JOIN pg_catalog.pg_class sc ON sc.oid = con.conrelid
JOIN pg_catalog.pg_namespace sn ON sn.oid = sc.relnamespace
JOIN pg_catalog.pg_class rc ON rc.oid = con.confrelid
JOIN pg_catalog.pg_namespace rn ON rn.oid = rc.relnamespace
WHERE con.contype = 'f' AND %s
ORDER BY sn.nspname, sc.relname, con.conname`

func loadFKs(ctx context.Context, db *sql.DB, oid int64, incoming bool) ([]driver.ForeignKey, error) {
	cond := "con.conrelid = $1::int8::oid"
	if incoming {
		cond = "con.confrelid = $1::int8::oid AND con.conparentid = 0"
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf(fkSQL, cond), oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.ForeignKey{}
	for rows.Next() {
		var (
			fk         driver.ForeignKey
			cols, refs string
			upd, del   string
		)
		if err := rows.Scan(&fk.Name, &fk.Schema, &fk.Table, &fk.RefSchema, &fk.RefTable, &cols, &refs, &upd, &del); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(cols), &fk.Columns); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(refs), &fk.RefColumns); err != nil {
			return nil, err
		}
		fk.OnUpdate, fk.OnDelete = driver.ReferentialAction(upd), driver.ReferentialAction(del)
		out = append(out, fk)
	}
	return out, rows.Err()
}

// pgConstraint is a table constraint for the DDL: quoted name and
// pg_get_constraintdef text.
type pgConstraint struct {
	QName string
	Def   string
}

func loadConstraints(ctx context.Context, db *sql.DB, oid int64) ([]pgConstraint, error) {
	rows, err := db.QueryContext(ctx, `
SELECT quote_ident(conname), pg_catalog.pg_get_constraintdef(oid)
FROM pg_catalog.pg_constraint
WHERE conrelid = $1::int8::oid AND contype IN ('p', 'u', 'c', 'f', 'x') AND conislocal
ORDER BY CASE contype WHEN 'p' THEN 0 WHEN 'u' THEN 1 WHEN 'x' THEN 2 WHEN 'c' THEN 3 ELSE 4 END, conname`, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pgConstraint
	for rows.Next() {
		var c pgConstraint
		if err := rows.Scan(&c.QName, &c.Def); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func loadChecks(ctx context.Context, db *sql.DB, oid int64) ([]driver.Check, error) {
	rows, err := db.QueryContext(ctx, `
SELECT conname, pg_catalog.pg_get_expr(conbin, conrelid, true)
FROM pg_catalog.pg_constraint
WHERE conrelid = $1::int8::oid AND contype = 'c'
ORDER BY conname`, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Check{}
	for rows.Next() {
		var c driver.Check
		if err := rows.Scan(&c.Name, &c.Expression); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func loadTriggers(ctx context.Context, db *sql.DB, oid int64) ([]driver.Trigger, error) {
	rows, err := db.QueryContext(ctx, `
SELECT tgname, tgtype::int, pg_catalog.pg_get_triggerdef(oid)
FROM pg_catalog.pg_trigger
WHERE tgrelid = $1::int8::oid AND NOT tgisinternal
ORDER BY tgname`, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Trigger{}
	for rows.Next() {
		var (
			t   driver.Trigger
			typ int
		)
		if err := rows.Scan(&t.Name, &typ, &t.Definition); err != nil {
			return nil, err
		}
		t.Timing, t.Event = triggerType(typ)
		out = append(out, t)
	}
	return out, rows.Err()
}

// triggerType decodes pg_trigger.tgtype (see pg_trigger.h).
func triggerType(typ int) (timing, event string) {
	switch {
	case typ&(1<<6) != 0:
		timing = "INSTEAD OF"
	case typ&(1<<1) != 0:
		timing = "BEFORE"
	default:
		timing = "AFTER"
	}
	var ev []string
	for _, e := range []struct {
		bit  int
		name string
	}{{2, "INSERT"}, {3, "DELETE"}, {4, "UPDATE"}, {5, "TRUNCATE"}} {
		if typ&(1<<e.bit) != 0 {
			ev = append(ev, e.name)
		}
	}
	return timing, strings.Join(ev, " OR ")
}

// TableInfo implements driver.Introspector from pg_catalog; every name is a
// bound parameter (the relation is looked up once, then by OID).
func (*Driver) TableInfo(ctx context.Context, db *sql.DB, schema, table string) (*driver.TableDetail, error) {
	wrap := func(err error) error { return fmt.Errorf("postgres: table info: %w", err) }
	rel, err := loadRel(ctx, db, schema, table)
	if err != nil {
		return nil, wrap(err)
	}
	info := driver.NewTableDetail(driver.Table{Schema: schema, Name: table, Type: tableType(rel.Kind)})
	info.Comment = rel.Comment
	if info.Type == driver.TypeTable && rel.Tuples >= 0 {
		n := rel.Tuples
		info.EstimatedRows = &n
	}
	if rel.Size.Valid {
		n := rel.Size.Int64
		info.SizeBytes = &n
	}
	cols, err := loadColumns(ctx, db, rel.OID)
	if err != nil {
		return nil, wrap(err)
	}
	for _, c := range cols {
		d, err := c.detail()
		if err != nil {
			return nil, err
		}
		info.Columns = append(info.Columns, d)
	}
	idx, err := loadIndexes(ctx, db, rel.OID)
	if err != nil {
		return nil, wrap(err)
	}
	for _, ix := range idx {
		info.Indexes = append(info.Indexes, ix.Index)
	}
	if info.ForeignKeys, err = loadFKs(ctx, db, rel.OID, false); err != nil {
		return nil, wrap(err)
	}
	if info.ReferencedBy, err = loadFKs(ctx, db, rel.OID, true); err != nil {
		return nil, wrap(err)
	}
	if info.Checks, err = loadChecks(ctx, db, rel.OID); err != nil {
		return nil, wrap(err)
	}
	info.ChecksSupported = true
	if info.Triggers, err = loadTriggers(ctx, db, rel.OID); err != nil {
		return nil, wrap(err)
	}
	return info, nil
}

// TableDDL implements driver.Introspector. PostgreSQL has no built-in
// CREATE TABLE output, so it is rebuilt from the catalog (see buildDDL).
func (*Driver) TableDDL(ctx context.Context, db *sql.DB, schema, table string) (string, error) {
	wrap := func(err error) error { return fmt.Errorf("postgres: table DDL: %w", err) }
	rel, err := loadRel(ctx, db, schema, table)
	if err != nil {
		return "", wrap(err)
	}
	cols, err := loadColumns(ctx, db, rel.OID)
	if err != nil {
		return "", wrap(err)
	}
	var cons []pgConstraint
	if rel.Kind != "v" && rel.Kind != "m" {
		if cons, err = loadConstraints(ctx, db, rel.OID); err != nil {
			return "", wrap(err)
		}
	}
	idx, err := loadIndexes(ctx, db, rel.OID)
	if err != nil {
		return "", wrap(err)
	}
	var indexDefs []string
	for _, ix := range idx {
		if !ix.Constraint {
			indexDefs = append(indexDefs, ix.Definition)
		}
	}
	trg, err := loadTriggers(ctx, db, rel.OID)
	if err != nil {
		return "", wrap(err)
	}
	var trgDefs []string
	for _, t := range trg {
		trgDefs = append(trgDefs, t.Definition)
	}
	return buildDDL(rel, cols, cons, indexDefs, trgDefs), nil
}
