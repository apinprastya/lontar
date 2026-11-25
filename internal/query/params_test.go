package query

import (
	"context"
	"reflect"
	"strings"
	"testing"

	dbdriver "lontar/internal/driver"
	"lontar/internal/sqlsplit"

	"github.com/jackc/pgx/v5"
)

func TestBindParams(t *testing.T) {
	vals := map[string]ParamValue{
		"a": {Value: "x"},
		"b": {Value: " 42 ", Type: ParamNumber},
		"c": {Null: true, Value: "ignored"},
		"f": {Value: "1.5", Type: ParamNumber},
	}
	const text = "SELECT :a, :b, :a, :c, :f, ':z', x::int"
	tests := []struct {
		kind dbdriver.Kind
		sql  string
		args []any
	}{
		{dbdriver.Postgres, "SELECT $1, $2, $1, $3, $4, ':z', x::int", []any{"x", int64(42), nil, 1.5}},
		{dbdriver.MySQL, "SELECT ?, ?, ?, ?, ?, ':z', x::int", []any{"x", int64(42), "x", nil, 1.5}},
		{dbdriver.SQLite, "SELECT ?, ?, ?, ?, ?, ':z', x::int", []any{"x", int64(42), "x", nil, 1.5}},
	}
	for _, tc := range tests {
		b, err := bindParams(tc.kind, text, vals)
		if err != nil {
			t.Fatal(err)
		}
		if b.SQL != tc.sql || !reflect.DeepEqual(b.Args, tc.args) {
			t.Fatalf("%s: %q %#v", tc.kind, b.SQL, b.Args)
		}
	}

	b, err := bindParams(dbdriver.SQLite, "SELECT 1", nil)
	if err != nil || b.SQL != "SELECT 1" || b.Args != nil || b.Pos(3) != 3 {
		t.Fatalf("no params: %+v %v", b, err)
	}
	if _, err := bindParams(dbdriver.SQLite, "SELECT :q", vals); err == nil || !strings.Contains(err.Error(), ":q") {
		t.Fatalf("missing: %v", err)
	}
	bad := map[string]ParamValue{"a": {Value: "abc", Type: ParamNumber}}
	for _, k := range []dbdriver.Kind{dbdriver.Postgres, dbdriver.SQLite} {
		if _, err := bindParams(k, "SELECT :a", bad); err == nil || !strings.Contains(err.Error(), "not a number") {
			t.Fatalf("%s: bad number: %v", k, err)
		}
	}
	if _, err := bindParams(dbdriver.SQLite, "SELECT :a", map[string]ParamValue{"a": {Type: "date"}}); err == nil {
		t.Fatal("unknown type accepted")
	}
}

func TestBoundPos(t *testing.T) {
	// "SELECT :name, x" → "SELECT $1, x"
	b, err := bindParams(dbdriver.Postgres, "SELECT :name, x, :name", map[string]ParamValue{"name": {}})
	if err != nil || b.SQL != "SELECT $1, x, $1" {
		t.Fatalf("%q %v", b.SQL, err)
	}
	for _, c := range [][2]int{{0, 0}, {6, 6}, {7, 7}, {8, 7}, {9, 12}, {11, 14}, {14, 17}, {15, 17}, {16, 22}} {
		if got := b.Pos(c[0]); got != c[1] {
			t.Errorf("Pos(%d) = %d, want %d", c[0], got, c[1])
		}
	}
}

func TestDriverArgs(t *testing.T) {
	plain := boundStatement{SQL: "SELECT 1"}
	if got := plain.driverArgs(dbdriver.Postgres); len(got) != 1 || got[0] != pgExecMode {
		t.Fatalf("pg plain = %v", got)
	}
	if got := plain.driverArgs(dbdriver.SQLite); got != nil {
		t.Fatalf("sqlite plain = %v", got)
	}
	withArgs := boundStatement{SQL: "SELECT $1", Args: []any{"x"}}
	if got := withArgs.driverArgs(dbdriver.Postgres); len(got) != 2 || got[0] != pgx.QueryExecModeDescribeExec || got[1] != "x" {
		t.Fatalf("pg args = %v", got)
	}
	if got := withArgs.driverArgs(dbdriver.MySQL); !reflect.DeepEqual(got, []any{"x"}) {
		t.Fatalf("mysql args = %v", got)
	}
}

func TestCheckParams(t *testing.T) {
	stmts := sqlsplit.Split(dbdriver.Postgres, "SELECT :a, :b; \\echo :c\nSELECT :b, :d")
	missing, err := checkParams(dbdriver.Postgres, stmts, map[string]ParamValue{"b": {}})
	if err != nil || !reflect.DeepEqual(missing, []string{"a", "d"}) {
		t.Fatalf("missing = %q %v", missing, err)
	}
	if _, err := checkParams(dbdriver.Postgres, stmts, map[string]ParamValue{"a": {Value: "x", Type: ParamNumber}}); err == nil {
		t.Fatal("bad number accepted")
	}
	if got := MissingParams(dbdriver.Postgres, "SELECT :a, :b; \\echo :c\nSELECT :b, :d", map[string]ParamValue{"b": {}}); !reflect.DeepEqual(got, []string{"a", "d"}) {
		t.Fatalf("MissingParams = %q", got)
	}
	if got := ParamNames(dbdriver.SQLite, "SELECT :x, :y, :x"); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Fatalf("ParamNames = %q", got)
	}
}

func TestExecuteParamsSQLite(t *testing.T) {
	e, _ := liteEditors(t)
	id := openEditor(t, e)
	run(t, e, id, "CREATE TABLE t (a INTEGER PRIMARY KEY, b TEXT); INSERT INTO t VALUES (1, 'x'), (2, NULL), (3, 'z')", ExecOptions{})

	// Missing values: nothing runs, the names are reported.
	script := "INSERT INTO t VALUES (4, 'w'); SELECT a FROM t WHERE b = :b OR a = :a ORDER BY a LIMIT :n"
	res := run(t, e, id, script, ExecOptions{Params: map[string]ParamValue{"b": {Value: "x"}}})
	if !reflect.DeepEqual(res.NeedsParams, []string{"a", "n"}) || len(res.Statements) != 0 {
		t.Fatalf("needs params = %+v", res)
	}
	if r := run(t, e, id, "SELECT count(*) FROM t", ExecOptions{}); r.Statements[0].ResultSets[0].Rows[0][0].V != int64(3) {
		t.Fatalf("a statement ran: %v", r.Statements[0].ResultSets[0].Rows[0][0])
	}
	// A bad number fails before anything runs.
	if _, err := e.Execute(context.Background(), id, "", script, ExecOptions{Params: map[string]ParamValue{
		"a": {Value: "1"}, "b": {Value: "x"}, "n": {Value: "ten", Type: ParamNumber},
	}}); err == nil || !strings.Contains(err.Error(), "not a number") {
		t.Fatalf("bad number: %v", err)
	}

	res = run(t, e, id, script, ExecOptions{Params: map[string]ParamValue{
		"a": {Value: "3"}, "b": {Value: "x"}, "n": {Value: "5", Type: ParamNumber},
	}})
	if statuses(res) != "ok,ok" || res.NeedsParams != nil {
		t.Fatalf("run = %+v", res)
	}
	st := res.Statements[1]
	if !strings.Contains(st.SQL, ":b") {
		t.Fatalf("statement SQL should be as written: %q", st.SQL)
	}
	rows := st.ResultSets[0].Rows
	if len(rows) != 2 || rows[0][0].V != int64(1) || rows[1][0].V != int64(3) {
		t.Fatalf("rows = %v", rows)
	}

	// NULL binds NULL.
	res = run(t, e, id, "SELECT count(*) FROM t WHERE b IS :v", ExecOptions{Params: map[string]ParamValue{"v": {Null: true}}})
	if v := res.Statements[0].ResultSets[0].Rows[0][0].V; v != int64(1) {
		t.Fatalf("null count = %v", v)
	}

	// Error positions point into the text as written.
	text := "SELECT :a_long_parameter, nosuch FROM t"
	res = run(t, e, id, text, ExecOptions{BaseOffset: 3, Params: map[string]ParamValue{"a_long_parameter": {Value: "1"}}})
	if p := res.Statements[0].Error.Position; p == nil || *p != 3+strings.Index(text, "nosuch") {
		t.Fatalf("error position = %v (%+v)", p, res.Statements[0].Error)
	}
}

func TestExplainParamsSQLite(t *testing.T) {
	e, _ := liteEditors(t)
	id := openEditor(t, e)
	run(t, e, id, "CREATE TABLE t (a INTEGER PRIMARY KEY, b TEXT)", ExecOptions{})
	ctx := context.Background()

	const q = "SELECT * FROM t WHERE a = :id"
	p, err := e.Explain(ctx, id, "", q, ExplainOptions{})
	if err != nil || !reflect.DeepEqual(p.NeedsParams, []string{"id"}) || len(p.Nodes) != 0 {
		t.Fatalf("needs params = %+v %v", p, err)
	}
	if _, err := e.Explain(ctx, id, "", q, ExplainOptions{Params: map[string]ParamValue{"id": {Value: "x", Type: ParamNumber}}}); err == nil {
		t.Fatal("bad number accepted")
	}
	p, err = e.Explain(ctx, id, "", q, ExplainOptions{Params: map[string]ParamValue{"id": {Value: "1"}}})
	if err != nil || p.Error != nil || len(p.Nodes) == 0 || !strings.HasSuffix(p.SQL, q) {
		t.Fatalf("plan = %+v %v", p, err)
	}
	text := "SELECT :a_long_parameter, nosuch FROM t"
	p, err = e.Explain(ctx, id, "", text, ExplainOptions{Params: map[string]ParamValue{"a_long_parameter": {Value: "1"}}})
	if err != nil || p.Error == nil || p.Error.Position == nil || *p.Error.Position != strings.Index(text, "nosuch") {
		t.Fatalf("explain error = %+v %v", p.Error, err)
	}
}
