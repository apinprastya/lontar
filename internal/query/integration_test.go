package query

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

	"dbedit/internal/driver/mysql"
	"dbedit/internal/driver/postgres"
	"dbedit/internal/driver/sqlite"
	"dbedit/internal/testutil"
	"dbedit/internal/values"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

type engineCase struct {
	engine string
	start  func(testing.TB) testutil.DB
	e      Engine
	// sleep is a raw WHERE condition that takes ~10s.
	sleep string
}

var engines = []engineCase{
	{testutil.EngineSQLite, testutil.SQLiteTemp, sqlite.New(),
		"(WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n) SELECT COUNT(*) FROM n) > 0"},
	{testutil.EnginePostgres, testutil.SharedPostgres, postgres.New(), "(SELECT count(*) FROM pg_sleep(10)) > 0"},
	{testutil.EngineMySQL, testutil.SharedMySQL, mysql.New(), "SLEEP(10) = 0"},
	{testutil.EngineMariaDB, testutil.SharedMariaDB, mysql.NewMariaDB(), "SLEEP(10) = 0"},
}

// forEachEngine runs fn against every engine with the browse fixture.
func forEachEngine(t *testing.T, fn func(t *testing.T, ec engineCase, db *sql.DB, schema string)) {
	for _, ec := range engines {
		t.Run(ec.engine, func(t *testing.T) {
			target := ec.start(t)
			db := testutil.Open(t, target)
			testutil.SeedBrowse(t, ec.engine, target, db)
			fn(t, ec, db, testutil.SeedSchema(ec.engine))
		})
	}
}

func cellString(c values.Cell) string {
	if c.K == values.Null {
		return "NULL"
	}
	if f, ok := c.V.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return fmt.Sprint(c.V)
}

func TestIntegrationValues(t *testing.T) {
	forEachEngine(t, func(t *testing.T, ec engineCase, db *sql.DB, schema string) {
		p, err := FetchTable(context.Background(), db, ec.e, Request{Schema: schema, Table: testutil.SeedItems})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Rows) != 3 || p.HasMore || p.TotalCount == nil || *p.TotalCount != 3 {
			t.Fatalf("rows=%d hasMore=%v total=%v", len(p.Rows), p.HasMore, p.TotalCount)
		}
		col := map[string]int{}
		for i, c := range p.Columns {
			col[c.Name] = i
		}
		get := func(row int, name string) values.Cell { return p.Rows[row][col[name]] }
		expect := func(row int, name string, k values.Kind, v string) {
			t.Helper()
			c := get(row, name)
			if c.K != k || (v != "*" && cellString(c) != v) {
				t.Errorf("row %d %s = %+v (%q), want %s %q", row+1, name, c, cellString(c), k, v)
			}
		}
		expect(0, "id", values.Num, "1")
		expect(0, "big", values.Num, testutil.BigInt)
		expect(2, "big", values.Num, "-"+testutil.BigInt)
		expect(0, "name", values.Str, "alpha")
		expect(0, "note", values.Str, "")
		expect(1, "note", values.Null, "NULL")
		expect(1, "price", values.Null, "NULL")
		expect(0, "data", values.Bytes, "00ff10")
		expect(2, "data", values.Bytes, "fffe")
		expect(1, "data", values.Null, "NULL")
		expect(0, "born", values.Time, "2024-01-02")
		if c := get(0, "seen_at"); c.K != values.Time || !strings.HasPrefix(cellString(c), "2024-01-02") || !strings.Contains(cellString(c), "03:04:05") {
			t.Errorf("seen_at = %+v", c)
		}
		expect(0, "uid", values.Str, testutil.SeedUUID)
		switch ec.engine {
		case testutil.EngineSQLite:
			expect(0, "price", values.Num, "12.34")
			expect(0, "doc", values.JSON, `{"a": 1}`)
			expect(0, "flag", values.Num, "1")
		case testutil.EnginePostgres:
			expect(0, "price", values.Num, "12.3400")
			expect(2, "price", values.Num, "-0.5000")
			expect(0, "doc", values.JSON, `{"a": 1}`)
			expect(0, "flag", values.Bool, "true")
			expect(1, "flag", values.Bool, "false")
			expect(0, "tags", values.Str, "{1,2}")
			expect(0, "dur", values.Str, "1 day")
		default:
			expect(0, "price", values.Num, "12.3400")
			expect(0, "flag", values.Num, "1")
			if c := get(0, "doc"); (c.K != values.JSON && c.K != values.Str) || !strings.Contains(cellString(c), `"a"`) {
				t.Errorf("doc = %+v", c)
			}
		}
		if !p.Columns[0].IsPK || p.Columns[col["name"]].IsPK || !p.Columns[col["name"]].Nullable {
			t.Errorf("columns = %+v", p.Columns)
		}
	})
}

func countMatching(pred func(n int) bool) int64 {
	var c int64
	for n := 1; n <= 1000; n++ {
		if pred(n) {
			c++
		}
	}
	return c
}

func TestIntegrationFilters(t *testing.T) {
	forEachEngine(t, func(t *testing.T, ec engineCase, db *sql.DB, schema string) {
		ctx := context.Background()
		label := func(n int) string { return "n" + strconv.Itoa(n) }
		cases := []struct {
			f    []Filter
			raw  string
			want int64
		}{
			{[]Filter{{Column: "n", Op: OpEq, Value: "5"}}, "", 1},
			{[]Filter{{Column: "n", Op: OpNe, Value: "5"}}, "", 999},
			{[]Filter{{Column: "n", Op: OpLt, Value: "10"}}, "", 9},
			{[]Filter{{Column: "n", Op: OpGt, Value: "990"}}, "", 10},
			{[]Filter{{Column: "n", Op: OpLe, Value: "10"}}, "", 10},
			{[]Filter{{Column: "n", Op: OpGe, Value: "991"}}, "", 10},
			{[]Filter{{Column: "label", Op: OpContains, Value: "N99"}}, "", countMatching(func(n int) bool { return strings.Contains(label(n), "n99") })},
			{[]Filter{{Column: "label", Op: OpNotContains, Value: "1"}}, "", countMatching(func(n int) bool { return !strings.Contains(label(n), "1") })},
			{[]Filter{{Column: "label", Op: OpStartsWith, Value: "n10"}}, "", 12}, // n10, n100..n109, n1000
			{[]Filter{{Column: "label", Op: OpEndsWith, Value: "00"}}, "", 10},
			{[]Filter{{Column: "n", Op: OpIn, Value: "1, 2,3"}}, "", 3},
			{[]Filter{{Column: "n", Op: OpNotIn, Value: "1,2"}}, "", 998},
			{[]Filter{{Column: "label", Op: OpIsNull}}, "", 0},
			{[]Filter{{Column: "label", Op: OpIsNotNull}}, "", 1000},
			{[]Filter{{Column: "n", Op: OpBetween, Value: "10", Value2: "19"}}, "", 10},
			{[]Filter{{Column: "grp", Op: OpEq, Value: "3"}, {Column: "n", Op: OpLe, Value: "100"}}, "", 10},
			{nil, "n <= 20 AND grp = 0", 2},
			{[]Filter{{Column: "grp", Op: OpEq, Value: "0"}}, "n > 500", 50},
			// Wildcards in values are literal.
			{[]Filter{{Column: "label", Op: OpContains, Value: "%"}}, "", 0},
			{[]Filter{{Column: "label", Op: OpContains, Value: "_"}}, "", 0},
			{[]Filter{{Column: "label", Op: OpStartsWith, Value: "n!"}}, "", 0},
			// Injection attempts are just values.
			{[]Filter{{Column: "label", Op: OpEq, Value: "' OR 1=1 --"}}, "", 0},
			{[]Filter{{Column: "label", Op: OpContains, Value: "' OR '1'='1"}}, "", 0},
			{[]Filter{{Column: "label", Op: OpIn, Value: "n1') OR ('1'='1, n2"}}, "", 1},
		}
		for _, tc := range cases {
			req := Request{Schema: schema, Table: testutil.SeedNumbers, Filters: tc.f, RawWhere: tc.raw}
			n, err := CountRows(ctx, db, ec.e, req)
			if err != nil {
				t.Errorf("%+v: %v", tc.f, err)
				continue
			}
			if n != tc.want {
				t.Errorf("%+v %q: count = %d, want %d", tc.f, tc.raw, n, tc.want)
			}
			p, err := FetchTable(ctx, db, ec.e, Request{Schema: schema, Table: testutil.SeedNumbers, Filters: tc.f, RawWhere: tc.raw, Limit: MaxLimit})
			if err != nil || int64(len(p.Rows)) != tc.want {
				t.Errorf("%+v: fetch rows = %v, %v", tc.f, p, err)
			}
		}

		if ec.engine == testutil.EnginePostgres {
			for _, f := range []Filter{
				{Column: "uid", Op: OpEq, Value: testutil.SeedUUID},
				{Column: "flag", Op: OpEq, Value: "true"},
				{Column: "big", Op: OpGt, Value: "9007199254740992"},
				{Column: "price", Op: OpBetween, Value: "12", Value2: "13"},
				{Column: "born", Op: OpGe, Value: "2024-01-01"},
				{Column: "doc", Op: OpContains, Value: `"A"`},
				{Column: "data", Op: OpIsNotNull},
			} {
				n, err := CountRows(ctx, db, ec.e, Request{Schema: schema, Table: testutil.SeedItems, Filters: []Filter{f}})
				want := int64(1)
				if f.Column == "data" {
					want = 2
				}
				if err != nil || n != want {
					t.Errorf("pg typed filter %+v: %d, %v", f, n, err)
				}
			}
		}
	})
}

func TestIntegrationSortPaging(t *testing.T) {
	forEachEngine(t, func(t *testing.T, ec engineCase, db *sql.DB, schema string) {
		ctx := context.Background()
		ids := func(p *Page) string {
			var s []string
			for _, r := range p.Rows {
				s = append(s, cellString(r[0]))
			}
			return strings.Join(s, ",")
		}
		p, err := FetchTable(ctx, db, ec.e, Request{Schema: schema, Table: testutil.SeedNumbers,
			Sort: []Sort{{Column: "grp", Desc: true}, {Column: "n"}}, Limit: 3})
		if err != nil || ids(p) != "9,19,29" || !p.HasMore {
			t.Fatalf("multi-sort = %s %v, %v", ids(p), p, err)
		}
		// Default order is the primary key.
		p, _ = FetchTable(ctx, db, ec.e, Request{Schema: schema, Table: testutil.SeedNumbers})
		if len(p.Rows) != DefaultLimit || !p.HasMore || cellString(p.Rows[0][0]) != "1" || cellString(p.Rows[299][0]) != "300" {
			t.Fatalf("first page: %d rows hasMore=%v", len(p.Rows), p.HasMore)
		}
		p, _ = FetchTable(ctx, db, ec.e, Request{Schema: schema, Table: testutil.SeedNumbers, Offset: 300})
		if cellString(p.Rows[0][0]) != "301" || !p.HasMore {
			t.Fatalf("second page starts at %s", cellString(p.Rows[0][0]))
		}
		p, _ = FetchTable(ctx, db, ec.e, Request{Schema: schema, Table: testutil.SeedNumbers, Offset: 900})
		if len(p.Rows) != 100 || p.HasMore || p.TotalCount == nil || *p.TotalCount != 1000 {
			t.Fatalf("last page: %d rows hasMore=%v total=%v", len(p.Rows), p.HasMore, p.TotalCount)
		}
		p, _ = FetchTable(ctx, db, ec.e, Request{Schema: schema, Table: testutil.SeedNumbers, Limit: 1000})
		if len(p.Rows) != 1000 || p.HasMore {
			t.Fatalf("exact page: %d rows hasMore=%v", len(p.Rows), p.HasMore)
		}
		if n, err := CountRows(ctx, db, ec.e, Request{Schema: schema, Table: testutil.SeedNumbers}); err != nil || n != 1000 {
			t.Fatalf("count = %d, %v", n, err)
		}

		// Composite PK default order (b, a).
		p, _ = FetchTable(ctx, db, ec.e, Request{Schema: schema, Table: testutil.SeedComposite})
		if ids(p) != "2,1" {
			t.Fatalf("composite order = %s", ids(p))
		}
		// No PK and views: no default order, all rows.
		for _, tbl := range []string{testutil.SeedNoPK, testutil.SeedView} {
			p, err = FetchTable(ctx, db, ec.e, Request{Schema: schema, Table: tbl})
			if err != nil || len(p.Rows) < 2 {
				t.Fatalf("%s: %v, %v", tbl, p, err)
			}
		}
		if ec.engine == testutil.EnginePostgres {
			p, err = FetchTable(ctx, db, ec.e, Request{Schema: schema, Table: testutil.SeedMatView, Sort: []Sort{{Column: "id", Desc: true}}})
			if err != nil || ids(p) != "3,2,1" {
				t.Fatalf("matview: %v, %v", p, err)
			}
		}

		// Awkward names.
		p, err = FetchTable(ctx, db, ec.e, Request{Schema: schema, Table: testutil.SeedOdd,
			Filters: []Filter{{Column: "Mixed Case", Op: OpContains, Value: "t"}, {Column: `quote"col`, Op: OpNe, Value: "zz"}},
			Sort:    []Sort{{Column: "order", Desc: true}}})
		if err != nil || ids(p) != "2" || cellString(p.Rows[0][2]) != "q2" {
			t.Fatalf("odd: %v, %v", p, err)
		}
	})
}

func TestIntegrationRejections(t *testing.T) {
	forEachEngine(t, func(t *testing.T, ec engineCase, db *sql.DB, schema string) {
		ctx := context.Background()
		for _, tbl := range []string{"numbers; DROP TABLE numbers", `numbers" --`, "numbers` --", "NUMBERS_X", "pg_user"} {
			if _, err := FetchTable(ctx, db, ec.e, Request{Schema: schema, Table: tbl}); !errors.Is(err, ErrUnknownTable) {
				t.Errorf("table %q: err = %v", tbl, err)
			}
		}
		if _, err := FetchTable(ctx, db, ec.e, Request{Schema: "no_such_schema", Table: testutil.SeedNumbers}); !errors.Is(err, ErrUnknownTable) {
			t.Errorf("schema: err = %v", err)
		}
		if _, err := FetchTable(ctx, db, ec.e, Request{Schema: schema, Table: testutil.SeedNumbers,
			Filters: []Filter{{Column: "n) OR (1=1", Op: OpEq, Value: "1"}}}); !errors.Is(err, ErrUnknownColumn) {
			t.Errorf("column: err = %v", err)
		}
		// The table survived.
		if n, err := CountRows(ctx, db, ec.e, Request{Schema: schema, Table: testutil.SeedNumbers}); err != nil || n != 1000 {
			t.Fatalf("count = %d, %v", n, err)
		}
	})
}

func TestIntegrationCancel(t *testing.T) {
	forEachEngine(t, func(t *testing.T, ec engineCase, db *sql.DB, schema string) {
		r := NewRunner()
		req := Request{Schema: schema, Table: testutil.SeedItems, RawWhere: ec.sleep}
		errc := make(chan error, 1)
		start := time.Now()
		go func() {
			errc <- r.Run(context.Background(), "slow", func(ctx context.Context) error {
				_, err := CountRows(ctx, db, ec.e, req)
				return err
			})
		}()
		deadline := time.Now().Add(5 * time.Second)
		for r.Running() == 0 {
			if time.Now().After(deadline) {
				t.Fatal("never registered")
			}
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond) // let the statement reach the server
		if !r.Cancel("slow") {
			t.Fatal("cancel found nothing")
		}
		select {
		case err := <-errc:
			if !errors.Is(err, ErrCanceled) {
				t.Fatalf("err = %v", err)
			}
			if d := time.Since(start); d > 8*time.Second {
				t.Fatalf("cancel took %s", d)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("cancel did not stop the query")
		}

		// Timeout.
		r.Timeout = 300 * time.Millisecond
		err := r.Run(context.Background(), "t", func(ctx context.Context) error {
			_, err := FetchTable(ctx, db, ec.e, req)
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("timeout err = %v", err)
		}
	})
}
