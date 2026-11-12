package export

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbedit/internal/driver"
	"dbedit/internal/values"
)

var (
	pgD   = driver.PostgresDialect{}
	myD   = driver.MySQLDialect{}
	liteD = driver.SQLiteDialect{}
)

func str(s string) Value { return Value{Kind: values.Str, Text: s} }
func num(s string, safe bool) Value {
	return Value{Kind: values.Num, Text: s, Safe: safe}
}

func TestLiteral(t *testing.T) {
	cases := []struct {
		kind driver.Kind
		v    Value
		want string
	}{
		{driver.Postgres, Null, "NULL"},
		{driver.Postgres, str("it's"), "'it''s'"},
		{driver.Postgres, str(`a\b`), `E'a\\b'`},
		{driver.Postgres, str("l1\nl2\t\r\x01'"), `E'l1\nl2\t\r\x01\''`},
		{driver.Postgres, str("ünï ✓"), "'ünï ✓'"},
		{driver.Postgres, Value{Kind: values.Bool, Bool: true}, "TRUE"},
		{driver.Postgres, Value{Kind: values.Bool}, "FALSE"},
		{driver.Postgres, Value{Kind: values.Bytes, Bytes: []byte{0, 0x5c, 0xff}}, "decode('005cff', 'hex')"},
		{driver.Postgres, num("9007199254740993", false), "9007199254740993"},
		{driver.Postgres, num("-12.3400", false), "-12.3400"},
		{driver.Postgres, num("NaN", false), "'NaN'"},
		{driver.Postgres, num("1e999", false), "1e999"},
		{driver.Postgres, Value{Kind: values.JSON, Text: `{"a": "b'c"}`}, `'{"a": "b''c"}'`},
		{driver.MySQL, str("it's"), "'it''s'"},
		{driver.MySQL, str(`a\b`), "_utf8mb4 X'615c62'"},
		{driver.MySQL, str("nul\x00"), "_utf8mb4 X'6e756c00'"},
		{driver.MySQL, str("bad\xff\\"), "X'626164ff5c'"},
		{driver.MySQL, str("line\nbreak"), "'line\nbreak'"},
		{driver.MariaDB, Value{Kind: values.Bool, Bool: true}, "1"},
		{driver.MySQL, Value{Kind: values.Bool}, "0"},
		{driver.MySQL, Value{Kind: values.Bytes, Bytes: []byte{1, 2}}, "X'0102'"},
		{driver.SQLite, str("o'k"), "'o''k'"},
		{driver.SQLite, str("a\x00b"), "CAST(X'610062' AS TEXT)"},
		{driver.SQLite, str(`back\slash`), `'back\slash'`},
		{driver.SQLite, Value{Kind: values.Bytes, Bytes: []byte{}}, "X''"},
	}
	for _, tc := range cases {
		if got := Literal(tc.kind, tc.v); got != tc.want {
			t.Errorf("%s %+v = %s, want %s", tc.kind, tc.v, got, tc.want)
		}
	}
	for _, s := range []string{"", "0x10", "1_000", "Infinity", "inf", "abc"} {
		if plainNumber(s) {
			t.Errorf("%q is not a plain number", s)
		}
	}
}

func cols(names ...string) []Column {
	out := make([]Column, len(names))
	for i, n := range names {
		out[i] = Column{Name: n}
	}
	return out
}

type sliceSource struct {
	cols []Column
	rows [][]Value
	i    int
	err  error
}

func (s *sliceSource) Columns() []Column { return s.cols }
func (s *sliceSource) Next() ([]Value, error) {
	if s.i >= len(s.rows) {
		if s.err != nil {
			return nil, s.err
		}
		return nil, io.EOF
	}
	s.i++
	return s.rows[s.i-1], nil
}

func render(t *testing.T, kind driver.Kind, d driver.Dialect, opts Options, src Source) string {
	t.Helper()
	var buf bytes.Buffer
	if _, err := Write(context.Background(), src, &buf, kind, d, opts, nil); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func sample() *sliceSource {
	return &sliceSource{cols: cols("id", "name", "note", "data", "ok"), rows: [][]Value{
		{num("1", true), str(`say "hi", ok`), str("multi\nline"), {Kind: values.Bytes, Bytes: []byte{0xde, 0xad}}, {Kind: values.Bool, Bool: true}},
		{num("2", true), str(""), Null, Null, {Kind: values.Bool}},
		{num("3", true), str(`\N`), str(" pad "), Null, Null},
	}}
}

func TestCSV(t *testing.T) {
	out := render(t, driver.Postgres, pgD, Options{Format: FormatCSV, CSV: CSVOptions{Header: true, Null: NullBackN}}, sample())
	want := "id,name,note,data,ok\n" +
		"1,\"say \"\"hi\"\", ok\",\"multi\nline\",dead,true\n" +
		"2,,\\N,\\N,false\n" +
		"3,\"\\N\",\" pad \",\\N,\\N\n"
	if out != want {
		t.Fatalf("csv =\n%q\nwant\n%q", out, want)
	}
	// encoding/csv reads it back.
	recs, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil || recs[1][1] != `say "hi", ok` || recs[1][2] != "multi\nline" {
		t.Fatalf("parse back = %q, %v", recs, err)
	}

	// Empty NULL: an empty string is quoted to stay distinct.
	out = render(t, driver.Postgres, pgD, Options{Format: FormatCSV, CSV: CSVOptions{Null: NullEmpty}}, sample())
	if !strings.Contains(out, "2,\"\",,,false\n") || strings.HasPrefix(out, "id") {
		t.Fatalf("empty null =\n%s", out)
	}
	// Custom NULL text, ';' delimiter, ' quote, CRLF and BOM.
	out = render(t, driver.Postgres, pgD, Options{Format: FormatCSV, CSV: CSVOptions{Delimiter: ";", Quote: "'", Null: NullCustom, NullText: "NULL", CRLF: true, BOM: true, Header: true}},
		&sliceSource{cols: cols("a", "b"), rows: [][]Value{{str("it's;x"), Null}, {str("NULL"), str("x")}}})
	want = "\xef\xbb\xbfa;b\r\n'it''s;x';NULL\r\n'NULL';x\r\n"
	if out != want {
		t.Fatalf("custom =\n%q\nwant\n%q", out, want)
	}
}

func TestJSON(t *testing.T) {
	src := &sliceSource{cols: cols("id", "big", "price", "f", "data", "doc", "n", "id"), rows: [][]Value{
		{num("1", true), num("9007199254740993", false), num("12.3400", false), num("1.5", true),
			{Kind: values.Bytes, Bytes: []byte{0, 255}}, {Kind: values.JSON, Text: `{"a":1}`}, Null, {Kind: values.Bool, Bool: true}},
	}}
	out := render(t, driver.Postgres, pgD, Options{Format: FormatJSON}, src)
	var arr []map[string]any
	dec := json.NewDecoder(strings.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(&arr); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	o := arr[0]
	if o["id"] != json.Number("1") || o["big"] != "9007199254740993" || o["price"] != "12.3400" || o["f"] != json.Number("1.5") ||
		o["data"] != "00ff" || o["doc"] != `{"a":1}` || o["n"] != nil || o["id_2"] != true {
		t.Fatalf("object = %#v", o)
	}
	src.i = 0
	out = render(t, driver.Postgres, pgD, Options{Format: FormatJSON, JSON: JSONOptions{NDJSON: true, NumbersAsStrings: true, Bytes: BytesBase64}}, src)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"id":"1"`) || !strings.Contains(lines[0], `"data":"AP8="`) {
		t.Fatalf("ndjson = %q", out)
	}
	// Empty result.
	if out := render(t, driver.Postgres, pgD, Options{Format: FormatJSON}, &sliceSource{cols: cols("a")}); out != "[]\n" {
		t.Fatalf("empty = %q", out)
	}
	if out := render(t, driver.Postgres, pgD, Options{Format: FormatJSON, JSON: JSONOptions{NDJSON: true}}, &sliceSource{cols: cols("a")}); out != "" {
		t.Fatalf("empty ndjson = %q", out)
	}
}

func TestSQLWriter(t *testing.T) {
	src := sample()
	out := render(t, driver.MySQL, myD, Options{Format: FormatSQL, SQL: SQLOptions{Table: "t", BatchSize: 2, CreateTable: true,
		DDL: "CREATE TABLE `t` (id int)"}}, src)
	for _, want := range []string{
		"SET NAMES utf8mb4;\n",
		"CREATE TABLE `t` (id int);\n",
		"INSERT INTO `t` (`id`, `name`, `note`, `data`, `ok`) VALUES\n  (1, 'say \"hi\", ok', 'multi\nline', X'dead', 1),\n  (2, '', NULL, NULL, 0);\n",
		"INSERT INTO `t` (`id`, `name`, `note`, `data`, `ok`) VALUES\n  (3, _utf8mb4 X'5c4e', ' pad ', NULL, NULL);\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
	src.i = 0
	out = render(t, driver.Postgres, pgD, Options{Format: FormatSQL, SQL: SQLOptions{Table: "t", Schema: "s", OverridingSystemValue: true, BatchSize: 50000}}, src)
	if !strings.Contains(out, `INSERT INTO "s"."t" ("id", "name", "note", "data", "ok") OVERRIDING SYSTEM VALUE VALUES`) ||
		strings.Count(out, "INSERT") != 1 || strings.Contains(out, "SET NAMES") {
		t.Fatalf("pg =\n%s", out)
	}
	// No rows: no INSERT.
	out = render(t, driver.SQLite, liteD, Options{Format: FormatSQL, SQL: SQLOptions{Table: "t"}}, &sliceSource{cols: cols("a")})
	if strings.Contains(out, "INSERT") {
		t.Fatalf("empty = %s", out)
	}
}

func TestWriterOptionErrors(t *testing.T) {
	bad := []Options{
		{Format: "xml"},
		{Format: FormatCSV, CSV: CSVOptions{Delimiter: "ab"}},
		{Format: FormatCSV, CSV: CSVOptions{Quote: "\n"}},
		{Format: FormatCSV, CSV: CSVOptions{Delimiter: "'", Quote: "'"}},
		{Format: FormatCSV, CSV: CSVOptions{Null: "maybe"}},
		{Format: FormatJSON, JSON: JSONOptions{Bytes: "base32"}},
		{Format: FormatSQL},
	}
	for _, o := range bad {
		if _, err := NewWriter(io.Discard, driver.Postgres, pgD, o); !errors.Is(err, ErrOptions) {
			t.Errorf("%+v: err = %v", o, err)
		}
	}
}

type failWriter struct{ after int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.after <= 0 {
		return 0, errors.New("disk full")
	}
	f.after--
	return len(p), nil
}

func TestWriteErrors(t *testing.T) {
	ctx := context.Background()
	opts := Options{Format: FormatCSV, CSV: CSVOptions{Header: true, BOM: true}}
	// Source error.
	src := sample()
	src.err = errors.New("network")
	if _, err := Write(ctx, src, io.Discard, driver.Postgres, pgD, opts, nil); err == nil || err.Error() != "network" {
		t.Fatalf("err = %v", err)
	}
	// Option error.
	if _, err := Write(ctx, sample(), io.Discard, driver.Postgres, pgD, Options{}, nil); !errors.Is(err, ErrOptions) {
		t.Fatal(err)
	}
	// Flush error.
	if _, err := Write(ctx, sample(), &failWriter{}, driver.Postgres, pgD, opts, nil); err == nil {
		t.Fatal("expected write error")
	}
	// Cancelled.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Write(cctx, sample(), io.Discard, driver.Postgres, pgD, opts, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Progress gets the final count.
	var got int64
	n, err := Write(ctx, sample(), io.Discard, driver.Postgres, pgD, opts, func(r int64) { got = r })
	if err != nil || n != 3 || got != 3 {
		t.Fatalf("n = %d, progress = %d, %v", n, got, err)
	}
	// Writers propagate write errors directly (unbuffered).
	for _, o := range []Options{
		{Format: FormatCSV, CSV: CSVOptions{BOM: true}},
		{Format: FormatCSV, CSV: CSVOptions{Header: true}},
		{Format: FormatJSON},
		{Format: FormatSQL, SQL: SQLOptions{Table: "t"}},
	} {
		w, _ := NewWriter(&failWriter{}, driver.Postgres, pgD, o)
		if err := w.Begin(cols("a")); err == nil && o.Format != FormatJSON {
			if o.Format == FormatSQL {
				continue
			}
			t.Errorf("%s: Begin should fail", o.Format)
		}
	}
	w, _ := NewWriter(&failWriter{after: 1}, driver.Postgres, pgD, Options{Format: FormatJSON})
	_ = w.Begin(cols("a"))
	if err := w.Row([]Value{Null}); err == nil {
		t.Fatal("json row should fail")
	}
	if err := w.End(); err == nil {
		t.Fatal("json end should fail")
	}
}

func TestFromCellAndDriver(t *testing.T) {
	cases := []struct {
		c    values.Cell
		want Value
	}{
		{values.Cell{K: values.Null}, Null},
		{values.Cell{K: values.Bool, V: true}, Value{Kind: values.Bool, Bool: true}},
		{values.Cell{K: values.Num, V: float64(42)}, num("42", true)},
		{values.Cell{K: values.Num, V: 1.25}, num("1.25", true)},
		{values.Cell{K: values.Num, V: "12.30"}, num("12.30", false)},
		{values.Cell{K: values.Num, V: int64(7)}, num("7", true)},
		{values.Cell{K: values.Num, V: uint64(1 << 60)}, num("1152921504606846976", false)},
		{values.Cell{K: values.Num, V: json.Number("9")}, num("9", true)},
		{values.Cell{K: values.Num, V: int32(3)}, num("3", true)},
		{values.Cell{K: values.Num, V: 5}, num("5", true)},
		{values.Cell{K: values.Bytes, V: "00ff"}, Value{Kind: values.Bytes, Bytes: []byte{0, 255}}},
		{values.Cell{K: values.Time, V: "2024-01-02"}, Value{Kind: values.Time, Text: "2024-01-02"}},
	}
	for _, tc := range cases {
		got, err := FromCell(tc.c)
		if err != nil || got.Kind != tc.want.Kind || got.Text != tc.want.Text || got.Safe != tc.want.Safe || got.Bool != tc.want.Bool || !bytes.Equal(got.Bytes, tc.want.Bytes) {
			t.Errorf("%+v → %+v, %v", tc.c, got, err)
		}
	}
	for _, c := range []values.Cell{{K: values.Str, V: "x", Trunc: true}, {K: values.Bytes, V: "zz"}, {K: "weird"}, {K: values.Num, V: []int{}}} {
		if _, err := FromCell(c); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
	ts := time.Date(2024, 1, 2, 3, 4, 5, 600000000, time.FixedZone("x", 3600))
	for cat, want := range map[values.Category]string{
		values.CatDate: "2024-01-02", values.CatTimestamp: "2024-01-02 03:04:05.6", values.CatTime: "03:04:05.6",
		values.CatTimestampTZ: "2024-01-02 03:04:05.6+01:00",
	} {
		if v := FromDriver(ts, cat); v.Text != want || v.Kind != values.Time {
			t.Errorf("%v: %+v", cat, v)
		}
	}
	if v := FromDriver([]byte{1, 2}, values.CatBinary); v.Kind != values.Bytes || len(v.Bytes) != 2 {
		t.Fatalf("bytes = %+v", v)
	}
	big := make([]byte, values.MaxBytes+10)
	if v := FromDriver(big, values.CatBinary); len(v.Bytes) != len(big) {
		t.Fatal("bytes truncated")
	}
	long := strings.Repeat("x", values.MaxText+10)
	if v := FromDriver(long, values.CatText); len(v.Text) != len(long) {
		t.Fatal("text truncated")
	}
	if v := FromDriver(float32(0.5), values.CatFloat); v.Text != "0.5" || !v.Safe {
		t.Fatalf("float32 = %+v", v)
	}
	inf := 1e300
	inf *= 1e300
	if v := FromDriver(inf, values.CatFloat); v.Safe || v.Text != "+Inf" {
		t.Fatalf("inf = %+v", v)
	}
	if v := FromDriver(int64(-9007199254740993), values.CatInt); v.Safe || v.Text != "-9007199254740993" {
		t.Fatalf("big = %+v", v)
	}
}

func TestCellSource(t *testing.T) {
	src := &CellSource{Cols: cols("a", "b"), Rows: [][]values.Cell{{{K: values.Num, V: float64(1)}, {K: values.Null}}, {{K: values.Str, V: "x"}}}}
	if _, err := src.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Next(); err == nil || !strings.Contains(err.Error(), "2 columns") {
		t.Fatalf("err = %v", err)
	}
	if _, err := src.Next(); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	trunc := &CellSource{Cols: cols("a"), Rows: [][]values.Cell{{{K: values.Str, V: "x", Trunc: true}}}}
	if _, err := trunc.Next(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v", err)
	}
	if len(trunc.Columns()) != 1 {
		t.Fatal("columns")
	}
}

func TestToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.csv")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Failure keeps the old file and removes the temp file.
	err := ToFile(path, func(w io.Writer) error {
		_, _ = io.WriteString(w, "partial")
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "old" {
		t.Fatalf("target = %q", data)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp file left: %v", entries)
	}
	if err := ToFile(path, func(w io.Writer) error { _, err := io.WriteString(w, "new"); return err }); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "new" {
		t.Fatalf("target = %q", data)
	}
	if err := ToFile("", nil); !errors.Is(err, ErrOptions) {
		t.Fatal(err)
	}
	if err := ToFile(filepath.Join(dir, "missing", "x.csv"), nil); err == nil {
		t.Fatal("missing dir accepted")
	}
	// Rename onto a directory fails and cleans up.
	sub := filepath.Join(dir, "sub")
	_ = os.Mkdir(sub, 0o700)
	_ = os.WriteFile(filepath.Join(sub, "f"), nil, 0o600)
	if err := ToFile(sub, func(io.Writer) error { return nil }); err == nil {
		t.Fatal("rename onto a non-empty directory succeeded")
	}
}

func TestCheckRerunnable(t *testing.T) {
	ok := map[driver.Kind][]string{
		driver.Postgres: {"SELECT 1", "  select * from t;  ", "WITH x AS (SELECT 1) SELECT * FROM x", "VALUES (1)", "TABLE t", "EXPLAIN SELECT 1"},
		driver.MySQL:    {"SHOW TABLES", "DESCRIBE t", "SELECT 1 -- c"},
		driver.SQLite:   {"PRAGMA table_info(t)"},
	}
	for kind, list := range ok {
		for _, s := range list {
			if _, err := CheckRerunnable(kind, s); err != nil {
				t.Errorf("%s %q: %v", kind, s, err)
			}
		}
	}
	bad := map[driver.Kind][]string{
		driver.Postgres: {"INSERT INTO t VALUES (1)", "SELECT 1; SELECT 2", "", "  ;  ", "SELECT * INTO x FROM t",
			"WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d", "UPDATE t SET a=1 RETURNING *", "CALL p()", "SET x = 1",
			"EXPLAIN ANALYZE DELETE FROM t"},
		driver.SQLite: {"PRAGMA foreign_keys = 0"},
	}
	for kind, list := range bad {
		for _, s := range list {
			if _, err := CheckRerunnable(kind, s); !errors.Is(err, ErrNotRerunnable) {
				t.Errorf("%s %q: err = %v", kind, s, err)
			}
		}
	}
}
