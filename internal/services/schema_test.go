package services

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"dbedit/internal/conn"
	"dbedit/internal/driver"
	"dbedit/internal/driver/all"
	"dbedit/internal/query"
	"dbedit/internal/store"
)

// openSQLiteSession saves an SQLite connection, seeds it and connects.
func openSQLiteSession(t *testing.T) (*conn.Manager, string) {
	t.Helper()
	return openSQLiteSessionMode(t, store.SafeOff)
}

// openSQLiteSessionMode is openSQLiteSession with a safe mode. The file is
// seeded before connecting, so read-only sessions work too.
func openSQLiteSessionMode(t *testing.T, mode store.SafeMode) (*conn.Manager, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x.db")
	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)`,
		`INSERT INTO t VALUES (1, 'a'), (2, NULL), (3, 'c')`,
		`CREATE VIEW v AS SELECT name FROM t`,
	} {
		if _, err := seed.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = seed.Close()
	st := store.New(t.TempDir(), store.NewMemorySecretStore())
	c, err := st.Save(store.Connection{Name: "lite", Kind: driver.SQLite, FilePath: path, SafeMode: mode}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := conn.NewManager(st, all.Registry())
	t.Cleanup(func() { _ = m.DisconnectAll() })
	id, err := m.Connect(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m, id
}

func TestSchemaServiceSQLite(t *testing.T) {
	m, id := openSQLiteSession(t)
	svc := NewSchemaService(m, all.Registry())
	ctx := context.Background()

	loc, err := svc.Current(ctx, id)
	if err != nil || loc != (Location{Database: "main", Schema: "main"}) {
		t.Fatalf("Current = %+v, %v", loc, err)
	}
	dbs, err := svc.ListDatabases(ctx, id, false)
	if err != nil || len(dbs) != 1 || dbs[0] != "main" {
		t.Fatalf("ListDatabases = %v, %v", dbs, err)
	}
	schemas, err := svc.ListSchemas(ctx, id, false)
	if err != nil || len(schemas) != 1 || schemas[0] != "main" {
		t.Fatalf("ListSchemas = %v, %v", schemas, err)
	}
	tables, err := svc.ListTables(ctx, id, "main")
	if err != nil || len(tables) != 2 || tables[0].Name != "t" || tables[1].Type != driver.TypeView {
		t.Fatalf("ListTables = %+v, %v", tables, err)
	}
	cols, err := svc.Columns(ctx, id, "main", "t")
	if err != nil || len(cols) != 2 || !cols[0].IsPrimaryKey {
		t.Fatalf("Columns = %+v, %v", cols, err)
	}
	ti, err := svc.TableInfo(ctx, id, "main", "t")
	if err != nil || ti.Name != "t" || len(ti.Columns) != 2 || !ti.Columns[0].AutoIncrement {
		t.Fatalf("TableInfo = %+v, %v", ti, err)
	}
	ddl, err := svc.TableDDL(ctx, id, "main", "v")
	if err != nil || ddl != "CREATE VIEW v AS SELECT name FROM t;\n" {
		t.Fatalf("TableDDL = %q, %v", ddl, err)
	}
	if _, err := svc.TableDDL(ctx, id, "main", "nope"); !errors.Is(err, driver.ErrTableNotFound) {
		t.Fatalf("TableDDL err = %v", err)
	}
	if _, err := svc.UseDatabase(ctx, id, "other"); !errors.Is(err, conn.ErrUnsupported) {
		t.Fatalf("UseDatabase err = %v", err)
	}

	// Unknown session everywhere.
	for name, call := range map[string]func() error{
		"Current":       func() error { _, err := svc.Current(ctx, "nope"); return err },
		"ListDatabases": func() error { _, err := svc.ListDatabases(ctx, "nope", false); return err },
		"ListSchemas":   func() error { _, err := svc.ListSchemas(ctx, "nope", false); return err },
		"ListTables":    func() error { _, err := svc.ListTables(ctx, "nope", "main"); return err },
		"Columns":       func() error { _, err := svc.Columns(ctx, "nope", "main", "t"); return err },
		"TableInfo":     func() error { _, err := svc.TableInfo(ctx, "nope", "main", "t"); return err },
		"TableDDL":      func() error { _, err := svc.TableDDL(ctx, "nope", "main", "t"); return err },
	} {
		if err := call(); !errors.Is(err, conn.ErrSessionNotFound) {
			t.Errorf("%s err = %v", name, err)
		}
	}
}

// fakeSessionsKind returns a session whose kind has no driver.
type unknownKindSessions struct{ conn.Session }

func (u *unknownKindSessions) Get(string) (*conn.Session, error) { return &u.Session, nil }
func (u *unknownKindSessions) UseDatabase(context.Context, string, string) (*conn.Session, error) {
	return &u.Session, nil
}

type failingIntrospector struct {
	driver.Driver
	failSchema bool
}

func (f failingIntrospector) CurrentDatabase(context.Context, *sql.DB) (string, error) {
	if f.failSchema {
		return "db", nil
	}
	return "", errors.New("db boom")
}
func (f failingIntrospector) CurrentSchema(context.Context, *sql.DB) (string, error) {
	return "", errors.New("schema boom")
}

type oneDriver struct{ d driver.Driver }

func (o oneDriver) Get(driver.Kind) (driver.Driver, error) { return o.d, nil }

func TestSchemaServiceErrors(t *testing.T) {
	ctx := context.Background()
	sess := &unknownKindSessions{conn.Session{ID: "s", Kind: "oracle"}}
	svc := NewSchemaService(sess, all.Registry())
	if _, err := svc.ListTables(ctx, "s", "x"); !errors.Is(err, driver.ErrUnknownKind) {
		t.Fatalf("err = %v", err)
	}
	// UseDatabase succeeds but reading the location fails.
	if _, err := svc.UseDatabase(ctx, "s", "x"); !errors.Is(err, driver.ErrUnknownKind) {
		t.Fatalf("err = %v", err)
	}
	for _, failSchema := range []bool{false, true} {
		svc = NewSchemaService(sess, oneDriver{failingIntrospector{failSchema: failSchema}})
		if _, err := svc.Current(ctx, "s"); err == nil {
			t.Fatal("expected error")
		}
	}
}

func TestBrowseServiceSQLite(t *testing.T) {
	m, id := openSQLiteSession(t)
	svc := NewBrowseService(m, all.Registry(), nil)
	ctx := context.Background()

	p, err := svc.FetchTable(ctx, id, "r1", query.Request{Schema: "main", Table: "t", Sort: []query.Sort{{Column: "id", Desc: true}}})
	if err != nil || len(p.Rows) != 3 || p.Rows[0][0].V != int64(3) {
		t.Fatalf("FetchTable = %+v, %v", p, err)
	}
	n, err := svc.CountRows(ctx, id, "r2", query.Request{Schema: "main", Table: "t", Filters: []query.Filter{{Column: "name", Op: query.OpIsNull}}})
	if err != nil || n != 1 {
		t.Fatalf("CountRows = %d, %v", n, err)
	}
	if svc.CancelFetch("r1") {
		t.Fatal("nothing should be running")
	}
	if _, err := svc.FetchTable(ctx, "nope", "r", query.Request{}); !errors.Is(err, conn.ErrSessionNotFound) {
		t.Fatalf("err = %v", err)
	}
	if _, err := svc.CountRows(ctx, "nope", "r", query.Request{}); !errors.Is(err, conn.ErrSessionNotFound) {
		t.Fatalf("err = %v", err)
	}

	// Cancel a slow count.
	errc := make(chan error, 1)
	go func() {
		_, err := svc.CountRows(ctx, id, "slow", query.Request{Schema: "main", Table: "t",
			RawWhere: "(WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n) SELECT COUNT(*) FROM n) > 0"})
		errc <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !svc.CancelFetch("slow") {
		if time.Now().After(deadline) {
			t.Fatal("never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case err := <-errc:
		if !errors.Is(err, query.ErrCanceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("not cancelled")
	}
}
