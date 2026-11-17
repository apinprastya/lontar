package sqlite

import (
	"context"
	"errors"
	"testing"

	"lontar/internal/driver"
	"lontar/internal/testutil"
)

func TestObjects(t *testing.T) {
	target := testutil.SQLiteTemp(t)
	db := testutil.Open(t, target)
	testutil.SeedObjects(t, testutil.EngineSQLite, target, db)
	testutil.CheckObjects(t, testutil.EngineSQLite, New(), db)

	ctx := context.Background()
	d := New()
	objs, err := d.ListObjects(ctx, db, "main")
	if err != nil || len(objs) != 1 {
		t.Fatalf("ListObjects = %+v, %v", objs, err)
	}
	ddl, err := d.ObjectDDL(ctx, db, "main", objs[0].Ref())
	if err != nil || ddl[:len("CREATE TRIGGER obj_log_ai")] != "CREATE TRIGGER obj_log_ai" {
		t.Fatalf("ObjectDDL = %q, %v", ddl, err)
	}
	if _, err := d.ListObjects(ctx, db, "nope"); !errors.Is(err, driver.ErrTableNotFound) {
		t.Fatalf("unknown schema err = %v", err)
	}
	_ = db.Close()
	if _, err := d.ListObjects(ctx, db, "main"); err == nil {
		t.Fatal("closed db: expected error")
	}
}
