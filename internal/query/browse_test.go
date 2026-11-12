package query

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"lontar/internal/driver"
)

var testCols = []driver.Column{
	{Name: "id", DataType: "integer", IsPrimaryKey: true, PKPosition: 1},
	{Name: "name", DataType: "text", Nullable: true},
	{Name: `we"ird`, DataType: "text"},
}

type dialectCase struct {
	name string
	d    driver.Dialect
	// q quotes an identifier, p(n) renders a placeholder, like(col) the
	// pattern-matching expression and operator.
	q    func(string) string
	p    func(int) string
	like func(col string, not bool) string
}

var dialects = []dialectCase{
	{
		name: "postgres", d: driver.PostgresDialect{},
		q: func(s string) string { return `"` + s + `"` },
		p: func(n int) string { return "$" + string(rune('0'+n)) },
		like: func(col string, not bool) string {
			if not {
				return "CAST(" + col + " AS TEXT) NOT ILIKE"
			}
			return "CAST(" + col + " AS TEXT) ILIKE"
		},
	},
	{
		name: "mysql", d: driver.MySQLDialect{},
		q: func(s string) string { return "`" + s + "`" },
		p: func(int) string { return "?" },
		like: func(col string, not bool) string {
			if not {
				return col + " NOT LIKE"
			}
			return col + " LIKE"
		},
	},
	{
		name: "sqlite", d: driver.SQLiteDialect{},
		q: func(s string) string { return `"` + s + `"` },
		p: func(int) string { return "?" },
		like: func(col string, not bool) string {
			if not {
				return col + " NOT LIKE"
			}
			return col + " LIKE"
		},
	},
}

func TestFilterOpsAllDialects(t *testing.T) {
	for _, dc := range dialects {
		t.Run(dc.name, func(t *testing.T) {
			col := dc.q("name")
			p1, p2, p3 := dc.p(1), dc.p(2), dc.p(3)
			cases := []struct {
				f     Filter
				where string
				args  []any
			}{
				{Filter{Column: "name", Op: OpEq, Value: "a"}, col + " = " + p1, []any{"a"}},
				{Filter{Column: "name", Op: OpNe, Value: "a"}, col + " <> " + p1, []any{"a"}},
				{Filter{Column: "name", Op: OpLt, Value: "5"}, col + " < " + p1, []any{"5"}},
				{Filter{Column: "name", Op: OpGt, Value: "5"}, col + " > " + p1, []any{"5"}},
				{Filter{Column: "name", Op: OpLe, Value: "5"}, col + " <= " + p1, []any{"5"}},
				{Filter{Column: "name", Op: OpGe, Value: "5"}, col + " >= " + p1, []any{"5"}},
				{Filter{Column: "name", Op: OpContains, Value: "a%b_c!"}, dc.like(col, false) + " " + p1 + " ESCAPE '!'", []any{"%a!%b!_c!!%"}},
				{Filter{Column: "name", Op: OpNotContains, Value: "x"}, dc.like(col, true) + " " + p1 + " ESCAPE '!'", []any{"%x%"}},
				{Filter{Column: "name", Op: OpStartsWith, Value: "x"}, dc.like(col, false) + " " + p1 + " ESCAPE '!'", []any{"x%"}},
				{Filter{Column: "name", Op: OpEndsWith, Value: "x"}, dc.like(col, false) + " " + p1 + " ESCAPE '!'", []any{"%x"}},
				{Filter{Column: "name", Op: OpIn, Value: " a, b ,,c"}, col + " IN (" + p1 + ", " + p2 + ", " + p3 + ")", []any{"a", "b", "c"}},
				{Filter{Column: "name", Op: OpNotIn, Value: "a"}, col + " NOT IN (" + p1 + ")", []any{"a"}},
				{Filter{Column: "name", Op: OpIsNull}, col + " IS NULL", nil},
				{Filter{Column: "name", Op: OpIsNotNull, Value: "ignored"}, col + " IS NOT NULL", nil},
				{Filter{Column: "name", Op: OpBetween, Value: "1", Value2: "9"}, col + " BETWEEN " + p1 + " AND " + p2, []any{"1", "9"}},
				{Filter{Column: "name", Op: OpEq, Value: "' OR 1=1 --"}, col + " = " + p1, []any{"' OR 1=1 --"}},
			}
			for _, tc := range cases {
				b, err := BuildCount(dc.d, testCols, Request{Schema: "s", Table: "t", Filters: []Filter{tc.f}})
				if err != nil {
					t.Fatalf("%s: %v", tc.f.Op, err)
				}
				want := "SELECT COUNT(*) FROM " + dc.q("s") + "." + dc.q("t") + " WHERE " + tc.where
				if b.SQL != want {
					t.Errorf("%s:\n got %s\nwant %s", tc.f.Op, b.SQL, want)
				}
				if !reflect.DeepEqual(b.Args, tc.args) {
					t.Errorf("%s: args = %#v, want %#v", tc.f.Op, b.Args, tc.args)
				}
			}
		})
	}
}

func TestBuildSelect(t *testing.T) {
	for _, dc := range dialects {
		t.Run(dc.name, func(t *testing.T) {
			q := dc.q
			// Default: all columns, PK order, limit+1.
			b, err := BuildSelect(dc.d, testCols, Request{Schema: "s", Table: "t"})
			if err != nil {
				t.Fatal(err)
			}
			wantCols := q("id") + ", " + q("name") + ", " + strings.ReplaceAll(q(`we"ird`), `"ird`, `""ird`)
			if dc.name == "mysql" {
				wantCols = q("id") + ", " + q("name") + ", " + q(`we"ird`)
			}
			want := "SELECT " + wantCols + " FROM " + q("s") + "." + q("t") + " ORDER BY " + q("id") + " ASC LIMIT 301 OFFSET 0"
			if b.SQL != want || len(b.Args) != 0 {
				t.Fatalf("\n got %s\nwant %s", b.SQL, want)
			}

			// Filters ANDed, multi-sort, explicit paging, raw WHERE.
			b, err = BuildSelect(dc.d, testCols, Request{
				Table:    "t",
				Filters:  []Filter{{Column: "id", Op: OpGt, Value: "1"}, {Column: "name", Op: OpIsNotNull}},
				RawWhere: " id < 100 ",
				Sort:     []Sort{{Column: "name", Desc: true}, {Column: "id"}},
				Offset:   600, Limit: 300,
			})
			if err != nil {
				t.Fatal(err)
			}
			want = "SELECT " + wantCols + " FROM " + q("t") +
				" WHERE " + q("id") + " > " + dc.p(1) + " AND " + q("name") + " IS NOT NULL AND (id < 100)" +
				" ORDER BY " + q("name") + " DESC, " + q("id") + " ASC LIMIT 301 OFFSET 600"
			if b.SQL != want || !reflect.DeepEqual(b.Args, []any{"1"}) {
				t.Fatalf("\n got %s %v\nwant %s", b.SQL, b.Args, want)
			}
		})
	}
}

func TestBuildSelectNoPKNoOrder(t *testing.T) {
	b, err := BuildSelect(driver.SQLiteDialect{}, []driver.Column{{Name: "a"}}, Request{Table: "t", Offset: -5, Limit: 99999})
	if err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "a" FROM "t" LIMIT 5001 OFFSET 0`; b.SQL != want {
		t.Fatalf("got %s", b.SQL)
	}
}

func TestBuildErrors(t *testing.T) {
	d := driver.PostgresDialect{}
	cases := []struct {
		name string
		req  Request
		want error
	}{
		{"unknown filter column", Request{Table: "t", Filters: []Filter{{Column: "nope", Op: OpEq}}}, ErrUnknownColumn},
		{"case mismatch column", Request{Table: "t", Filters: []Filter{{Column: "ID", Op: OpEq}}}, ErrUnknownColumn},
		{"injection column", Request{Table: "t", Filters: []Filter{{Column: `id" OR 1=1 --`, Op: OpEq}}}, ErrUnknownColumn},
		{"unknown sort column", Request{Table: "t", Sort: []Sort{{Column: "nope"}}}, ErrUnknownColumn},
		{"unknown op", Request{Table: "t", Filters: []Filter{{Column: "id", Op: "LIKE"}}}, ErrBadFilter},
		{"empty in", Request{Table: "t", Filters: []Filter{{Column: "id", Op: OpIn, Value: " , "}}}, ErrBadFilter},
	}
	for _, tc := range cases {
		if _, err := BuildSelect(d, testCols, tc.req); !errors.Is(err, tc.want) {
			t.Errorf("%s: select err = %v, want %v", tc.name, err, tc.want)
		}
		if tc.req.Sort == nil {
			if _, err := BuildCount(d, testCols, tc.req); !errors.Is(err, tc.want) {
				t.Errorf("%s: count err = %v, want %v", tc.name, err, tc.want)
			}
		}
	}
	if _, err := BuildSelect(d, nil, Request{Table: "t"}); !errors.Is(err, ErrUnknownTable) {
		t.Fatalf("err = %v", err)
	}
	if _, err := BuildCount(d, nil, Request{Table: "t"}); !errors.Is(err, ErrUnknownTable) {
		t.Fatalf("err = %v", err)
	}
}

func TestNormalizeLimit(t *testing.T) {
	for in, want := range map[int]int{-1: 300, 0: 300, 1: 1, 300: 300, 5000: 5000, 5001: 5000} {
		if got := NormalizeLimit(in); got != want {
			t.Errorf("NormalizeLimit(%d) = %d", in, got)
		}
	}
}

func TestEscapeLike(t *testing.T) {
	if got := escapeLike(`a!b%c_d\e`); got != `a!!b!%c!_d\e` {
		t.Fatalf("got %q", got)
	}
}

func TestQualified(t *testing.T) {
	if qualified("", "t") != "t" || qualified("s", "t") != "s.t" {
		t.Fatal("qualified")
	}
}

func TestBuildSelectCompositePKOrder(t *testing.T) {
	cols := []driver.Column{
		{Name: "a", IsPrimaryKey: true, PKPosition: 2},
		{Name: "v"},
		{Name: "b", IsPrimaryKey: true, PKPosition: 1},
	}
	b, err := BuildSelect(driver.MySQLDialect{}, cols, Request{Table: "t", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if want := "SELECT `a`, `v`, `b` FROM `t` ORDER BY `b` ASC, `a` ASC LIMIT 11 OFFSET 0"; b.SQL != want {
		t.Fatalf("got %s", b.SQL)
	}
}
