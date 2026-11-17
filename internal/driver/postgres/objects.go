package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"lontar/internal/driver"
)

// Objects other than tables and views (driver.ObjectLister). Every query
// binds the schema name; ObjectDDL re-reads the object by the OID that
// the listing reported (FindObject), so nothing from the UI reaches SQL.
// Objects that belong to an extension are not listed (they are created by
// CREATE EXTENSION), nor are the sequences behind identity columns (part
// of their table's DDL).

var _ driver.ObjectLister = (*Driver)(nil)

// notExtension excludes members of an extension (pg_depend deptype 'e');
// %s is the catalog (pg_proc, pg_type, pg_class) and the object's OID
// expression.
const notExtension = `NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend dx
  WHERE dx.classid = 'pg_catalog.%s'::regclass AND dx.objid = %s AND dx.deptype = 'e')`

// pgRoutine is a pg_proc row.
type pgRoutine struct {
	OID        int64
	Name       string
	Kind       string // f, p, a, w
	Args       string // pg_get_function_identity_arguments
	Result     string // pg_get_function_result ("" for procedures)
	Language   string
	Volatility string // i, s, v
	SecDef     bool
	Comment    string
	Owner      string
}

var routinesSQL = `
SELECT p.oid::int8, p.proname, p.prokind::text,
       pg_catalog.pg_get_function_identity_arguments(p.oid),
       COALESCE(pg_catalog.pg_get_function_result(p.oid), ''),
       l.lanname, p.provolatile::text, p.prosecdef,
       COALESCE(pg_catalog.obj_description(p.oid, 'pg_proc'), ''),
       pg_catalog.pg_get_userbyid(p.proowner)
FROM pg_catalog.pg_proc p
JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
JOIN pg_catalog.pg_language l ON l.oid = p.prolang
WHERE n.nspname = $1 AND p.prokind IN ('f', 'p', 'a', 'w')
  AND ` + fmt.Sprintf(notExtension, "pg_proc", "p.oid") + `
ORDER BY p.proname, 4`

func loadRoutines(ctx context.Context, db *sql.DB, schema string) ([]pgRoutine, error) {
	rows, err := db.QueryContext(ctx, routinesSQL, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pgRoutine
	for rows.Next() {
		var r pgRoutine
		if err := rows.Scan(&r.OID, &r.Name, &r.Kind, &r.Args, &r.Result, &r.Language, &r.Volatility, &r.SecDef, &r.Comment, &r.Owner); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (r pgRoutine) object(schema string) driver.Object {
	o := driver.Object{Schema: schema, Name: r.Name, Signature: r.Args, ID: strconv.FormatInt(r.OID, 10)}
	security := "invoker"
	if r.SecDef {
		security = "definer"
	}
	volatility := map[string]string{"i": "immutable", "s": "stable", "v": "volatile"}[r.Volatility]
	switch r.Kind {
	case "p":
		o.Kind = driver.ObjectProcedure
		o.Detail = r.Language
		o.Properties = driver.Props("Arguments", r.Args, "Language", r.Language, "Security", security, "Owner", r.Owner, "Comment", r.Comment)
	case "a":
		o.Kind = driver.ObjectAggregate
		o.Detail = "aggregate returning " + r.Result
		o.Properties = driver.Props("Arguments", r.Args, "Returns", r.Result, "Owner", r.Owner, "Comment", r.Comment)
	default:
		o.Kind = driver.ObjectFunction
		o.Detail = "returns " + r.Result + " · " + r.Language
		o.Properties = driver.Props("Arguments", r.Args, "Returns", r.Result, "Language", r.Language,
			"Volatility", volatility, "Security", security, "Owner", r.Owner, "Comment", r.Comment)
	}
	return o
}

// pgSequence is a pg_sequence row with its owning column.
type pgSequence struct {
	OID       int64
	Name      string
	QSchema   string
	QName     string
	Type      string
	Start     int64
	Increment int64
	Min       int64
	Max       int64
	Cache     int64
	Cycle     bool
	Last      sql.NullInt64 // NULL before the first nextval or without privilege
	OwnedBy   string        // table.column
	QOwnedBy  string        // schema.table.column, quoted
	Comment   string
}

// sequencesSQL lists the sequences of $1, or only OID $2 when it is not 0.
var sequencesSQL = `
SELECT c.oid::int8, c.relname, quote_ident(n.nspname), quote_ident(c.relname),
       pg_catalog.format_type(s.seqtypid, NULL),
       s.seqstart, s.seqincrement, s.seqmin, s.seqmax, s.seqcache, s.seqcycle,
       CASE WHEN pg_catalog.has_sequence_privilege(c.oid, 'SELECT,USAGE')
            THEN pg_catalog.pg_sequence_last_value(c.oid) END,
       COALESCE(o.owned, ''), COALESCE(o.qowned, ''),
       COALESCE(pg_catalog.obj_description(c.oid, 'pg_class'), '')
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
JOIN pg_catalog.pg_sequence s ON s.seqrelid = c.oid
LEFT JOIN LATERAL (
  SELECT t.relname || '.' || a.attname AS owned,
         quote_ident(tn.nspname) || '.' || quote_ident(t.relname) || '.' || quote_ident(a.attname) AS qowned
  FROM pg_catalog.pg_depend d
  JOIN pg_catalog.pg_class t ON t.oid = d.refobjid
  JOIN pg_catalog.pg_namespace tn ON tn.oid = t.relnamespace
  JOIN pg_catalog.pg_attribute a ON a.attrelid = t.oid AND a.attnum = d.refobjsubid
  WHERE d.classid = 'pg_catalog.pg_class'::regclass AND d.objid = c.oid
    AND d.refclassid = 'pg_catalog.pg_class'::regclass AND d.deptype = 'a' AND d.refobjsubid > 0
  LIMIT 1) o ON true
WHERE n.nspname = $1 AND c.relkind = 'S' AND ($2::int8 = 0 OR c.oid = $2::int8::oid)
  AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend di
                  WHERE di.classid = 'pg_catalog.pg_class'::regclass AND di.objid = c.oid AND di.deptype = 'i')
  AND ` + fmt.Sprintf(notExtension, "pg_class", "c.oid") + `
ORDER BY c.relname`

func loadSequences(ctx context.Context, db *sql.DB, schema string, oid int64) ([]pgSequence, error) {
	rows, err := db.QueryContext(ctx, sequencesSQL, schema, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pgSequence
	for rows.Next() {
		var s pgSequence
		if err := rows.Scan(&s.OID, &s.Name, &s.QSchema, &s.QName, &s.Type, &s.Start, &s.Increment, &s.Min, &s.Max,
			&s.Cache, &s.Cycle, &s.Last, &s.OwnedBy, &s.QOwnedBy, &s.Comment); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (s pgSequence) object(schema string) driver.Object {
	i := func(n int64) string { return strconv.FormatInt(n, 10) }
	current := ""
	if s.Last.Valid {
		current = i(s.Last.Int64)
	}
	detail := s.Type
	if s.OwnedBy != "" {
		detail += " · owned by " + s.OwnedBy
	}
	return driver.Object{
		Schema: schema, Kind: driver.ObjectSequence, Name: s.Name, ID: i(s.OID), Detail: detail,
		Properties: driver.Props("Type", s.Type, "Current value", current, "Start", i(s.Start), "Increment", i(s.Increment),
			"Minimum", i(s.Min), "Maximum", i(s.Max), "Cache", i(s.Cache), "Cycle", yesNo(s.Cycle),
			"Owned by", s.OwnedBy, "Comment", s.Comment),
	}
}

// sequenceDDL rebuilds CREATE SEQUENCE (and OWNED BY, COMMENT ON).
func sequenceDDL(s pgSequence) string {
	q := s.QSchema + "." + s.QName
	i := func(n int64) string { return strconv.FormatInt(n, 10) }
	cycle := "NO CYCLE"
	if s.Cycle {
		cycle = "CYCLE"
	}
	var b strings.Builder
	b.WriteString("CREATE SEQUENCE " + q + "\n    AS " + s.Type + "\n    INCREMENT BY " + i(s.Increment) +
		"\n    MINVALUE " + i(s.Min) + "\n    MAXVALUE " + i(s.Max) + "\n    START WITH " + i(s.Start) +
		"\n    CACHE " + i(s.Cache) + "\n    " + cycle + ";\n")
	if s.QOwnedBy != "" {
		b.WriteString("\nALTER SEQUENCE " + q + " OWNED BY " + s.QOwnedBy + ";\n")
	}
	if s.Comment != "" {
		b.WriteString("\nCOMMENT ON SEQUENCE " + q + " IS " + literal(s.Comment) + ";\n")
	}
	return b.String()
}

// pgType is an enum, composite or domain pg_type row.
type pgType struct {
	OID         int64
	Name        string
	QSchema     string
	QName       string
	Kind        string // e, c, d
	Comment     string
	Base        string // domain base type
	NotNull     bool
	Default     sql.NullString
	Labels      sql.NullString // enum: JSON array of labels
	Attrs       sql.NullString // composite: JSON array of [name, quoted name, type, collation]
	Constraints sql.NullString // domain: JSON array of [quoted name, definition]
}

// typesSQL lists the enum, composite and domain types of $1, or only OID
// $2 when it is not 0. Composite types are those of CREATE TYPE ... AS
// (relkind 'c'), not the row types of tables.
var typesSQL = `
SELECT t.oid::int8, t.typname, quote_ident(n.nspname), quote_ident(t.typname), t.typtype::text,
       COALESCE(pg_catalog.obj_description(t.oid, 'pg_type'), ''),
       CASE WHEN t.typtype = 'd' THEN pg_catalog.format_type(t.typbasetype, t.typtypmod) ELSE '' END,
       t.typnotnull, t.typdefault,
       (SELECT array_to_json(array_agg(e.enumlabel ORDER BY e.enumsortorder))::text
        FROM pg_catalog.pg_enum e WHERE e.enumtypid = t.oid),
       (SELECT array_to_json(array_agg(json_build_array(a.attname, quote_ident(a.attname),
                 pg_catalog.format_type(a.atttypid, a.atttypmod),
                 CASE WHEN a.attcollation <> 0 AND a.attcollation <> at.typcollation
                      THEN quote_ident(cn.nspname) || '.' || quote_ident(co.collname) ELSE '' END) ORDER BY a.attnum))::text
        FROM pg_catalog.pg_attribute a
        JOIN pg_catalog.pg_type at ON at.oid = a.atttypid
        LEFT JOIN pg_catalog.pg_collation co ON co.oid = a.attcollation
        LEFT JOIN pg_catalog.pg_namespace cn ON cn.oid = co.collnamespace
        WHERE a.attrelid = t.typrelid AND a.attnum > 0 AND NOT a.attisdropped),
       (SELECT array_to_json(array_agg(json_build_array(quote_ident(k.conname), pg_catalog.pg_get_constraintdef(k.oid)) ORDER BY k.conname))::text
        FROM pg_catalog.pg_constraint k WHERE k.contypid = t.oid)
FROM pg_catalog.pg_type t
JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace
LEFT JOIN pg_catalog.pg_class r ON r.oid = t.typrelid
WHERE n.nspname = $1 AND ($2::int8 = 0 OR t.oid = $2::int8::oid)
  AND (t.typtype IN ('e', 'd') OR (t.typtype = 'c' AND r.relkind = 'c'))
  AND ` + fmt.Sprintf(notExtension, "pg_type", "t.oid") + `
ORDER BY t.typname`

func loadTypes(ctx context.Context, db *sql.DB, schema string, oid int64) ([]pgType, error) {
	rows, err := db.QueryContext(ctx, typesSQL, schema, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pgType
	for rows.Next() {
		var t pgType
		if err := rows.Scan(&t.OID, &t.Name, &t.QSchema, &t.QName, &t.Kind, &t.Comment, &t.Base, &t.NotNull, &t.Default,
			&t.Labels, &t.Attrs, &t.Constraints); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// jsonRows decodes a JSON array (NULL: empty).
func jsonRows[T any](s sql.NullString) ([]T, error) {
	var out []T
	if !s.Valid {
		return out, nil
	}
	err := json.Unmarshal([]byte(s.String), &out)
	return out, err
}

// typeParts decodes the JSON columns of a type.
func (t pgType) parts() (labels []string, attrs, cons [][]string, err error) {
	if labels, err = jsonRows[string](t.Labels); err != nil {
		return nil, nil, nil, err
	}
	if attrs, err = jsonRows[[]string](t.Attrs); err != nil {
		return nil, nil, nil, err
	}
	if cons, err = jsonRows[[]string](t.Constraints); err != nil {
		return nil, nil, nil, err
	}
	return labels, attrs, cons, nil
}

func (t pgType) object(schema string) (driver.Object, error) {
	labels, attrs, cons, err := t.parts()
	if err != nil {
		return driver.Object{}, err
	}
	o := driver.Object{Schema: schema, Name: t.Name, ID: strconv.FormatInt(t.OID, 10)}
	switch t.Kind {
	case "e":
		o.Kind = driver.ObjectEnum
		o.Detail = "enum (" + strings.Join(labels, ", ") + ")"
		o.Properties = driver.Props("Values", strings.Join(labels, ", "), "Comment", t.Comment)
	case "c":
		var list []string
		for _, a := range attrs {
			list = append(list, a[0]+" "+a[2])
		}
		o.Kind = driver.ObjectComposite
		o.Detail = "composite (" + strings.Join(list, ", ") + ")"
		o.Properties = driver.Props("Attributes", strings.Join(list, ", "), "Comment", t.Comment)
	default:
		var checks []string
		for _, c := range cons {
			checks = append(checks, c[1])
		}
		o.Kind = driver.ObjectDomain
		o.Detail = "domain over " + t.Base
		o.Properties = driver.Props("Base type", t.Base, "Default", t.Default.String, "Not null", yesNo(t.NotNull),
			"Constraints", strings.Join(checks, "\n"), "Comment", t.Comment)
	}
	return o, nil
}

// typeDDL rebuilds CREATE TYPE / CREATE DOMAIN (and COMMENT ON).
func typeDDL(t pgType) (string, error) {
	labels, attrs, cons, err := t.parts()
	if err != nil {
		return "", err
	}
	q := t.QSchema + "." + t.QName
	keyword := "TYPE"
	var b strings.Builder
	switch t.Kind {
	case "e":
		quoted := make([]string, len(labels))
		for i, l := range labels {
			quoted[i] = literal(l)
		}
		b.WriteString("CREATE TYPE " + q + " AS ENUM (\n    " + strings.Join(quoted, ",\n    ") + "\n);\n")
	case "c":
		items := make([]string, len(attrs))
		for i, a := range attrs {
			items[i] = a[1] + " " + a[2]
			if a[3] != "" {
				items[i] += " COLLATE " + a[3]
			}
		}
		b.WriteString("CREATE TYPE " + q + " AS (")
		if len(items) > 0 {
			b.WriteString("\n    " + strings.Join(items, ",\n    ") + "\n")
		}
		b.WriteString(");\n")
	default:
		keyword = "DOMAIN"
		b.WriteString("CREATE DOMAIN " + q + " AS " + t.Base)
		if t.Default.Valid {
			b.WriteString("\n    DEFAULT " + t.Default.String)
		}
		notNullConstraint := false
		for _, c := range cons {
			notNullConstraint = notNullConstraint || c[1] == "NOT NULL"
		}
		if t.NotNull && !notNullConstraint {
			b.WriteString("\n    NOT NULL")
		}
		for _, c := range cons {
			b.WriteString("\n    CONSTRAINT " + c[0] + " " + c[1])
		}
		b.WriteString(";\n")
	}
	if t.Comment != "" {
		b.WriteString("\nCOMMENT ON " + keyword + " " + q + " IS " + literal(t.Comment) + ";\n")
	}
	return b.String(), nil
}

// pgObjTrigger is a pg_trigger row of any table in the schema.
type pgObjTrigger struct {
	OID        int64
	Name       string
	Table      string
	Type       int
	Definition string
	Function   string
	Enabled    string // O, D, R, A
}

const objTriggersSQL = `
SELECT t.oid::int8, t.tgname, c.relname, t.tgtype::int, pg_catalog.pg_get_triggerdef(t.oid),
       t.tgfoid::regprocedure::text, t.tgenabled::text
FROM pg_catalog.pg_trigger t
JOIN pg_catalog.pg_class c ON c.oid = t.tgrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND NOT t.tgisinternal
ORDER BY t.tgname, c.relname`

func loadObjTriggers(ctx context.Context, db *sql.DB, schema string) ([]pgObjTrigger, error) {
	rows, err := db.QueryContext(ctx, objTriggersSQL, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pgObjTrigger
	for rows.Next() {
		var t pgObjTrigger
		if err := rows.Scan(&t.OID, &t.Name, &t.Table, &t.Type, &t.Definition, &t.Function, &t.Enabled); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (t pgObjTrigger) object(schema string) driver.Object {
	timing, event := triggerType(t.Type)
	level := "statement"
	if t.Type&1 != 0 {
		level = "row"
	}
	enabled := map[string]string{"O": "yes", "D": "no", "R": "replica only", "A": "always"}[t.Enabled]
	return driver.Object{
		Schema: schema, Kind: driver.ObjectTrigger, Name: t.Name, Table: t.Table, ID: strconv.FormatInt(t.OID, 10),
		Detail: timing + " " + event + " on " + t.Table,
		Properties: driver.Props("Table", t.Table, "Timing", timing, "Events", event, "Level", level,
			"Function", t.Function, "Enabled", enabled),
	}
}

// ListObjects implements driver.ObjectLister: functions, procedures and
// aggregates (pg_proc), sequences, enum/composite/domain types and the
// triggers of every table of schema.
func (*Driver) ListObjects(ctx context.Context, db *sql.DB, schema string) ([]driver.Object, error) {
	wrap := func(err error) error { return fmt.Errorf("postgres: list objects: %w", err) }
	out := []driver.Object{}
	routines, err := loadRoutines(ctx, db, schema)
	if err != nil {
		return nil, wrap(err)
	}
	for _, r := range routines {
		out = append(out, r.object(schema))
	}
	seqs, err := loadSequences(ctx, db, schema, 0)
	if err != nil {
		return nil, wrap(err)
	}
	for _, s := range seqs {
		out = append(out, s.object(schema))
	}
	types, err := loadTypes(ctx, db, schema, 0)
	if err != nil {
		return nil, wrap(err)
	}
	for _, t := range types {
		o, err := t.object(schema)
		if err != nil {
			return nil, wrap(err)
		}
		out = append(out, o)
	}
	trg, err := loadObjTriggers(ctx, db, schema)
	if err != nil {
		return nil, wrap(err)
	}
	for _, t := range trg {
		out = append(out, t.object(schema))
	}
	driver.SortObjects(out)
	return out, nil
}

// ObjectDDL implements driver.ObjectLister. Routines and triggers use the
// server's pg_get_functiondef / pg_get_triggerdef; aggregates, sequences
// and types are rebuilt from the catalog. The object is read by the OID
// its listing reported.
func (d *Driver) ObjectDDL(ctx context.Context, db *sql.DB, schema string, ref driver.ObjectRef) (string, error) {
	wrap := func(err error) error { return fmt.Errorf("postgres: object DDL: %w", err) }
	o, err := driver.FindObject(ctx, d, db, schema, ref)
	if err != nil {
		return "", wrap(err)
	}
	oid, err := strconv.ParseInt(o.ID, 10, 64)
	if err != nil {
		return "", wrap(err)
	}
	var ddl string
	switch o.Kind {
	case driver.ObjectFunction, driver.ObjectProcedure:
		ddl, err = routineDDL(ctx, db, oid)
	case driver.ObjectAggregate:
		ddl, err = aggregateDDL(ctx, db, oid)
	case driver.ObjectSequence:
		var seqs []pgSequence
		if seqs, err = loadSequences(ctx, db, schema, oid); err == nil {
			if len(seqs) == 0 {
				return "", wrap(fmt.Errorf("%w: sequence %s", driver.ErrObjectNotFound, o.Name))
			}
			ddl = sequenceDDL(seqs[0])
		}
	case driver.ObjectTrigger:
		ddl, err = driver.ScanString(ctx, db, `SELECT pg_catalog.pg_get_triggerdef($1::int8::oid, true)`, oid)
		ddl = strings.TrimSpace(ddl) + ";\n"
	default: // enum, composite, domain
		var types []pgType
		if types, err = loadTypes(ctx, db, schema, oid); err == nil {
			if len(types) == 0 {
				return "", wrap(fmt.Errorf("%w: type %s", driver.ErrObjectNotFound, o.Name))
			}
			ddl, err = typeDDL(types[0])
		}
	}
	if err != nil {
		return "", wrap(err)
	}
	return ddl, nil
}

// routineDDL is pg_get_functiondef (always schema-qualified) plus the
// comment.
func routineDDL(ctx context.Context, db *sql.DB, oid int64) (string, error) {
	var def, qname, args, kind, comment string
	err := db.QueryRowContext(ctx, `
SELECT pg_catalog.pg_get_functiondef(p.oid),
       quote_ident(n.nspname) || '.' || quote_ident(p.proname),
       pg_catalog.pg_get_function_identity_arguments(p.oid), p.prokind::text,
       COALESCE(pg_catalog.obj_description(p.oid, 'pg_proc'), '')
FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
WHERE p.oid = $1::int8::oid`, oid).Scan(&def, &qname, &args, &kind, &comment)
	if err != nil {
		return "", err
	}
	out := strings.TrimRight(def, "; \n") + ";\n"
	if comment != "" {
		keyword := "FUNCTION"
		if kind == "p" {
			keyword = "PROCEDURE"
		}
		out += "\nCOMMENT ON " + keyword + " " + qname + "(" + args + ") IS " + literal(comment) + ";\n"
	}
	return out, nil
}

// pgAggregate is a pg_aggregate row (pg_get_functiondef rejects
// aggregates).
type pgAggregate struct {
	QName, Args, SFunc, SType, FinalFunc, CombineFunc, SortOp, Kind, Parallel, Comment string
	InitCond                                                                           sql.NullString
	FinalExtra                                                                         bool
}

func aggregateDDL(ctx context.Context, db *sql.DB, oid int64) (string, error) {
	var a pgAggregate
	err := db.QueryRowContext(ctx, `
SELECT quote_ident(n.nspname) || '.' || quote_ident(p.proname),
       pg_catalog.pg_get_function_identity_arguments(p.oid),
       a.aggtransfn::regproc::text, pg_catalog.format_type(a.aggtranstype, NULL),
       CASE WHEN a.aggfinalfn::oid <> 0 THEN a.aggfinalfn::regproc::text ELSE '' END,
       CASE WHEN a.aggcombinefn::oid <> 0 THEN a.aggcombinefn::regproc::text ELSE '' END,
       COALESCE((SELECT o.oprname FROM pg_catalog.pg_operator o WHERE o.oid = a.aggsortop), ''),
       a.aggkind::text, p.proparallel::text,
       COALESCE(pg_catalog.obj_description(p.oid, 'pg_proc'), ''),
       a.agginitval, a.aggfinalextra
FROM pg_catalog.pg_aggregate a
JOIN pg_catalog.pg_proc p ON p.oid = a.aggfnoid
JOIN pg_catalog.pg_namespace n ON n.oid = p.pronamespace
WHERE a.aggfnoid::oid = $1::int8::oid`, oid).Scan(&a.QName, &a.Args, &a.SFunc, &a.SType, &a.FinalFunc, &a.CombineFunc,
		&a.SortOp, &a.Kind, &a.Parallel, &a.Comment, &a.InitCond, &a.FinalExtra)
	if err != nil {
		return "", err
	}
	return buildAggregateDDL(a), nil
}

// buildAggregateDDL renders CREATE AGGREGATE. Moving-aggregate and
// serialization functions are not included.
func buildAggregateDDL(a pgAggregate) string {
	opts := []string{"SFUNC = " + a.SFunc, "STYPE = " + a.SType}
	if a.FinalFunc != "" {
		opts = append(opts, "FINALFUNC = "+a.FinalFunc)
		if a.FinalExtra {
			opts = append(opts, "FINALFUNC_EXTRA")
		}
	}
	if a.CombineFunc != "" {
		opts = append(opts, "COMBINEFUNC = "+a.CombineFunc)
	}
	if a.InitCond.Valid {
		opts = append(opts, "INITCOND = "+literal(a.InitCond.String))
	}
	if a.SortOp != "" {
		opts = append(opts, "SORTOP = "+a.SortOp)
	}
	if a.Kind == "h" {
		opts = append(opts, "HYPOTHETICAL")
	}
	switch a.Parallel {
	case "s":
		opts = append(opts, "PARALLEL = SAFE")
	case "r":
		opts = append(opts, "PARALLEL = RESTRICTED")
	}
	args := a.Args
	if args == "" {
		args = "*"
	}
	out := "CREATE AGGREGATE " + a.QName + "(" + args + ") (\n    " + strings.Join(opts, ",\n    ") + "\n);\n"
	if a.Comment != "" {
		out += "\nCOMMENT ON AGGREGATE " + a.QName + "(" + args + ") IS " + literal(a.Comment) + ";\n"
	}
	return out
}
