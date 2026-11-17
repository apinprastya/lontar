package query

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	dbdriver "lontar/internal/driver"
	"lontar/internal/testutil"
)

// forEachExplainEngine runs fn on every engine with the browse fixture and
// an editor manager over it.
func forEachExplainEngine(t *testing.T, fn func(t *testing.T, kind dbdriver.Kind, e *Editors, db *sql.DB)) {
	all := append([]editorEngine{{testutil.EngineSQLite, dbdriver.SQLite, testutil.SQLiteTemp}}, editorEngines...)
	for _, ee := range all {
		t.Run(ee.name, func(t *testing.T) {
			target := ee.start(t)
			var db *sql.DB
			if ee.kind == dbdriver.SQLite {
				db = testutil.Open(t, target)
			} else {
				db = openLikeApp(t, target)
			}
			testutil.SeedBrowse(t, ee.name, target, db)
			e := NewEditors(func(string) (*sql.DB, dbdriver.Kind, error) { return db, ee.kind, nil }, nil)
			t.Cleanup(e.CloseAll)
			fn(t, ee.kind, e, db)
		})
	}
}

const explainJoin = "SELECT n.label, i.name FROM numbers n JOIN items i ON i.id = n.grp WHERE n.n < 500 ORDER BY n.label"

func explainOK(t *testing.T, e *Editors, id, sqlText string, opts ExplainOptions) *Plan {
	t.Helper()
	p, err := e.Explain(context.Background(), id, "", sqlText, opts)
	if err != nil {
		t.Fatalf("Explain(%q): %v", sqlText, err)
	}
	if p.Error != nil {
		t.Fatalf("Explain(%q): %+v", sqlText, p.Error)
	}
	return p
}

func walkNodes(ns []PlanNode, fn func(PlanNode)) {
	for _, n := range ns {
		fn(n)
		walkNodes(n.Children, fn)
	}
}

func label1(t *testing.T, db *sql.DB) string {
	t.Helper()
	var s string
	if err := db.QueryRow("SELECT label FROM numbers WHERE n = 1").Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestIntegrationExplain(t *testing.T) {
	forEachExplainEngine(t, func(t *testing.T, kind dbdriver.Kind, e *Editors, db *sql.DB) {
		id, _ := e.Open("s")

		// A join: a tree with the tables in it.
		p := explainOK(t, e, id, explainJoin, ExplainOptions{})
		if len(p.Nodes) == 0 || p.Raw == "" || p.Analyzed || p.Engine != string(kind) || !strings.HasSuffix(p.SQL, explainJoin) || len(p.Notices) != 0 {
			t.Fatalf("plan = %+v", p)
		}
		objects := ""
		walkNodes(p.Nodes, func(n PlanNode) { objects += " " + n.Object })
		if !strings.Contains(objects, "n") || !strings.Contains(objects, "i") {
			t.Fatalf("objects = %q\n%s", objects, outline(p.Nodes))
		}

		// Analyze: actual rows (no Analyze on SQLite).
		if kind == dbdriver.SQLite {
			if _, err := e.Explain(context.Background(), id, "", explainJoin, ExplainOptions{Analyze: true}); explainCode(err) != ExplainAnalyzeUnsupported {
				t.Fatalf("sqlite analyze = %v", err)
			}
		} else {
			p = explainOK(t, e, id, explainJoin, ExplainOptions{Analyze: true})
			actual := false
			walkNodes(p.Nodes, func(n PlanNode) { actual = actual || n.ActualRows != nil })
			if !p.Analyzed || !actual {
				t.Fatalf("analyze = %+v\n%s", p, outline(p.Nodes))
			}
		}

		// Analyze of a write is refused before anything runs.
		upd := "UPDATE numbers SET label = 'changed' WHERE n = 1"
		before := label1(t, db)
		if kind != dbdriver.SQLite {
			if _, err := e.Explain(context.Background(), id, "", upd, ExplainOptions{Analyze: true}); explainCode(err) != ExplainAnalyzeWrite {
				t.Fatalf("analyze update = %v", err)
			}
		}
		// Plain Explain of a write changes nothing.
		p = explainOK(t, e, id, upd, ExplainOptions{})
		if len(p.Nodes) == 0 || label1(t, db) != before {
			t.Fatalf("explain update: %+v, label %q", p, label1(t, db))
		}

		// The editor's session applies: a temporary table created by a run.
		run(t, e, id, "CREATE TEMPORARY TABLE explain_tmp (a INT)", ExecOptions{})
		explainOK(t, e, id, "SELECT * FROM explain_tmp", ExplainOptions{})

		// An error points into the editor (BaseOffset applied).
		bad := "SELECT * FROM numbers ORDER BY BY n"
		p, err := e.Explain(context.Background(), id, "", "  "+bad, ExplainOptions{BaseOffset: 100})
		if err != nil || p.Error == nil {
			t.Fatalf("syntax error: %+v %v", p, err)
		}
		want := 100 + 2 + strings.LastIndex(bad, "BY")
		if kind == dbdriver.SQLite || kind == dbdriver.Postgres {
			want = 100 + 2 + len(bad) - 1 // near "n" (BY is a column name there)
		}
		if p.Error.Position == nil || *p.Error.Position != want {
			t.Fatalf("position = %v, want %d (%s)", p.Error.Position, want, p.Error.Message)
		}

		// Validation errors and unknown editors.
		if _, err := e.Explain(context.Background(), id, "", "SELECT 1; SELECT 2", ExplainOptions{}); explainCode(err) != ExplainOneStatement {
			t.Fatalf("two statements = %v", err)
		}
		if _, err := e.Explain(context.Background(), "nope", "", "SELECT 1", ExplainOptions{}); !errors.Is(err, ErrEditorNotFound) {
			t.Fatalf("unknown editor = %v", err)
		}

		// A cancelled context reports a cancelled plan.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		p, err = e.Explain(ctx, id, "", explainJoin, ExplainOptions{})
		if err == nil && (!p.Cancelled || p.Error == nil) {
			t.Fatalf("pre-cancelled = %+v", p)
		}
	})
}

func TestIntegrationExplainCancel(t *testing.T) {
	forEachServer(t, func(t *testing.T, ee editorEngine, e *Editors, db *sql.DB) {
		id, _ := e.Open("s")
		slow := "SELECT SLEEP(30)"
		if ee.kind == dbdriver.Postgres {
			slow = "SELECT pg_sleep(30)"
		}
		done := make(chan *Plan, 1)
		go func() {
			p, err := e.Explain(context.Background(), id, "explain-"+ee.name, slow, ExplainOptions{Analyze: true})
			if err != nil {
				t.Error(err)
			}
			done <- p
		}()
		time.Sleep(500 * time.Millisecond)
		start := time.Now()
		if !e.Cancel("explain-" + ee.name) {
			t.Fatal("explain not running")
		}
		select {
		case p := <-done:
			if p == nil || !p.Cancelled || p.Error == nil || p.Error.Message != "Query cancelled" {
				t.Fatalf("cancelled = %+v", p)
			}
			if took := time.Since(start); took > 3*time.Second {
				t.Fatalf("cancel took %s", took)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("cancel did not stop the explain")
		}
		// The editor still works (MySQL reconnects and says so).
		p := explainOK(t, e, id, "SELECT 1", ExplainOptions{})
		if ee.kind != dbdriver.Postgres && !p.SessionReset {
			t.Fatalf("after cancel = %+v", p)
		}
	})
}

func TestExplainSQLiteSession(t *testing.T) {
	e, db := liteEditors(t)
	id := openEditor(t, e)
	run(t, e, id, "CREATE TABLE t (a INTEGER PRIMARY KEY, b TEXT); CREATE INDEX t_b ON t (b)", ExecOptions{})
	p := explainOK(t, e, id, "SELECT * FROM t WHERE b = 'x' AND a IN (SELECT a FROM t)", ExplainOptions{})
	if !strings.HasPrefix(p.Raw, "QUERY PLAN\n") || p.RawFormat != PlanFormatText || p.Tx.State != TxIdle {
		t.Fatalf("plan = %+v", p)
	}
	// A truncated result's cursor is closed by Explain.
	run(t, e, id, "INSERT INTO t (b) VALUES ('a'), ('b'), ('c')", ExecOptions{})
	res := run(t, e, id, "SELECT * FROM t", ExecOptions{MaxRows: 1})
	if !res.Statements[0].ResultSets[0].CanLoadMore {
		t.Fatal("no cursor")
	}
	explainOK(t, e, id, "SELECT 1", ExplainOptions{})
	if _, err := e.FetchMore(context.Background(), id, "", res.Statements[0].ResultSets[0].ID, 10); !errors.Is(err, ErrNoCursor) {
		t.Fatalf("cursor after explain = %v", err)
	}
	// An unknown session fails before anything runs.
	e.resolve = func(string) (*sql.DB, dbdriver.Kind, error) { return nil, "", errors.New("session gone") }
	if _, err := e.Explain(context.Background(), id, "", "SELECT 1", ExplainOptions{}); err == nil || err.Error() != "session gone" {
		t.Fatalf("resolve error = %v", err)
	}
	e.resolve = func(string) (*sql.DB, dbdriver.Kind, error) { return db, dbdriver.SQLite, nil }
	// A busy editor rejects Explain.
	ed, _ := e.get(id)
	ed.run.Lock()
	if _, err := e.Explain(context.Background(), id, "", "SELECT 1", ExplainOptions{}); !errors.Is(err, ErrEditorBusy) {
		t.Fatalf("busy = %v", err)
	}
	ed.run.Unlock()
	// A dead connection is replaced, and the reset is reported.
	_ = ed.conn.Close()
	p = explainOK(t, e, id, "SELECT 1", ExplainOptions{})
	if !p.SessionReset || len(p.Notices) != 1 {
		t.Fatalf("after drop = %+v", p)
	}
	// A switched pool releases the old connection first.
	other := testutil.Open(t, testutil.SQLiteTemp(t))
	e.resolve = func(string) (*sql.DB, dbdriver.Kind, error) { return other, dbdriver.SQLite, nil }
	p = explainOK(t, e, id, "SELECT 1", ExplainOptions{})
	if !p.SessionReset {
		t.Fatalf("after switch = %+v", p)
	}
}
