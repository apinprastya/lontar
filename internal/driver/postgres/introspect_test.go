package postgres_test

import (
	"context"
	"database/sql"
	"testing"

	"dbedit/internal/driver/postgres"
	"dbedit/internal/testutil"
)

func TestIntrospection(t *testing.T) {
	target := testutil.SharedPostgres(t)
	db := testutil.Open(t, target)
	testutil.SeedBrowse(t, testutil.EnginePostgres, target, db)
	testutil.CheckIntrospection(t, testutil.EnginePostgres, postgres.New(), db)
}

func TestIntrospectionErrors(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://u@127.0.0.1:1/x?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	testutil.CheckIntrospectionErrors(t, postgres.New(), db)
	if _, err := postgres.New().ListDatabases(context.Background(), db, false); err == nil {
		t.Fatal("expected error")
	}
}
