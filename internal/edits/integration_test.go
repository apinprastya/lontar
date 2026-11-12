package edits

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"lontar/internal/driver"
	"lontar/internal/driver/mysql"
	"lontar/internal/driver/postgres"
	"lontar/internal/driver/sqlite"
	"lontar/internal/query"
	"lontar/internal/testutil"
	"lontar/internal/values"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

type engine struct {
	name   string
	drv    driver.Driver
	start  func(testing.TB) testutil.DB
	schema func(context.Context, *sql.DB) string
}

func currentSchema(d driver.Driver) func(context.Context, *sql.DB) string {
	return func(ctx context.Context, db *sql.DB) string {
		s, _ := d.CurrentSchema(ctx, db)
		return s
	}
}

var engines = func() []engine {
	pg, my, ma, lite := postgres.New(), mysql.New(), mysql.NewMariaDB(), sqlite.New()
	return []engine{
		{"sqlite", lite, testutil.SQLiteTemp, currentSchema(lite)},
		{"postgres", pg, testutil.SharedPostgres, currentSchema(pg)},
		{"mysql", my, testutil.SharedMySQL, currentSchema(my)},
		{"mariadb", ma, testutil.SharedMariaDB, currentSchema(ma)},
	}
}()

type env struct {
	e      engine
	target testutil.DB
	db     *sql.DB
	schema string
}

func (v env) kind() driver.Kind { return v.e.drv.Kind() }

func (v env) exec(t *testing.T, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := v.db.ExecContext(context.Background(), s); err != nil {
			t.Fatalf("%s: %v\n%s", v.e.name, err, s)
		}
	}
}

// q quotes an identifier for hand-written fixture SQL.
func (v env) q(name string) string { return v.e.drv.QuoteIdent(name) }

func forEach(t *testing.T, fn func(t *testing.T, v env)) {
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			target := e.start(t)
			db := testutil.OpenDriver(t, e.drv, target, false)
			fn(t, env{e: e, target: target, db: db, schema: e.schema(context.Background(), db)})
		})
	}
}

func fetch(t *testing.T, v env, table string) *query.Page {
	t.Helper()
	p, err := query.FetchTable(context.Background(), v.db, v.e.drv, query.Request{Schema: v.schema, Table: table, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func colIndex(p *query.Page) map[string]int {
	m := map[string]int{}
	for i, c := range p.Columns {
		m[c.Name] = i
	}
	return m
}

// pkOf returns the key cells of a fetched row.
func pkOf(p *query.Page, row int) map[string]values.Cell {
	pk := map[string]values.Cell{}
	for i, c := range p.Columns {
		if c.IsPK {
			pk[c.Name] = p.Rows[row][i]
		}
	}
	return pk
}

func cellStr(c values.Cell) string {
	switch c.K {
	case values.Null:
		return "NULL"
	}
	if f, ok := c.V.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(c.V)
}

func apply(t *testing.T, v env, cs ChangeSet) *Result {
	t.Helper()
	cs.Schema = v.schema
	res, err := Apply(context.Background(), v.db, v.e.drv, cs, Options{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return res
}

func applyErr(t *testing.T, v env, cs ChangeSet) *ChangeError {
	t.Helper()
	cs.Schema = v.schema
	_, err := Apply(context.Background(), v.db, v.e.drv, cs, Options{})
	var ce *ChangeError
	if !errors.As(err, &ce) {
		t.Fatalf("Apply err = %v, want *ChangeError", err)
	}
	return ce
}

// typeCase is one column of the type round-trip table.
type typeCase struct {
	col, typ   string
	v1, v2     EditValue
	want1      string // cellStr after insert ("" = same as v1.Text)
	want2      string // cellStr after update ("" = same as v2.Text)
	kind       values.Kind
	equivalent func(got, want string) bool
}

func sameInstant(got, want string) bool {
	g, err1 := time.Parse(time.RFC3339Nano, got)
	w, err2 := time.Parse(time.RFC3339Nano, want)
	return err1 == nil && err2 == nil && g.Equal(w)
}

const (
	bigInt    = "9007199254740993"
	bigInt2   = "-9223372036854775807"
	decimal1  = "12345678901234567890.0123456789"
	decimal2  = "-0.0000000001"
	injection = "'); DROP TABLE x; --"
)

func typeCases(kind driver.Kind) []typeCase {
	t := func(s string) EditValue { return EditValue{Kind: ValText, Text: s} }
	hex := func(h string) EditValue { return EditValue{Kind: ValBytes, Hex: h} }
	common := []typeCase{
		{col: "i", typ: "INTEGER", v1: t("42"), v2: t("-7"), kind: values.Num},
		{col: "b", typ: "BIGINT", v1: t(bigInt), v2: t(bigInt2), kind: values.Num},
		{col: "s", typ: "TEXT", v1: t(injection), v2: t("it's \\ \"quoted\"\nline"), kind: values.Str},
		{col: "e", typ: "TEXT", v1: t(""), v2: EditValue{Kind: ValNull}, want2: "NULL", kind: values.Str},
	}
	switch kind {
	case driver.Postgres:
		return append(common,
			typeCase{col: "n", typ: "numeric(30,10)", v1: t(decimal1), v2: t(decimal2), want2: "-0.0000000001", kind: values.Num},
			typeCase{col: "flag", typ: "boolean", v1: t("true"), v2: t("false"), kind: values.Bool},
			typeCase{col: "d", typ: "date", v1: t("2024-02-29"), v2: t("1999-12-31"), kind: values.Time},
			typeCase{col: "ts", typ: "timestamp(6)", v1: t("2024-01-02T03:04:05.123456"), v2: t("2000-01-01 00:00:00"), want2: "2000-01-01T00:00:00", kind: values.Time},
			typeCase{col: "tz", typ: "timestamptz", v1: t("2024-01-02T03:04:05.123456+02:00"), v2: t("2024-06-01T00:00:00Z"), kind: values.Time, equivalent: sameInstant},
			typeCase{col: "j", typ: "json", v1: t(`{"b": 1, "a": [1, 2]}`), v2: t(`[]`), kind: values.JSON},
			typeCase{col: "jb", typ: "jsonb", v1: t(`{"b": 1, "a": 2}`), v2: t(`"x"`), want1: `{"a": 2, "b": 1}`, kind: values.JSON},
			typeCase{col: "u", typ: "uuid", v1: t("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"), v2: t("A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A12"), want2: "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a12", kind: values.Str},
			typeCase{col: "by", typ: "bytea", v1: hex("00ff10"), v2: hex(""), want1: "00ff10", want2: "", kind: values.Bytes},
			typeCase{col: "arr", typ: "text[]", v1: t(`{a,"b c"}`), v2: t(`{}`), kind: values.Str},
			typeCase{col: "mood", typ: "mood", v1: t("happy"), v2: t("sad"), kind: values.Str},
			typeCase{col: "iv", typ: "interval", v1: t("1 day 02:00:00"), v2: t("3 mons"), kind: values.Str},
			typeCase{col: "df", typ: "integer DEFAULT 5", v1: EditValue{Kind: ValDefault}, v2: t("6"), want1: "5", kind: values.Num},
		)
	case driver.MySQL, driver.MariaDB:
		jsonKind := values.JSON
		if kind == driver.MariaDB {
			jsonKind = values.Str // JSON is an alias of LONGTEXT on MariaDB
		}
		return append(common,
			typeCase{col: "n", typ: "DECIMAL(30,10)", v1: t(decimal1), v2: t(decimal2), kind: values.Num},
			typeCase{col: "flag", typ: "BOOLEAN", v1: t("true"), v2: t("false"), want1: "1", want2: "0", kind: values.Num},
			typeCase{col: "d", typ: "DATE", v1: t("2024-02-29"), v2: t("1999-12-31"), kind: values.Time},
			typeCase{col: "ts", typ: "DATETIME(6)", v1: t("2024-01-02T03:04:05.123456"), v2: t("2000-01-01 00:00:00"), want2: "2000-01-01T00:00:00", kind: values.Time},
			typeCase{col: "j", typ: "JSON", v1: t(`{"a": [1, 2]}`), v2: t(`[]`), kind: jsonKind},
			typeCase{col: "by", typ: "BLOB", v1: hex("00ff10"), v2: hex("ab"), want1: "00ff10", want2: "ab", kind: values.Bytes},
			typeCase{col: "mood", typ: "ENUM('happy','sad')", v1: t("happy"), v2: t("sad"), kind: values.Str},
			typeCase{col: "df", typ: "INT DEFAULT 5", v1: EditValue{Kind: ValDefault}, v2: t("6"), want1: "5", kind: values.Num},
		)
	}
	return append(common,
		// SQLite: TEXT-declared decimals stay exact (NUMERIC affinity would
		// store them as REAL).
		typeCase{col: "n", typ: "TEXT", v1: t(decimal1), v2: t(decimal2), kind: values.Str},
		typeCase{col: "flag", typ: "BOOLEAN", v1: t("true"), v2: t("false"), want1: "1", want2: "0", kind: values.Num},
		typeCase{col: "d", typ: "DATE", v1: t("2024-02-29"), v2: t("1999-12-31"), kind: values.Time},
		typeCase{col: "j", typ: "JSON", v1: t(`{"a": [1, 2]}`), v2: t(`[]`), kind: values.JSON},
		typeCase{col: "by", typ: "BLOB", v1: hex("00ff10"), v2: hex("ab"), want1: "00ff10", want2: "ab", kind: values.Bytes},
		typeCase{col: "df", typ: "INTEGER DEFAULT 5", v1: EditValue{Kind: ValDefault}, v2: t("6"), want1: "5", kind: values.Num},
	)
}

func check(t *testing.T, stage string, tc typeCase, got values.Cell, want string, wantKind values.Kind) {
	t.Helper()
	s := cellStr(got)
	ok := s == want
	if tc.equivalent != nil {
		ok = tc.equivalent(s, want)
	}
	if want == "NULL" {
		wantKind = values.Null
	}
	if !ok || got.K != wantKind {
		t.Errorf("%s %s (%s): got %s %q, want %s %q", stage, tc.col, tc.typ, got.K, s, wantKind, want)
	}
}

func TestIntegrationTypesRoundTrip(t *testing.T) {
	forEach(t, func(t *testing.T, v env) {
		cases := typeCases(v.kind())
		v.exec(t, "DROP TABLE IF EXISTS edit_types")
		if v.kind() == driver.Postgres {
			v.exec(t, "DROP TYPE IF EXISTS mood", "CREATE TYPE mood AS ENUM ('happy', 'sad')")
		}
		defs := []string{"id INTEGER PRIMARY KEY"}
		for _, tc := range cases {
			defs = append(defs, v.q(tc.col)+" "+tc.typ)
		}
		v.exec(t, "CREATE TABLE edit_types ("+strings.Join(defs, ", ")+")")

		ins := map[string]EditValue{"id": {Kind: ValText, Text: "1"}}
		for _, tc := range cases {
			ins[tc.col] = tc.v1
		}
		res := apply(t, v, ChangeSet{Table: "edit_types", Inserts: []Insert{{Values: ins}}})
		if res.Inserted != 1 {
			t.Fatalf("result = %+v", res)
		}
		p := fetch(t, v, "edit_types")
		if len(p.Rows) != 1 {
			t.Fatalf("rows = %d", len(p.Rows))
		}
		idx := colIndex(p)
		set, old := map[string]EditValue{}, map[string]values.Cell{}
		for _, tc := range cases {
			want := tc.want1
			if want == "" && tc.v1.Kind == ValText {
				want = tc.v1.Text
			}
			check(t, "insert", tc, p.Rows[0][idx[tc.col]], want, tc.kind)
			set[tc.col] = tc.v2
			old[tc.col] = p.Rows[0][idx[tc.col]]
		}
		if v.kind() == driver.Postgres {
			if got := p.Columns[idx["mood"]].EnumValues; strings.Join(got, ",") != "happy,sad" {
				t.Errorf("pg enum values = %q", got)
			}
		}
		if v.kind() == driver.MySQL || v.kind() == driver.MariaDB {
			if got := p.Columns[idx["mood"]].EnumValues; strings.Join(got, ",") != "happy,sad" {
				t.Errorf("mysql enum values = %q", got)
			}
		}

		// Update every column, keyed by the fetched key cells.
		res = apply(t, v, ChangeSet{Table: "edit_types", Updates: []Update{{PK: pkOf(p, 0), Set: set, Old: old}}})
		if res.Updated != 1 {
			t.Fatalf("result = %+v", res)
		}
		p = fetch(t, v, "edit_types")
		for _, tc := range cases {
			want := tc.want2
			if want == "" && tc.v2.Kind == ValText {
				want = tc.v2.Text
			}
			check(t, "update", tc, p.Rows[0][idx[tc.col]], want, tc.kind)
		}

		// The injection value was stored literally and nothing was dropped.
		var n int
		if err := v.db.QueryRow("SELECT COUNT(*) FROM edit_types").Scan(&n); err != nil || n != 1 {
			t.Fatalf("count = %d, %v", n, err)
		}

		res = apply(t, v, ChangeSet{Table: "edit_types", Deletes: []Delete{{PK: pkOf(p, 0)}}})
		if res.Deleted != 1 || len(fetch(t, v, "edit_types").Rows) != 0 {
			t.Fatalf("delete result = %+v", res)
		}
	})
}

// keyCase is a table keyed by one column of a given type.
type keyCase struct {
	typ, value string
	bytes      bool
}

func keyCases(kind driver.Kind) []keyCase {
	switch kind {
	case driver.Postgres:
		return []keyCase{
			{typ: "bigint", value: bigInt},
			{typ: "numeric(30,10)", value: decimal1},
			{typ: "timestamptz", value: "2024-01-02T03:04:05.123456+02:00"},
			{typ: "timestamp(6)", value: "2024-01-02T03:04:05.123456"},
			{typ: "date", value: "2024-01-02"},
			{typ: "uuid", value: "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"},
			{typ: "bytea", value: "00ff10", bytes: true},
			{typ: "text", value: "it's"},
			{typ: "boolean", value: "true"},
			{typ: "double precision", value: "0.1"},
		}
	case driver.MySQL, driver.MariaDB:
		return []keyCase{
			{typ: "BIGINT", value: bigInt},
			{typ: "BIGINT UNSIGNED", value: "18446744073709551615"},
			{typ: "DECIMAL(30,10)", value: decimal1},
			{typ: "DATETIME(6)", value: "2024-01-02 03:04:05.123456"},
			{typ: "TIMESTAMP(3)", value: "2024-01-02 03:04:05.123"},
			{typ: "DATE", value: "2024-01-02"},
			{typ: "VARBINARY(16)", value: "00ff10", bytes: true},
			{typ: "VARCHAR(20)", value: "it's"},
			{typ: "DOUBLE", value: "0.1"},
		}
	}
	return []keyCase{
		{typ: "INTEGER", value: bigInt},
		{typ: "TEXT", value: "it's"},
		{typ: "BLOB", value: "00ff10", bytes: true},
		{typ: "REAL", value: "0.1"},
	}
}

// TestIntegrationKeyRoundTrip fetches a row and uses its cells as the key
// of an update: the row must be found exactly (big ints, decimals, time
// precision, bytes).
func TestIntegrationKeyRoundTrip(t *testing.T) {
	forEach(t, func(t *testing.T, v env) {
		for i, kc := range keyCases(v.kind()) {
			t.Run(kc.typ, func(t *testing.T) {
				name := fmt.Sprintf("edit_key_%d", i)
				v.exec(t, "DROP TABLE IF EXISTS "+name, "CREATE TABLE "+name+" (k "+kc.typ+" PRIMARY KEY, v VARCHAR(10))")
				val := EditValue{Kind: ValText, Text: kc.value}
				if kc.bytes {
					val = EditValue{Kind: ValBytes, Hex: kc.value}
				}
				// A near neighbour that a lossy comparison would also match.
				ins := []Insert{{Values: map[string]EditValue{"k": val, "v": {Kind: ValText, Text: "a"}}}}
				if kc.typ == "BIGINT" || kc.typ == "bigint" || kc.typ == "INTEGER" {
					ins = append(ins, Insert{Values: map[string]EditValue{"k": {Kind: ValText, Text: "9007199254740992"}, "v": {Kind: ValText, Text: "b"}}})
				}
				apply(t, v, ChangeSet{Table: name, Inserts: ins})
				p := fetch(t, v, name)
				row := -1
				for r := range p.Rows {
					if cellStr(p.Rows[r][1]) == "a" {
						row = r
					}
				}
				if row < 0 {
					t.Fatalf("rows = %+v", p.Rows)
				}
				res := apply(t, v, ChangeSet{Table: name, Updates: []Update{{
					PK:  pkOf(p, row),
					Set: map[string]EditValue{"v": {Kind: ValText, Text: "changed"}},
					Old: map[string]values.Cell{"v": p.Rows[row][1]},
				}}})
				if res.Updated != 1 {
					t.Fatalf("updated = %d", res.Updated)
				}
				var n int
				if err := v.db.QueryRow("SELECT COUNT(*) FROM " + name + " WHERE v = 'changed'").Scan(&n); err != nil || n != 1 {
					t.Fatalf("changed rows = %d, %v (key cell %+v)", n, err, p.Rows[row][0])
				}
			})
		}
	})
}

func TestIntegrationCompositeAwkwardAndKeyEdit(t *testing.T) {
	forEach(t, func(t *testing.T, v env) {
		tbl := `Odd "Edit" Tbl`
		v.exec(t, "DROP TABLE IF EXISTS "+v.q(tbl),
			"CREATE TABLE "+v.q(tbl)+" ("+v.q("order")+" INT NOT NULL, "+v.q("quote\"col`")+" VARCHAR(10) NOT NULL, "+
				v.q("Mixed Case")+" VARCHAR(20), PRIMARY KEY ("+v.q("quote\"col`")+", "+v.q("order")+"))",
			"INSERT INTO "+v.q(tbl)+" VALUES (1, 'a', 'one'), (2, 'a', 'two'), (1, 'b', 'three')")
		p := fetch(t, v, tbl)
		if len(p.Rows) != 3 {
			t.Fatalf("rows = %d", len(p.Rows))
		}
		idx := colIndex(p)
		find := func(p *query.Page, mixed string) int {
			for r := range p.Rows {
				if cellStr(p.Rows[r][idx["Mixed Case"]]) == mixed {
					return r
				}
			}
			t.Fatalf("no row %q", mixed)
			return -1
		}
		r := find(p, "two")
		// Edit a key column: the WHERE uses the old key.
		res := apply(t, v, ChangeSet{Table: tbl, Updates: []Update{{
			PK:  pkOf(p, r),
			Set: map[string]EditValue{"order": {Kind: ValText, Text: "20"}, "Mixed Case": {Kind: ValText, Text: "twenty"}},
			Old: map[string]values.Cell{"order": p.Rows[r][idx["order"]], "Mixed Case": p.Rows[r][idx["Mixed Case"]]},
		}}, Deletes: []Delete{{PK: pkOf(p, find(p, "three"))}}})
		if res.Updated != 1 || res.Deleted != 1 {
			t.Fatalf("res = %+v", res)
		}
		p = fetch(t, v, tbl)
		if len(p.Rows) != 2 {
			t.Fatalf("rows = %+v", p.Rows)
		}
		r = find(p, "twenty")
		if cellStr(p.Rows[r][idx["order"]]) != "20" || cellStr(p.Rows[r][idx["quote\"col`"]]) != "a" {
			t.Fatalf("row = %+v", p.Rows[r])
		}

		// Preview renders the same statement with literals.
		cs := ChangeSet{Schema: v.schema, Table: tbl, Updates: []Update{{PK: pkOf(p, r),
			Set: map[string]EditValue{"Mixed Case": {Kind: ValText, Text: "it's"}}, Old: map[string]values.Cell{"Mixed Case": p.Rows[r][idx["Mixed Case"]]}}}}
		prev, err := Preview(context.Background(), v.db, v.e.drv, cs)
		if err != nil || len(prev) != 1 || prev[0].Op != OpUpdate || !strings.Contains(prev[0].SQL, v.q("Mixed Case")) || !strings.HasSuffix(prev[0].SQL, ";") {
			t.Fatalf("preview = %+v, %v", prev, err)
		}
		if !strings.Contains(prev[0].SQL, "'it''s'") && !strings.Contains(prev[0].SQL, `'it\'s'`) {
			t.Fatalf("preview literal = %s", prev[0].SQL)
		}
	})
}

func TestIntegrationStaleRowRollsBack(t *testing.T) {
	forEach(t, func(t *testing.T, v env) {
		v.exec(t, "DROP TABLE IF EXISTS edit_stale", "CREATE TABLE edit_stale (id INT PRIMARY KEY, v VARCHAR(10))",
			"INSERT INTO edit_stale VALUES (1, 'a'), (2, 'b'), (3, 'c')")
		p := fetch(t, v, "edit_stale")
		upd := func(row int, val string) Update {
			return Update{PK: pkOf(p, row), Set: map[string]EditValue{"v": {Kind: ValText, Text: val}}, Old: map[string]values.Cell{"v": p.Rows[row][1]}}
		}
		// Elsewhere: row 2's key changes, row 3 is deleted.
		v.exec(t, "UPDATE edit_stale SET id = 20 WHERE id = 2", "DELETE FROM edit_stale WHERE id = 3")

		ce := applyErr(t, v, ChangeSet{Table: "edit_stale",
			Updates: []Update{upd(0, "A"), upd(1, "B")},
			Inserts: []Insert{{Values: map[string]EditValue{"id": {Kind: ValText, Text: "9"}}}}})
		if ce.Kind != FailStale || ce.Op != OpUpdate || ce.Index != 1 || ce.SQL == "" || !errors.Is(ce, ErrStale) {
			t.Fatalf("err = %+v", ce)
		}
		ce = applyErr(t, v, ChangeSet{Table: "edit_stale", Deletes: []Delete{{PK: pkOf(p, 2)}}})
		if ce.Kind != FailStale || ce.Op != OpDelete || ce.Index != 0 {
			t.Fatalf("delete err = %+v", ce)
		}
		// Nothing was applied: row 1 is unchanged and no row 9 exists.
		var got string
		if err := v.db.QueryRow("SELECT v FROM edit_stale WHERE id = 1").Scan(&got); err != nil || got != "a" {
			t.Fatalf("row 1 = %q, %v", got, err)
		}
		var n int
		if err := v.db.QueryRow("SELECT COUNT(*) FROM edit_stale").Scan(&n); err != nil || n != 2 {
			t.Fatalf("count = %d, %v", n, err)
		}
	})
}

// TestIntegrationUnchangedUpdateCounts checks that writing the current value
// still counts as one affected row (MySQL needs clientFoundRows).
func TestIntegrationUnchangedUpdateCounts(t *testing.T) {
	forEach(t, func(t *testing.T, v env) {
		v.exec(t, "DROP TABLE IF EXISTS edit_same", "CREATE TABLE edit_same (id INT PRIMARY KEY, v VARCHAR(10))", "INSERT INTO edit_same VALUES (1, 'a')")
		p := fetch(t, v, "edit_same")
		res := apply(t, v, ChangeSet{Table: "edit_same", Updates: []Update{{PK: pkOf(p, 0),
			Set: map[string]EditValue{"v": {Kind: ValText, Text: "a"}}, Old: map[string]values.Cell{"v": p.Rows[0][1]}}}})
		if res.Updated != 1 {
			t.Fatalf("updated = %d", res.Updated)
		}
	})
}

func TestIntegrationDatabaseErrorRollsBack(t *testing.T) {
	forEach(t, func(t *testing.T, v env) {
		v.exec(t, "DROP TABLE IF EXISTS edit_err", "CREATE TABLE edit_err (id INT PRIMARY KEY, v VARCHAR(10) NOT NULL)", "INSERT INTO edit_err VALUES (1, 'a'), (2, 'b')")
		p := fetch(t, v, "edit_err")
		ce := applyErr(t, v, ChangeSet{Table: "edit_err",
			Deletes: []Delete{{PK: pkOf(p, 0)}},
			Inserts: []Insert{
				{Values: map[string]EditValue{"id": {Kind: ValText, Text: "3"}, "v": {Kind: ValText, Text: "c"}}},
				{Values: map[string]EditValue{"id": {Kind: ValText, Text: "2"}, "v": {Kind: ValText, Text: "dup"}}},
			}})
		if ce.Kind != FailDatabase || ce.Op != OpInsert || ce.Index != 1 || !strings.Contains(ce.Message, "insert #2") {
			t.Fatalf("err = %+v", ce)
		}
		var n int
		if err := v.db.QueryRow("SELECT COUNT(*) FROM edit_err").Scan(&n); err != nil || n != 2 {
			t.Fatalf("count = %d, %v (the delete must be rolled back)", n, err)
		}
	})
}

func TestIntegrationInsertedKeys(t *testing.T) {
	forEach(t, func(t *testing.T, v env) {
		auto := map[driver.Kind]string{
			driver.Postgres: "id BIGSERIAL PRIMARY KEY",
			driver.MySQL:    "id BIGINT AUTO_INCREMENT PRIMARY KEY",
			driver.MariaDB:  "id BIGINT AUTO_INCREMENT PRIMARY KEY",
			driver.SQLite:   "id INTEGER PRIMARY KEY",
		}[v.kind()]
		v.exec(t, "DROP TABLE IF EXISTS edit_auto", "CREATE TABLE edit_auto ("+auto+", v VARCHAR(10))")
		res := apply(t, v, ChangeSet{Table: "edit_auto", Inserts: []Insert{
			{Values: map[string]EditValue{"v": {Kind: ValText, Text: "x"}}},
			{Values: map[string]EditValue{"v": {Kind: ValDefault}}},
		}})
		if res.Inserted != 2 || len(res.InsertedKeys) != 2 {
			t.Fatalf("res = %+v", res)
		}
		for i, k := range res.InsertedKeys {
			if cellStr(k["id"]) != strconv.Itoa(i+1) {
				t.Fatalf("key %d = %+v", i, k)
			}
		}
	})
}

func TestIntegrationRefusals(t *testing.T) {
	forEach(t, func(t *testing.T, v env) {
		v.exec(t, "DROP VIEW IF EXISTS edit_view", "DROP TABLE IF EXISTS edit_nopk", "DROP TABLE IF EXISTS edit_ref",
			"CREATE TABLE edit_nopk (a INT, b VARCHAR(10))", "INSERT INTO edit_nopk VALUES (1, 'x')",
			"CREATE TABLE edit_ref (id INT PRIMARY KEY, v TEXT)", "INSERT INTO edit_ref VALUES (1, 'x')",
			"CREATE VIEW edit_view AS SELECT id, v FROM edit_ref")
		ins := []Insert{{Values: map[string]EditValue{"a": {Kind: ValText, Text: "2"}}}}

		if ce := applyErr(t, v, ChangeSet{Table: "edit_nopk", Inserts: ins}); !errors.Is(ce, ErrNoPrimaryKey) {
			t.Fatalf("no pk: %v", ce)
		}
		if ce := applyErr(t, v, ChangeSet{Table: "edit_view", Inserts: ins}); !errors.Is(ce, ErrNotATable) {
			t.Fatalf("view: %v", ce)
		}
		if ce := applyErr(t, v, ChangeSet{Table: "edit_missing", Inserts: ins}); !errors.Is(ce, ErrUnknownTable) {
			t.Fatalf("missing: %v", ce)
		}
		if ce := applyErr(t, v, ChangeSet{Inserts: ins}); !errors.Is(ce, ErrUnknownTable) {
			t.Fatalf("no table: %v", ce)
		}
		p := fetch(t, v, "edit_ref")
		trunc := p.Rows[0][1]
		trunc.Trunc = true
		ce := applyErr(t, v, ChangeSet{Table: "edit_ref", Updates: []Update{{PK: pkOf(p, 0),
			Set: map[string]EditValue{"v": {Kind: ValText, Text: "y"}}, Old: map[string]values.Cell{"v": trunc}}}})
		if !errors.Is(ce, ErrTruncated) || ce.Column != "v" {
			t.Fatalf("truncated: %+v", ce)
		}
		// Read-only safe mode refuses before touching the database.
		_, err := Apply(context.Background(), v.db, v.e.drv, ChangeSet{Schema: v.schema, Table: "edit_ref", Inserts: ins}, Options{ReadOnly: true})
		if !errors.Is(err, ErrReadOnly) {
			t.Fatalf("readonly: %v", err)
		}
		if _, err := Preview(context.Background(), v.db, v.e.drv, ChangeSet{Schema: v.schema, Table: "edit_view", Inserts: ins}); !errors.Is(err, ErrNotATable) {
			t.Fatalf("preview view: %v", err)
		}
		if _, err := Preview(context.Background(), v.db, v.e.drv, ChangeSet{Schema: v.schema, Table: "edit_ref"}); !errors.Is(err, ErrEmpty) {
			t.Fatalf("preview empty: %v", err)
		}
	})
}

// TestIntegrationReadOnlySession checks the database-level enforcement of
// read-only safe mode on every engine: the server rejects writes.
func TestIntegrationReadOnlySession(t *testing.T) {
	forEach(t, func(t *testing.T, v env) {
		v.exec(t, "DROP TABLE IF EXISTS edit_ro", "CREATE TABLE edit_ro (id INT PRIMARY KEY)", "INSERT INTO edit_ro VALUES (1)")
		ro := testutil.OpenDriver(t, v.e.drv, v.target, true)
		var n int
		if err := ro.QueryRow("SELECT COUNT(*) FROM edit_ro").Scan(&n); err != nil || n != 1 {
			t.Fatalf("read on read-only session: %d, %v", n, err)
		}
		_, err := ro.Exec("INSERT INTO edit_ro VALUES (2)")
		if err == nil {
			t.Fatal("write on a read-only session succeeded")
		}
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "read-only") && !strings.Contains(msg, "read only") && !strings.Contains(msg, "readonly") {
			t.Fatalf("unexpected error: %v", err)
		}
		// edits.Apply without the app-level check is still stopped by the
		// database.
		_, err = Apply(context.Background(), ro, v.e.drv, ChangeSet{Schema: v.schema, Table: "edit_ro",
			Inserts: []Insert{{Values: map[string]EditValue{"id": {Kind: ValText, Text: "3"}}}}}, Options{})
		var ce *ChangeError
		if !errors.As(err, &ce) || ce.Kind != FailDatabase {
			t.Fatalf("apply on read-only session: %v", err)
		}
		if err := v.db.QueryRow("SELECT COUNT(*) FROM edit_ro").Scan(&n); err != nil || n != 1 {
			t.Fatalf("count = %d, %v", n, err)
		}
	})
}
