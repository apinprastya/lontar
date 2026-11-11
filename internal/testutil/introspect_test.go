package testutil

import (
	"testing"

	"dbedit/internal/driver"
	"dbedit/internal/driver/mysql"
	"dbedit/internal/driver/postgres"
	"dbedit/internal/driver/sqlite"
)

func TestCheckIntrospectionSQLite(t *testing.T) {
	target := SQLiteTemp(t)
	db := Open(t, target)
	SeedBrowse(t, EngineSQLite, target, db)
	CheckIntrospection(t, EngineSQLite, sqlite.New(), db)
	_ = db.Close()
	CheckIntrospectionErrors(t, sqlite.New(), db)
	for e, want := range map[string]string{EnginePostgres: "public", EngineMySQL: "dbedit", EngineMariaDB: "dbedit", EngineSQLite: "main"} {
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
