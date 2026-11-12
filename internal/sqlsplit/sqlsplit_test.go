package sqlsplit

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"lontar/internal/driver"
)

func texts(stmts []Statement) []string {
	out := make([]string, len(stmts))
	for i, s := range stmts {
		out[i] = s.Text
	}
	return out
}

func TestSplit(t *testing.T) {
	const (
		pg     = driver.Postgres
		my     = driver.MySQL
		maria  = driver.MariaDB
		lite   = driver.SQLite
		others = driver.Kind("other")
	)
	tests := []struct {
		name   string
		kinds  []driver.Kind
		script string
		want   []string
	}{
		{"empty", []driver.Kind{pg, my, lite}, "", []string{}},
		{"blank", []driver.Kind{pg, my, lite}, "  \n\t\r\n ", []string{}},
		{"only delimiters", []driver.Kind{pg, my, lite}, " ; ;;\n;", []string{}},
		{"single no delimiter", []driver.Kind{pg, my, lite, others}, "SELECT 1", []string{"SELECT 1"}},
		{"trailing whitespace", []driver.Kind{pg, my, lite}, "  SELECT 1  \n\n", []string{"SELECT 1"}},
		{"two", []driver.Kind{pg, my, lite}, "SELECT 1; SELECT 2;", []string{"SELECT 1", "SELECT 2"}},
		{"space before delimiter", []driver.Kind{pg, my, lite}, "SELECT 1 \n ;\nSELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"multiline", []driver.Kind{pg, my, lite}, "SELECT a,\n  b\nFROM t\nWHERE x = 1;\nDELETE FROM t", []string{"SELECT a,\n  b\nFROM t\nWHERE x = 1", "DELETE FROM t"}},
		{"semicolon in string", []driver.Kind{pg, my, lite}, "SELECT 'a;b'; SELECT 2", []string{"SELECT 'a;b'", "SELECT 2"}},
		{"doubled quote", []driver.Kind{pg, my, lite}, "SELECT 'it''s;'; SELECT 2", []string{"SELECT 'it''s;'", "SELECT 2"}},
		{"unterminated string", []driver.Kind{pg, my, lite}, "SELECT 1; SELECT 'abc;", []string{"SELECT 1", "SELECT 'abc;"}},
		{"unterminated string keeps blanks", []driver.Kind{my}, `"\" `, []string{`"\" `}},
		{"quoted identifier", []driver.Kind{pg, lite}, `SELECT "a;""b" FROM t; SELECT 2`, []string{`SELECT "a;""b" FROM t`, "SELECT 2"}},
		{"line comment", []driver.Kind{pg, lite}, "SELECT 1 -- x; y\n; SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"leading comments dropped", []driver.Kind{pg, my, lite}, "-- intro\n/* block; */\nSELECT 1;", []string{"SELECT 1"}},
		{"comment only statement", []driver.Kind{pg, my, lite}, "SELECT 1; -- done\n/* x */;", []string{"SELECT 1"}},
		{"inner comment kept", []driver.Kind{pg, my, lite}, "SELECT /* ; */ 1;", []string{"SELECT /* ; */ 1"}},
		{"unterminated block comment", []driver.Kind{pg, my, lite}, "SELECT 1; /* never ; closed", []string{"SELECT 1"}},
		{"block comment after content unterminated", []driver.Kind{pg, my, lite}, "SELECT 1 /* x; y", []string{"SELECT 1"}},

		// PostgreSQL
		{"pg dollar", []driver.Kind{pg}, "CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END; $$ LANGUAGE plpgsql; SELECT f();",
			[]string{"CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END; $$ LANGUAGE plpgsql", "SELECT f()"}},
		{"pg tagged dollar", []driver.Kind{pg}, "DO $body$ BEGIN PERFORM '$$;'; END $body$; SELECT 1",
			[]string{"DO $body$ BEGIN PERFORM '$$;'; END $body$", "SELECT 1"}},
		{"pg dollar after keyword", []driver.Kind{pg}, "SELECT $$a;b$$; SELECT 2", []string{"SELECT $$a;b$$", "SELECT 2"}},
		{"pg unterminated dollar", []driver.Kind{pg}, "SELECT 1; SELECT $x$ ; more", []string{"SELECT 1", "SELECT $x$ ; more"}},
		{"pg positional params", []driver.Kind{pg}, "SELECT $1; SELECT $2", []string{"SELECT $1", "SELECT $2"}},
		{"pg dollar in identifier", []driver.Kind{pg}, "SELECT a$b$c; SELECT 2", []string{"SELECT a$b$c", "SELECT 2"}},
		{"pg lone dollar", []driver.Kind{pg}, "SELECT $; SELECT 2", []string{"SELECT $", "SELECT 2"}},
		{"pg E string", []driver.Kind{pg}, `SELECT E'it\'s;'; SELECT 2`, []string{`SELECT E'it\'s;'`, "SELECT 2"}},
		{"pg lower e string", []driver.Kind{pg}, `SELECT e'\\'; SELECT 2`, []string{`SELECT e'\\'`, "SELECT 2"}},
		{"pg plain string no backslash", []driver.Kind{pg}, `SELECT 'a\'; SELECT 2`, []string{`SELECT 'a\'`, "SELECT 2"}},
		{"pg identifier ending in e", []driver.Kind{pg}, `SELECT some'x\'; SELECT 2`, []string{`SELECT some'x\'`, "SELECT 2"}},
		{"pg nested comment", []driver.Kind{pg}, "SELECT /* a /* b; */ c; */ 1; SELECT 2", []string{"SELECT /* a /* b; */ c; */ 1", "SELECT 2"}},
		{"pg hash is operator", []driver.Kind{pg}, "SELECT 1 # 2; SELECT 3", []string{"SELECT 1 # 2", "SELECT 3"}},
		{"pg dash dash no space", []driver.Kind{pg}, "SELECT 1 --x;\n; SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"pg begin atomic", []driver.Kind{pg}, "CREATE OR REPLACE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT CASE WHEN true THEN 1 END; SELECT 2; END; SELECT f()",
			[]string{"CREATE OR REPLACE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT CASE WHEN true THEN 1 END; SELECT 2; END", "SELECT f()"}},
		{"pg begin transaction", []driver.Kind{pg}, "BEGIN; UPDATE t SET a = 1; COMMIT;", []string{"BEGIN", "UPDATE t SET a = 1", "COMMIT"}},
		{"pg procedure without atomic", []driver.Kind{pg}, "CREATE PROCEDURE p() LANGUAGE sql AS 'SELECT 1'; CALL p()", []string{"CREATE PROCEDURE p() LANGUAGE sql AS 'SELECT 1'", "CALL p()"}},
		{"pg backtick is plain", []driver.Kind{pg}, "SELECT `a;b`", []string{"SELECT `a", "b`"}},
		{"pg number then E", []driver.Kind{pg}, `SELECT 1e'\'; SELECT 2`, []string{`SELECT 1e'\'`, "SELECT 2"}},

		// MySQL / MariaDB
		{"my backslash escape", []driver.Kind{my, maria}, `SELECT 'it\'s;'; SELECT 2`, []string{`SELECT 'it\'s;'`, "SELECT 2"}},
		{"my double quoted string", []driver.Kind{my, maria}, `SELECT "a\";b"; SELECT 2`, []string{`SELECT "a\";b"`, "SELECT 2"}},
		{"my backtick", []driver.Kind{my, maria}, "SELECT `a;``b`; SELECT 2", []string{"SELECT `a;``b`", "SELECT 2"}},
		{"my hash comment", []driver.Kind{my, maria}, "SELECT 1 # x; y\n; SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"my dash needs space", []driver.Kind{my, maria}, "SELECT 1--1; SELECT 2", []string{"SELECT 1--1", "SELECT 2"}},
		{"my dash with space", []driver.Kind{my, maria}, "SELECT 1 -- x;\n; SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"my dash at end", []driver.Kind{my}, "SELECT 1; --", []string{"SELECT 1"}},
		{"my flat comment", []driver.Kind{my}, "SELECT /* a /* b */ 1; SELECT 2", []string{"SELECT /* a /* b */ 1", "SELECT 2"}},
		{"my executable comment", []driver.Kind{my}, "/*!40101 SET NAMES utf8 */; SELECT 2", []string{"/*!40101 SET NAMES utf8 */", "SELECT 2"}},
		{"my optimizer hint", []driver.Kind{my}, "SELECT /*+ MAX_EXECUTION_TIME(1) */ 1", []string{"SELECT /*+ MAX_EXECUTION_TIME(1) */ 1"}},
		{"my dollar is plain", []driver.Kind{my}, "SELECT $$; SELECT 2", []string{"SELECT $$", "SELECT 2"}},
		{"my delimiter", []driver.Kind{my, maria},
			"DELIMITER //\nCREATE PROCEDURE p()\nBEGIN\n  SELECT 1;\n  SELECT 2;\nEND //\nDELIMITER ;\nCALL p();\n",
			[]string{"CREATE PROCEDURE p()\nBEGIN\n  SELECT 1;\n  SELECT 2;\nEND", "CALL p()"}},
		{"my delimiter lowercase and indented", []driver.Kind{my}, "  delimiter $$\nSELECT 1; SELECT 2$$\ndelimiter ;\nSELECT 3", []string{"SELECT 1; SELECT 2", "SELECT 3"}},
		{"my delimiter flushes pending", []driver.Kind{my}, "SELECT 1\nDELIMITER //\nSELECT 2//", []string{"SELECT 1", "SELECT 2"}},
		{"my delimiter mid line is text", []driver.Kind{my}, "SELECT 1 DELIMITER //;", []string{"SELECT 1 DELIMITER //"}},
		{"my delimiter without arg", []driver.Kind{my}, "DELIMITER \nSELECT 1", []string{"DELIMITER \nSELECT 1"}},
		{"my delimiter word only", []driver.Kind{my}, "DELIMITER", []string{"DELIMITER"}},
		{"my delimiter after comment line", []driver.Kind{my}, "-- set\nDELIMITER //\nSELECT 1//", []string{"SELECT 1"}},
		{"my delimiter at eof", []driver.Kind{my}, "SELECT 1;\nDELIMITER ;;", []string{"SELECT 1"}},
		{"pg has no delimiter directive", []driver.Kind{pg}, "DELIMITER //\nSELECT 1//", []string{"DELIMITER //\nSELECT 1//"}},

		// SQLite
		{"sqlite trigger", []driver.Kind{lite},
			"CREATE TRIGGER tr AFTER INSERT ON t BEGIN\n  UPDATE t SET a = 1;\n  INSERT INTO log VALUES (CASE WHEN 1 THEN 'x' END);\nEND;\nSELECT 1;",
			[]string{"CREATE TRIGGER tr AFTER INSERT ON t BEGIN\n  UPDATE t SET a = 1;\n  INSERT INTO log VALUES (CASE WHEN 1 THEN 'x' END);\nEND", "SELECT 1"}},
		{"sqlite temp trigger with when case", []driver.Kind{lite},
			"create temp trigger tr before delete on t when (case when old.a then 1 end) begin select raise(abort, 'no;'); end; select 2",
			[]string{"create temp trigger tr before delete on t when (case when old.a then 1 end) begin select raise(abort, 'no;'); end", "select 2"}},
		{"sqlite begin transaction", []driver.Kind{lite}, "BEGIN; SELECT 1; END;", []string{"BEGIN", "SELECT 1", "END"}},
		{"sqlite brackets", []driver.Kind{lite}, "SELECT [a;b] FROM t; SELECT 2", []string{"SELECT [a;b] FROM t", "SELECT 2"}},
		{"sqlite unterminated bracket", []driver.Kind{lite}, "SELECT [a; b", []string{"SELECT [a; b"}},
		{"sqlite backtick", []driver.Kind{lite}, "SELECT `a;b`; SELECT 2", []string{"SELECT `a;b`", "SELECT 2"}},
		{"sqlite no backslash escapes", []driver.Kind{lite}, `SELECT 'a\'; SELECT 2`, []string{`SELECT 'a\'`, "SELECT 2"}},
		{"sqlite create table not trigger", []driver.Kind{lite}, "CREATE TABLE t (a); SELECT 1", []string{"CREATE TABLE t (a)", "SELECT 1"}},

		{"unicode", []driver.Kind{pg, my, lite}, "SELECT 'ä;ö', ñame FROM tæble; SELECT '日本'", []string{"SELECT 'ä;ö', ñame FROM tæble", "SELECT '日本'"}},
	}
	for _, tc := range tests {
		for _, k := range tc.kinds {
			t.Run(tc.name+"/"+string(k), func(t *testing.T) {
				got := Split(k, tc.script)
				if !reflect.DeepEqual(texts(got), tc.want) {
					t.Fatalf("Split(%q)\n got %q\nwant %q", tc.script, texts(got), tc.want)
				}
				checkInvariants(t, tc.script, got)
			})
		}
	}
}

func checkInvariants(t *testing.T, script string, stmts []Statement) {
	t.Helper()
	prev := 0
	for i, s := range stmts {
		if s.Start < prev || s.Start >= s.End || s.End > s.DelimEnd || s.DelimEnd > len(script) {
			t.Fatalf("stmt %d bad offsets %+v (prev %d, len %d)", i, s, prev, len(script))
		}
		if script[s.Start:s.End] != s.Text {
			t.Fatalf("stmt %d text %q != script[%d:%d] %q", i, s.Text, s.Start, s.End, script[s.Start:s.End])
		}
		// An unterminated string may end in blanks; the start never does.
		if strings.TrimLeft(s.Text, " \t\r\n") != s.Text {
			t.Fatalf("stmt %d not trimmed: %q", i, s.Text)
		}
		prev = s.DelimEnd
	}
}

func TestSplitOffsets(t *testing.T) {
	script := "  SELECT 1 ;\n\nSELECT 2"
	got := Split(driver.Postgres, script)
	want := []Statement{
		{Text: "SELECT 1", Start: 2, End: 10, DelimEnd: 12},
		{Text: "SELECT 2", Start: 14, End: 22, DelimEnd: 22},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestStatementAt(t *testing.T) {
	//          0         1         2         3
	//          0123456789012345678901234567890123
	script := "  SELECT 1;  SELECT 2;\n\n  SELECT 3  "
	tests := []struct {
		cursor int
		want   string
	}{
		{0, "SELECT 1"},  // before the first statement
		{2, "SELECT 1"},  // at its start
		{10, "SELECT 1"}, // just before ;
		{11, "SELECT 1"}, // just after ;
		{12, "SELECT 1"}, // blank space after it
		{13, "SELECT 2"},
		{22, "SELECT 2"}, // after the second ;
		{23, "SELECT 2"}, // blank line
		{26, "SELECT 3"},
		{35, "SELECT 3"}, // trailing blanks
		{-5, "SELECT 1"}, // clamped
		{999, "SELECT 3"},
	}
	for _, tc := range tests {
		st, ok := StatementAt(driver.Postgres, script, tc.cursor)
		if !ok || st.Text != tc.want {
			t.Errorf("StatementAt(%d) = %q, %v; want %q", tc.cursor, st.Text, ok, tc.want)
		}
	}
	if _, ok := StatementAt(driver.SQLite, " -- nothing\n", 3); ok {
		t.Fatal("StatementAt on an empty script should report !ok")
	}
}

func FuzzSplit(f *testing.F) {
	for _, s := range []string{
		"SELECT 1; SELECT 2",
		"SELECT 'a;b' -- c\n; /* d; */ SELECT $$x;$$",
		"DELIMITER //\nCREATE PROCEDURE p() BEGIN SELECT 1; END //\nDELIMITER ;\nCALL p();",
		"CREATE TRIGGER t AFTER INSERT ON x BEGIN SELECT 1; END; SELECT 2",
		"SELECT E'\\'';",
		"/* /* */ */ $a$ $a$ `x` [y] \"z\" #c\n",
		"CREATE FUNCTION f() BEGIN ATOMIC SELECT 1; END;",
		"SELECT 'ä;ö'; SELECT '\xff\xfe';",
		"delimiter\t;;\nselect 1;;",
	} {
		f.Add(s, 0)
	}
	kinds := []driver.Kind{driver.Postgres, driver.MySQL, driver.SQLite}
	f.Fuzz(func(t *testing.T, script string, cursor int) {
		for _, k := range kinds {
			stmts := Split(k, script)
			checkInvariants(t, script, stmts)
			if utf8.ValidString(script) {
				for _, s := range stmts {
					if !utf8.ValidString(s.Text) {
						t.Fatalf("statement splits a UTF-8 sequence: %q", s.Text)
					}
				}
			}
			st, ok := StatementAt(k, script, cursor)
			if ok != (len(stmts) > 0) {
				t.Fatalf("StatementAt ok=%v with %d statements", ok, len(stmts))
			}
			if ok && (st.Start < 0 || st.End > len(script)) {
				t.Fatalf("StatementAt out of range: %+v", st)
			}
		}
	})
}

func TestWords(t *testing.T) {
	got := Words(driver.Postgres, "/* c */ insert INTO \"t\" VALUES ('returning') RETURNING id; select", 0)
	want := []string{"INSERT", "INTO", "VALUES", "RETURNING", "ID", "SELECT"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Words = %q, want %q", got, want)
	}
	if got := Words(driver.MySQL, "  -- x\n  SELECT a FROM b", 2); !reflect.DeepEqual(got, []string{"SELECT", "A"}) {
		t.Fatalf("Words limit = %q", got)
	}
	if got := Words(driver.SQLite, "  ", 0); len(got) != 0 {
		t.Fatalf("Words empty = %q", got)
	}
}
