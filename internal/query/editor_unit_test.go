package query

import (
	"errors"
	"fmt"
	"testing"

	dbdriver "dbedit/internal/driver"
	"dbedit/internal/sqlsplit"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestTxTracker(t *testing.T) {
	type step struct {
		sql  string
		err  error
		open bool
	}
	deadlock := &gomysql.MySQLError{Number: 1213, Message: "Deadlock found"}
	other := &gomysql.MySQLError{Number: 1062, Message: "Duplicate entry"}
	tests := []struct {
		name  string
		kind  dbdriver.Kind
		steps []step
	}{
		{"mysql begin commit", dbdriver.MySQL, []step{{"BEGIN", nil, true}, {"INSERT INTO t VALUES (1)", nil, true}, {"COMMIT", nil, false}}},
		{"mysql start transaction rollback", dbdriver.MariaDB, []step{{"START TRANSACTION READ ONLY", nil, true}, {"ROLLBACK WORK", nil, false}}},
		{"rollback to savepoint keeps", dbdriver.MySQL, []step{{"BEGIN", nil, true}, {"SAVEPOINT a", nil, true}, {"ROLLBACK TO a", nil, true}, {"ROLLBACK WORK TO SAVEPOINT a", nil, true}}},
		{"mysql ddl commits", dbdriver.MySQL, []step{{"BEGIN", nil, true}, {"CREATE TABLE x (a int)", nil, false}}},
		{"mysql temporary does not commit", dbdriver.MySQL, []step{{"BEGIN", nil, true}, {"CREATE TEMPORARY TABLE x (a int)", nil, true}, {"DROP TEMPORARY TABLE x", nil, true}}},
		{"mysql savepoint does not begin", dbdriver.MySQL, []step{{"SAVEPOINT a", nil, false}}},
		{"mysql autocommit off", dbdriver.MySQL, []step{{"SET autocommit = 0", nil, false}, {"SELECT 1", nil, true}, {"COMMIT", nil, false}, {"UPDATE t SET a = 1", nil, true}, {"SET @@session.autocommit=1", nil, false}, {"SELECT 1", nil, false}}},
		{"mysql autocommit off words", dbdriver.MySQL, []step{{"set session autocommit = 'OFF'", nil, false}, {"DELETE FROM t", nil, true}, {"SET autocommit=ON", nil, false}}},
		{"mysql other set", dbdriver.MySQL, []step{{"BEGIN", nil, true}, {"SET NAMES utf8mb4", nil, true}}},
		{"mysql deadlock rolls back", dbdriver.MySQL, []step{{"BEGIN", nil, true}, {"UPDATE t SET a = 1", other, true}, {"UPDATE t SET a = 1", deadlock, false}}},
		{"sqlite begin end", dbdriver.SQLite, []step{{"BEGIN IMMEDIATE", nil, true}, {"END TRANSACTION", nil, false}}},
		{"sqlite savepoint begins", dbdriver.SQLite, []step{{"SAVEPOINT a", nil, true}, {"ROLLBACK", nil, false}}},
		{"sqlite ddl in tx", dbdriver.SQLite, []step{{"BEGIN", nil, true}, {"CREATE TABLE x (a)", nil, true}, {"COMMIT", nil, false}}},
		{"sqlite error ignored", dbdriver.SQLite, []step{{"BEGIN", nil, true}, {"COMMIT", errors.New("busy"), true}}},
		{"empty", dbdriver.SQLite, []step{{"-- nothing", nil, false}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var tr txTracker
			for i, s := range tc.steps {
				tr.observe(tc.kind, s.sql, sqlsplit.Words(tc.kind, s.sql, 0), s.err)
				if tr.open != s.open {
					t.Fatalf("step %d %q: open = %v, want %v", i, s.sql, tr.open, s.open)
				}
			}
		})
	}
}

func TestErrorPositionAndCodes(t *testing.T) {
	pos := func(b int) int { return 1000 + b }
	stmt := "SELECT 'ä'\nFROMM t WHERE"

	// PostgreSQL: 1-based character position.
	pg := &pgconn.PgError{Code: "42601", Message: "syntax error at or near \"FROMM\"", Detail: "d", Hint: "h", Position: 12}
	e := toExecError(fmt.Errorf("wrapped: %w", pg), stmt, pos)
	if e.Code != "42601" || e.Detail != "d" || e.Hint != "h" || e.Message != pg.Message || e.Position == nil || *e.Position != 1000+12 {
		t.Fatalf("pg = %+v pos=%v", e, e.Position)
	}
	if p, ok := errorPosition(&pgconn.PgError{Position: 999}, stmt); !ok || p != len(stmt) {
		t.Fatalf("pg past end = %d %v", p, ok)
	}
	if _, ok := errorPosition(&pgconn.PgError{}, stmt); ok {
		t.Fatal("pg without position")
	}

	// MySQL: line + quoted rest of the statement.
	my := &gomysql.MySQLError{Number: 1064, SQLState: [5]byte{'4', '2', '0', '0', '0'},
		Message: "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near 'FROMM t WHERE' at line 2"}
	e = toExecError(my, stmt, pos)
	if e.Code != "1064" || e.SQLState != "42000" || e.Position == nil || *e.Position != 1000+12 {
		t.Fatalf("mysql = %+v pos=%v", e, e.Position)
	}
	for msg, want := range map[string]int{
		"... near '' at line 1":                 len(stmt),
		"... near 'not in statement' at line 2": 12,
		"... near 'FROMM t WHERE' at line 9":    12,
	} {
		p, ok := errorPosition(&gomysql.MySQLError{Message: msg}, stmt)
		if !ok || p != want {
			t.Errorf("mysql %q = %d %v, want %d", msg, p, ok, want)
		}
	}
	long := "SELECT * FROM t WHERE a = 1 AND b = 2 AND c = 3 AND d = 4 AND e = 5 ORDER BY 1"
	if p, ok := errorPosition(&gomysql.MySQLError{Message: "near 'a = 1 AND b = 2 AND c = 3 AND d = 4 AND e = 5 ORDER BY 1' at line 1"}, long); !ok || p != 22 {
		t.Fatalf("mysql long near = %d %v", p, ok)
	}
	if _, ok := errorPosition(&gomysql.MySQLError{Message: "Table 'x' doesn't exist"}, stmt); ok {
		t.Fatal("mysql without position")
	}
	// Other errors: message only.
	e = toExecError(errors.New("boom"), stmt, pos)
	if e.Message != "boom" || e.Code != "" || e.Position != nil {
		t.Fatalf("plain = %+v", e)
	}
}

func TestIndexWord(t *testing.T) {
	stmt := "SELECT nme, name_x FROM main.items WHERE x.nme = 1"
	for name, want := range map[string]int{"nme": 7, "NME": 7, "main.items": 24, "other.items": 29, "missing": -1, "": -1, "x.": -1} {
		if got := indexWord(stmt, name); got != want {
			t.Errorf("indexWord(%q) = %d, want %d", name, got, want)
		}
	}
}
