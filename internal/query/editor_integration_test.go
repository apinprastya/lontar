package query

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	dbdriver "lontar/internal/driver"
	"lontar/internal/driver/postgres"
	"lontar/internal/testutil"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

// openLikeApp opens a container database the way the app's drivers do:
// PostgreSQL with server-side cancel requests, MySQL without
// multiStatements (the editor splits scripts itself).
func openLikeApp(t *testing.T, target testutil.DB) *sql.DB {
	t.Helper()
	var db *sql.DB
	switch target.Driver {
	case testutil.DriverPostgres:
		pc, err := pgx.ParseConfig(target.DSN)
		if err != nil {
			t.Fatal(err)
		}
		db = postgres.OpenConfig(pc)
	default:
		cfg, err := gomysql.ParseDSN(target.DSN)
		if err != nil {
			t.Fatal(err)
		}
		cfg.MultiStatements = false
		c, err := gomysql.NewConnector(cfg)
		if err != nil {
			t.Fatal(err)
		}
		db = sql.OpenDB(c)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type editorEngine struct {
	name  string
	kind  dbdriver.Kind
	start func(testing.TB) testutil.DB
}

var editorEngines = []editorEngine{
	{testutil.EnginePostgres, dbdriver.Postgres, testutil.SharedPostgres},
	{testutil.EngineMySQL, dbdriver.MySQL, testutil.SharedMySQL},
	{testutil.EngineMariaDB, dbdriver.MariaDB, testutil.SharedMariaDB},
}

func forEachServer(t *testing.T, fn func(t *testing.T, ee editorEngine, e *Editors, db *sql.DB)) {
	for _, ee := range editorEngines {
		t.Run(ee.name, func(t *testing.T) {
			db := openLikeApp(t, ee.start(t))
			e := NewEditors(func(string) (*sql.DB, dbdriver.Kind, error) { return db, ee.kind, nil }, nil)
			e.BasePoolSize = 8
			t.Cleanup(e.CloseAll)
			fn(t, ee, e, db)
		})
	}
}

func TestIntegrationEditorScripts(t *testing.T) {
	forEachServer(t, func(t *testing.T, ee editorEngine, e *Editors, db *sql.DB) {
		id, _ := e.Open("s")
		var script string
		if ee.kind == dbdriver.Postgres {
			script = `DROP TABLE IF EXISTS ed_items;
CREATE TABLE ed_items (id serial PRIMARY KEY, name text);
CREATE OR REPLACE FUNCTION ed_add(a int, b int) RETURNS int AS $$
BEGIN
  -- a semicolon; inside the body
  RETURN a + b;
END;
$$ LANGUAGE plpgsql;
INSERT INTO ed_items (name) VALUES ('a'), ('b;c') RETURNING id;
UPDATE ed_items SET name = upper(name);
SELECT ed_add(2, 3) AS sum, E'it\'s' AS s;`
		} else {
			script = `DROP TABLE IF EXISTS ed_items;
CREATE TABLE ed_items (id int AUTO_INCREMENT PRIMARY KEY, name text);
DROP PROCEDURE IF EXISTS ed_two;
DELIMITER //
CREATE PROCEDURE ed_two()
BEGIN
  SELECT 1 AS one;
  SELECT 'x;y' AS two, 2 AS n;
END //
DELIMITER ;
INSERT INTO ed_items (name) VALUES ('a'), ('b;c');
UPDATE ed_items SET name = upper(name);
CALL ed_two();`
		}
		res := run(t, e, id, script, ExecOptions{})
		for _, s := range res.Statements {
			if s.Status != StatusOK {
				t.Fatalf("%q: %s %+v", s.SQL, s.Status, s.Error)
			}
			if script[s.Start:s.End] != s.SQL {
				t.Fatalf("offsets of %q", s.SQL)
			}
		}
		last := res.Statements[len(res.Statements)-1]
		upd := res.Statements[len(res.Statements)-2]
		if upd.RowsAffected == nil || *upd.RowsAffected != 2 {
			t.Fatalf("update rows affected = %v", upd.RowsAffected)
		}
		if ee.kind == dbdriver.Postgres {
			if len(res.Statements) != 6 {
				t.Fatalf("statements = %d", len(res.Statements))
			}
			ins := res.Statements[3]
			if len(ins.ResultSets) != 1 || len(ins.ResultSets[0].Rows) != 2 {
				t.Fatalf("returning = %+v", ins.ResultSets)
			}
			r := last.ResultSets[0]
			if r.Columns[0].Name != "sum" || r.Columns[0].Type != "int4" || cellString(r.Rows[0][0]) != "5" || r.Rows[0][1].V != "it's" {
				t.Fatalf("select = %+v", r)
			}
		} else {
			if len(res.Statements) != 7 {
				t.Fatalf("statements = %d: %+v", len(res.Statements), res.Statements)
			}
			// CALL returns two result sets.
			if len(last.ResultSets) != 2 || last.ResultSets[0].Columns[0].Name != "one" ||
				last.ResultSets[1].Rows[0][0].V != "x;y" || cellString(last.ResultSets[1].Rows[0][1]) != "2" {
				t.Fatalf("CALL result sets = %+v", last.ResultSets)
			}
			if last.ResultSets[0].ID == last.ResultSets[1].ID {
				t.Fatal("result set IDs must differ")
			}
		}
	})
}

func TestIntegrationEditorErrors(t *testing.T) {
	forEachServer(t, func(t *testing.T, ee editorEngine, e *Editors, db *sql.DB) {
		id, _ := e.Open("s")
		script := "SELECT 1;\nSELECT 2 FROMM dual WHERE;"
		res := run(t, e, id, script, ExecOptions{BaseOffset: 7})
		if statuses(res) != "ok,error" {
			t.Fatalf("statuses = %s", statuses(res))
		}
		err := res.Statements[1].Error
		// Both parse "FROMM" as a column alias, so the error is at "dual".
		want := 7 + strings.Index(script, "dual")
		switch ee.kind {
		case dbdriver.Postgres:
			if err.Code != "42601" {
				t.Fatalf("pg code = %+v", err)
			}
		default:
			if err.Code != "1064" || err.SQLState != "42000" {
				t.Fatalf("mysql code = %+v", err)
			}
		}
		if err.Position == nil || *err.Position != want {
			t.Fatalf("position = %v, want %d (%s)", err.Position, want, err.Message)
		}
		// A runtime error.
		res = run(t, e, id, "SELECT * FROM no_such_table_xyz", ExecOptions{})
		err = res.Statements[0].Error
		if err == nil || (ee.kind == dbdriver.Postgres && err.Code != "42P01") || (ee.kind != dbdriver.Postgres && err.Code != "1146") {
			t.Fatalf("missing table = %+v", err)
		}
	})
}

func TestIntegrationEditorSessionState(t *testing.T) {
	forEachServer(t, func(t *testing.T, ee editorEngine, e *Editors, db *sql.DB) {
		id, _ := e.Open("s")
		// Temporary tables persist across runs.
		run(t, e, id, "CREATE TEMPORARY TABLE ed_tmp (x int); INSERT INTO ed_tmp VALUES (7)", ExecOptions{})
		res := run(t, e, id, "SELECT x FROM ed_tmp", ExecOptions{})
		if res.Statements[0].Status != StatusOK {
			t.Fatalf("temp table lost: %+v", res.Statements[0].Error)
		}
		var setup, check, want string
		if ee.kind == dbdriver.Postgres {
			setup = "CREATE SCHEMA IF NOT EXISTS ed_other; SET search_path = ed_other, public"
			check = "SELECT current_schema()"
			want = "ed_other"
		} else {
			setup = "USE information_schema"
			check = "SELECT DATABASE()"
			want = "information_schema"
		}
		run(t, e, id, setup, ExecOptions{})
		res = run(t, e, id, check, ExecOptions{})
		if got := res.Statements[0].ResultSets[0].Rows[0][0].V; got != want {
			t.Fatalf("session setting lost: %v", got)
		}
		// The pool is unaffected.
		var poolValue sql.NullString
		if err := db.QueryRow(check).Scan(&poolValue); err != nil || poolValue.String == want {
			t.Fatalf("pool sees the editor's setting: %v %v", poolValue, err)
		}
	})
}

func TestIntegrationEditorTransaction(t *testing.T) {
	forEachServer(t, func(t *testing.T, ee editorEngine, e *Editors, db *sql.DB) {
		if _, err := db.Exec("CREATE TABLE IF NOT EXISTS ed_tx (a int)"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("DELETE FROM ed_tx"); err != nil {
			t.Fatal(err)
		}
		id, _ := e.Open("s")
		source := TxSourceHeuristic
		if ee.kind == dbdriver.Postgres {
			source = TxSourceServer
		}
		res := run(t, e, id, "BEGIN; INSERT INTO ed_tx VALUES (1)", ExecOptions{})
		if res.Tx != (TxInfo{State: TxActive, Source: source}) {
			t.Fatalf("tx = %+v", res.Tx)
		}
		// Not visible outside, visible inside on the next run.
		var n int
		if err := db.QueryRow("SELECT count(*) FROM ed_tx").Scan(&n); err != nil || n != 0 {
			t.Fatalf("uncommitted visible: %d %v", n, err)
		}
		res = run(t, e, id, "SELECT count(*) FROM ed_tx", ExecOptions{})
		if cellString(res.Statements[0].ResultSets[0].Rows[0][0]) != "1" {
			t.Fatal("transaction lost between runs")
		}
		if ee.kind == dbdriver.Postgres {
			// An error aborts the PostgreSQL transaction.
			res = run(t, e, id, "SELECT 1/0", ExecOptions{})
			if res.Tx.State != TxFailed || res.Statements[0].Error.Code != "22012" {
				t.Fatalf("failed tx = %+v %+v", res.Tx, res.Statements[0].Error)
			}
		}
		res = run(t, e, id, "ROLLBACK", ExecOptions{})
		if res.Tx.State != TxIdle {
			t.Fatalf("after rollback = %+v", res.Tx)
		}
		// Closing with an open transaction rolls it back.
		run(t, e, id, "BEGIN; INSERT INTO ed_tx VALUES (2)", ExecOptions{})
		if err := e.Close(id); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT count(*) FROM ed_tx").Scan(&n); err != nil || n != 0 {
			t.Fatalf("after close = %d %v", n, err)
		}
	})
}

func TestIntegrationEditorCancel(t *testing.T) {
	forEachServer(t, func(t *testing.T, ee editorEngine, e *Editors, db *sql.DB) {
		id, _ := e.Open("s")
		sleep := "SELECT SLEEP(30)"
		setup := "SET @ed_marker = 'kept'"
		check := "SELECT @ed_marker"
		if ee.kind == dbdriver.Postgres {
			sleep = "SELECT pg_sleep(30)"
			setup = "SET application_name = 'ed_marker'"
			check = "SHOW application_name"
		}
		run(t, e, id, setup, ExecOptions{})

		done := make(chan *ExecResult, 1)
		go func() {
			res, err := e.Execute(context.Background(), id, "sleep-"+ee.name, sleep, ExecOptions{})
			if err != nil {
				t.Error(err)
			}
			done <- res
		}()
		time.Sleep(500 * time.Millisecond)
		start := time.Now()
		if !e.Cancel("sleep-" + ee.name) {
			t.Fatal("request not running")
		}
		var res *ExecResult
		select {
		case res = <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("cancel took too long")
		}
		if took := time.Since(start); took > 3*time.Second {
			t.Fatalf("cancel took %s", took)
		}
		if !res.Cancelled || res.Statements[0].Status != StatusCancelled {
			t.Fatalf("cancelled = %+v", res)
		}
		next := run(t, e, id, check, ExecOptions{})
		got := next.Statements[0].ResultSets[0].Rows[0][0]
		if ee.kind == dbdriver.Postgres {
			// PostgreSQL keeps the connection and its session.
			if next.SessionReset || len(res.Notices) != 0 || got.V != "ed_marker" {
				t.Fatalf("pg session after cancel: reset=%v notices=%v value=%v", next.SessionReset, res.Notices, got)
			}
		} else {
			// MySQL/MariaDB drop the connection: re-pinned and reported.
			if len(res.Notices) != 1 || !next.SessionReset || len(next.Notices) != 1 || got.K != "null" {
				t.Fatalf("mysql after cancel: notices=%v reset=%v next=%v value=%v", res.Notices, next.SessionReset, next.Notices, got)
			}
			if e.Pinned(db) != 1 {
				t.Fatalf("pinned = %d", e.Pinned(db))
			}
		}
	})
}

func TestIntegrationEditorLoadMore(t *testing.T) {
	forEachServer(t, func(t *testing.T, ee editorEngine, e *Editors, db *sql.DB) {
		ctx := context.Background()
		id, _ := e.Open("s")
		big := "SELECT a.n * 100 + b.n AS i FROM (SELECT 0 AS n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4) a CROSS JOIN (SELECT 0 AS n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9) b ORDER BY 1"
		if ee.kind == dbdriver.Postgres {
			big = "SELECT i FROM generate_series(1, 100000) AS i"
		}
		res := run(t, e, id, big, ExecOptions{MaxRows: 10})
		rs := res.Statements[0].ResultSets[0]
		if len(rs.Rows) != 10 || !rs.CanLoadMore {
			t.Fatalf("first = %d %v", len(rs.Rows), rs.CanLoadMore)
		}
		more, err := e.FetchMore(ctx, id, "", rs.ID, 15)
		if err != nil || len(more.Rows) != 15 || !more.Truncated {
			t.Fatalf("more = %+v %v", more, err)
		}
		// The next run closes the open cursor without losing the
		// connection (PostgreSQL cancels the rest of the result).
		start := time.Now()
		res = run(t, e, id, "SELECT 1", ExecOptions{})
		if res.Statements[0].Status != StatusOK || res.SessionReset || time.Since(start) > 5*time.Second {
			t.Fatalf("after cursor = %+v (%s)", res, time.Since(start))
		}
		// Inside a transaction the cursor is drained, keeping it alive.
		run(t, e, id, "BEGIN", ExecOptions{})
		res = run(t, e, id, big, ExecOptions{MaxRows: 5})
		if !res.Statements[0].ResultSets[0].CanLoadMore {
			t.Fatal("no cursor in transaction")
		}
		res = run(t, e, id, "SELECT 2", ExecOptions{})
		if res.Statements[0].Status != StatusOK || res.Tx.State != TxActive {
			t.Fatalf("tx after drain = %+v %+v", res.Tx, res.Statements[0].Error)
		}
		run(t, e, id, "ROLLBACK", ExecOptions{})
	})
}
