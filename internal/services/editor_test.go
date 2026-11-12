package services

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lontar/internal/conn"
	"lontar/internal/driver"
	"lontar/internal/driver/all"
	"lontar/internal/history"
	"lontar/internal/query"
)

func newEditorService(t *testing.T) (*EditorService, *conn.Manager, string, *history.Store) {
	t.Helper()
	m, id := openSQLiteSession(t)
	hist := history.New(t.TempDir())
	svc := NewEditorService(m, all.Registry(), hist)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, m, id, hist
}

func TestEditorServiceExecuteAndHistory(t *testing.T) {
	ctx := context.Background()
	svc, m, sid, hist := newEditorService(t)
	sess, _ := m.Get(sid)

	eid, err := svc.OpenEditor(sid)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Execute(ctx, eid, "r1", "SELECT * FROM t ORDER BY id; UPDATE t SET name = 'z' WHERE id < 3", query.ExecOptions{MaxRows: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Statements) != 2 || len(res.Statements[0].ResultSets[0].Rows) != 2 || !res.Statements[0].ResultSets[0].Truncated {
		t.Fatalf("result = %+v", res.Statements)
	}
	if _, err := svc.Execute(ctx, eid, "r2", "SELEC 1", query.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	// Nothing to run: not recorded.
	if _, err := svc.Execute(ctx, eid, "r3", "-- nothing", query.ExecOptions{}); err != nil {
		t.Fatal(err)
	}

	hs := NewHistoryService(hist)
	entries, err := hs.List(ctx, sess.ConnectionID, "", 10, 0)
	if err != nil || len(entries) != 2 {
		t.Fatalf("history = %+v, %v", entries, err)
	}
	if !entries[0].IsError || entries[0].Error == "" || entries[0].SQL != "SELEC 1" {
		t.Fatalf("error entry = %+v", entries[0])
	}
	ok := entries[1]
	if ok.Database != "main" || ok.RowCount == nil || *ok.RowCount != 2 || ok.RowsAffected == nil || *ok.RowsAffected != 2 || ok.IsError {
		t.Fatalf("ok entry = %+v", ok)
	}
	if found, _ := hs.List(ctx, sess.ConnectionID, "UPDATE", 10, 0); len(found) != 1 {
		t.Fatalf("search = %+v", found)
	}
	if err := hs.Delete(ctx, ok.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := hs.Clear(ctx, sess.ConnectionID); err != nil || n != 1 {
		t.Fatalf("Clear = %d, %v", n, err)
	}

	tx, err := svc.TxState(eid)
	if err != nil || tx.State != query.TxIdle {
		t.Fatalf("TxState = %+v, %v", tx, err)
	}
	if svc.Cancel("nope") {
		t.Fatal("Cancel of an unknown request")
	}
	res, _ = svc.Execute(ctx, eid, "", "SELECT * FROM t ORDER BY id", query.ExecOptions{MaxRows: 1})
	more, err := svc.FetchMore(ctx, eid, "", res.Statements[0].ResultSets[0].ID, 10)
	if err != nil || len(more.Rows) != 2 || more.Truncated {
		t.Fatalf("FetchMore = %+v, %v", more, err)
	}
	if err := svc.CloseEditor(eid); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(ctx, eid, "", "SELECT 1", query.ExecOptions{}); !errors.Is(err, query.ErrEditorNotFound) {
		t.Fatalf("closed editor = %v", err)
	}
	if _, err := svc.OpenEditor("nope"); !errors.Is(err, conn.ErrSessionNotFound) {
		t.Fatalf("unknown session = %v", err)
	}

	// Recording without a session, or with a broken history store, is
	// silently skipped.
	svc.record("unknown-editor", "SELECT 1", time.Now(), res)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	broken := NewEditorService(m, all.Registry(), history.New(file))
	eid2, _ := broken.OpenEditor(sid)
	if _, err := broken.Execute(ctx, eid2, "", "SELECT 1", query.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := broken.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	// No history at all.
	none := NewEditorService(m, all.Registry(), nil)
	eid3, _ := none.OpenEditor(sid)
	if _, err := none.Execute(ctx, eid3, "", "SELECT 1", query.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := none.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestEditorServiceSplit(t *testing.T) {
	svc, _, _, _ := newEditorService(t)
	script := "SELECT 'ä';\n  SELECT 2;"
	got := svc.Split(driver.SQLite, script)
	if len(got) != 2 || got[0] != (Statement{Text: "SELECT 'ä'", Start: 0, End: 10, DelimEnd: 11}) || got[1].Start != 14 || got[1].DelimEnd != 23 {
		t.Fatalf("Split = %+v", got)
	}
	if st := svc.StatementAt(driver.SQLite, script, 12); st == nil || st.Text != "SELECT 'ä'" {
		t.Fatalf("StatementAt(12) = %+v", st)
	}
	if st := svc.StatementAt(driver.SQLite, script, 16); st == nil || st.Text != "SELECT 2" {
		t.Fatalf("StatementAt(16) = %+v", st)
	}
	if st := svc.StatementAt(driver.Postgres, "  ", 1); st != nil {
		t.Fatalf("StatementAt(empty) = %+v", st)
	}
	if got := svc.Split(driver.MySQL, ""); len(got) != 0 {
		t.Fatalf("Split empty = %+v", got)
	}
}

func TestEditorServiceSessionEvents(t *testing.T) {
	ctx := context.Background()
	svc, m, sid, _ := newEditorService(t)
	eid, _ := svc.OpenEditor(sid)
	if _, err := svc.Execute(ctx, eid, "", "BEGIN; INSERT INTO t VALUES (9, 'tx')", query.ExecOptions{}); err != nil {
		t.Fatal(err)
	}
	cs, err := svc.CompletionSchema(ctx, sid, "main", false)
	if err != nil || cs == nil {
		t.Fatal(err)
	}
	if err := m.Disconnect(sid); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TxState(eid); !errors.Is(err, query.ErrEditorNotFound) {
		t.Fatalf("editor survived disconnect: %v", err)
	}
	svc.mu.Lock()
	n := len(svc.cache)
	svc.mu.Unlock()
	if n != 0 {
		t.Fatalf("completion cache not invalidated: %d", n)
	}
	svc.onSessionEvent(conn.Event{SessionID: sid, Kind: conn.DatabaseChanged})
}

func TestEditorServiceCompletion(t *testing.T) {
	ctx := context.Background()
	svc, m, sid, _ := newEditorService(t)
	cs, err := svc.CompletionSchema(ctx, sid, "main", false)
	if err != nil {
		t.Fatal(err)
	}
	if cs.Database != "main" || cs.Schema != "main" || len(cs.Schemas) != 1 || !cs.ColumnsLoaded || len(cs.Tables) != 2 {
		t.Fatalf("completion = %+v", cs)
	}
	tb := cs.Tables[0]
	if tb.Name != "t" || tb.Type != driver.TypeTable || len(tb.Columns) != 2 || tb.Columns[0] != (CompletionColumn{Name: "id", Type: "INTEGER"}) {
		t.Fatalf("table = %+v", tb)
	}
	if cs.Tables[1].Name != "v" || len(cs.Tables[1].Columns) != 1 {
		t.Fatalf("view = %+v", cs.Tables[1])
	}
	again, _ := svc.CompletionSchema(ctx, sid, "main", false)
	if again != cs {
		t.Fatal("not cached")
	}
	s, _ := m.Get(sid)
	if _, err := s.DB.Exec("CREATE TABLE added (x)"); err != nil {
		t.Fatal(err)
	}
	fresh, _ := svc.CompletionSchema(ctx, sid, "main", true)
	if fresh == cs || len(fresh.Tables) != 3 {
		t.Fatalf("refresh = %+v", fresh)
	}

	// Big schemas: names only, columns on demand.
	old := CompletionTableLimit
	CompletionTableLimit = 1
	t.Cleanup(func() { CompletionTableLimit = old })
	big, _ := svc.CompletionSchema(ctx, sid, "main", true)
	if big.ColumnsLoaded || big.Tables[0].Columns != nil {
		t.Fatalf("big schema = %+v", big)
	}
	cols, err := svc.CompletionColumns(ctx, sid, "main", "t")
	if err != nil || len(cols) != 2 || cols[1].Name != "name" {
		t.Fatalf("CompletionColumns = %+v, %v", cols, err)
	}

	CompletionTableLimit = old

	// Errors.
	if _, err := svc.CompletionSchema(ctx, "nope", "main", false); !errors.Is(err, conn.ErrSessionNotFound) {
		t.Fatalf("unknown session = %v", err)
	}
	if _, err := svc.CompletionColumns(ctx, "nope", "main", "t"); !errors.Is(err, conn.ErrSessionNotFound) {
		t.Fatalf("unknown session = %v", err)
	}
	if _, err := svc.CompletionSchema(ctx, sid, "no such schema", false); err != nil {
		t.Fatalf("unknown schema = %v", err)
	}
	bad := NewEditorService(m, failingDrivers{}, nil)
	if _, err := bad.CompletionSchema(ctx, sid, "main", false); err == nil {
		t.Fatal("driver error expected")
	}
	if _, err := bad.CompletionColumns(ctx, sid, "main", "t"); err == nil {
		t.Fatal("driver error expected")
	}
	for _, fail := range []string{"schemas", "tables", "columns", "cols"} {
		svc := NewEditorService(m, oneDriver{failingCompletion{Driver: mustDriver(t, driver.SQLite), fail: fail}}, nil)
		if fail == "cols" {
			if _, err := svc.CompletionColumns(ctx, sid, "main", "t"); err == nil {
				t.Fatal("columns error expected")
			}
			continue
		}
		if _, err := svc.CompletionSchema(ctx, sid, "main", false); err == nil {
			t.Fatalf("%s error expected", fail)
		}
	}
}

type failingDrivers struct{}

func (failingDrivers) Get(driver.Kind) (driver.Driver, error) { return nil, driver.ErrUnknownKind }

func mustDriver(t *testing.T, k driver.Kind) driver.Driver {
	d, err := all.Registry().Get(k)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

type failingCompletion struct {
	driver.Driver
	fail string
}

func (f failingCompletion) ListSchemas(ctx context.Context, db *sql.DB, sys bool) ([]string, error) {
	if f.fail == "schemas" {
		return nil, errors.New("boom")
	}
	return f.Driver.ListSchemas(ctx, db, sys)
}

func (f failingCompletion) ListTables(ctx context.Context, db *sql.DB, schema string) ([]driver.Table, error) {
	if f.fail == "tables" {
		return nil, errors.New("boom")
	}
	return f.Driver.ListTables(ctx, db, schema)
}

func (f failingCompletion) SchemaColumns(ctx context.Context, db *sql.DB, schema string) ([]driver.TableColumn, error) {
	if f.fail == "columns" {
		return nil, errors.New("boom")
	}
	return f.Driver.SchemaColumns(ctx, db, schema)
}

func (f failingCompletion) Columns(ctx context.Context, db *sql.DB, schema, table string) ([]driver.Column, error) {
	if f.fail == "cols" {
		return nil, errors.New("boom")
	}
	return f.Driver.Columns(ctx, db, schema, table)
}
