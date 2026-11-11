package edits

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"dbedit/internal/driver"
	"dbedit/internal/values"
)

func col(name, typ string, ord, pkPos int) driver.Column {
	return driver.Column{Name: name, DataType: typ, Ordinal: ord, IsPrimaryKey: pkPos > 0, PKPosition: pkPos, Nullable: pkPos == 0}
}

// awkward is a table with a composite key (b, a) and awkward names.
func awkward(schema string) Table {
	return Table{Schema: schema, Name: `Odd "Name" Tbl`, Type: driver.TypeTable, Columns: []driver.Column{
		col("a", "integer", 1, 2),
		col("b`x", "varchar(10)", 2, 1),
		col("Mixed Case", "text", 3, 0),
		col("order", "bigint", 4, 0),
		col("data", "bytea", 5, 0),
		col("flag", "boolean", 6, 0),
		col("price", "numeric(12,4)", 7, 0),
		col("seen", "timestamp", 8, 0),
	}}
}

func dialect(k driver.Kind) driver.Dialect { return driver.DialectFor(k) }

func num(v any) values.Cell    { return values.Cell{K: values.Num, V: v} }
func str(v string) values.Cell { return values.Cell{K: values.Str, V: v} }

func text(s string) EditValue { return EditValue{Kind: ValText, Text: s} }

func build(t *testing.T, k driver.Kind, cs ChangeSet) []Statement {
	t.Helper()
	tbl := awkward(cs.Schema)
	st, err := Build(k, dialect(k), tbl, cs)
	if err != nil {
		t.Fatalf("Build(%s): %v", k, err)
	}
	return st
}

func TestBuildPerDialect(t *testing.T) {
	cs := ChangeSet{
		Table:   `Odd "Name" Tbl`,
		Deletes: []Delete{{PK: map[string]values.Cell{"a": num(float64(1)), "b`x": str("k1")}}},
		Updates: []Update{{
			PK: map[string]values.Cell{"a": num(float64(2)), "b`x": str("k'2")},
			Set: map[string]EditValue{
				"order":      text("9007199254740993"),
				"Mixed Case": {Kind: ValNull},
				"data":       {Kind: ValBytes, Hex: "00ff"},
				"flag":       text("true"),
				"a":          text("20"), // editing a key column
			},
			Old: map[string]values.Cell{"order": num("1"), "Mixed Case": str("x"), "data": {K: values.Null}, "flag": {K: values.Null}, "a": num(float64(2))},
		}},
		Inserts: []Insert{
			{Values: map[string]EditValue{"a": text("3"), "b`x": text("k3"), "price": text("12.3400"), "Mixed Case": {Kind: ValDefault}}},
			{Values: map[string]EditValue{}},
		},
	}

	t.Run("postgres", func(t *testing.T) {
		cs := cs
		cs.Schema = "public"
		st := build(t, driver.Postgres, cs)
		if len(st) != 4 || st[0].Op != OpDelete || st[1].Op != OpUpdate || st[2].Op != OpInsert || st[3].Op != OpInsert {
			t.Fatalf("order = %+v", st)
		}
		want := []string{
			`DELETE FROM "public"."Odd ""Name"" Tbl" WHERE "b` + "`" + `x" = $1 AND "a" = $2`,
			`UPDATE "public"."Odd ""Name"" Tbl" SET "a" = $1, "Mixed Case" = NULL, "order" = $2, "data" = $3, "flag" = $4 WHERE "b` + "`" + `x" = $5 AND "a" = $6`,
			`INSERT INTO "public"."Odd ""Name"" Tbl" ("a", "b` + "`" + `x", "Mixed Case", "price") VALUES ($1, $2, DEFAULT, $3) RETURNING "b` + "`" + `x", "a"`,
			`INSERT INTO "public"."Odd ""Name"" Tbl" DEFAULT VALUES RETURNING "b` + "`" + `x", "a"`,
		}
		for i, w := range want {
			if st[i].SQL != w {
				t.Errorf("stmt %d\n got %s\nwant %s", i, st[i].SQL, w)
			}
		}
		if !reflect.DeepEqual(st[0].Args, []any{"k1", "1"}) {
			t.Errorf("delete args = %#v", st[0].Args)
		}
		if !reflect.DeepEqual(st[1].Args, []any{"20", "9007199254740993", []byte{0, 0xff}, "true", "k'2", "2"}) {
			t.Errorf("update args = %#v", st[1].Args)
		}
		if !reflect.DeepEqual(st[2].Args, []any{"3", "k3", "12.3400"}) {
			t.Errorf("insert args = %#v", st[2].Args)
		}
		wantDisp := `UPDATE "public"."Odd ""Name"" Tbl" SET "a" = 20, "Mixed Case" = NULL, "order" = 9007199254740993, "data" = '\x00ff'::bytea, "flag" = TRUE WHERE "b` + "`" + `x" = 'k''2' AND "a" = 2`
		if st[1].Display != wantDisp {
			t.Errorf("display\n got %s\nwant %s", st[1].Display, wantDisp)
		}
		if len(st[2].returning) != 2 {
			t.Error("postgres inserts return the key")
		}
	})

	t.Run("mysql", func(t *testing.T) {
		cs := cs
		cs.Schema = "shop"
		st := build(t, driver.MySQL, cs)
		want := []string{
			"DELETE FROM `shop`.`Odd \"Name\" Tbl` WHERE `b``x` = ? AND `a` = ?",
			"UPDATE `shop`.`Odd \"Name\" Tbl` SET `a` = ?, `Mixed Case` = NULL, `order` = ?, `data` = ?, `flag` = ? WHERE `b``x` = ? AND `a` = ?",
			"INSERT INTO `shop`.`Odd \"Name\" Tbl` (`a`, `b``x`, `Mixed Case`, `price`) VALUES (?, ?, DEFAULT, ?)",
			"INSERT INTO `shop`.`Odd \"Name\" Tbl` () VALUES ()",
		}
		for i, w := range want {
			if st[i].SQL != w {
				t.Errorf("stmt %d\n got %s\nwant %s", i, st[i].SQL, w)
			}
		}
		// Integers are bound as integers (exact), booleans as 1/0.
		if !reflect.DeepEqual(st[1].Args, []any{int64(20), int64(9007199254740993), []byte{0, 0xff}, int64(1), "k'2", int64(2)}) {
			t.Errorf("update args = %#v", st[1].Args)
		}
		if st[1].Display != "UPDATE `shop`.`Odd \"Name\" Tbl` SET `a` = 20, `Mixed Case` = NULL, `order` = 9007199254740993, `data` = X'00ff', `flag` = 1 WHERE `b``x` = 'k\\'2' AND `a` = 2" {
			t.Errorf("display = %s", st[1].Display)
		}
		if st[2].rowidKey != "" {
			t.Error("composite keys have no LastInsertId key")
		}
	})

	t.Run("sqlite", func(t *testing.T) {
		cs := cs
		cs.Schema = "main"
		cs.Updates = []Update{{
			PK:  cs.Updates[0].PK,
			Set: map[string]EditValue{"order": text("-5"), "flag": text("false"), "seen": text("2024-01-02T03:04:05")},
			Old: map[string]values.Cell{"order": num(float64(1)), "flag": num(float64(1)), "seen": {K: values.Null}},
		}}
		st := build(t, driver.SQLite, cs)
		// DEFAULT is left out of SQLite inserts.
		if st[2].SQL != `INSERT INTO "main"."Odd ""Name"" Tbl" ("a", "b`+"`"+`x", "price") VALUES (?, ?, ?)` {
			t.Errorf("insert = %s", st[2].SQL)
		}
		if st[3].SQL != `INSERT INTO "main"."Odd ""Name"" Tbl" DEFAULT VALUES` {
			t.Errorf("empty insert = %s", st[3].SQL)
		}
		if !reflect.DeepEqual(st[1].Args, []any{int64(-5), int64(0), "2024-01-02T03:04:05", "k'2", int64(2)}) {
			t.Errorf("update args = %#v", st[1].Args)
		}
	})
}

func TestBuildErrors(t *testing.T) {
	tbl := awkward("s")
	pk := map[string]values.Cell{"a": num(float64(1)), "b`x": str("k")}
	cases := []struct {
		name string
		kind driver.Kind
		tbl  Table
		cs   ChangeSet
		want error
		op   Op
		col  string
	}{
		{"no pk", driver.Postgres, Table{Schema: "s", Name: "t", Columns: []driver.Column{col("x", "int", 1, 0)}},
			ChangeSet{Schema: "s", Table: "t", Deletes: []Delete{{PK: map[string]values.Cell{"x": num(1.0)}}}}, ErrNoPrimaryKey, "", ""},
		{"wrong table", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: "other"}, ErrUnknownTable, "", ""},
		{"empty", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name}, ErrEmpty, "", ""},
		{"unknown set column", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Updates: []Update{{PK: pk,
			Set: map[string]EditValue{"nope": text("1")}, Old: map[string]values.Cell{"nope": str("")}}}}, ErrUnknownColumn, OpUpdate, "nope"},
		{"unknown insert column", driver.MySQL, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Inserts: []Insert{{Values: map[string]EditValue{"x; DROP": text("1")}}}},
			ErrUnknownColumn, OpInsert, "x; DROP"},
		{"unknown key column", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Deletes: []Delete{{PK: map[string]values.Cell{"a": num(1.0), "zz": str("")}}}},
			ErrUnknownColumn, OpDelete, "zz"},
		{"partial key", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Deletes: []Delete{{PK: map[string]values.Cell{"a": num(1.0)}}}},
			ErrInvalidValue, OpDelete, "b`x"},
		{"extra key column", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Deletes: []Delete{{PK: map[string]values.Cell{"a": num(1.0), "b`x": str("k"), "order": num(1.0)}}}},
			ErrInvalidValue, OpDelete, ""},
		{"missing key col", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Deletes: []Delete{{PK: map[string]values.Cell{"a": num(1.0), "order": str("")}}}},
			ErrInvalidValue, OpDelete, "b`x"},
		{"null key", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Deletes: []Delete{{PK: map[string]values.Cell{"a": num(1.0), "b`x": {K: values.Null}}}}},
			ErrInvalidValue, OpDelete, "b`x"},
		{"truncated key", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Deletes: []Delete{{PK: map[string]values.Cell{"a": num(1.0), "b`x": {K: values.Str, V: "x", Trunc: true}}}}},
			ErrTruncated, OpDelete, "b`x"},
		{"truncated original", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Updates: []Update{{PK: pk,
			Set: map[string]EditValue{"Mixed Case": text("new")}, Old: map[string]values.Cell{"Mixed Case": {K: values.Str, V: "long…", Trunc: true}}}}}, ErrTruncated, OpUpdate, "Mixed Case"},
		{"missing original", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Updates: []Update{{PK: pk,
			Set: map[string]EditValue{"Mixed Case": text("new")}}}}, ErrInvalidValue, OpUpdate, "Mixed Case"},
		{"empty set", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Updates: []Update{{PK: pk}}}, ErrInvalidValue, OpUpdate, ""},
		{"bad hex", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Inserts: []Insert{{Values: map[string]EditValue{"data": {Kind: ValBytes, Hex: "zz"}}}}},
			ErrInvalidValue, OpInsert, "data"},
		{"bad kind", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Inserts: []Insert{{Values: map[string]EditValue{"data": {Kind: "sql", Text: "now()"}}}}},
			ErrInvalidValue, OpInsert, "data"},
		{"sqlite default update", driver.SQLite, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Updates: []Update{{PK: pk,
			Set: map[string]EditValue{"order": {Kind: ValDefault}}, Old: map[string]values.Cell{"order": num(1.0)}}}}, ErrInvalidValue, OpUpdate, "order"},
		{"row changed twice", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Deletes: []Delete{{PK: pk}},
			Updates: []Update{{PK: pk, Set: map[string]EditValue{"order": text("1")}, Old: map[string]values.Cell{"order": num(1.0)}}}}, ErrInvalidValue, OpUpdate, ""},
		{"deleted twice", driver.Postgres, tbl, ChangeSet{Schema: "s", Table: tbl.Name, Deletes: []Delete{{PK: pk}, {PK: pk}}}, ErrInvalidValue, OpDelete, ""},
		{"bad bytes key", driver.Postgres, Table{Schema: "s", Name: "b", Columns: []driver.Column{col("k", "bytea", 1, 1)}},
			ChangeSet{Schema: "s", Table: "b", Deletes: []Delete{{PK: map[string]values.Cell{"k": {K: values.Bytes, V: "xyz"}}}}}, ErrInvalidValue, OpDelete, "k"},
		{"bad num key", driver.Postgres, Table{Schema: "s", Name: "b", Columns: []driver.Column{col("k", "int", 1, 1)}},
			ChangeSet{Schema: "s", Table: "b", Deletes: []Delete{{PK: map[string]values.Cell{"k": {K: values.Num, V: true}}}}}, ErrInvalidValue, OpDelete, "k"},
		{"bad str key", driver.Postgres, Table{Schema: "s", Name: "b", Columns: []driver.Column{col("k", "text", 1, 1)}},
			ChangeSet{Schema: "s", Table: "b", Deletes: []Delete{{PK: map[string]values.Cell{"k": {K: values.Str, V: 5}}}}}, ErrInvalidValue, OpDelete, "k"},
		{"bad cell kind", driver.Postgres, Table{Schema: "s", Name: "b", Columns: []driver.Column{col("k", "text", 1, 1)}},
			ChangeSet{Schema: "s", Table: "b", Deletes: []Delete{{PK: map[string]values.Cell{"k": {K: "weird", V: "x"}}}}}, ErrInvalidValue, OpDelete, "k"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Build(c.kind, dialect(c.kind), c.tbl, c.cs)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			var ce *ChangeError
			if !errors.As(err, &ce) {
				t.Fatalf("err %T is not a *ChangeError", err)
			}
			if ce.Op != c.op || ce.Column != c.col || ce.Kind != FailInvalid {
				t.Fatalf("ChangeError = %+v", ce)
			}
			if c.op != "" && ce.Index != 0 && c.name != "deleted twice" {
				t.Fatalf("index = %d", ce.Index)
			}
			if ce.Message == "" || ce.Unwrap() == nil {
				t.Fatal("message/unwrap")
			}
		})
	}
}

func TestKeyParams(t *testing.T) {
	c := func(typ string) driver.Column { return col("k", typ, 1, 1) }
	cases := []struct {
		kind  driver.Kind
		col   driver.Column
		cell  values.Cell
		bound any
		disp  string
		cast  string
	}{
		{driver.Postgres, c("bigint"), num("9007199254740993"), "9007199254740993", "9007199254740993", ""},
		{driver.Postgres, c("integer"), num(float64(42)), "42", "42", ""},
		{driver.Postgres, c("double precision"), num(0.1), "0.1", "0.1", ""},
		{driver.Postgres, c("double precision"), num(1e300), "1e+300", "1e+300", ""},
		{driver.Postgres, c("boolean"), values.Cell{K: values.Bool, V: true}, "true", "TRUE", ""},
		{driver.Postgres, c("timestamptz"), values.Cell{K: values.Time, V: "2024-01-02T03:04:05.123456+01:00"}, "2024-01-02T03:04:05.123456+01:00", "'2024-01-02T03:04:05.123456+01:00'", ""},
		{driver.Postgres, c("bytea"), values.Cell{K: values.Bytes, V: "00ff", Len: 2}, []byte{0, 0xff}, `'\x00ff'::bytea`, ""},
		{driver.MySQL, c("bigint unsigned"), num("18446744073709551615"), uint64(18446744073709551615), "18446744073709551615", ""},
		{driver.MySQL, c("bigint"), num("-9007199254740993"), int64(-9007199254740993), "-9007199254740993", ""},
		{driver.MySQL, c("decimal(12,4)"), num("12.3400"), "12.3400", "CAST('12.3400' AS DECIMAL(65,30))", "DECIMAL(65,30)"},
		{driver.MySQL, c("double"), num(0.5), 0.5, "0.5", ""},
		{driver.MySQL, c("datetime(6)"), values.Cell{K: values.Time, V: "2024-01-02T03:04:05.5"}, "2024-01-02 03:04:05.5", "'2024-01-02 03:04:05.5'", ""},
		{driver.MariaDB, c("tinyint(1)"), values.Cell{K: values.Bool, V: false}, int64(0), "0", ""},
		{driver.MySQL, c("varchar(5)"), num("NaN"), "NaN", "'NaN'", ""},
		{driver.SQLite, c("INTEGER"), num("9007199254740993"), int64(9007199254740993), "9007199254740993", ""},
		{driver.SQLite, c("REAL"), num(1.5), 1.5, "1.5", ""},
		{driver.SQLite, c("TEXT"), num("abc"), "abc", "'abc'", ""},
		{driver.SQLite, c("TIMESTAMP"), values.Cell{K: values.Time, V: "2024-01-02T03:04:05"}, "2024-01-02T03:04:05", "'2024-01-02T03:04:05'", ""},
		{driver.Postgres, c("text"), num(int64(7)), "7", "7", ""},
		{driver.Postgres, c("text"), num(int(7)), "7", "7", ""},
		{driver.Postgres, c("text"), num(int32(7)), "7", "7", ""},
		{driver.Postgres, c("text"), num(uint64(7)), "7", "7", ""},
		{driver.Postgres, c("real"), num(float32(0.5)), "0.5", "0.5", ""},
	}
	for _, tc := range cases {
		p, err := keyParam(tc.kind, tc.col, tc.cell)
		if err != nil {
			t.Errorf("%s %s %+v: %v", tc.kind, tc.col.DataType, tc.cell, err)
			continue
		}
		b := &sqlb{kind: tc.kind, d: dialect(tc.kind)}
		b.param(p)
		if !reflect.DeepEqual(p.bound, tc.bound) || b.disp.String() != tc.disp || p.cast != tc.cast {
			t.Errorf("%s %s %+v → %#v %q (cast %q), want %#v %q", tc.kind, tc.col.DataType, tc.cell, p.bound, b.disp.String(), p.cast, tc.bound, tc.disp)
		}
	}
	// The cast wraps the executed placeholder too.
	b := &sqlb{kind: driver.MySQL, d: dialect(driver.MySQL)}
	b.param(param{bound: "1", display: "'1'", cast: "DECIMAL(65,30)"})
	if b.exec.String() != "CAST(? AS DECIMAL(65,30))" {
		t.Fatalf("exec = %s", b.exec.String())
	}
}

func TestTextParams(t *testing.T) {
	c := func(typ string) driver.Column { return col("v", typ, 1, 0) }
	cases := []struct {
		kind  driver.Kind
		typ   string
		in    string
		bound any
		disp  string
	}{
		{driver.Postgres, "integer", "42", "42", "42"},
		{driver.Postgres, "integer", "4x", "4x", "'4x'"},
		{driver.Postgres, "double precision", "NaN", "NaN", "'NaN'"},
		{driver.Postgres, "boolean", "False", "False", "FALSE"},
		{driver.Postgres, "boolean", "t", "t", "'t'"},
		{driver.Postgres, "jsonb", `{"a": "it's"}`, `{"a": "it's"}`, `'{"a": "it''s"}'`},
		{driver.Postgres, "text", "line\nbreak", "line\nbreak", `E'line\nbreak'`},
		{driver.MySQL, "int", "TRUE", int64(1), "1"},
		{driver.MySQL, "int", "12abc", "12abc", "'12abc'"},
		{driver.MySQL, "datetime", "2024-01-02T03:04:05", "2024-01-02 03:04:05", "'2024-01-02 03:04:05'"},
		{driver.MySQL, "date", "2024-01-02", "2024-01-02", "'2024-01-02'"},
		{driver.MySQL, "decimal(10,2)", "1.50", "1.50", "'1.50'"},
		{driver.SQLite, "BOOLEAN", "true", int64(1), "1"},
		{driver.SQLite, "INTEGER", "18446744073709551615", uint64(18446744073709551615), "18446744073709551615"},
		{driver.SQLite, "TIMESTAMP", "2024-01-02T03:04:05", "2024-01-02T03:04:05", "'2024-01-02T03:04:05'"},
	}
	for _, tc := range cases {
		p := textParam(tc.kind, c(tc.typ), tc.in)
		if !reflect.DeepEqual(p.bound, tc.bound) || p.display != tc.disp {
			t.Errorf("%s %s %q → %#v %q, want %#v %q", tc.kind, tc.typ, tc.in, p.bound, p.display, tc.bound, tc.disp)
		}
	}
}

func TestQuoteEscaping(t *testing.T) {
	cases := []struct {
		kind driver.Kind
		in   string
		want string
	}{
		{driver.Postgres, "it's", "'it''s'"},
		{driver.Postgres, `back\slash`, `'back\slash'`},
		{driver.Postgres, "'); DROP TABLE x; --", "'''); DROP TABLE x; --'"},
		{driver.Postgres, "nul\x00x\ttab\r\x7f'\\", `E'nul\x00x\ttab\r\x7f\'\\'`},
		{driver.MySQL, "it's", `'it\'s'`},
		{driver.MySQL, `back\slash`, `'back\\slash'`},
		{driver.MySQL, "nul\x00 nl\n cr\r ctrlz\x1a", `'nul\0 nl\n cr\r ctrlz\Z'`},
		{driver.MariaDB, `"dq"`, `'"dq"'`},
		{driver.SQLite, "it's", "'it''s'"},
		{driver.SQLite, `back\slash`, `'back\slash'`},
		{driver.SQLite, "a\nb", "'a\nb'"},
		{driver.SQLite, "nul\x00", "CAST(X'6e756c00' AS TEXT)"},
		{driver.Postgres, "", "''"},
	}
	for _, c := range cases {
		if got := quote(c.kind, c.in); got != c.want {
			t.Errorf("quote(%s, %q) = %s, want %s", c.kind, c.in, got, c.want)
		}
	}
	if got := bytesLiteral(driver.Postgres, nil); got != `'\x'::bytea` {
		t.Errorf("empty bytea = %s", got)
	}
	if got := bytesLiteral(driver.SQLite, []byte{0xab}); got != "X'ab'" {
		t.Errorf("sqlite blob = %s", got)
	}
}

func TestHelpers(t *testing.T) {
	if b, err := decodeHex(" 0xDE AD "); err != nil || !reflect.DeepEqual(b, []byte{0xde, 0xad}) {
		t.Fatalf("decodeHex = %x, %v", b, err)
	}
	if _, err := decodeHex("abc"); !errors.Is(err, ErrInvalidValue) {
		t.Fatal("odd hex must fail")
	}
	for in, want := range map[string]bool{"1": true, "-2.5": true, "1e5": true, "": false, "NaN": false, "Infinity": false, "0x10": false, "1_000": false, "abc": false} {
		if looksNumeric(in) != want {
			t.Errorf("looksNumeric(%q) != %v", in, want)
		}
	}
	if mysqlTime("2024-01-02") != "2024-01-02" || mysqlTime("hello world!") != "hello world!" {
		t.Fatal("mysqlTime")
	}
	if (ChangeSet{Updates: make([]Update, 2), Deletes: make([]Delete, 1)}).Len() != 3 {
		t.Fatal("Len")
	}
	tbl := awkward("s")
	if pk := tbl.PrimaryKey(); len(pk) != 2 || pk[0].Name != "b`x" || pk[1].Name != "a" {
		t.Fatalf("PrimaryKey = %+v", pk)
	}
	if !strings.Contains(changeError(OpInsert, 2, "c", ErrTruncated).Error(), `insert #3, column "c"`) {
		t.Fatal("message")
	}
}

func TestRowidKey(t *testing.T) {
	tbl := Table{Schema: "main", Name: "t", Columns: []driver.Column{col("id", "INTEGER", 1, 1), col("v", "TEXT", 2, 0)}}
	cs := ChangeSet{Schema: "main", Table: "t", Inserts: []Insert{{Values: map[string]EditValue{"v": text("x")}}}}
	st, err := Build(driver.SQLite, dialect(driver.SQLite), tbl, cs)
	if err != nil || st[0].rowidKey != "id" {
		t.Fatalf("sqlite rowid key = %+v, %v", st, err)
	}
	tbl.Columns[0].DataType = "INT" // not a rowid alias
	st, _ = Build(driver.SQLite, dialect(driver.SQLite), tbl, cs)
	if st[0].rowidKey != "" {
		t.Fatal("INT PRIMARY KEY is not a rowid alias")
	}
	st, _ = Build(driver.MySQL, dialect(driver.MySQL), tbl, cs)
	if st[0].rowidKey != "id" {
		t.Fatal("mysql auto-increment key")
	}
}

func TestApplyReadOnly(t *testing.T) {
	_, err := Apply(t.Context(), nil, nil, ChangeSet{}, Options{ReadOnly: true})
	var ce *ChangeError
	if !errors.Is(err, ErrReadOnly) || !errors.As(err, &ce) || ce.Kind != FailReadOnly {
		t.Fatalf("err = %v", err)
	}
}
