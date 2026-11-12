package sqlite

import (
	"context"
	"slices"
	"testing"

	"lontar/internal/testutil"
)

func TestIntrospection(t *testing.T) {
	target := testutil.SQLiteTemp(t)
	db := testutil.Open(t, target)
	testutil.SeedBrowse(t, testutil.EngineSQLite, target, db)
	testutil.CheckIntrospection(t, testutil.EngineSQLite, New(), db)
}

func TestAttachedSchemas(t *testing.T) {
	db := testutil.Open(t, testutil.SQLiteTemp(t))
	// ATTACH and TEMP are per connection: pin the pool to one.
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	for _, q := range []string{
		`ATTACH DATABASE ':memory:' AS aux`,
		`CREATE TABLE aux.t (x INT, y TEXT NOT NULL, PRIMARY KEY (x, y))`,
		`CREATE TEMP TABLE tmp (x)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	d := New()
	got, err := d.ListSchemas(ctx, db, false)
	if err != nil || !slices.Equal(got, []string{"main", "aux"}) {
		t.Fatalf("ListSchemas = %v, %v", got, err)
	}
	got, _ = d.ListSchemas(ctx, db, true)
	if !slices.Contains(got, "temp") {
		t.Fatalf("ListSchemas(system) = %v", got)
	}
	tables, err := d.ListTables(ctx, db, "aux")
	if err != nil || len(tables) != 1 || tables[0].Name != "t" || tables[0].Schema != "aux" {
		t.Fatalf("ListTables = %+v, %v", tables, err)
	}
	cols, err := d.Columns(ctx, db, "aux", "t")
	if err != nil || len(cols) != 2 || !cols[0].Nullable || cols[1].Nullable {
		t.Fatalf("Columns = %+v, %v", cols, err)
	}
}

func TestIntrospectionErrors(t *testing.T) {
	db := testutil.Open(t, testutil.SQLiteTemp(t))
	_ = db.Close()
	testutil.CheckIntrospectionErrors(t, New(), db)
}

func TestUnknownSchema(t *testing.T) {
	db := testutil.Open(t, testutil.SQLiteTemp(t))
	ctx := context.Background()
	d := New()
	if cols, err := d.Columns(ctx, db, "nope", "t"); err != nil || len(cols) != 0 {
		t.Fatalf("Columns = %v, %v", cols, err)
	}
	if pk, err := d.PrimaryKey(ctx, db, "nope", "t"); err != nil || len(pk) != 0 {
		t.Fatalf("PrimaryKey = %v, %v", pk, err)
	}
}
