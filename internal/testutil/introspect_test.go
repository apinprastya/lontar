package testutil

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"lontar/internal/driver"
	"lontar/internal/driver/mysql"
	"lontar/internal/driver/postgres"
	"lontar/internal/driver/sqlite"
)

func TestCheckIntrospectionSQLite(t *testing.T) {
	target := SQLiteTemp(t)
	db := Open(t, target)
	SeedBrowse(t, EngineSQLite, target, db)
	CheckIntrospection(t, EngineSQLite, sqlite.New(), db)
	_ = db.Close()
	CheckIntrospectionErrors(t, sqlite.New(), db)
	for e, want := range map[string]string{EnginePostgres: "public", EngineMySQL: "lontar", EngineMariaDB: "lontar", EngineSQLite: "main"} {
		if got := SeedSchema(e); got != want {
			t.Errorf("SeedSchema(%s) = %q", e, got)
		}
	}
}

func TestCheckStructureSQLite(t *testing.T) {
	target := SQLiteTemp(t)
	db := Open(t, target)
	SeedStructure(t, EngineSQLite, target, db)
	SeedStructure(t, EngineSQLite, target, db) // once per DSN
	CheckStructure(t, EngineSQLite, sqlite.New(), db)
	for _, e := range []string{EnginePostgres, EngineMySQL, EngineMariaDB} {
		if s, err := StructureStatements(e); err != nil || len(s) < 8 {
			t.Fatalf("StructureStatements(%s) = %d, %v", e, len(s), err)
		}
	}
	if _, err := StructureStatements("oracle"); err == nil {
		t.Fatal("expected error")
	}
	ft := &fakeTB{TB: t}
	SeedStructure(ft, "oracle", target, db)
	if ft.fatal == "" {
		t.Fatal("unknown engine should fail")
	}
	if StructSchema(EnginePostgres) != "shop" || StructSchema(EngineSQLite) != "main" {
		t.Fatal("StructSchema")
	}
	if FindIndex(nil, "x") != nil || FindFK(nil, "a", "b") != nil || FindColumn(nil, "c") != nil {
		t.Fatal("finders")
	}
}

func TestCheckStructureServers(t *testing.T) {
	for _, tc := range []struct {
		engine string
		start  func(testing.TB) DB
		in     driver.Introspector
	}{
		{EnginePostgres, SharedPostgres, postgres.New()},
		{EngineMySQL, SharedMySQL, mysql.New()},
		{EngineMariaDB, SharedMariaDB, mysql.NewMariaDB()},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			target := tc.start(t)
			db := Open(t, target)
			SeedStructure(t, tc.engine, target, db)
			CheckStructure(t, tc.engine, tc.in, db)
		})
	}
}

func TestCheckObjectsSQLite(t *testing.T) {
	target := SQLiteTemp(t)
	db := Open(t, target)
	SeedObjects(t, EngineSQLite, target, db)
	SeedObjects(t, EngineSQLite, target, db) // once per DSN
	CheckObjects(t, EngineSQLite, sqlite.New(), db)
	for _, e := range []string{EnginePostgres, EngineMySQL, EngineMariaDB} {
		if s, err := ObjectStatements(e); err != nil || len(s) < 5 {
			t.Fatalf("ObjectStatements(%s) = %d, %v", e, len(s), err)
		}
		if len(WantObjects(e)) < 4 {
			t.Fatalf("WantObjects(%s)", e)
		}
	}
	if _, err := ObjectStatements("oracle"); err == nil {
		t.Fatal("expected error")
	}
	ft := &fakeTB{TB: t}
	SeedObjects(ft, "oracle", target, db)
	if ft.fatal == "" {
		t.Fatal("unknown engine should fail")
	}
	if ObjSchema(EnginePostgres) != ObjSchemaPG || ObjSchema(EngineSQLite) != "main" {
		t.Fatal("ObjSchema")
	}
	if FindObject(nil, WantObject{Name: "x"}) != nil {
		t.Fatal("FindObject")
	}
	// A lister that gets things wrong is reported.
	ft = &fakeTB{TB: t}
	CheckObjects(ft, EngineSQLite, badLister{}, db)
	if !ft.failed {
		t.Fatal("bad lister should fail")
	}
	ft = &fakeTB{TB: t}
	CheckObjects(ft, EngineSQLite, badLister{err: errors.New("boom")}, db)
	if ft.fatal == "" {
		t.Fatal("listing error should be fatal")
	}
}

type badLister struct{ err error }

func (b badLister) ListObjects(context.Context, *sql.DB, string) ([]driver.Object, error) {
	return []driver.Object{{Kind: driver.ObjectTrigger, Name: "obj_log_ai", Table: "obj_log", Detail: "wrong"}}, b.err
}

func (badLister) ObjectDDL(context.Context, *sql.DB, string, driver.ObjectRef) (string, error) {
	return "", nil
}

func TestCheckObjectsServers(t *testing.T) {
	for _, tc := range []struct {
		engine string
		start  func(testing.TB) DB
		l      driver.ObjectLister
	}{
		{EnginePostgres, SharedPostgres, postgres.New()},
		{EngineMySQL, SharedMySQL, mysql.New()},
		{EngineMariaDB, SharedMariaDB, mysql.NewMariaDB()},
	} {
		t.Run(tc.engine, func(t *testing.T) {
			target := tc.start(t)
			db := Open(t, target)
			SeedObjects(t, tc.engine, target, db)
			CheckObjects(t, tc.engine, tc.l, db)
		})
	}
}
