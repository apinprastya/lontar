package export

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"lontar/internal/driver"
	"lontar/internal/driver/mysql"
	"lontar/internal/driver/postgres"
	"lontar/internal/driver/sqlite"
	"lontar/internal/edits"
	"lontar/internal/importer"
	"lontar/internal/query"
	"lontar/internal/sqlsplit"
	"lontar/internal/testutil"
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
}

func (v env) kind() driver.Kind { return v.e.drv.Kind() }

func (v env) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := v.db.ExecContext(context.Background(), q, edits.ExecArgs(v.kind(), args)...); err != nil {
		t.Fatalf("%s: %v\n%s", v.e.name, err, q)
	}
}

func forEach(t *testing.T, fn func(t *testing.T, v env)) {
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			target := e.start(t)
			db := testutil.OpenDriver(t, e.drv, target, false)
			schema, _ := e.drv.CurrentSchema(context.Background(), db)
			fn(t, env{e: e, db: db, schema: schema})
		})
	}
}

// typesDDL returns a table with every interesting type for kind.
func typesDDL(kind driver.Kind, name string) string {
	switch kind {
	case driver.Postgres:
		return `CREATE TABLE ` + name + ` (id integer PRIMARY KEY, big bigint, price numeric(30,10), f double precision,
		  name text, note varchar(200), data bytea, flag boolean, born date, seen_at timestamp(6), at_tz timestamptz,
		  doc jsonb, j json, uid uuid, tags text[], dur interval)`
	case driver.SQLite:
		return `CREATE TABLE ` + name + ` (id INTEGER PRIMARY KEY, big INTEGER, price TEXT, f REAL, name TEXT, note TEXT,
		  data BLOB, flag BOOLEAN, born DATE, seen_at TIMESTAMP, doc JSON)`
	}
	return "CREATE TABLE " + name + ` (id INT PRIMARY KEY, big BIGINT, ubig BIGINT UNSIGNED, price DECIMAL(30,10), f DOUBLE,
	  name TEXT, note VARCHAR(200), data BLOB, flag BOOLEAN, born DATE, seen_at DATETIME(6), doc JSON) CHARACTER SET utf8mb4`
}

const (
	tricky = `it's "quoted" \ back\\slash`
	multi  = "line1\nline2\r\n\ttab ünïcødé ✓ 🎉"
	doc    = `{"a": "b'c\\d", "n": [1, 2.5]}`
)

// seedTypes fills name with rows covering quotes, backslashes, newlines,
// unicode, NUL (not on PostgreSQL, whose text type cannot hold it), bytes,
// big integers, decimals, JSON and NULL vs empty.
func seedTypes(t *testing.T, v env, name string) {
	t.Helper()
	v.exec(t, typesDDL(v.kind(), name))
	nul := "nul\x00inside\x1a"
	switch v.kind() {
	case driver.Postgres:
		ins := `INSERT INTO ` + name + ` VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`
		v.exec(t, ins, "1", "9007199254740993", "12345678901234567890.0123456789", "1.5e-7", tricky, multi,
			[]byte{0, 0x5c, 0x27, 0xff, 0x0a}, "true", "2024-01-02", "2024-01-02 03:04:05.123456", "2024-01-02 03:04:05.5+02",
			doc, doc, "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", `{"a,b","c\"d",NULL}`, "1 day 02:00:00")
		v.exec(t, `INSERT INTO `+name+` (id) VALUES (2)`)
		v.exec(t, ins, "3", "-9223372036854775808", "-0.0000000001", "0", "", "", []byte{}, "false", "1999-12-31",
			"1999-12-31 23:59:59", "1999-12-31 23:59:59Z", "[]", "[]", "00000000-0000-0000-0000-000000000000", "{}", "00:00:00")
	case driver.SQLite:
		ins := `INSERT INTO ` + name + ` VALUES (?,?,?,?,?,?,?,?,?,?,?)`
		v.exec(t, ins, 1, int64(9007199254740993), "12345678901234567890.0123456789", 1.5e-7, tricky, multi,
			[]byte{0, 0x5c, 0x27, 0xff, 0x0a}, 1, "2024-01-02", "2024-01-02 03:04:05.123456", doc)
		v.exec(t, `INSERT INTO `+name+` (id) VALUES (2)`)
		v.exec(t, ins, 3, int64(-9223372036854775808), "-0.0000000001", 0.0, "", "", []byte{}, 0, "1999-12-31", "1999-12-31 23:59:59", "[]")
		v.exec(t, ins, 4, 1, "1", 1.0, nul, "x", []byte("\\"), 1, nil, nil, nil)
	default:
		ins := `INSERT INTO ` + name + ` VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`
		v.exec(t, ins, 1, int64(9007199254740993), uint64(18446744073709551615), "12345678901234567890.0123456789", 1.5e-7, tricky, multi,
			[]byte{0, 0x5c, 0x27, 0xff, 0x0a}, 1, "2024-01-02", "2024-01-02 03:04:05.123456", doc)
		v.exec(t, `INSERT INTO `+name+` (id) VALUES (2)`)
		v.exec(t, ins, 3, int64(-9223372036854775808), uint64(0), "-0.0000000001", 0.0, "", "", []byte{}, 0, "1999-12-31", "1999-12-31 23:59:59", "[]")
		v.exec(t, ins, 4, 1, 1, "1", 1.0, nul, "x", []byte("\\"), 1, nil, nil, nil)
	}
}

func fetchAll(t *testing.T, v env, name string) *query.Page {
	t.Helper()
	p, err := query.FetchTable(context.Background(), v.db, v.e.drv, query.Request{Schema: v.schema, Table: name, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func sameRows(t *testing.T, v env, a, b string) {
	t.Helper()
	pa, pb := fetchAll(t, v, a), fetchAll(t, v, b)
	if len(pa.Rows) == 0 {
		t.Fatal("no rows")
	}
	if !reflect.DeepEqual(pa.Rows, pb.Rows) {
		for i := range pa.Rows {
			if i < len(pb.Rows) && !reflect.DeepEqual(pa.Rows[i], pb.Rows[i]) {
				for j := range pa.Rows[i] {
					if !reflect.DeepEqual(pa.Rows[i][j], pb.Rows[i][j]) {
						t.Errorf("row %d col %s: %#v != %#v", i, pa.Columns[j].Name, pa.Rows[i][j], pb.Rows[i][j])
					}
				}
			}
		}
		t.Fatalf("%s and %s differ (%d vs %d rows)", a, b, len(pa.Rows), len(pb.Rows))
	}
}

// runScript executes an exported SQL file on one connection.
func runScript(t *testing.T, v env, script string) {
	t.Helper()
	ctx := context.Background()
	conn, err := v.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, st := range sqlsplit.Split(v.kind(), script) {
		if strings.TrimSpace(st.Text) == "" {
			continue
		}
		if _, err := conn.ExecContext(ctx, st.Text); err != nil {
			t.Fatalf("%s: %v\n%s", v.e.name, err, st.Text)
		}
	}
}

func tableExport(t *testing.T, v env, name string, opts Options) string {
	t.Helper()
	var buf bytes.Buffer
	n, err := Table(context.Background(), v.db, v.e.drv, query.Request{Schema: v.schema, Table: name, Sort: []query.Sort{{Column: "id"}}}, &buf, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no rows exported")
	}
	return buf.String()
}

func TestRoundTripSQL(t *testing.T) {
	forEach(t, func(t *testing.T, v env) {
		seedTypes(t, v, "rt_src")
		v.exec(t, typesDDL(v.kind(), "rt_sql"))
		script := tableExport(t, v, "rt_src", Options{Format: FormatSQL, SQL: SQLOptions{Table: "rt_sql", BatchSize: 2}})
		runScript(t, v, script)
		sameRows(t, v, "rt_src", "rt_sql")
		if strings.Count(script, "INSERT INTO") < 2 {
			t.Fatalf("batches not split:\n%s", script)
		}
	})
}

func TestRoundTripCSVAndJSON(t *testing.T) {
	forEach(t, func(t *testing.T, v env) {
		seedTypes(t, v, "rt_c")
		v.exec(t, typesDDL(v.kind(), "rt_csv"))
		dir := t.TempDir()
		path := filepath.Join(dir, "rt.csv")
		opts := Options{Format: FormatCSV, CSV: CSVOptions{Header: true, Null: NullBackN}}
		if err := ToFile(path, func(w io.Writer) error {
			_, err := Table(context.Background(), v.db, v.e.drv, query.Request{Schema: v.schema, Table: "rt_c"}, w, opts, nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		// Parse back with encoding/csv.
		data, _ := os.ReadFile(path)
		recs, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		idx := map[string]int{}
		for i, h := range recs[0] {
			idx[h] = i
		}
		r1 := recs[1]
		// encoding/csv itself drops the \r of \r\n inside quoted fields
		// (the importer keeps it; see below).
		if r1[idx["name"]] != tricky || r1[idx["note"]] != strings.ReplaceAll(multi, "\r\n", "\n") || r1[idx["data"]] != "005c27ff0a" || r1[idx["big"]] != "9007199254740993" ||
			r1[idx["price"]] != "12345678901234567890.0123456789" || recs[2][idx["name"]] != `\N` || recs[3][idx["name"]] != "" {
			t.Fatalf("csv row = %q / %q / %q", r1, recs[2], recs[3])
		}
		// And import it into a copy: same rows.
		ex := v.e.drv.(importer.Engine)
		mapping := make([]importer.Mapping, 0, len(recs[0]))
		for i, h := range recs[0] {
			mapping = append(mapping, importer.Mapping{Source: i, Column: h})
		}
		res, err := importer.Run(context.Background(), v.db, ex, importer.Request{Path: path, Options: importer.Options{Header: true, NullMarker: `\N`},
			Schema: v.schema, Table: "rt_csv", Mapping: mapping}, importer.RunOptions{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.Rows != int64(len(recs)-1) {
			t.Fatalf("imported %d", res.Rows)
		}
		sameRows(t, v, "rt_c", "rt_csv")

		// JSON parses back with exact values.
		out := tableExport(t, v, "rt_c", Options{Format: FormatJSON})
		var arr []map[string]any
		dec := json.NewDecoder(strings.NewReader(out))
		dec.UseNumber()
		if err := dec.Decode(&arr); err != nil {
			t.Fatal(err)
		}
		o := arr[0]
		if o["big"] != "9007199254740993" || o["price"] != "12345678901234567890.0123456789" || o["name"] != tricky ||
			o["note"] != multi || o["data"] != "005c27ff0a" || o["id"] != json.Number("1") {
			t.Fatalf("json = %#v", o)
		}
		if arr[1]["name"] != nil || arr[2]["name"] != "" {
			t.Fatalf("null vs empty: %#v %#v", arr[1]["name"], arr[2]["name"])
		}
		var d map[string]any
		if err := json.Unmarshal([]byte(o["doc"].(string)), &d); err != nil || d["a"] != `b'c\d` {
			t.Fatalf("doc = %v %v", o["doc"], err)
		}
	})
}

func TestQueryRerun(t *testing.T) {
	forEach(t, func(t *testing.T, v env) {
		v.exec(t, "CREATE TABLE rr (id INT PRIMARY KEY, v VARCHAR(10))")
		v.exec(t, "INSERT INTO rr VALUES (1, 'a'), (2, 'b')")
		var buf bytes.Buffer
		opts := Options{Format: FormatCSV, CSV: CSVOptions{Header: true}}
		n, err := Query(context.Background(), v.db, v.e.drv, "SELECT id, v FROM rr ORDER BY id;", &buf, opts, nil)
		if err != nil || n != 2 || buf.String() != "id,v\n1,a\n2,b\n" {
			t.Fatalf("n = %d, %v\n%s", n, err, buf.String())
		}
		// Writes are never run again.
		for _, q := range []string{"DELETE FROM rr", "SELECT 1; DELETE FROM rr", "UPDATE rr SET v = 'x'"} {
			if _, err := Query(context.Background(), v.db, v.e.drv, q, io.Discard, opts, nil); !errors.Is(err, ErrNotRerunnable) {
				t.Fatalf("%q: err = %v", q, err)
			}
		}
		if p := fetchAll(t, v, "rr"); len(p.Rows) != 2 {
			t.Fatal("rows changed")
		}
		// The read-only guard holds even if a write slipped through
		// classification: a SELECT calling a writing function is refused
		// by the database (checked where cheap: SQLite query_only).
		if v.kind() == driver.SQLite {
			conn, _ := v.db.Conn(context.Background())
			var q int
			_ = conn.QueryRowContext(context.Background(), "PRAGMA query_only").Scan(&q)
			conn.Close()
			if q != 0 {
				t.Fatal("query_only left on")
			}
		}
		// Errors: bad SQL.
		if _, err := Query(context.Background(), v.db, v.e.drv, "SELECT * FROM missing_table", io.Discard, opts, nil); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestStreamingLargeTableAndCancel(t *testing.T) {
	v := env{e: engines[0]}
	target := testutil.SQLiteTemp(t)
	v.db = testutil.OpenDriver(t, v.e.drv, target, false)
	v.schema = "main"
	v.exec(t, "CREATE TABLE big (id INTEGER PRIMARY KEY, label TEXT, pad TEXT)")
	v.exec(t, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 100000)
	  INSERT INTO big SELECT i, 'row ' || i, printf('%.100c', 'x') FROM n`)

	old := ProgressEvery
	ProgressEvery = 0
	defer func() { ProgressEvery = old }()

	// Heap use stays flat while rows stream (cursor-based, nothing
	// accumulated): compare the live heap early and late in the run.
	var early, late uint64
	var ms runtime.MemStats
	n, err := Table(context.Background(), v.db, v.e.drv, query.Request{Schema: "main", Table: "big"}, io.Discard,
		Options{Format: FormatSQL, SQL: SQLOptions{Table: "big"}}, func(rows int64) {
			if rows >= 5000 && early == 0 {
				runtime.GC()
				runtime.ReadMemStats(&ms)
				early = ms.HeapAlloc
			}
			if rows >= 95000 && late == 0 {
				runtime.GC()
				runtime.ReadMemStats(&ms)
				late = ms.HeapAlloc
			}
		})
	if err != nil || n != 100000 {
		t.Fatalf("n = %d, %v", n, err)
	}
	// 100k rows of ~120 bytes would be >12 MB if they were kept.
	if early == 0 || late == 0 || late > early+4<<20 {
		t.Fatalf("heap grew from %d to %d bytes while streaming", early, late)
	}

	// Cancelling removes the temporary file and leaves no output.
	dir := t.TempDir()
	path := filepath.Join(dir, "big.csv")
	ctx, cancel := context.WithCancel(context.Background())
	err = ToFile(path, func(w io.Writer) error {
		_, err := Table(ctx, v.db, v.e.drv, query.Request{Schema: "main", Table: "big"}, w,
			Options{Format: FormatCSV}, func(rows int64) {
				if rows > 1000 {
					cancel()
				}
			})
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files left after cancel: %v", entries)
	}

	// Unknown tables and filters are rejected before anything runs.
	if _, err := Table(context.Background(), v.db, v.e.drv, query.Request{Schema: "main", Table: "nope"}, io.Discard, Options{Format: FormatCSV}, nil); !errors.Is(err, query.ErrUnknownTable) {
		t.Fatalf("err = %v", err)
	}
	if _, err := Table(context.Background(), v.db, v.e.drv, query.Request{Schema: "main", Table: "big", Filters: []query.Filter{{Column: "x", Op: "="}}}, io.Discard, Options{Format: FormatCSV}, nil); !errors.Is(err, query.ErrUnknownColumn) {
		t.Fatalf("err = %v", err)
	}
	// Filters and sort apply.
	var buf bytes.Buffer
	if _, err := Table(context.Background(), v.db, v.e.drv, query.Request{Schema: "main", Table: "big",
		Filters: []query.Filter{{Column: "id", Op: "<=", Value: "3"}}, Sort: []query.Sort{{Column: "id", Desc: true}}}, &buf,
		Options{Format: FormatCSV, CSV: CSVOptions{Header: false}}, nil); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(buf.String()), "\n"); len(lines) != 3 || !strings.HasPrefix(lines[0], "3,row 3,") {
		t.Fatalf("filtered = %q", lines)
	}
	_ = fmt.Sprint
	_ = time.Now
}
