package importer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dbedit/internal/driver"
	"dbedit/internal/driver/mysql"
	"dbedit/internal/driver/postgres"
	"dbedit/internal/driver/sqlite"
	"dbedit/internal/edits"
	"dbedit/internal/query"
	"dbedit/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

type engine struct {
	name  string
	drv   driver.Driver
	start func(testing.TB) testutil.DB
}

var engines = []engine{
	{"sqlite", sqlite.New(), testutil.SQLiteTemp},
	{"postgres", postgres.New(), testutil.SharedPostgres},
	{"mysql", mysql.New(), testutil.SharedMySQL},
	{"mariadb", mysql.NewMariaDB(), testutil.SharedMariaDB},
}

type env struct {
	e      engine
	db     *sql.DB
	schema string
	n      int
}

func (v *env) exec(t *testing.T, q string) {
	t.Helper()
	if _, err := v.db.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("%s: %v\n%s", v.e.name, err, q)
	}
}

// table creates a fresh target table and returns its name.
func (v *env) table(t *testing.T) string {
	t.Helper()
	v.n++
	name := "imp_" + strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_")
	name = name[:min(len(name), 40)] + "_" + string(rune('a'+v.n))
	bin := "BLOB"
	if v.e.drv.Kind() == driver.Postgres {
		bin = "bytea"
	}
	v.exec(t, "CREATE TABLE "+name+" (id INT PRIMARY KEY, name VARCHAR(50) NOT NULL, qty INT, note TEXT, data "+bin+")")
	return name
}

func forEach(t *testing.T, fn func(t *testing.T, v *env)) {
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			target := e.start(t)
			db := testutil.OpenDriver(t, e.drv, target, false)
			schema, _ := e.drv.CurrentSchema(context.Background(), db)
			fn(t, &env{e: e, db: db, schema: schema})
		})
	}
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.csv")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func rows(t *testing.T, v *env, table string) [][]string {
	t.Helper()
	p, err := query.FetchTable(context.Background(), v.db, v.e.drv, query.Request{Schema: v.schema, Table: table, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	out := make([][]string, len(p.Rows))
	for i, r := range p.Rows {
		for _, c := range r {
			if c.K == "null" {
				out[i] = append(out[i], "NULL")
			} else {
				out[i] = append(out[i], strings.TrimSpace(strings.ReplaceAll(fmtCell(c.V), "\x00", "")))
			}
		}
	}
	return out
}

func fmtCell(v any) string { return fmt.Sprint(v) }

func run(v *env, path, table string, o Options, m []Mapping, batch int, ro RunOptions, p Progress) (*Result, error) {
	return Run(context.Background(), v.db, v.e.drv, Request{Path: path, Options: o, Schema: v.schema, Table: table, Mapping: m, BatchSize: batch}, ro, p)
}

func TestImport(t *testing.T) {
	forEach(t, func(t *testing.T, v *env) {
		ctx := context.Background()
		tbl := v.table(t)
		// BOM, header in another case and order, quoted delimiters and
		// newlines, NULL marker, an ignored column.
		content := "\xef\xbb\xbfNAME,Id,ignored,Note,qty,data\r\n" +
			"\"a, \"\"b\"\"\",1,x,\"two\r\nlines\",\\N,0xdead\r\n" +
			"plain,2,y,,7,\r\n"
		path := writeFile(t, content)
		o := Options{Header: true, NullMarker: `\N`}
		p, err := PreviewFile(ctx, v.db, v.e.drv, path, o, v.schema, tbl)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(p.Headers, "|") != "NAME|Id|ignored|Note|qty|data" || len(p.Rows) != 2 || p.Rows[0][4] != nil ||
			*p.Rows[0][0] != `a, "b"` || *p.Rows[0][3] != "two\r\nlines" || len(p.Columns) != 5 {
			t.Fatalf("preview = %+v", p)
		}
		want := map[string]int{"name": 0, "id": 1, "note": 3, "qty": 4, "data": 5}
		if len(p.Mapping) != 5 {
			t.Fatalf("mapping = %+v", p.Mapping)
		}
		for _, m := range p.Mapping {
			if want[m.Column] != m.Source {
				t.Fatalf("mapping = %+v", p.Mapping)
			}
		}
		res, err := run(v, path, tbl, o, p.Mapping, 0, RunOptions{}, nil)
		if err != nil || res.Rows != 2 {
			t.Fatalf("res = %+v, %v", res, err)
		}
		got := rows(t, v, tbl)
		if got[0][1] != `a, "b"` || got[0][2] != "NULL" || got[0][3] != "two\r\nlines" || got[0][4] != "dead" ||
			got[1][2] != "7" || got[1][3] != "" {
			t.Fatalf("rows = %q", got)
		}

		// Empty-as-NULL, ';' delimiter, ' quote, skipped rows, no header.
		tbl2 := v.table(t)
		path = writeFile(t, "junk line\nmore junk\n5;'x;y';'it''s';\n")
		res, err = run(v, path, tbl2, Options{Delimiter: ";", Quote: "'", Skip: 2, EmptyAsNull: true},
			[]Mapping{{0, "id"}, {1, "name"}, {2, "note"}, {3, "qty"}}, 0, RunOptions{}, nil)
		if err != nil || res.Rows != 1 {
			t.Fatalf("res = %+v, %v", res, err)
		}
		if got := rows(t, v, tbl2); got[0][1] != "x;y" || got[0][3] != "it's" || got[0][2] != "NULL" {
			t.Fatalf("rows = %q", got)
		}
		p2, err := PreviewFile(ctx, v.db, v.e.drv, path, Options{Delimiter: ";", Quote: "'", Skip: 2}, v.schema, tbl2)
		if err != nil || strings.Join(p2.Headers, "|") != "Column 1|Column 2|Column 3|Column 4" || len(p2.Mapping) != 0 {
			t.Fatalf("preview = %+v, %v", p2, err)
		}
	})
}

func TestImportErrorsRollBack(t *testing.T) {
	forEach(t, func(t *testing.T, v *env) {
		tbl := v.table(t)
		m := []Mapping{{0, "id"}, {1, "name"}, {2, "qty"}}
		// Line 6 (second batch of 2) has a NULL name (NOT NULL column).
		content := "id,name,qty\n1,a,1\n2,b,2\n3,c,3\n4,d,4\n5,,5\n6,f,6\n"
		path := writeFile(t, content)
		var progress []int64
		_, err := run(v, path, tbl, Options{Header: true, EmptyAsNull: true}, m, 2, RunOptions{}, func(n int64) { progress = append(progress, n) })
		var re *RowError
		if !errors.As(err, &re) || re.Line != 6 || re.Column != "name" {
			t.Fatalf("err = %#v", err)
		}
		if !strings.Contains(err.Error(), "line 6, column name") || !strings.Contains(err.Error(), "Nothing was imported") {
			t.Fatalf("message = %v", err)
		}
		if got := rows(t, v, tbl); len(got) != 0 {
			t.Fatalf("rows after rollback = %q", got)
		}
		if len(progress) == 0 || progress[0] != 2 {
			t.Fatalf("progress = %v", progress)
		}
		// A value the column type rejects (SQLite stores text in an INT
		// column, so it has no such error).
		if v.e.drv.Kind() != driver.SQLite {
			_, err = run(v, writeFile(t, "id,name,qty\n1,a,notanumber\n"), tbl, Options{Header: true}, m, 0, RunOptions{}, nil)
			if !errors.As(err, &re) || re.Line != 2 || re.Column != "qty" {
				t.Fatalf("err = %#v", err)
			}
		}

		// A NOT NULL violation in the first batch names the line and column.
		tbl = v.table(t)
		path = writeFile(t, "id,name\n1,a\n2,\\N\n")
		_, err = run(v, path, tbl, Options{Header: true, NullMarker: `\N`}, []Mapping{{0, "id"}, {1, "name"}}, 0, RunOptions{}, nil)
		if !errors.As(err, &re) || re.Line != 3 || re.Column != "name" {
			t.Fatalf("err = %#v", err)
		}
		if got := rows(t, v, tbl); len(got) != 0 {
			t.Fatalf("rows after rollback = %q", got)
		}
		// A duplicate key in the same batch.
		path = writeFile(t, "id,name\n1,a\n1,b\n")
		_, err = run(v, path, tbl, Options{Header: true}, []Mapping{{0, "id"}, {1, "name"}}, 0, RunOptions{}, nil)
		if !errors.As(err, &re) || re.Line != 3 {
			t.Fatalf("err = %#v", err)
		}
		// Client-side errors: bad hex, short record, bad CSV, bad UTF-8.
		cases := []struct {
			content string
			m       []Mapping
			line    int
			want    string
		}{
			{"id,name,data\n1,a,zz\n", []Mapping{{0, "id"}, {1, "name"}, {2, "data"}}, 2, "hex"},
			{"id,name\n1\n", []Mapping{{0, "id"}, {1, "name"}}, 2, "needs field 2"},
			{"id,name\n1,\"a\"b\n", []Mapping{{0, "id"}, {1, "name"}}, 2, "CSV syntax"},
			{"id,name\n1,\xc3\x28\n", []Mapping{{0, "id"}, {1, "name"}}, 2, "UTF-8"},
			{"id,name\n1,\xff\n", []Mapping{{0, "id"}, {1, "name"}}, 2, "UTF-8"},
		}
		for _, tc := range cases {
			_, err := run(v, writeFile(t, tc.content), tbl, Options{Header: true}, tc.m, 0, RunOptions{}, nil)
			if !errors.As(err, &re) || re.Line != tc.line || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%q: err = %v", tc.content, err)
			}
		}
		if got := rows(t, v, tbl); len(got) != 0 {
			t.Fatalf("rows = %q", got)
		}
	})
}

func TestImportGuards(t *testing.T) {
	v := &env{e: engines[0]}
	v.db = testutil.OpenDriver(t, v.e.drv, testutil.SQLiteTemp(t), false)
	v.schema = "main"
	tbl := v.table(t)
	v.exec(t, "CREATE VIEW imp_view AS SELECT id FROM "+tbl)
	path := writeFile(t, "1,a\n2,b\n")
	m := []Mapping{{0, "id"}, {1, "name"}}

	if _, err := run(v, path, tbl, Options{}, m, 0, RunOptions{ReadOnly: true}, nil); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("readonly: %v", err)
	}
	if _, err := run(v, path, "imp_view", Options{}, m, 0, RunOptions{}, nil); !errors.Is(err, edits.ErrNotATable) {
		t.Fatalf("view: %v", err)
	}
	for _, bad := range [][]Mapping{nil, {{0, "nope"}}, {{0, "id"}, {1, "id"}}, {{-1, "id"}}} {
		if _, err := run(v, path, tbl, Options{}, bad, 0, RunOptions{}, nil); !errors.Is(err, ErrMapping) {
			t.Fatalf("%v: %v", bad, err)
		}
	}
	for _, o := range []Options{{Delimiter: "ab"}, {Quote: "é"}, {Delimiter: ";", Quote: ";"}, {Encoding: "latin1"}, {Skip: -1}} {
		if _, err := run(v, path, tbl, o, m, 0, RunOptions{}, nil); !errors.Is(err, ErrOptions) {
			t.Fatalf("%+v: %v", o, err)
		}
		if _, err := PreviewFile(context.Background(), v.db, v.e.drv, path, o, "main", tbl); !errors.Is(err, ErrOptions) {
			t.Fatalf("preview %+v: %v", o, err)
		}
	}
	if _, err := run(v, filepath.Join(t.TempDir(), "missing.csv"), tbl, Options{}, m, 0, RunOptions{}, nil); err == nil {
		t.Fatal("missing file")
	}
	if _, err := PreviewFile(context.Background(), v.db, v.e.drv, filepath.Join(t.TempDir(), "missing.csv"), Options{}, "main", tbl); err == nil {
		t.Fatal("missing file")
	}
	if _, err := PreviewFile(context.Background(), v.db, v.e.drv, path, Options{}, "main", "nope"); err == nil {
		t.Fatal("unknown table")
	}

	// Cancel rolls back: cancel after the first batch.
	ctx, cancel := context.WithCancel(context.Background())
	big := writeFile(t, strings.Repeat("x\n", 0)+func() string {
		var b strings.Builder
		for i := 1; i <= 50; i++ {
			b.WriteString(strings.Join([]string{itoa(i), "n"}, ","))
			b.WriteString("\n")
		}
		return b.String()
	}())
	_, err := Run(ctx, v.db, v.e.drv, Request{Path: big, Schema: "main", Table: tbl, Mapping: m, BatchSize: 10}, RunOptions{}, func(n int64) {
		if n >= 10 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if got := rows(t, v, tbl); len(got) != 0 {
		t.Fatalf("rows after cancel = %q", got)
	}
	// Tab delimiter as "\t" text, the "utf-8" encoding name, skip past
	// the end, header-only file.
	if _, err := run(v, writeFile(t, "1\ta\n"), tbl, Options{Delimiter: `\t`, Encoding: "UTF-8"}, m, 0, RunOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	p, err := PreviewFile(context.Background(), v.db, v.e.drv, writeFile(t, "a,b\n"), Options{Skip: 5, Header: true}, "main", tbl)
	if err != nil || len(p.Rows) != 0 || len(p.Headers) != 0 || p.Mapping == nil {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	p, err = PreviewFile(context.Background(), v.db, v.e.drv, writeFile(t, "id,name\n"), Options{Header: true}, "main", tbl)
	if err != nil || len(p.Rows) != 0 || len(p.Mapping) != 2 {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	if _, err := PreviewFile(context.Background(), v.db, v.e.drv, writeFile(t, "a,\"b\n"), Options{Header: true}, "main", tbl); err == nil {
		t.Fatal("bad header accepted")
	}
	re := &RowError{Line: 3, Message: "x"}
	if re.Error() != "line 3: x. Nothing was imported." || re.Unwrap() != nil {
		t.Fatal(re.Error())
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}
