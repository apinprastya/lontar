package query

import (
	"reflect"
	"testing"

	dbdriver "lontar/internal/driver"
)

func TestParseSelectSourceEditable(t *testing.T) {
	col := func(name string) SelectItem { return SelectItem{Column: name} }
	star := SelectItem{Star: true}
	expr := SelectItem{}
	cases := []struct {
		kind   dbdriver.Kind
		sql    string
		schema string
		table  string
		items  []SelectItem
	}{
		{dbdriver.Postgres, "SELECT * FROM users", "", "users", []SelectItem{star}},
		{dbdriver.Postgres, "select * from public.users;", "public", "users", []SelectItem{star}},
		{dbdriver.Postgres, "SELECT u.*, upper(name) AS shout FROM users u WHERE id > 3 ORDER BY id LIMIT 10", "", "users", []SelectItem{star, expr}},
		{dbdriver.Postgres, "SELECT id, users.name, public.users.email AS mail, 1, NULL, x::text FROM users AS u", "", "users", []SelectItem{col("id"), col("name"), col("email"), expr, expr, expr}},
		{dbdriver.Postgres, `SELECT "Id", n nick FROM "My Table" FOR UPDATE`, "", "My Table", []SelectItem{{Column: "Id", Quoted: true}, col("n")}},
		{dbdriver.Postgres, "SELECT ALL id FROM t WHERE id IN (SELECT id FROM u JOIN v USING (id) GROUP BY id)", "", "t", []SelectItem{col("id")}},
		{dbdriver.Postgres, "-- comment\nSELECT /* c */ id, 'a,b' AS s, $$x$$, E'it''s', $1 FROM t", "", "t", []SelectItem{col("id"), expr, expr, expr, expr}},
		{dbdriver.MySQL, "SELECT `id`, \"str\" FROM db.`t` # comment", "db", "t", []SelectItem{{Column: "id", Quoted: true}, expr}},
		{dbdriver.SQLite, "SELECT [id], count(*) OVER () FROM main.t", "main", "t", []SelectItem{{Column: "id", Quoted: true}, expr}},
		{dbdriver.SQLite, "SELECT t.* FROM t LIMIT 5 OFFSET 2", "", "t", []SelectItem{star}},
	}
	for _, c := range cases {
		src, reason := ParseSelectSource(c.kind, c.sql)
		if src == nil {
			t.Errorf("%s: not editable: %s", c.sql, reason)
			continue
		}
		if src.Schema != c.schema || src.Table != c.table || !reflect.DeepEqual(src.Items, c.items) {
			t.Errorf("%s:\n got %q.%q %+v\nwant %q.%q %+v", c.sql, src.Schema, src.Table, src.Items, c.schema, c.table, c.items)
		}
	}
}

func TestParseSelectSourceReadOnly(t *testing.T) {
	cases := []struct {
		kind   dbdriver.Kind
		sql    string
		reason string
	}{
		{dbdriver.Postgres, "", ReasonUnsupported},
		{dbdriver.Postgres, "UPDATE t SET a = 1", ReasonNotSelect},
		{dbdriver.Postgres, "WITH x AS (SELECT 1) SELECT * FROM x", ReasonNotSelect},
		{dbdriver.Postgres, "SELECT 1", ReasonUnsupported},
		{dbdriver.Postgres, "SELECT * FROM a JOIN b ON a.id = b.id", ReasonJoin},
		{dbdriver.Postgres, "SELECT * FROM a, b", ReasonJoin},
		{dbdriver.Postgres, "SELECT * FROM (SELECT 1) x", ReasonJoin},
		{dbdriver.Postgres, "SELECT * FROM ONLY t", ReasonJoin},
		{dbdriver.Postgres, "SELECT * FROM a UNION SELECT * FROM b", ReasonJoin},
		{dbdriver.Postgres, "SELECT * INTO copy FROM t", ReasonJoin},
		{dbdriver.Postgres, "SELECT DISTINCT a FROM t", ReasonAggregate},
		{dbdriver.Postgres, "SELECT a, count(*) FROM t GROUP BY a", ReasonAggregate},
		{dbdriver.Postgres, "SELECT * FROM t TABLESAMPLE SYSTEM (1)", ReasonJoin},
		{dbdriver.MySQL, "SELECT * FROM t USE INDEX (i)", ReasonJoin},
		{dbdriver.Postgres, "SELECT * FROM t; SELECT 1", ReasonUnsupported},
		{dbdriver.Postgres, "SELECT (1 FROM t", ReasonUnsupported},
		{dbdriver.Postgres, "SELECT 1) FROM t", ReasonUnsupported},
		{dbdriver.Postgres, "SELECT 'open FROM t", ReasonUnsupported},
		{dbdriver.Postgres, "SELECT /* open FROM t", ReasonUnsupported},
		{dbdriver.Postgres, "SELECT $a$ open FROM t", ReasonUnsupported},
		{dbdriver.Postgres, "SELECT `id` FROM t", ReasonUnsupported},
		{dbdriver.Postgres, "SELECT FROM t", ReasonUnsupported},
		{dbdriver.Postgres, "SELECT * FROM t AS", ReasonJoin},
		{dbdriver.MySQL, "SELECT [id] FROM t", ReasonUnsupported},
	}
	for _, c := range cases {
		src, reason := ParseSelectSource(c.kind, c.sql)
		if src != nil || reason != c.reason {
			t.Errorf("%s: got %+v %q, want %q", c.sql, src, reason, c.reason)
		}
	}
}

func TestParseItemShapes(t *testing.T) {
	toks := func(s string) []token {
		out, ok := tokenize(dbdriver.Postgres, s)
		if !ok {
			t.Fatalf("tokenize %q", s)
		}
		return out
	}
	for s, want := range map[string]SelectItem{
		"a":            {Column: "a"},
		"a.b.c":        {Column: "c"},
		"a.b.c.d":      {},
		"a b c":        {},
		"t.* AS x":     {},
		"* x":          {},
		"a..b":         {},
		"CURRENT_DATE": {},
		"a.*.b":        {},
	} {
		if got := parseItem(toks(s)); got != want {
			t.Errorf("%q: got %+v, want %+v", s, got, want)
		}
	}
}
