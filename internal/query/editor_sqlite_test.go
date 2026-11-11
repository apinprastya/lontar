package query

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	dbdriver "dbedit/internal/driver"
	"dbedit/internal/testutil"
)

// liteEditors returns an editor manager over a fresh SQLite database.
func liteEditors(t *testing.T) (*Editors, *sql.DB) {
	t.Helper()
	db := testutil.Open(t, testutil.SQLiteTemp(t))
	e := NewEditors(func(id string) (*sql.DB, dbdriver.Kind, error) {
		if id != "s1" {
			return nil, "", errors.New("session not found")
		}
		return db, dbdriver.SQLite, nil
	}, nil)
	t.Cleanup(e.CloseAll)
	return e, db
}

func openEditor(t *testing.T, e *Editors) string {
	t.Helper()
	id, err := e.Open("s1")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func run(t *testing.T, e *Editors, id, sqlText string, opts ExecOptions) *ExecResult {
	t.Helper()
	res, err := e.Execute(context.Background(), id, "", sqlText, opts)
	if err != nil {
		t.Fatalf("Execute(%q): %v", sqlText, err)
	}
	return res
}

func statuses(res *ExecResult) string {
	var out []string
	for _, s := range res.Statements {
		out = append(out, s.Status)
	}
	return strings.Join(out, ",")
}

func TestExecuteMixedStatements(t *testing.T) {
	e, _ := liteEditors(t)
	id := openEditor(t, e)
	script := `CREATE TABLE t (a INTEGER PRIMARY KEY, b TEXT);
INSERT INTO t VALUES (1, 'x'), (2, NULL);
SELECT a, b FROM t ORDER BY a;
UPDATE t SET b = 'y' WHERE a = 2;
-- comment only;
SELECT count(*) AS n FROM t;
INSERT INTO t VALUES (3, 'z') RETURNING a`
	res := run(t, e, id, script, ExecOptions{})
	if statuses(res) != "ok,ok,ok,ok,ok,ok" || res.Cancelled || res.SessionReset || len(res.Notices) != 0 {
		t.Fatalf("statuses = %s %+v", statuses(res), res)
	}
	st := res.Statements
	if st[0].RowsAffected != nil || st[1].RowsAffected == nil || *st[1].RowsAffected != 2 || *st[3].RowsAffected != 1 {
		t.Fatalf("rows affected: %v %v %v", st[0].RowsAffected, st[1].RowsAffected, st[3].RowsAffected)
	}
	sel := st[2].ResultSets
	if len(sel) != 1 || len(sel[0].Rows) != 2 || sel[0].Truncated || sel[0].CanLoadMore || sel[0].ID == "" {
		t.Fatalf("select = %+v", sel)
	}
	if c := sel[0].Columns; len(c) != 2 || c[0].Name != "a" || c[0].Type != "integer" || c[1].Name != "b" {
		t.Fatalf("columns = %+v", c)
	}
	if r := sel[0].Rows; r[0][0].V != int64(1) || r[0][1].V != "x" || r[1][1].K != "null" {
		t.Fatalf("rows = %+v", r)
	}
	if st[4].SQL != "SELECT count(*) AS n FROM t" || st[4].ResultSets[0].Rows[0][0].V != int64(2) {
		t.Fatalf("count = %+v", st[4])
	}
	if len(st[5].ResultSets) != 1 || st[5].ResultSets[0].Rows[0][0].V != int64(3) || st[5].RowsAffected != nil {
		t.Fatalf("returning = %+v", st[5])
	}
	// Offsets point into the script.
	for _, s := range st {
		if script[s.Start:s.End] != s.SQL || s.DurationMs < 0 {
			t.Fatalf("offsets %d..%d = %q, want %q", s.Start, s.End, script[s.Start:s.End], s.SQL)
		}
	}
	if res.Tx != (TxInfo{State: TxIdle, Source: TxSourceHeuristic}) || res.DurationMs <= 0 {
		t.Fatalf("tx/duration = %+v %v", res.Tx, res.DurationMs)
	}
	// An empty script runs nothing.
	if res := run(t, e, id, "  -- nothing\n", ExecOptions{}); len(res.Statements) != 0 {
		t.Fatalf("empty = %+v", res.Statements)
	}
}

func TestExecuteStopAndContinueOnError(t *testing.T) {
	e, _ := liteEditors(t)
	id := openEditor(t, e)
	script := "SELECT 1;\nSELEC 2;\nSELECT 3"
	res := run(t, e, id, script, ExecOptions{BaseOffset: 100})
	if statuses(res) != "ok,error,skipped" {
		t.Fatalf("stop: %s", statuses(res))
	}
	err := res.Statements[1].Error
	if err == nil || err.Code != "SQLITE_ERROR" || !strings.Contains(err.Message, "SELEC") || err.Position == nil || *err.Position != 100+10 {
		t.Fatalf("error = %+v pos=%v", err, err.Position)
	}
	if res.FirstError() != err || res.Statements[2].Error != nil || res.Statements[2].Start != 100+19 {
		t.Fatalf("first error / skipped = %+v", res.Statements[2])
	}
	res = run(t, e, id, script, ExecOptions{ContinueOnError: true})
	if statuses(res) != "ok,error,ok" {
		t.Fatalf("continue: %s", statuses(res))
	}
	if (&ExecResult{}).FirstError() != nil {
		t.Fatal("FirstError on success")
	}
	// Unknown names point at the identifier.
	res = run(t, e, id, "SELECT 1;\nSELECT * FROM missing_table", ExecOptions{ContinueOnError: true})
	if e := res.Statements[1].Error; e == nil || e.Position == nil || *e.Position != 10+14 {
		t.Fatalf("missing table = %+v", e)
	}
	res = run(t, e, id, "SELECT nme FROM (SELECT 1 AS name)", ExecOptions{})
	if e := res.Statements[0].Error; e == nil || e.Position == nil || *e.Position != 7 {
		t.Fatalf("missing column = %+v", e)
	}
	// Errors without a usable name have no position.
	res = run(t, e, id, "SELECT abs(1, 2)", ExecOptions{})
	if e := res.Statements[0].Error; e == nil || e.Position != nil {
		t.Fatalf("no position = %+v", e)
	}
}

func TestExecuteUnicodeOffsets(t *testing.T) {
	e, _ := liteEditors(t)
	id := openEditor(t, e)
	// "ä" is one UTF-16 unit (2 bytes), "😀" two units (4 bytes).
	script := "SELECT 'ä😀';\nSELEC 'x'"
	res := run(t, e, id, script, ExecOptions{BaseOffset: 5})
	s0, s1 := res.Statements[0], res.Statements[1]
	if s0.Start != 5 || s0.End != 5+12 || s1.Start != 5+14 {
		t.Fatalf("utf16 offsets: %d..%d, %d", s0.Start, s0.End, s1.Start)
	}
	if p := s1.Error.Position; p == nil || *p != 5+14 {
		t.Fatalf("error position = %v", p)
	}
	if s0.ResultSets[0].Rows[0][0].V != "ä😀" {
		t.Fatalf("value = %v", s0.ResultSets[0].Rows[0][0])
	}
}

const series = "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < %d) SELECT i FROM n"

func seriesSQL(n int) string { return strings.Replace(series, "%d", strconv.Itoa(n), 1) }

func TestExecuteRowCapAndLoadMore(t *testing.T) {
	ctx := context.Background()
	e, _ := liteEditors(t)
	id := openEditor(t, e)

	res := run(t, e, id, seriesSQL(2500), ExecOptions{})
	rs := res.Statements[0].ResultSets[0]
	if len(rs.Rows) != DefaultMaxRows || !rs.Truncated || !rs.CanLoadMore {
		t.Fatalf("first page: %d rows, truncated=%v canLoadMore=%v", len(rs.Rows), rs.Truncated, rs.CanLoadMore)
	}
	more, err := e.FetchMore(ctx, id, "", rs.ID, 1000)
	if err != nil || len(more.Rows) != 1000 || !more.Truncated || more.Rows[0][0].V != int64(1001) {
		t.Fatalf("FetchMore 1 = %d rows %v, %v", len(more.Rows), more.Rows[0], err)
	}
	more, err = e.FetchMore(ctx, id, "", rs.ID, 1000)
	if err != nil || len(more.Rows) != 500 || more.Truncated || more.CanLoadMore || more.Rows[499][0].V != int64(2500) {
		t.Fatalf("FetchMore 2 = %d rows truncated=%v, %v", len(more.Rows), more.Truncated, err)
	}
	if _, err := e.FetchMore(ctx, id, "", rs.ID, 1000); !errors.Is(err, ErrNoCursor) {
		t.Fatalf("exhausted FetchMore = %v", err)
	}

	// A custom cap; exactly the cap is not truncated.
	res = run(t, e, id, seriesSQL(10), ExecOptions{MaxRows: 10})
	if rs := res.Statements[0].ResultSets[0]; len(rs.Rows) != 10 || rs.Truncated {
		t.Fatalf("exact cap = %d %v", len(rs.Rows), rs.Truncated)
	}
	// Truncated results that are not last cannot load more.
	res = run(t, e, id, seriesSQL(30)+"; SELECT 1", ExecOptions{MaxRows: 20})
	if rs := res.Statements[0].ResultSets[0]; len(rs.Rows) != 20 || !rs.Truncated || rs.CanLoadMore {
		t.Fatalf("non-last truncated = %d %v %v", len(rs.Rows), rs.Truncated, rs.CanLoadMore)
	}
	if statuses(res) != "ok,ok" {
		t.Fatalf("statuses %s", statuses(res))
	}
	// A new run closes the open cursor.
	res = run(t, e, id, seriesSQL(30), ExecOptions{MaxRows: 20})
	open := res.Statements[0].ResultSets[0]
	run(t, e, id, "SELECT 1", ExecOptions{})
	if _, err := e.FetchMore(ctx, id, "", open.ID, 5); !errors.Is(err, ErrNoCursor) {
		t.Fatalf("FetchMore after rerun = %v", err)
	}
	// Wrong result ID.
	res = run(t, e, id, seriesSQL(30), ExecOptions{MaxRows: 20})
	if _, err := e.FetchMore(ctx, id, "", "nope", 5); !errors.Is(err, ErrNoCursor) {
		t.Fatalf("FetchMore wrong id = %v", err)
	}
	// Batch smaller than the rest.
	more, err = e.FetchMore(ctx, id, "", res.Statements[0].ResultSets[0].ID, 4)
	if err != nil || len(more.Rows) != 4 || !more.Truncated {
		t.Fatalf("small batch = %+v %v", more, err)
	}
	if _, err := e.FetchMore(ctx, "nope", "", "x", 1); !errors.Is(err, ErrEditorNotFound) {
		t.Fatalf("unknown editor = %v", err)
	}
	if NormalizeMaxRows(0) != DefaultMaxRows || NormalizeMaxRows(1e9) != MaxMaxRows || NormalizeMaxRows(7) != 7 {
		t.Fatal("NormalizeMaxRows")
	}
}

func TestExecutePinnedConnection(t *testing.T) {
	e, db := liteEditors(t)
	e.BasePoolSize = 8
	id := openEditor(t, e)
	res := run(t, e, id, "CREATE TEMP TABLE scratch (x); INSERT INTO scratch VALUES (42)", ExecOptions{})
	if statuses(res) != "ok,ok" {
		t.Fatalf("create temp: %s %+v", statuses(res), res.Statements)
	}
	if e.Pinned(db) != 1 || db.Stats().MaxOpenConnections != 9 {
		t.Fatalf("pinned=%d max=%d", e.Pinned(db), db.Stats().MaxOpenConnections)
	}
	// Same editor, next run: the temp table is still there.
	res = run(t, e, id, "SELECT x FROM scratch", ExecOptions{})
	if res.Statements[0].Status != StatusOK || res.Statements[0].ResultSets[0].Rows[0][0].V != int64(42) {
		t.Fatalf("temp table lost: %+v", res.Statements[0])
	}
	// Other connections (the pool, another editor) do not see it.
	if _, err := db.Exec("SELECT x FROM scratch"); err == nil {
		t.Fatal("temp table visible in the pool")
	}
	other := openEditor(t, e)
	if res := run(t, e, other, "SELECT x FROM scratch", ExecOptions{}); res.Statements[0].Status != StatusError {
		t.Fatal("temp table visible in another editor")
	}
	if e.Pinned(db) != 2 || e.Len() != 2 {
		t.Fatalf("pinned=%d len=%d", e.Pinned(db), e.Len())
	}
	if err := e.Close(other); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(id); err != nil {
		t.Fatal(err)
	}
	if e.Pinned(db) != 0 || db.Stats().MaxOpenConnections != 8 {
		t.Fatalf("after close pinned=%d max=%d", e.Pinned(db), db.Stats().MaxOpenConnections)
	}
}

func TestExecuteTransactionAcrossRuns(t *testing.T) {
	e, db := liteEditors(t)
	if _, err := db.Exec("CREATE TABLE t (a)"); err != nil {
		t.Fatal(err)
	}
	id := openEditor(t, e)
	res := run(t, e, id, "BEGIN; INSERT INTO t VALUES (1)", ExecOptions{})
	if res.Tx.State != TxActive {
		t.Fatalf("tx after BEGIN = %+v", res.Tx)
	}
	if tx, _ := e.TxState(id); tx.State != TxActive || tx.Source != TxSourceHeuristic {
		t.Fatalf("TxState = %+v", tx)
	}
	res = run(t, e, id, "SELECT count(*) FROM t", ExecOptions{})
	if res.Statements[0].ResultSets[0].Rows[0][0].V != int64(1) || res.Tx.State != TxActive {
		t.Fatal("insert not visible inside the transaction")
	}
	res = run(t, e, id, "ROLLBACK", ExecOptions{})
	if res.Tx.State != TxIdle {
		t.Fatalf("tx after ROLLBACK = %+v", res.Tx)
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil || n != 0 {
		t.Fatalf("rolled back rows = %d, %v", n, err)
	}

	// Closing an editor with an open transaction rolls it back.
	run(t, e, id, "BEGIN; INSERT INTO t VALUES (2)", ExecOptions{})
	if err := e.Close(id); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil || n != 0 {
		t.Fatalf("after close rows = %d, %v", n, err)
	}
	if err := e.Close(id); !errors.Is(err, ErrEditorNotFound) {
		t.Fatalf("second close = %v", err)
	}
	if _, err := e.Execute(context.Background(), id, "", "SELECT 1", ExecOptions{}); !errors.Is(err, ErrEditorNotFound) {
		t.Fatalf("execute after close = %v", err)
	}
	if _, err := e.TxState(id); !errors.Is(err, ErrEditorNotFound) {
		t.Fatalf("TxState after close = %v", err)
	}
	// COMMIT keeps the row.
	id = openEditor(t, e)
	run(t, e, id, "BEGIN; INSERT INTO t VALUES (3); COMMIT", ExecOptions{})
	if err := db.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil || n != 1 {
		t.Fatalf("committed rows = %d, %v", n, err)
	}
}

func TestExecuteCancelAndBusy(t *testing.T) {
	e, _ := liteEditors(t)
	id := openEditor(t, e)
	slow := "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n) SELECT count(*) FROM n"

	done := make(chan *ExecResult)
	go func() {
		res, err := e.Execute(context.Background(), id, "req-1", "SELECT 1; "+slow+"; SELECT 2", ExecOptions{})
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !e.Cancel("req-1") {
		if time.Now().After(deadline) {
			t.Fatal("run never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// A busy editor rejects a second run.
	if _, err := e.Execute(context.Background(), id, "", "SELECT 1", ExecOptions{}); err != nil && !errors.Is(err, ErrEditorBusy) {
		t.Fatalf("concurrent run = %v", err)
	}
	var res *ExecResult
	select {
	case res = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not stop the query")
	}
	if !res.Cancelled || statuses(res) != "ok,cancelled,skipped" || res.Statements[1].Error.Message != "Query cancelled" {
		t.Fatalf("cancelled run = %s %+v", statuses(res), res)
	}
	// SQLite keeps the connection: no reset on the next run.
	res = run(t, e, id, "SELECT 3", ExecOptions{})
	if res.SessionReset || res.Statements[0].Status != StatusOK {
		t.Fatalf("after cancel = %+v", res)
	}
	// A context that is already cancelled stops before the first statement.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if res, err := e.Execute(ctx, id, "", "SELECT 1", ExecOptions{}); err == nil && res.Statements[0].Status != StatusCancelled {
		t.Fatalf("pre-cancelled = %+v %v", res, err)
	}
}

func TestFetchMoreCancel(t *testing.T) {
	e, _ := liteEditors(t)
	id := openEditor(t, e)
	res := run(t, e, id, "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n) SELECT i FROM n", ExecOptions{MaxRows: 5})
	rs := res.Statements[0].ResultSets[0]
	if !rs.CanLoadMore {
		t.Fatal("expected an open cursor")
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := e.FetchMore(ctx, id, "fm", rs.ID, MaxMaxRows)
		if err == nil {
			t.Error("FetchMore of an infinite result should not finish")
		}
	}()
	time.Sleep(20 * time.Millisecond)
	// Large batches finish quickly on SQLite; cancel anyway and make sure
	// the editor stays usable.
	cancel()
	wg.Wait()
	if res := run(t, e, id, "SELECT 1", ExecOptions{}); res.Statements[0].Status != StatusOK {
		t.Fatalf("after FetchMore cancel = %+v", res)
	}
}

func TestEditorLifecycle(t *testing.T) {
	e, db := liteEditors(t)
	now := time.Unix(1_000_000, 0)
	e.now = func() time.Time { return now }
	if _, err := e.Open("nope"); err == nil {
		t.Fatal("Open with an unknown session should fail")
	}
	id := openEditor(t, e)

	// Idle cursor.
	res := run(t, e, id, seriesSQL(50), ExecOptions{MaxRows: 10})
	rs := res.Statements[0].ResultSets[0]
	now = now.Add(DefaultCursorIdle)
	e.Sweep()
	if _, err := e.FetchMore(context.Background(), id, "", rs.ID, 5); !errors.Is(err, ErrNoCursor) {
		t.Fatalf("cursor survived sweep: %v", err)
	}
	// Idle connection: released with a notice on the next run.
	run(t, e, id, "CREATE TEMP TABLE tt (x)", ExecOptions{})
	now = now.Add(DefaultEditorIdle - time.Minute)
	e.Sweep()
	if e.Pinned(db) != 1 {
		t.Fatal("released too early")
	}
	now = now.Add(time.Minute)
	e.Sweep()
	if e.Pinned(db) != 0 {
		t.Fatal("idle connection not released")
	}
	res = run(t, e, id, "SELECT * FROM tt", ExecOptions{})
	if !res.SessionReset || res.Notices[0] != msgIdle || res.Statements[0].Status != StatusError {
		t.Fatalf("after idle = %+v", res)
	}

	// Database switch: the pool changes.
	run(t, e, id, "SELECT 1", ExecOptions{})
	e.SessionChanged("s1")
	res = run(t, e, id, "SELECT 1", ExecOptions{})
	if !res.SessionReset || res.Notices[0] != msgSwitched {
		t.Fatalf("after switch = %+v", res)
	}
	// The resolver returns another pool (switch noticed lazily).
	db2 := testutil.Open(t, testutil.SQLiteTemp(t))
	e.resolve = func(string) (*sql.DB, dbdriver.Kind, error) { return db2, dbdriver.SQLite, nil }
	res = run(t, e, id, "SELECT 1", ExecOptions{})
	if !res.SessionReset || res.Notices[0] != msgSwitched || e.Pinned(db) != 0 || e.Pinned(db2) != 1 {
		t.Fatalf("pool swap = %+v", res)
	}
	// The session is gone.
	e.resolve = func(string) (*sql.DB, dbdriver.Kind, error) { return nil, "", errors.New("session not found") }
	if _, err := e.Execute(context.Background(), id, "", "SELECT 1", ExecOptions{}); err == nil {
		t.Fatal("Execute without a session should fail")
	}
	// SessionClosed closes the editors of that session only.
	e.resolve = func(string) (*sql.DB, dbdriver.Kind, error) { return db2, dbdriver.SQLite, nil }
	keep, _ := e.Open("s2")
	e.SessionClosed("s1")
	if _, err := e.TxState(id); !errors.Is(err, ErrEditorNotFound) {
		t.Fatal("editor of a closed session still open")
	}
	if _, err := e.TxState(keep); err != nil {
		t.Fatal("editor of another session closed")
	}
	stop := e.StartJanitor(time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	stop()
	stop()
}

func TestExecuteReconnectsDeadConnection(t *testing.T) {
	e, _ := liteEditors(t)
	id := openEditor(t, e)
	run(t, e, id, "SELECT 1", ExecOptions{})
	ed, _ := e.get(id)
	_ = ed.conn.Close() // the connection is gone behind the editor's back
	res := run(t, e, id, "SELECT 2", ExecOptions{})
	if res.Statements[0].Status != StatusOK || !res.SessionReset || res.Notices[0] != msgReconnect {
		t.Fatalf("reconnect = %+v", res)
	}
}

func TestReturnsRows(t *testing.T) {
	for words, want := range map[string]bool{
		"":                          true,
		"SELECT":                    true,
		"WITH X AS SELECT":          true,
		"PRAGMA TABLE_INFO":         true,
		"INSERT INTO T VALUES":      false,
		"INSERT INTO T RETURNING A": true,
		"UPDATE T SET":              false,
		"CREATE TABLE":              false,
		"CALL P":                    true,
	} {
		if got := returnsRows(strings.Fields(words)); got != want {
			t.Errorf("returnsRows(%q) = %v", words, got)
		}
	}
}
