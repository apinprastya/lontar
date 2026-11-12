package mysql_test

import (
	"context"
	"database/sql"
	"testing"

	"lontar/internal/driver"
	"lontar/internal/driver/mysql"
	"lontar/internal/testutil"
)

func TestIntrospection(t *testing.T) {
	cases := []struct {
		engine string
		start  func(testing.TB) testutil.DB
		d      driver.Introspector
	}{
		{testutil.EngineMySQL, testutil.SharedMySQL, mysql.New()},
		{testutil.EngineMariaDB, testutil.SharedMariaDB, mysql.NewMariaDB()},
	}
	for _, tc := range cases {
		t.Run(tc.engine, func(t *testing.T) {
			target := tc.start(t)
			db := testutil.Open(t, target)
			testutil.SeedBrowse(t, tc.engine, target, db)
			testutil.CheckIntrospection(t, tc.engine, tc.d, db)

			// Without a default database there is no schema.
			root, err := testutil.AsRoot(target)
			if err != nil {
				t.Fatal(err)
			}
			rdb := testutil.Open(t, root)
			schemas, err := tc.d.ListSchemas(context.Background(), rdb, false)
			if err != nil || len(schemas) != 0 {
				t.Fatalf("ListSchemas without database = %v, %v", schemas, err)
			}
		})
	}
}

func TestIntrospectionErrors(t *testing.T) {
	db, err := sql.Open("mysql", "u:p@tcp(127.0.0.1:1)/x?timeout=1s")
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	d := mysql.New()
	testutil.CheckIntrospectionErrors(t, d, db)
	if _, err := d.ListDatabases(context.Background(), db, false); err == nil {
		t.Fatal("expected error")
	}
	if _, err := d.CurrentSchema(context.Background(), db); err == nil {
		t.Fatal("expected error")
	}
}
