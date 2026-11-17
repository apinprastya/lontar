package sqlsplit

import (
	"errors"
	"io"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"

	"lontar/internal/driver"
)

// chunkReader returns the script in random chunk sizes (1..max).
type chunkReader struct {
	s   string
	rnd *rand.Rand
	max int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	n := min(1+c.rnd.Intn(c.max), len(p), len(c.s))
	copy(p, c.s[:n])
	c.s = c.s[n:]
	return n, nil
}

// scanned is a statement with its COPY data read.
type scanned struct {
	Statement
	data    string
	hasData bool
}

func scanAll(t *testing.T, sc *Scanner) ([]scanned, error) {
	t.Helper()
	var out []scanned
	for {
		st, err := sc.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
		s := scanned{Statement: st}
		if st.CopyData != nil {
			b, _ := io.ReadAll(st.CopyData)
			s.data, s.hasData = string(b), true
			s.CopyData = nil
		}
		out = append(out, s)
	}
}

func splitScanned(kind driver.Kind, script string) []scanned {
	var out []scanned
	for _, st := range Split(kind, script) {
		s := scanned{Statement: st}
		if st.CopyData != nil {
			b, _ := io.ReadAll(st.CopyData)
			s.data, s.hasData = string(b), true
			s.CopyData = nil
		}
		out = append(out, s)
	}
	return out
}

// checkEquivalent asserts that the scanner over script read in random
// chunk sizes yields exactly Split(script).
func checkEquivalent(t *testing.T, kind driver.Kind, script string, seed int64) {
	t.Helper()
	want := splitScanned(kind, script)
	for _, maxChunk := range []int{1, 3, 17} {
		r := &chunkReader{s: script, rnd: rand.New(rand.NewSource(seed)), max: maxChunk}
		got, err := scanAll(t, NewScanner(kind, r))
		if err != nil && !errors.Is(err, ErrCopyUnterminated) {
			t.Fatalf("scanner error %v", err)
		}
		if len(got) != len(want) || (len(got) > 0 && !reflect.DeepEqual(got, want)) {
			t.Fatalf("kind %s chunk %d: scanner\n%+v\nSplit\n%+v\nscript %q", kind, maxChunk, got, want, script)
		}
	}
}

func TestScannerChunkBoundaries(t *testing.T) {
	scripts := map[driver.Kind][]string{
		driver.Postgres: {
			"SELECT 'a;b''c'; SELECT E'x\\';y'; SELECT $tag$ ; $tag$; SELECT $$a$$;",
			"/* a /* nested ; */ b */ SELECT 1 -- c ;\n; SELECT \"q;\"\"x\"",
			"CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT 1; SELECT 2; END; SELECT 3",
			"COPY public.t (a, b) FROM stdin;\n1\tx\n2\t\\N\n\\.\nSELECT 1;\n",
			"select 1;\n\n\nselect\n2;",
		},
		driver.MySQL: {
			"DELIMITER //\nCREATE PROCEDURE p()\nBEGIN\n  SELECT 1;\nEND//\nDELIMITER ;\nCALL p();\n",
			"DELIMITER $$\nSELECT 1$$\nSELECT abc$$\nDELIMITER ;\nSELECT 'a\\';b';",
			"/*!40101 SET NAMES utf8 */;\n# comment ;\nINSERT INTO `t` VALUES (1,'a;'),(2,\"b\\\"\");\n-- x\n",
			"  delimiter ;;\nselect 1;;\ndelimiter ;\n",
		},
		driver.SQLite: {
			"CREATE TRIGGER tr AFTER INSERT ON t BEGIN\n  UPDATE t SET a = 1;\nEND;\nSELECT [a;b] FROM `c;d`;",
			"BEGIN TRANSACTION;\nCREATE TABLE t(a);\nINSERT INTO t VALUES('x;y');\nCOMMIT;\n",
		},
	}
	for kind, list := range scripts {
		for _, s := range list {
			for seed := int64(0); seed < 20; seed++ {
				checkEquivalent(t, kind, s, seed)
			}
			got, err := scanAll(t, NewScanner(kind, iotest.OneByteReader(strings.NewReader(s))))
			if err != nil || len(got) != len(Split(kind, s)) {
				t.Fatalf("one byte reader: %v, %d statements", err, len(got))
			}
		}
	}
}

func TestScannerLinesAndOffsets(t *testing.T) {
	script := "-- head\nSELECT 1;\n\n  SELECT\n 2; COPY t FROM stdin;\na\nb\n\\.\nSELECT 3"
	got, err := scanAll(t, NewScanner(driver.Postgres, &chunkReader{s: script, rnd: rand.New(rand.NewSource(1)), max: 2}))
	if err != nil {
		t.Fatal(err)
	}
	type pos struct {
		text  string
		line  int
		start int
	}
	var have []pos
	for _, s := range got {
		have = append(have, pos{s.Text, s.Line, s.Start})
	}
	want := []pos{
		{"SELECT 1", 2, 8},
		{"SELECT\n 2", 4, strings.Index(script, "SELECT\n")},
		{"COPY t FROM stdin", 5, strings.Index(script, "COPY")},
		{"SELECT 3", 9, strings.Index(script, "SELECT 3")},
	}
	if !reflect.DeepEqual(have, want) {
		t.Fatalf("got %+v\nwant %+v", have, want)
	}
	if got[2].data != "a\nb\n" {
		t.Fatalf("copy data %q", got[2].data)
	}
}

func TestScannerOffset(t *testing.T) {
	sc := NewScanner(driver.SQLite, strings.NewReader("SELECT 1; SELECT 2"))
	if _, err := sc.Next(); err != nil {
		t.Fatal(err)
	}
	if sc.Offset() != 9 {
		t.Fatalf("offset %d", sc.Offset())
	}
	if _, err := sc.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Next(); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if sc.Offset() != 18 {
		t.Fatalf("offset %d", sc.Offset())
	}
}

func TestCopyBlocks(t *testing.T) {
	tests := []struct {
		name   string
		script string
		data   []string // data of each COPY, in order
		texts  []string
		err    error
	}{
		{"escaped terminator in data", "COPY t (a) FROM stdin;\n\\\\.\nx\\.y\n \\.\n\\.\nSELECT 1;", []string{"\\\\.\nx\\.y\n \\.\n"}, []string{"COPY t (a) FROM stdin", "SELECT 1"}, nil},
		{"crlf", "COPY t FROM stdin;\r\n1\tx\r\n2\ty\r\n\\.\r\nSELECT 2;\r\n", []string{"1\tx\r\n2\ty\r\n"}, []string{"COPY t FROM stdin", "SELECT 2"}, nil},
		{"empty block", "COPY t FROM STDIN;\n\\.\nCOPY u FROM stdin;\n\\.\n", []string{"", ""}, []string{"COPY t FROM STDIN", "COPY u FROM stdin"}, nil},
		{"terminator at eof", "COPY t FROM stdin;\n1\n\\.", []string{"1\n"}, []string{"COPY t FROM stdin"}, nil},
		{"crlf terminator at eof", "COPY t FROM stdin;\n1\n\\.\r", []string{"1\n"}, []string{"COPY t FROM stdin"}, nil},
		{"missing terminator", "COPY t FROM stdin;\n1\n2\n", []string{"1\n2\n"}, []string{"COPY t FROM stdin"}, ErrCopyUnterminated},
		{"missing terminator no newline", "COPY t FROM stdin;", []string{""}, []string{"COPY t FROM stdin"}, ErrCopyUnterminated},
		{"rest of line skipped", "COPY t FROM stdin; -- data next\nx\n\\.\n", []string{"x\n"}, []string{"COPY t FROM stdin"}, nil},
		{"backslash lines are data", "COPY t FROM stdin;\n\\N\n\\\n\\.x\n\\.\r\r\n\\.\n", []string{"\\N\n\\\n\\.x\n\\.\r\r\n"}, []string{"COPY t FROM stdin"}, nil},
		{"copy to stdout is plain", "COPY t TO stdout; SELECT 1", nil, []string{"COPY t TO stdout", "SELECT 1"}, nil},
		{"copy at eof without delimiter", "COPY t FROM stdin", nil, []string{"COPY t FROM stdin"}, nil},
		{"stdin elsewhere", "SELECT stdin FROM stdin; SELECT 2", nil, []string{"SELECT stdin FROM stdin", "SELECT 2"}, nil},
		{"copy with options", "COPY t (a) FROM STDIN WITH (FORMAT csv);\n\"a;b\"\n\\.\n", []string{"\"a;b\"\n"}, []string{"COPY t (a) FROM STDIN WITH (FORMAT csv)"}, nil},
	}
	for _, tc := range tests {
		for _, chunk := range []int{1, 2, 5, 1000} {
			t.Run(tc.name, func(t *testing.T) {
				sc := NewScanner(driver.Postgres, &chunkReader{s: tc.script, rnd: rand.New(rand.NewSource(int64(chunk))), max: chunk})
				var texts, data []string
				var err error
				for {
					var st Statement
					st, err = sc.Next()
					if err != nil {
						break
					}
					texts = append(texts, st.Text)
					if st.CopyData != nil {
						b, rerr := io.ReadAll(st.CopyData)
						data = append(data, string(b))
						if rerr != nil {
							err = rerr
							break
						}
					}
				}
				if tc.err == nil && !errors.Is(err, io.EOF) || tc.err != nil && !errors.Is(err, tc.err) {
					t.Fatalf("err = %v, want %v", err, tc.err)
				}
				if !reflect.DeepEqual(texts, tc.texts) || !reflect.DeepEqual(data, tc.data) {
					t.Fatalf("texts %q data %q\nwant %q %q", texts, data, tc.texts, tc.data)
				}
			})
		}
	}
	// On MySQL and SQLite COPY is an ordinary statement.
	for _, k := range []driver.Kind{driver.MySQL, driver.SQLite} {
		got := Split(k, "COPY t FROM stdin;\n1\n\\.\n")
		if len(got) != 2 || got[0].CopyData != nil {
			t.Fatalf("%s: %+v", k, got)
		}
	}
}

func TestCopyDataSkippedWhenUnread(t *testing.T) {
	script := "COPY t FROM stdin;\n" + strings.Repeat("1\tabc\n", 50000) + "\\.\nSELECT 9;"
	sc := NewScanner(driver.Postgres, strings.NewReader(script))
	st, err := sc.Next()
	if err != nil || st.CopyData == nil {
		t.Fatal(err)
	}
	st, err = sc.Next()
	if err != nil || st.Text != "SELECT 9" || st.Line != 50003 {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestCopyReaderStreams(t *testing.T) {
	// The data block is streamed: the buffer stays small.
	line := strings.Repeat("x", 100) + "\n"
	script := "COPY t FROM stdin;\n" + strings.Repeat(line, 20000) + "\\.\n"
	sc := NewScanner(driver.Postgres, strings.NewReader(script))
	sc.max = 4096
	st, err := sc.Next()
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, st.CopyData)
	if err != nil || n != int64(len(line)*20000) {
		t.Fatalf("copied %d, %v", n, err)
	}
	if cap(sc.buf) > 1<<20 {
		t.Fatalf("buffer grew to %d", cap(sc.buf))
	}
	buf := make([]byte, 0)
	if n, err := st.CopyData.Read(buf); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("read after end: %d %v", n, err)
	}
	if _, err := sc.Next(); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestStatementTooLarge(t *testing.T) {
	big := "SELECT '" + strings.Repeat("a", 10000) + "';"
	sc := NewScanner(driver.SQLite, strings.NewReader("SELECT 1;\n"+big))
	sc.max = 4096
	if st, err := sc.Next(); err != nil || st.Text != "SELECT 1" {
		t.Fatal(st, err)
	}
	_, err := sc.Next()
	if !errors.Is(err, ErrStatementTooLarge) || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v", err)
	}
	if _, err2 := sc.Next(); !errors.Is(err2, ErrStatementTooLarge) {
		t.Fatal("error is not sticky")
	}
	// Large statements below the limit are fine (the buffer grows).
	sc = NewScanner(driver.SQLite, iotest.HalfReader(strings.NewReader(big)))
	if st, err := sc.Next(); err != nil || len(st.Text) != len(big)-1 {
		t.Fatal(err)
	}
}

func TestScannerReadErrors(t *testing.T) {
	boom := errors.New("boom")
	sc := NewScanner(driver.Postgres, iotest.ErrReader(boom))
	if _, err := sc.Next(); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	// Errors inside a COPY block reach the data reader and Next.
	r := io.MultiReader(strings.NewReader("COPY t FROM stdin;\n1\n"), iotest.ErrReader(boom))
	sc = NewScanner(driver.Postgres, r)
	st, err := sc.Next()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(st.CopyData); !errors.Is(err, boom) {
		t.Fatalf("copy read: %v", err)
	}
	if _, err := sc.Next(); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	// A reader that makes no progress.
	sc = NewScanner(driver.Postgres, zeroReader{})
	if _, err := sc.Next(); !errors.Is(err, io.ErrNoProgress) {
		t.Fatal(err)
	}
	// DataErrReader returns data together with EOF.
	sc = NewScanner(driver.SQLite, iotest.DataErrReader(strings.NewReader("SELECT 1")))
	if st, err := sc.Next(); err != nil || st.Text != "SELECT 1" {
		t.Fatal(st, err)
	}
}

type zeroReader struct{}

func (zeroReader) Read([]byte) (int, error) { return 0, nil }

func TestTerminator(t *testing.T) {
	tests := []struct {
		in   string
		eof  bool
		n    int
		more bool
	}{
		{`\`, false, 0, true},
		{`\`, true, 0, false},
		{`\x`, false, 0, false},
		{`\.`, false, 0, true},
		{`\.`, true, 2, false},
		{"\\.\n", false, 3, false},
		{"\\.\r", false, 0, true},
		{"\\.\r", true, 3, false},
		{"\\.\r\n", false, 4, false},
		{"\\.\rx", false, 0, false},
		{"\\.x", false, 0, false},
	}
	for _, tc := range tests {
		n, more := terminator([]byte(tc.in), tc.eof)
		if n != tc.n || more != tc.more {
			t.Errorf("terminator(%q, %v) = %d, %v", tc.in, tc.eof, n, more)
		}
	}
}

func TestMetaCommands(t *testing.T) {
	got := Split(driver.Postgres, "\\restrict k\nSELECT 1;\n\\unrestrict k")
	if len(got) != 3 || !got[0].Meta || got[1].Meta || !got[2].Meta || got[2].Line != 3 || got[0].DelimEnd != 12 {
		t.Fatalf("%+v", got)
	}
	if w := Words(driver.Postgres, "\\restrict k", 0); len(w) != 0 {
		t.Fatalf("words %q", w)
	}
}
