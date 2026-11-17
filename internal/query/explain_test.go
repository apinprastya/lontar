package query

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dbdriver "lontar/internal/driver"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "explain", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func f(v float64) *float64 { return &v }

func eqNum(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	d := *a - *b
	return d < 1e-9 && d > -1e-9
}

// outline renders a tree as "op|object|detail" lines indented by depth.
func outline(nodes []PlanNode) string {
	var b strings.Builder
	var walk func(ns []PlanNode, depth int)
	walk = func(ns []PlanNode, depth int) {
		for _, n := range ns {
			b.WriteString(strings.Repeat("  ", depth) + n.Op + "|" + n.Object + "|" + n.Detail + "\n")
			walk(n.Children, depth+1)
		}
	}
	walk(nodes, 0)
	return b.String()
}

func prop(n PlanNode, key string) (string, bool) {
	for _, p := range n.Props {
		if p.Key == key {
			return p.Value, true
		}
	}
	return "", false
}

func TestExplainSQL(t *testing.T) {
	cases := []struct {
		kind    dbdriver.Kind
		analyze bool
		want    string
	}{
		{dbdriver.Postgres, false, "EXPLAIN (FORMAT JSON) SELECT 1"},
		{dbdriver.Postgres, true, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) SELECT 1"},
		{dbdriver.MySQL, false, "EXPLAIN FORMAT=JSON SELECT 1"},
		{dbdriver.MySQL, true, "EXPLAIN ANALYZE SELECT 1"},
		{dbdriver.MariaDB, false, "EXPLAIN FORMAT=JSON SELECT 1"},
		{dbdriver.MariaDB, true, "ANALYZE FORMAT=JSON SELECT 1"},
		{dbdriver.SQLite, false, "EXPLAIN QUERY PLAN SELECT 1"},
	}
	for _, c := range cases {
		got, n := explainSQL(c.kind, "SELECT 1", ExplainOptions{Analyze: c.analyze})
		if got != c.want || got[n:] != "SELECT 1" {
			t.Errorf("%s analyze=%v: %q (prefix %d)", c.kind, c.analyze, got, n)
		}
	}
}

func explainCode(err error) string {
	var ee *ExplainError
	if errors.As(err, &ee) {
		if ee.Kind != "explain" || ee.Message == "" || ee.Error() != ee.Message {
			panic("bad ExplainError")
		}
		return ee.Code
	}
	if err != nil {
		return "other: " + err.Error()
	}
	return ""
}

func TestPrepareExplain(t *testing.T) {
	cases := []struct {
		kind    dbdriver.Kind
		sql     string
		analyze bool
		want    string
	}{
		{dbdriver.Postgres, "SELECT 1", false, ""},
		{dbdriver.Postgres, "  -- note\n select 1;  ", true, ""},
		{dbdriver.Postgres, "SELECT 1; SELECT 2", false, ExplainOneStatement},
		{dbdriver.Postgres, "-- nothing", false, ExplainOneStatement},
		{dbdriver.Postgres, "EXPLAIN SELECT 1", false, ExplainAlreadyExplain},
		{dbdriver.MySQL, "DESCRIBE t", false, ExplainAlreadyExplain},
		{dbdriver.Postgres, "CREATE TABLE t (a int)", false, ExplainNotExplainable},
		{dbdriver.Postgres, "(SELECT 1)", false, ""},
		{dbdriver.MySQL, "SET @a = 1", false, ExplainNotExplainable},
		{dbdriver.Postgres, "UPDATE t SET a = 1", false, ""},
		{dbdriver.Postgres, "UPDATE t SET a = 1", true, ExplainAnalyzeWrite},
		{dbdriver.Postgres, "WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x", true, ExplainAnalyzeWrite},
		{dbdriver.Postgres, "WITH x AS (SELECT 1) SELECT * FROM x", true, ""},
		{dbdriver.Postgres, "SELECT * INTO t2 FROM t", true, ExplainAnalyzeWrite},
		{dbdriver.MySQL, "REPLACE INTO t VALUES (1)", false, ""},
		{dbdriver.MariaDB, "DELETE FROM t", true, ExplainAnalyzeWrite},
		{dbdriver.SQLite, "SELECT 1", true, ExplainAnalyzeUnsupported},
		{dbdriver.SQLite, "INSERT INTO t VALUES (1)", false, ""},
	}
	for _, c := range cases {
		_, err := prepareExplain(c.kind, c.sql, ExplainOptions{Analyze: c.analyze})
		if got := explainCode(err); got != c.want {
			t.Errorf("%s %q analyze=%v: %q, want %q", c.kind, c.sql, c.analyze, got, c.want)
		}
	}
	st, _ := prepareExplain(dbdriver.Postgres, "  -- note\n select 1;  ", ExplainOptions{})
	if st.Text != "select 1" || st.Start != 11 {
		t.Fatalf("statement = %+v", st)
	}
}

func TestShiftPosition(t *testing.T) {
	if shiftPosition(3, 10) != 0 || shiftPosition(10, 10) != 0 || shiftPosition(17, 10) != 7 {
		t.Fatal("shiftPosition")
	}
}

func TestParsePostgres(t *testing.T) {
	nodes, planning, exec, err := parsePostgresJSON(fixture(t, "postgres.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := `Sort||n.label
  Hash Join||(n.grp = i.id)
    Seq Scan|numbers n|(n < 500)
    Hash||
      Seq Scan|items i|
`
	if got := outline(nodes); got != want {
		t.Fatalf("outline:\n%s", got)
	}
	root := nodes[0]
	if planning != nil || exec != nil || !eqNum(root.EstRows, f(150)) || !eqNum(root.Cost, f(27.23)) || root.ActualRows != nil || root.TimeMs != nil || root.Loops != nil {
		t.Fatalf("root = %+v", root)
	}
	if v, ok := prop(root, "Sort Key"); !ok || v != "n.label" {
		t.Fatalf("Sort Key prop = %q", v)
	}
	if _, ok := prop(root, "Plans"); ok {
		t.Fatal("Plans is a property")
	}

	nodes, planning, exec, err = parsePostgresJSON(fixture(t, "postgres_analyze.json"))
	if err != nil {
		t.Fatal(err)
	}
	root = nodes[0]
	if planning == nil || exec == nil || !eqNum(root.ActualRows, f(150)) || !eqNum(root.Loops, f(1)) || !eqNum(root.TimeMs, f(0.393)) {
		t.Fatalf("analyze root = %+v planning=%v exec=%v", root, planning, exec)
	}
	if v, _ := prop(root, "Shared Hit Blocks"); v != "10" {
		t.Fatalf("buffers = %q", v)
	}

	nodes, _, _, err = parsePostgresJSON(fixture(t, "postgres_complex.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := outline(nodes)
	for _, s := range []string{"InitPlan 2 (returns $1)", "SubPlan 1", "Subquery Scan", "Append"} {
		if !strings.Contains(out, s) {
			t.Fatalf("complex outline lacks %q:\n%s", s, out)
		}
	}
}

func TestParsePostgresShapes(t *testing.T) {
	raw := `[{"Plan": {"Node Type": "Nested Loop", "Join Type": "Left", "Plans": [
		{"Node Type": "Hash Join", "Join Type": "Anti", "Plans": []},
		{"Node Type": "Index Scan", "Relation Name": "t", "Alias": "t", "Index Name": "t_pkey", "Index Cond": "(id = 1)", "Actual Loops": 0, "Actual Total Time": 0.5},
		{"Node Type": "Bitmap Index Scan", "Index Name": "t_idx", "Workers": [{"a": 1}], "Flags": [1, "x", true, null]},
		{"Node Type": "CTE Scan", "CTE Name": "c", "Alias": "c1"},
		{"Node Type": "Function Scan", "Function Name": "generate_series", "Alias": "g"},
		{"Node Type": "Subquery Scan", "Alias": "sq"},
		"ignored"
	]}, "Planning Time": 0.1, "Execution Time": 2}]`
	nodes, planning, exec, err := parsePostgresJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := `Nested Loop Left Join||
  Hash Anti Join||
  Index Scan|t|using t_pkey (id = 1)
  Bitmap Index Scan|t_idx|
  CTE Scan|c|
  Function Scan|generate_series|
  Subquery Scan|sq|
`
	if got := outline(nodes); got != want || !eqNum(planning, f(0.1)) || !eqNum(exec, f(2)) {
		t.Fatalf("outline:\n%s", got)
	}
	idx := nodes[0].Children[1]
	if !eqNum(idx.TimeMs, f(0)) || !eqNum(idx.Loops, f(0)) {
		t.Fatalf("never executed = %+v", idx)
	}
	bm := nodes[0].Children[2]
	if v, _ := prop(bm, "Workers"); v != `[{"a":1}]` {
		t.Fatalf("Workers = %q", v)
	}
	if v, _ := prop(bm, "Flags"); v != "1, x, true, null" {
		t.Fatalf("Flags = %q", v)
	}
	for _, bad := range []string{"", "{", `{"Plan": {}}`, `[]`, `[1]`, `[{"x": 1}]`, `[{"Plan": {}}] 1`} {
		if _, _, _, err := parsePostgresJSON(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestParseMySQLJSON(t *testing.T) {
	nodes, planning, exec, err := parseMySQLJSON(fixture(t, "mysql.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := "Query block #1||\n" +
		"  Sort||\n" +
		"    Nested loop||\n" +
		"      Full table scan|i|\n" +
		"      Index range scan|n|using PRIMARY; ((`t`.`n`.`grp` = `t`.`i`.`id`) and (`t`.`n`.`n` < 500))\n"
	if got := outline(nodes); got != want || planning != nil || exec != nil {
		t.Fatalf("outline:\n%s", got)
	}
	qb := nodes[0]
	scan := qb.Children[0].Children[0].Children[1]
	if !eqNum(qb.Cost, f(450.83)) || !eqNum(scan.EstRows, f(499)) || !eqNum(scan.Cost, f(301.13)) || scan.ActualRows != nil || scan.TimeMs != nil {
		t.Fatalf("numbers: qb=%+v scan=%+v", qb, scan)
	}
	if v, _ := prop(scan, "cost_info.read_cost"); v != "150.88" {
		t.Fatalf("flattened cost_info = %q", v)
	}
	if v, _ := prop(scan, "possible_keys"); v != "PRIMARY" {
		t.Fatalf("possible_keys = %q", v)
	}

	nodes, _, _, err = parseMySQLJSON(fixture(t, "mysql_complex.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := outline(nodes)
	for _, s := range []string{"Union|<union1,4>|", "Group||", "Query block #3", "Materialize", "Unique index lookup|<materialized_subquery>|using <auto_key>", "Query block #4"} {
		if !strings.Contains(out, s) {
			t.Fatalf("complex outline lacks %q:\n%s", s, out)
		}
	}

	nodes, _, _, err = parseMySQLJSON(fixture(t, "mysql_update.json"))
	if err != nil || nodes[0].Children[0].Object != "numbers" {
		t.Fatalf("update = %v %s", err, outline(nodes))
	}
	if v, _ := prop(nodes[0].Children[0], "update"); v != "true" {
		t.Fatalf("update prop = %q", v)
	}

	for _, bad := range []string{"", "[]", `{"x": 1}`, `{"query_block": 1} x`} {
		if _, _, _, err := parseMySQLJSON(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestParseMariaDBJSON(t *testing.T) {
	nodes, planning, exec, err := parseMySQLJSON(fixture(t, "mariadb.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := "Query block #1||\n" +
		"  Sort||n.label\n" +
		"    Temporary table||\n" +
		"      Nested loop||\n" +
		"        Index range scan|n|using PRIMARY; n.n < 500\n" +
		"        Block nested loop join||BNL i.`id` = n.grp\n" +
		"          Full table scan|i|\n"
	if got := outline(nodes); got != want || planning != nil || exec != nil {
		t.Fatalf("outline:\n%s", got)
	}

	nodes, planning, exec, err = parseMySQLJSON(fixture(t, "mariadb_analyze.json"))
	if err != nil {
		t.Fatal(err)
	}
	qb := nodes[0]
	sort := qb.Children[0]
	loop := sort.Children[0].Children[0]
	scan := loop.Children[0]
	if !eqNum(planning, f(0.199692941)) || !eqNum(exec, f(0.852275123)) || !eqNum(qb.TimeMs, exec) {
		t.Fatalf("times: planning=%v exec=%v qb=%v", planning, exec, qb.TimeMs)
	}
	if !eqNum(scan.ActualRows, f(499)) || !eqNum(scan.Loops, f(1)) || !eqNum(scan.TimeMs, f(0.158449932+0.134622648)) {
		t.Fatalf("scan = %+v", scan)
	}
	if !eqNum(sort.ActualRows, f(150)) || sort.TimeMs == nil || *sort.TimeMs <= *loop.TimeMs {
		t.Fatalf("sort = %+v (loop %v)", sort, loop.TimeMs)
	}
	if v, _ := prop(scan, "r_engine_stats.pages_accessed"); v != "8" {
		t.Fatalf("engine stats = %q", v)
	}

	nodes, _, _, err = parseMySQLJSON(fixture(t, "mariadb_complex.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := outline(nodes)
	for _, s := range []string{"Union|<union1,4>|", "Query block #3", "Materialize", "Query block #4|", "UNION"} {
		if !strings.Contains(out, s) {
			t.Fatalf("mariadb complex outline lacks %q:\n%s", s, out)
		}
	}
}

func TestParseMySQLJSONShapes(t *testing.T) {
	raw := `{"query_block": {"select_id": 1, "message": "No tables used", "nested_loop": [{"table": {"table_name": "t", "access_type": "weird"}}],
		"extra_wrapper": {"table": {"table_name": "u"}}, "other": {"a": 1}}}`
	nodes, _, _, err := parseMySQLJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := "Query block #1||No tables used\n  Table weird|t|\n  Extra wrapper||\n    Table|u|\n"
	if got := outline(nodes); got != want {
		t.Fatalf("outline:\n%s", got)
	}
	if v, _ := prop(nodes[0], "other.a"); v != "1" {
		t.Fatalf("other.a = %q", v)
	}
	if humanize("") != "" {
		t.Fatal("humanize")
	}
}

func TestParseMySQLTree(t *testing.T) {
	nodes, err := parseMySQLTree(fixture(t, "mysql_analyze.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := `Sort||n.label
  Stream results||
    Inner hash join||(n.grp = i.id)
      Filter||(n.n < 500)
        Index range scan|n|using PRIMARY over (n < 500)
      Hash||
        Table scan|i|
`
	if got := outline(nodes); got != want {
		t.Fatalf("outline:\n%s", got)
	}
	sort := nodes[0]
	if sort.Cost != nil || sort.EstRows != nil || !eqNum(sort.ActualRows, f(150)) || !eqNum(sort.Loops, f(1)) || !eqNum(sort.TimeMs, f(0.484)) {
		t.Fatalf("sort = %+v", sort)
	}
	join := sort.Children[0].Children[0]
	if !eqNum(join.Cost, f(166)) || !eqNum(join.EstRows, f(15)) {
		t.Fatalf("join = %+v", join)
	}
	if v, _ := prop(join, "Description"); !strings.HasPrefix(v, "Inner hash join") {
		t.Fatalf("Description = %q", v)
	}

	nodes, err = parseMySQLTree(fixture(t, "mysql_analyze_complex.txt"))
	if err != nil {
		t.Fatal(err)
	}
	out := outline(nodes)
	for _, s := range []string{"Table scan|<union temporary>|", "Select #2||(subquery in condition; run only once)", "Index lookup|<materialized_subquery>|using <auto_distinct_key> (id=numbers.n)"} {
		if !strings.Contains(out, s) {
			t.Fatalf("complex outline lacks %q:\n%s", s, out)
		}
	}
	// "rows=0.003 loops=1001" with times like 534e-6.
	var lookup *PlanNode
	var find func(ns []PlanNode)
	find = func(ns []PlanNode) {
		for i := range ns {
			if ns[i].Op == "Limit" {
				lookup = &ns[i]
			}
			find(ns[i].Children)
		}
	}
	find(nodes)
	if lookup == nil || !eqNum(lookup.Loops, f(1001)) || !eqNum(lookup.ActualRows, f(0.003)) || !eqNum(lookup.TimeMs, f(408e-6*1001)) || !eqNum(lookup.Cost, f(0.85)) {
		t.Fatalf("limit = %+v", lookup)
	}

	nodes, err = parseMySQLTree("-> Nested loop  (cost=2 rows=1) (never executed)\n    continued text\n-> Second root")
	if err != nil || len(nodes) != 2 || nodes[0].Detail != "continued text" || !eqNum(nodes[0].Loops, f(0)) || nodes[0].TimeMs != nil {
		t.Fatalf("never executed = %+v %v", nodes, err)
	}
	for _, bad := range []string{"", "  \n", "no arrow"} {
		if _, err := parseMySQLTree(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	if parseF("x") != nil {
		t.Fatal("parseF")
	}
}

func TestParseSQLite(t *testing.T) {
	rows := []sqliteQPRow{
		{2, 0, "SCAN n"},
		{5, 0, "SEARCH i USING INTEGER PRIMARY KEY (rowid=?)"},
		{9, 0, "CORRELATED SCALAR SUBQUERY 1"},
		{12, 9, "SCAN TABLE items"},
		{20, 0, "USE TEMP B-TREE FOR ORDER BY"},
		{30, 99, "orphan"},
	}
	nodes := parseSQLiteQP(rows)
	want := `SCAN|n|
SEARCH|i|USING INTEGER PRIMARY KEY (rowid=?)
CORRELATED SCALAR SUBQUERY 1||
  SCAN|items|
USE TEMP B-TREE FOR ORDER BY||
orphan||
`
	if got := outline(nodes); got != want {
		t.Fatalf("outline:\n%s", got)
	}
	for _, n := range nodes {
		if n.EstRows != nil || n.Cost != nil || n.TimeMs != nil {
			t.Fatalf("numbers in %+v", n)
		}
	}
	raw := sqliteRaw(nodes)
	wantRaw := "QUERY PLAN\n|--SCAN n\n|--SEARCH i USING INTEGER PRIMARY KEY (rowid=?)\n|--CORRELATED SCALAR SUBQUERY 1\n|  `--SCAN TABLE items\n|--USE TEMP B-TREE FOR ORDER BY\n`--orphan"
	if raw != wantRaw {
		t.Fatalf("raw:\n%s", raw)
	}
	if propOr(newNode("x"), "detail", "def") != "def" {
		t.Fatal("propOr")
	}
}

func TestParsePlanDispatch(t *testing.T) {
	cases := []struct {
		kind    dbdriver.Kind
		analyze bool
		raw     string
		format  string
	}{
		{dbdriver.Postgres, false, fixture(t, "postgres.json"), PlanFormatJSON},
		{dbdriver.MySQL, false, fixture(t, "mysql.json"), PlanFormatJSON},
		{dbdriver.MySQL, true, fixture(t, "mysql_analyze.txt"), PlanFormatText},
		{dbdriver.MariaDB, true, fixture(t, "mariadb_analyze.json"), PlanFormatJSON},
	}
	for _, c := range cases {
		p := &Plan{Notices: []string{}}
		parsePlan(p, c.kind, c.analyze, c.raw, nil)
		if len(p.Nodes) == 0 || p.RawFormat != c.format || p.Raw != c.raw || len(p.Notices) != 0 {
			t.Errorf("%s analyze=%v: %+v", c.kind, c.analyze, p)
		}
	}
	p := &Plan{Notices: []string{}}
	parsePlan(p, dbdriver.SQLite, false, "", []sqliteQPRow{{2, 0, "SCAN t"}})
	if len(p.Nodes) != 1 || p.RawFormat != PlanFormatText || p.Raw != "QUERY PLAN\n`--SCAN t" {
		t.Fatalf("sqlite = %+v", p)
	}
	p = &Plan{Notices: []string{}}
	parsePlan(p, dbdriver.Postgres, false, "not json", nil)
	if len(p.Nodes) != 0 || p.Raw != "not json" || len(p.Notices) != 1 || !strings.Contains(p.Notices[0], "could not be read") {
		t.Fatalf("broken = %+v", p)
	}
}

func TestPlanText(t *testing.T) {
	if planText(nil) != "" || planText("a") != "a" || planText([]byte("b")) != "b" || planText(map[string]int{"x": 1}) != "{\n  \"x\": 1\n}" {
		t.Fatal("planText")
	}
	if planText(func() {}) == "" {
		t.Fatal("planText of an unmarshalable value")
	}
	if toNum(true) != nil || toNum("x") != nil || !eqNum(toNum(" 2.5 "), f(2.5)) {
		t.Fatal("toNum")
	}
	if mul(nil, f(1)) != nil || sumNums() != nil {
		t.Fatal("mul/sumNums")
	}
}
