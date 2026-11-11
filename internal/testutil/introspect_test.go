package testutil

import (
	"testing"

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
