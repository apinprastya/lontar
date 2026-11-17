package importer

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"

	"lontar/internal/testutil"
)

func writeNamed(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func strs(rec []*string) []string {
	out := make([]string, len(rec))
	for i, f := range rec {
		if f == nil {
			out[i] = "<nil>"
		} else {
			out[i] = *f
		}
	}
	return out
}

// readAll runs a source to the end.
func readAll(src source) (header []string, recs [][]string, pos []int, err error) {
	header, err = src.start()
	if err != nil {
		return nil, nil, nil, err
	}
	for {
		rec, p, err := src.next()
		if errors.Is(err, io.EOF) {
			return header, recs, pos, nil
		}
		if err != nil {
			return header, recs, pos, err
		}
		recs = append(recs, strs(rec))
		pos = append(pos, p)
	}
}

func TestJSONSource(t *testing.T) {
	array := "\xef\xbb\xbf [\n" +
		`{"id": 1, "name": "a", "price": 1.50, "big": 12345678901234567890, "tags": ["x", {"k": null}], "meta": {"a" : 1}},` + "\n" +
		`{"name": "bé", "id": 2, "ok": true, "note": null, "price": -0.0e1},` + "\n" +
		`{"id": 3, "ok": false, "name": "dup", "name": "last"}` + "\n]\n"
	src := newJSONSource(bufio.NewReader(strings.NewReader(array)), false)
	header, recs, pos, err := readAll(src)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"id", "name", "price", "big", "tags", "meta", "ok", "note"}; !reflect.DeepEqual(header, want) {
		t.Fatalf("header %q", header)
	}
	want := [][]string{
		{"1", "a", "1.50", "12345678901234567890", `["x",{"k":null}]`, `{"a":1}`, "<nil>", "<nil>"},
		{"2", "bé", "-0.0e1", "<nil>", "<nil>", "<nil>", "true", "<nil>"},
		{"3", "last", "<nil>", "<nil>", "<nil>", "<nil>", "false", "<nil>"},
	}
	if !reflect.DeepEqual(recs, want) || !reflect.DeepEqual(pos, []int{1, 2, 3}) {
		t.Fatalf("records %q pos %v", recs, pos)
	}
	if !src.elements() || src.width() != 8 {
		t.Fatal("elements/width")
	}

	nd := "{\"id\": 1, \"v\": \"x\"}\n\n   \r\n{\"v\": null, \"id\": 2}\r\n{\"id\":3}"
	src = newJSONSource(bufio.NewReader(iotest.OneByteReader(strings.NewReader(nd))), true)
	header, recs, pos, err = readAll(src)
	if err != nil || !reflect.DeepEqual(header, []string{"id", "v"}) || !reflect.DeepEqual(recs, [][]string{{"1", "x"}, {"2", "<nil>"}, {"3", "<nil>"}}) ||
		!reflect.DeepEqual(pos, []int{1, 4, 5}) || src.elements() {
		t.Fatalf("ndjson %q %q %v %v", header, recs, pos, err)
	}

	// Empty inputs.
	for _, in := range []string{"", "[]", " [ ] "} {
		src := newJSONSource(bufio.NewReader(strings.NewReader(in)), false)
		if h, r, _, err := readAll(src); err != nil || len(h) != 0 || len(r) != 0 {
			t.Fatalf("%q: %v %v %v", in, h, r, err)
		}
	}
	if h, r, _, err := readAll(newJSONSource(bufio.NewReader(strings.NewReader("\n\n")), true)); err != nil || len(h)+len(r) != 0 {
		t.Fatal(err)
	}
}

func TestJSONLateKey(t *testing.T) {
	var b strings.Builder
	for i := 0; i < PreviewRows; i++ {
		b.WriteString(`{"id": 1}` + "\n")
	}
	b.WriteString(`{"id": 2, "late": "x"}` + "\n")
	src := newJSONSource(bufio.NewReader(strings.NewReader(b.String())), true)
	_, recs, _, err := readAll(src)
	var re *RowError
	if !errors.As(err, &re) || re.Line != PreviewRows+1 || !strings.Contains(re.Message, `"late"`) || len(recs) != PreviewRows {
		t.Fatalf("err %v, %d records", err, len(recs))
	}
	// A key first seen within the preview rows becomes a column.
	src = newJSONSource(bufio.NewReader(strings.NewReader(`[{"a":1},{"b":2}]`)), false)
	if h, _, _, err := readAll(src); err != nil || !reflect.DeepEqual(h, []string{"a", "b"}) {
		t.Fatal(h, err)
	}
}

func TestJSONMalformed(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		ndjson  bool
		line    int
		element int
		want    string
	}{
		{"not an array", `{"a": 1}`, false, 0, 0, "array of objects"},
		{"element not object", `[{"a":1}, 5]`, false, 0, 2, "found a number"},
		{"element array", `[[1]]`, false, 0, 1, "found an array"},
		{"element string", `["x"]`, false, 0, 1, "found a string"},
		{"element bool", `[true]`, false, 0, 1, "boolean true"},
		{"element null", `[null]`, false, 0, 1, "found null"},
		{"truncated", `[{"a":1}, {"a":`, false, 0, 2, "syntax"},
		{"bad value", `[{"a": tru}]`, false, 0, 1, "syntax"},
		{"missing comma", `[{"a":1} {"a":2}]`, false, 0, 2, "syntax"},
		{"trailing data", `[{"a":1}] x`, false, 0, 1, "after the JSON array"},
		{"bad start", `x`, false, 0, 0, "syntax"},
		{"unclosed object", `[{"a":1`, false, 0, 1, "syntax"},
		{"nd syntax", "{\"a\":1}\n{\"a\":}\n", true, 2, 0, "syntax"},
		{"nd two objects", "{\"a\":1} {\"a\":2}\n", true, 1, 0, "one object per line"},
		{"nd not object", "{\"a\":1}\n\n[1]\n", true, 3, 0, "found an array"},
		{"nd utf8", "{\"a\":\"\xff\"}\n", true, 1, 0, "UTF-8"},
		{"nd truncated", "{\"a\":1", true, 1, 0, "syntax"},
		{"nd empty object then bad", "{}\n{\"a\" 1}\n", true, 2, 0, "syntax"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := newJSONSource(bufio.NewReader(strings.NewReader(tc.in)), tc.ndjson)
			_, _, _, err := readAll(src)
			var re *RowError
			if !errors.As(err, &re) || re.Line != tc.line || re.Element != tc.element || !strings.Contains(re.Message, tc.want) {
				t.Fatalf("err = %#v", err)
			}
		})
	}
	// A read error that is not JSON.
	boom := errors.New("boom")
	src := newJSONSource(bufio.NewReader(iotest.ErrReader(boom)), true)
	if _, _, _, err := readAll(src); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if (&RowError{Element: 4, Message: "x"}).Error() != "element 4: x. Nothing was imported." {
		t.Fatal("element message")
	}
}

func TestDetectFormat(t *testing.T) {
	tests := []struct{ name, head, want string }{
		{"a.ndjson", "[", FormatNDJSON},
		{"a.JSONL", "", FormatNDJSON},
		{"a.csv", "[", FormatCSV},
		{"a.tsv", "{", FormatCSV},
		{"a.txt", "{", FormatCSV},
		{"a.json", "\xef\xbb\xbf \n[", FormatJSON},
		{"a.json", "{\"a\":1}", FormatNDJSON},
		{"a.json", "", FormatJSON},
		{"data", "  [", FormatJSON},
		{"data", "{", FormatNDJSON},
		{"data", "a,b", FormatCSV},
	}
	for _, tc := range tests {
		if got := DetectFormat(tc.name, []byte(tc.head)); got != tc.want {
			t.Errorf("DetectFormat(%q, %q) = %q, want %q", tc.name, tc.head, got, tc.want)
		}
	}
}

func TestImportJSON(t *testing.T) {
	forEach(t, func(t *testing.T, v *env) {
		ctx := context.Background()
		tbl := v.table(t)
		content := `[
  {"ID": 1, "name": "a", "qty": 10, "note": {"nested": [1, 2.50, null]}, "data": "dead"},
  {"id": 2, "name": "b", "qty": null, "note": "plain", "extra": true},
  {"id": 3, "name": "c"}
]`
		path := writeNamed(t, "in.json", content)
		p, err := PreviewFile(ctx, v.db, v.e.drv, path, Options{}, v.schema, tbl)
		if err != nil {
			t.Fatal(err)
		}
		if p.Format != FormatJSON || strings.Join(p.Headers, "|") != "ID|name|qty|note|data|id|extra" || len(p.Rows) != 3 {
			t.Fatalf("preview = %+v", p)
		}
		// Keys are matched by name ignoring case; the first match wins.
		want := map[string]int{"id": 0, "name": 1, "qty": 2, "note": 3, "data": 4}
		if len(p.Mapping) != 5 {
			t.Fatalf("mapping %+v", p.Mapping)
		}
		for _, m := range p.Mapping {
			if want[m.Column] != m.Source {
				t.Fatalf("mapping %+v", p.Mapping)
			}
		}
		// A key missing from an element is NULL: element 1 has no "extra"
		// for the NOT NULL name.
		m := []Mapping{{0, "id"}, {6, "name"}}
		_, err = run(v, path, tbl, Options{}, m, 0, RunOptions{}, nil)
		var re *RowError
		if !errors.As(err, &re) || re.Element != 1 || re.Line != 0 || !strings.Contains(err.Error(), "element 1") {
			t.Fatalf("err = %v", err)
		}
		path = writeNamed(t, "in.json", strings.Replace(content, `"ID": 1`, `"id": 1`, 1))
		res, err := run(v, path, tbl, Options{}, []Mapping{{0, "id"}, {1, "name"}, {2, "qty"}, {3, "note"}, {4, "data"}}, 2, RunOptions{}, nil)
		if err != nil || res.Rows != 3 {
			t.Fatalf("res %+v %v", res, err)
		}
		got := rows(t, v, tbl)
		if got[0][2] != "10" || got[0][3] != `{"nested":[1,2.50,null]}` || got[0][4] != "dead" || got[1][2] != "NULL" || got[1][3] != "plain" || got[2][3] != "NULL" {
			t.Fatalf("rows = %q", got)
		}

		// NDJSON with a malformed line: everything rolls back.
		tbl2 := v.table(t)
		nd := writeNamed(t, "in.ndjson", "{\"id\": 1, \"name\": \"x\"}\n\n{\"id\": 2, \"name\": }\n")
		_, err = run(v, nd, tbl2, Options{}, []Mapping{{0, "id"}, {1, "name"}}, 1, RunOptions{}, nil)
		if !errors.As(err, &re) || re.Line != 3 || re.Element != 0 {
			t.Fatalf("err = %v", err)
		}
		if got := rows(t, v, tbl2); len(got) != 0 {
			t.Fatalf("rows = %q", got)
		}
		// Forced format: the .txt file is NDJSON.
		txt := writeNamed(t, "data.txt", "{\"id\": 7, \"name\": \"n\", \"qty\": 3}\n")
		p, err = PreviewFile(ctx, v.db, v.e.drv, txt, Options{Format: "NDJSON"}, v.schema, tbl2)
		if err != nil || p.Format != FormatNDJSON || len(p.Mapping) != 3 {
			t.Fatalf("preview %+v %v", p, err)
		}
		if res, err := run(v, txt, tbl2, Options{Format: FormatNDJSON}, p.Mapping, 0, RunOptions{}, nil); err != nil || res.Rows != 1 {
			t.Fatal(err)
		}
		// A malformed record in the preview rows fails the preview.
		if _, err := PreviewFile(ctx, v.db, v.e.drv, writeNamed(t, "x.json", `[{"id":1}, 4]`), Options{}, v.schema, tbl2); !errors.As(err, &re) || re.Element != 2 {
			t.Fatalf("err = %v", err)
		}
		// A bad value for the column type names the element (SQLite
		// accepts anything in an INT column).
		if v.e.drv.Kind() != "sqlite" {
			bad := writeNamed(t, "bad.json", `[{"id": 10, "name": "a", "qty": "many"}]`)
			_, err = run(v, bad, tbl2, Options{}, []Mapping{{0, "id"}, {1, "name"}, {2, "qty"}}, 0, RunOptions{}, nil)
			if !errors.As(err, &re) || re.Element != 1 || re.Column != "qty" {
				t.Fatalf("err = %#v", err)
			}
		}
	})
}

func TestImportFormatOption(t *testing.T) {
	v := &env{e: engines[0]}
	v.db = testutil.OpenDriver(t, v.e.drv, testutil.SQLiteTemp(t), false)
	v.schema = "main"
	tbl := v.table(t)
	path := writeNamed(t, "a.json", "[]")
	if _, err := PreviewFile(context.Background(), v.db, v.e.drv, path, Options{Format: "xml"}, "main", tbl); !errors.Is(err, ErrOptions) {
		t.Fatal(err)
	}
	if _, err := run(v, path, tbl, Options{Format: "xml"}, []Mapping{{0, "id"}}, 0, RunOptions{}, nil); !errors.Is(err, ErrOptions) {
		t.Fatal(err)
	}
	p, err := PreviewFile(context.Background(), v.db, v.e.drv, path, Options{}, "main", tbl)
	if err != nil || p.Format != FormatJSON || len(p.Headers) != 0 || len(p.Rows) != 0 {
		t.Fatalf("%+v %v", p, err)
	}
}
