package mysql_test

import (
	"context"
	"strings"
	"testing"

	"lontar/internal/driver"
	"lontar/internal/driver/mysql"
	"lontar/internal/testutil"
)

func TestObjects(t *testing.T) {
	cases := []struct {
		engine string
		start  func(testing.TB) testutil.DB
		d      *mysql.Driver
	}{
		{testutil.EngineMySQL, testutil.SharedMySQL, mysql.New()},
		{testutil.EngineMariaDB, testutil.SharedMariaDB, mysql.NewMariaDB()},
	}
	for _, tc := range cases {
		t.Run(tc.engine, func(t *testing.T) {
			target := tc.start(t)
			db := testutil.Open(t, target)
			testutil.SeedObjects(t, tc.engine, target, db)
			testutil.CheckObjects(t, tc.engine, tc.d, db)

			ctx := context.Background()
			schema := testutil.ObjSchema(tc.engine)
			objs, err := tc.d.ListObjects(ctx, db, schema)
			if err != nil {
				t.Fatal(err)
			}
			// The procedure's body has ";": its DDL is wrapped in DELIMITER.
			p := testutil.FindObject(objs, testutil.WantObject{Kind: driver.ObjectProcedure, Name: "reset_log", Signature: "IN keep"})
			if p == nil {
				t.Fatalf("procedure not listed: %+v", objs)
			}
			ddl, err := tc.d.ObjectDDL(ctx, db, schema, p.Ref())
			if err != nil || !strings.HasPrefix(ddl, "DELIMITER ;;\nCREATE") {
				t.Fatalf("procedure DDL = %q, %v", ddl, err)
			}
			// Events and functions run again from their DDL.
			for _, w := range []testutil.WantObject{
				{Kind: driver.ObjectEvent, Name: "purge_log"},
				{Kind: driver.ObjectFunction, Name: "add_one", Signature: "x"},
			} {
				o := testutil.FindObject(objs, w)
				if o == nil {
					t.Fatalf("%s not listed", w.Name)
				}
				ddl, err := tc.d.ObjectDDL(ctx, db, schema, o.Ref())
				if err != nil {
					t.Fatal(err)
				}
				drop := "DROP " + strings.ToUpper(string(w.Kind)) + " " + w.Name
				if _, err := db.ExecContext(ctx, drop); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, strings.TrimSuffix(strings.TrimSpace(ddl), ";")); err != nil {
					t.Fatalf("re-running %s: %v\n%s", w.Name, err, ddl)
				}
			}
			if tc.engine == testutil.EngineMariaDB {
				s := testutil.FindObject(objs, testutil.WantObject{Kind: driver.ObjectSequence, Name: "ticket_seq"})
				if s == nil {
					t.Fatal("sequence not listed")
				}
				ddl, err := tc.d.ObjectDDL(ctx, db, schema, s.Ref())
				if err != nil || !strings.Contains(ddl, "CREATE SEQUENCE") {
					t.Fatalf("sequence DDL = %q, %v", ddl, err)
				}
			}
			closed := testutil.Open(t, target)
			_ = closed.Close()
			if _, err := tc.d.ListObjects(ctx, closed, schema); err == nil {
				t.Fatal("closed db: expected error")
			}
		})
	}
}
