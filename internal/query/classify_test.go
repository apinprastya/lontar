package query

import (
	"testing"

	dbdriver "dbedit/internal/driver"
)

func TestIsWrite(t *testing.T) {
	cases := []struct {
		kind  dbdriver.Kind
		sql   string
		write bool
	}{
		{dbdriver.Postgres, "SELECT * FROM t", false},
		{dbdriver.Postgres, "select 1 -- update", false},
		{dbdriver.Postgres, "  /* c */ (SELECT 1)", false},
		{dbdriver.Postgres, "WITH x AS (SELECT 1) SELECT * FROM x", false},
		{dbdriver.Postgres, "WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d", true},
		{dbdriver.Postgres, "SELECT * INTO copy FROM t", true},
		{dbdriver.MySQL, "SELECT * FROM t INTO OUTFILE '/tmp/x'", true},
		{dbdriver.Postgres, "EXPLAIN SELECT 1", false},
		{dbdriver.Postgres, "EXPLAIN ANALYZE SELECT 1", false},
		{dbdriver.Postgres, "EXPLAIN ANALYZE DELETE FROM t", true},
		{dbdriver.Postgres, "SHOW search_path", false},
		{dbdriver.MySQL, "DESCRIBE t", false},
		{dbdriver.Postgres, "BEGIN", false},
		{dbdriver.MySQL, "START TRANSACTION", false},
		{dbdriver.MySQL, "START REPLICA", true},
		{dbdriver.MySQL, "START", true},
		{dbdriver.Postgres, "COMMIT", false},
		{dbdriver.Postgres, "ROLLBACK", false},
		{dbdriver.SQLite, "PRAGMA table_info(t)", false},
		{dbdriver.SQLite, "PRAGMA foreign_keys = OFF", true},
		{dbdriver.Postgres, "INSERT INTO t VALUES (1)", true},
		{dbdriver.Postgres, "update t set a = 1", true},
		{dbdriver.Postgres, "DELETE FROM t", true},
		{dbdriver.Postgres, "CREATE TABLE x (a int)", true},
		{dbdriver.Postgres, "SET search_path = x", true},
		{dbdriver.Postgres, "CALL p()", true},
		{dbdriver.Postgres, "-- only a comment", false},
		{dbdriver.Postgres, "", false},
	}
	for _, c := range cases {
		if got := IsWrite(c.kind, c.sql); got != c.write {
			t.Errorf("IsWrite(%s, %q) = %v, want %v", c.kind, c.sql, got, c.write)
		}
	}
}

func TestWriteStatements(t *testing.T) {
	got := WriteStatements(dbdriver.Postgres, "SELECT 1; UPDATE t SET a = 1; BEGIN; DELETE FROM t WHERE id = 2;")
	if len(got) != 2 || got[0] != "UPDATE t SET a = 1" || got[1] != "DELETE FROM t WHERE id = 2" {
		t.Fatalf("WriteStatements = %q", got)
	}
	if got := WriteStatements(dbdriver.SQLite, "SELECT 1"); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}
