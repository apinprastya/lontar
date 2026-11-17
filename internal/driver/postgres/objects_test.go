package postgres_test

import (
	"context"
	"strings"
	"testing"

	"lontar/internal/driver"
	"lontar/internal/driver/postgres"
	"lontar/internal/testutil"
)

func TestObjects(t *testing.T) {
	target := testutil.SharedPostgres(t)
	db := testutil.Open(t, target)
	testutil.SeedObjects(t, testutil.EnginePostgres, target, db)
	testutil.CheckObjects(t, testutil.EnginePostgres, postgres.New(), db)

	ctx := context.Background()
	objs, err := postgres.New().ListObjects(ctx, db, testutil.ObjSchemaPG)
	if err != nil {
		t.Fatal(err)
	}
	props := func(w testutil.WantObject) map[string]string {
		o := testutil.FindObject(objs, w)
		if o == nil {
			t.Fatalf("%s not listed", w.Name)
		}
		m := map[string]string{}
		for _, p := range o.Properties {
			m[p.Key] = p.Value
		}
		return m
	}
	seq := props(testutil.WantObject{Kind: driver.ObjectSequence, Name: "ticket_seq"})
	if seq["Current value"] != "10" || seq["Increment"] != "5" || seq["Owned by"] != "tickets.id" || seq["Comment"] != "Ticket numbers" {
		t.Errorf("sequence properties = %v", seq)
	}
	fn := props(testutil.WantObject{Kind: driver.ObjectFunction, Name: "add_one", Signature: "x integer"})
	if fn["Volatility"] != "immutable" || fn["Comment"] != "Adds one" || fn["Security"] != "invoker" {
		t.Errorf("function properties = %v", fn)
	}
	dom := props(testutil.WantObject{Kind: driver.ObjectDomain, Name: "posint"})
	if dom["Default"] != "1" || dom["Not null"] != "yes" || !strings.Contains(dom["Constraints"], "VALUE > 0") {
		t.Errorf("domain properties = %v", dom)
	}
	trg := props(testutil.WantObject{Kind: driver.ObjectTrigger, Name: "tickets_touch", Table: "tickets"})
	if trg["Level"] != "row" || trg["Enabled"] != "yes" || !strings.Contains(trg["Function"], "touch()") {
		t.Errorf("trigger properties = %v", trg)
	}
	if objs, err := postgres.New().ListObjects(ctx, db, "no_such_schema"); err != nil || len(objs) != 0 {
		t.Errorf("unknown schema = %v, %v", objs, err)
	}
	closed := testutil.Open(t, target)
	_ = closed.Close()
	if _, err := postgres.New().ListObjects(ctx, closed, testutil.ObjSchemaPG); err == nil {
		t.Fatal("closed db: expected error")
	}
}

// TestObjectDDLRoundTrip executes the DDL of every fixture object in a
// fresh schema and checks that the copies list the same way and have the
// same DDL.
func TestObjectDDLRoundTrip(t *testing.T) {
	target := testutil.SharedPostgres(t)
	db := testutil.Open(t, target)
	testutil.SeedObjects(t, testutil.EnginePostgres, target, db)
	ctx := context.Background()
	d := postgres.New()
	const src, dst = testutil.ObjSchemaPG, "objs_rt"
	if _, err := db.ExecContext(ctx, `DROP SCHEMA IF EXISTS objs_rt CASCADE; CREATE SCHEMA objs_rt`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS objs_rt CASCADE`) })
	objs, err := d.ListObjects(ctx, db, src)
	if err != nil {
		t.Fatal(err)
	}
	move := func(ddl string) string { return strings.ReplaceAll(ddl, src+".", dst+".") }
	ddls := map[string]string{}
	run := func(kinds ...driver.ObjectKind) {
		t.Helper()
		for _, o := range objs {
			if !containsKind(kinds, o.Kind) {
				continue
			}
			ddl, err := d.ObjectDDL(ctx, db, src, o.Ref())
			if err != nil {
				t.Fatal(err)
			}
			ddls[string(o.Kind)+o.Name+o.Signature] = ddl
			if _, err := db.ExecContext(ctx, move(ddl)); err != nil {
				t.Fatalf("executing the DDL of %s: %v\n%s", o.Name, err, move(ddl))
			}
		}
	}
	// Types first (the table uses the enum), then routines, the table the
	// sequence and the trigger need, then those.
	run(driver.ObjectEnum, driver.ObjectComposite, driver.ObjectDomain)
	run(driver.ObjectFunction, driver.ObjectProcedure, driver.ObjectAggregate)
	tableDDL, err := d.TableDDL(ctx, db, src, "tickets")
	if err != nil {
		t.Fatal(err)
	}
	// Without its trigger: that comes from the object's own DDL.
	tableDDL = tableDDL[:strings.Index(tableDDL, ";")+1]
	if _, err := db.ExecContext(ctx, move(tableDDL)); err != nil {
		t.Fatalf("table: %v\n%s", err, move(tableDDL))
	}
	run(driver.ObjectSequence, driver.ObjectTrigger)

	copies, err := d.ListObjects(ctx, db, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(copies) != len(objs) {
		t.Fatalf("copied %d objects, want %d: %+v", len(copies), len(objs), copies)
	}
	for i, c := range copies {
		o := objs[i]
		if c.Kind != o.Kind || c.Name != o.Name || c.Signature != o.Signature || c.Detail != o.Detail || c.Table != o.Table {
			t.Errorf("copy %+v differs from %+v", c, o)
		}
		ddl, err := d.ObjectDDL(ctx, db, dst, c.Ref())
		if err != nil {
			t.Fatal(err)
		}
		if want := move(ddls[string(o.Kind)+o.Name+o.Signature]); ddl != want {
			t.Errorf("DDL of the copy of %s:\n%s\nwant:\n%s", o.Name, ddl, want)
		}
	}
}

func containsKind(kinds []driver.ObjectKind, k driver.ObjectKind) bool {
	for _, x := range kinds {
		if x == k {
			return true
		}
	}
	return false
}
