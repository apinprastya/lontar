package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"lontar/internal/driver"
	"lontar/internal/query"
	"lontar/internal/values"
)

func TestCellText(t *testing.T) {
	cases := []struct {
		cell values.Cell
		want string
		null bool
	}{
		{values.NullCell, "NULL", true},
		{values.Cell{K: values.Str, V: "hi"}, "hi", false},
		{values.Cell{K: values.Str, V: "long", Trunc: true}, "long…", false},
		{values.Cell{K: values.Num, V: int64(42)}, "42", false},
		{values.Cell{K: values.Num, V: 1.5}, "1.5", false},
		{values.Cell{K: values.Num, V: float32(2.5)}, "2.5", false},
		{values.Cell{K: values.Num, V: "9007199254740993"}, "9007199254740993", false},
		{values.Cell{K: values.Bool, V: true}, "true", false},
		{values.Cell{K: values.Bool, V: false}, "false", false},
		{values.Cell{K: values.Bytes, V: "beef", Len: 2}, `\xbeef`, false},
		{values.Cell{K: values.Bytes, V: "be", Len: 9, Trunc: true}, `\xbe… (9 bytes)`, false},
		{values.Cell{K: values.Time, V: "2024-05-01"}, "2024-05-01", false},
		{values.Cell{K: values.Str}, "", false},
	}
	for _, tc := range cases {
		got, null := cellText(tc.cell)
		if got != tc.want || null != tc.null {
			t.Errorf("%+v: got %q %v, want %q %v", tc.cell, got, null, tc.want, tc.null)
		}
	}
}

func TestCSVField(t *testing.T) {
	for in, want := range map[string]string{
		"plain": "plain", "": `""`, "null": `"null"`, "a,b": `"a,b"`, `say "hi"`: `"say ""hi"""`,
		"two\nlines": "\"two\nlines\"", " pad": `" pad"`,
	} {
		if got := csvField(in, false); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	if csvField("NULL", true) != "NULL" {
		t.Error("SQL NULL must stay bare")
	}
}

func TestClip(t *testing.T) {
	if clip("short", 10) != "short" {
		t.Fatal("short text changed")
	}
	got := clip("ééééé", 3) // é is 2 bytes: cut back to a rune start
	if got != "é… (10 bytes)" {
		t.Fatalf("got %q", got)
	}
}

func TestSummaryAndPlural(t *testing.T) {
	if summary(resultSet{}, 10) != "no result set" {
		t.Fatal("no columns")
	}
	if got := summary(resultSet{columns: []string{"a"}, rows: make([][]values.Cell, 1)}, 10); got != "1 row" {
		t.Fatalf("got %q", got)
	}
	out := renderResults([]resultSet{{}, {columns: []string{"a"}}}, 2, 10)
	if !strings.Contains(out, "-- Statement 1 of 2: no result set\n-- Statement 2 of 2: 0 rows\na\n") {
		t.Fatalf("got %q", out)
	}
}

func TestRenderResultsHeaderOverBudget(t *testing.T) {
	cols := make([]string, MaxOutputBytes/3)
	for i := range cols {
		cols[i] = "col"
	}
	out := renderResults([]resultSet{{columns: cols}, {columns: []string{"b"}}}, 2, 10)
	if !strings.Contains(out, "(output limit reached)") || strings.Contains(out, "Statement 2") {
		t.Fatalf("got %q", out[:200])
	}
}

func TestRenderTable(t *testing.T) {
	gen := "a + 1"
	def := "0"
	rows := int64(12)
	out := renderTable(&driver.TableDetail{
		Schema: "public", Name: "t", Type: driver.TypeTable, Comment: "things", EstimatedRows: &rows,
		Columns: []driver.ColumnDetail{
			{Name: "id", DataType: "bigint", IsPrimaryKey: true, PKPosition: 1, Identity: "ALWAYS"},
			{Name: "b", DataType: "int", Nullable: true, Generated: "STORED", GenerationExpr: &gen},
			{Name: "kind", DataType: "enum", Default: &def, EnumValues: []string{"x", "y"}, Comment: "the kind"},
		},
		Indexes:     []driver.Index{{Name: "pk", Columns: []string{"id"}, Primary: true}, {Name: "p", Columns: []string{"b"}, Predicate: "b > 0"}},
		ForeignKeys: []driver.ForeignKey{{Name: "fk", Schema: "public", Table: "t", Columns: []string{"b"}, RefSchema: "public", RefTable: "u", RefColumns: []string{"id"}, OnUpdate: "CASCADE"}},
		Checks:      []driver.Check{{Name: "ck", Expression: "b > 0"}},
	})
	wantContains(t, out, "table public.t (~12 rows)", "Comment: things", "- id bigint NOT NULL PRIMARY KEY(1) GENERATED ALWAYS AS IDENTITY",
		"- b int GENERATED STORED AS (a + 1)", "DEFAULT 0 values: x, y -- the kind", "- pk PRIMARY KEY (id)", "- p INDEX (b) WHERE b > 0",
		"- fk: public.t (b) -> public.u (id) ON UPDATE CASCADE", "Checks:\n- ck: b > 0")
}

func TestRenderPlan(t *testing.T) {
	est, cost := 10.0, 2.5
	out := renderPlan(&query.Plan{Engine: "postgres", SQL: "EXPLAIN x", Nodes: []query.PlanNode{
		{Op: "Hash Join", Detail: "cond", EstRows: &est, Cost: &cost, Children: []query.PlanNode{{Op: "Seq Scan", Object: "t"}}},
	}})
	wantContains(t, out, "postgres plan (EXPLAIN x):\n-> Hash Join (rows=10 cost=2.5) cond\n  -> Seq Scan on t\n")
	raw := renderPlan(&query.Plan{Engine: "mysql", SQL: "EXPLAIN y", Raw: "raw plan"})
	wantContains(t, raw, "raw plan")
}

func TestStatementChecks(t *testing.T) {
	if _, err := splitStatements(driver.Postgres, "SELECT 1;\n\\dt\n"); err == nil || !strings.Contains(err.Error(), "meta-command (\\dt)") {
		t.Fatalf("err = %v", err)
	}
	if err := checkRead(driver.Postgres, 1, "SELECT pg_terminate_backend(42)"); err == nil {
		t.Fatal("side-effect function allowed")
	}
	if err := checkRead(driver.MySQL, 1, "USE other"); err == nil {
		t.Fatal("USE allowed")
	}
	if err := checkRead(driver.Postgres, 1, "SELECT set_config('default_transaction_read_only', 'off', false)"); err == nil {
		t.Fatal("set_config allowed")
	}
	if err := checkRead(driver.Postgres, 1, "SELECT nextval('s')"); err == nil {
		t.Fatal("nextval allowed")
	}
	if err := checkRead(driver.Postgres, 1, "/* x */"); err != nil {
		t.Fatal(err)
	}
	if err := checkWrite(driver.Postgres, 1, "INSERT INTO t (id) VALUES (nextval('s'))"); err != nil {
		t.Fatalf("nextval in an insert: %v", err)
	}
	if err := checkWrite(driver.Postgres, 1, "WITH d AS (DELETE FROM t RETURNING *) INSERT INTO a SELECT * FROM d"); err != nil {
		t.Fatal(err)
	}
	if err := checkWrite(driver.Postgres, 1, "INSERT INTO t SELECT dblink_exec('x')"); err == nil {
		t.Fatal("dblink allowed")
	}
	if err := checkWrite(driver.Postgres, 1, "-- nothing"); err == nil {
		t.Fatal("empty allowed")
	}
	if firstLine("a\nb") != "a" || firstLine("ab") != "ab" {
		t.Fatal("firstLine")
	}
}

func TestDescribeErr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if got := describeErr(ctx, errors.New("x"), time.Minute); !strings.Contains(got, "did not finish within 1m0s") {
		t.Fatalf("got %q", got)
	}
	if got := describeErr(context.Background(), errors.New("boom"), time.Minute); got != "boom" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderWriteAndTotals(t *testing.T) {
	one := int64(1)
	out := renderWrite([]*int64{nil, &one})
	wantContains(t, out, "Statement 1: done", "Statement 2: 1 row affected")
	if totalAffected([]*int64{nil}) != nil || *totalAffected([]*int64{&one, &one}) != 2 {
		t.Fatal("totals")
	}
}

func TestPickObject(t *testing.T) {
	objs := []driver.Object{
		{Kind: driver.ObjectFunction, Name: "f", Signature: "integer"},
		{Kind: driver.ObjectFunction, Name: "f", Signature: "text"},
		{Kind: driver.ObjectTrigger, Name: "tr", Table: "a"},
	}
	if _, err := pickObject(objs, driver.ObjectFunction, "f", "", ""); err == nil || !strings.Contains(err.Error(), "pass signature") {
		t.Fatalf("err = %v", err)
	}
	if o, err := pickObject(objs, driver.ObjectFunction, "f", "text", ""); err != nil || o.Signature != "text" {
		t.Fatalf("o = %+v, %v", o, err)
	}
	if _, err := pickObject(objs, driver.ObjectTrigger, "tr", "", "b"); !errors.Is(err, driver.ErrObjectNotFound) {
		t.Fatalf("err = %v", err)
	}
}
