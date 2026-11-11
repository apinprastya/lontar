package sqlite

import (
	"context"
	"errors"
	"slices"
	"testing"

	"dbedit/internal/driver"
	"dbedit/internal/testutil"
)

func TestStructure(t *testing.T) {
	target := testutil.SQLiteTemp(t)
	db := testutil.Open(t, target)
	testutil.SeedStructure(t, testutil.EngineSQLite, target, db)
	testutil.CheckStructure(t, testutil.EngineSQLite, New(), db)
}

func TestStructureDetails(t *testing.T) {
	db := testutil.Open(t, testutil.SQLiteTemp(t))
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	for _, q := range []string{
		`CREATE TABLE p (a INTEGER, b TEXT, PRIMARY KEY (a, b)) WITHOUT ROWID`,
		`CREATE TABLE w (id INTEGER PRIMARY KEY, v INTEGER GENERATED ALWAYS AS (id * 2) VIRTUAL, x, UNIQUE (x, v))`,
		`CREATE TABLE c (pa INTEGER, pb TEXT, FOREIGN KEY (pa, pb) REFERENCES P)`,
		`CREATE TRIGGER c_del DELETE ON c BEGIN SELECT 1; END`,
		`ATTACH DATABASE ':memory:' AS aux`,
		`CREATE TABLE aux.t (id INTEGER PRIMARY KEY)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	d := New()
	w, err := d.TableInfo(ctx, db, "main", "w")
	if err != nil {
		t.Fatal(err)
	}
	if v := w.Columns[1]; v.Generated != "VIRTUAL" || !w.Columns[0].AutoIncrement {
		t.Fatalf("w columns = %+v", w.Columns)
	}
	if len(w.Indexes) != 1 || !w.Indexes[0].Unique || w.Indexes[0].Definition != "UNIQUE (x, v)" {
		t.Fatalf("w indexes = %+v", w.Indexes)
	}
	p, err := d.TableInfo(ctx, db, "main", "p")
	if err != nil {
		t.Fatal(err)
	}
	if p.Columns[0].AutoIncrement || len(p.Indexes) != 1 || !p.Indexes[0].Primary || p.Indexes[0].Definition != "PRIMARY KEY (a, b)" {
		t.Fatalf("p = %+v", p)
	}
	// REFERENCES P (other case, no columns): the parent's key, real name.
	c, err := d.TableInfo(ctx, db, "main", "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.ForeignKeys) != 1 || c.ForeignKeys[0].RefTable != "p" || !slices.Equal(c.ForeignKeys[0].RefColumns, []string{"a", "b"}) {
		t.Fatalf("c fks = %+v", c.ForeignKeys)
	}
	if len(c.Triggers) != 1 || c.Triggers[0].Timing != "BEFORE" || c.Triggers[0].Event != "DELETE" {
		t.Fatalf("c triggers = %+v", c.Triggers)
	}
	if p, _ := d.TableInfo(ctx, db, "main", "p"); len(p.ReferencedBy) != 1 || p.ReferencedBy[0].Table != "c" {
		t.Fatalf("p referenced by = %+v", p.ReferencedBy)
	}
	// Attached schemas work; unknown ones are rejected before any SQL.
	if a, err := d.TableInfo(ctx, db, "aux", "t"); err != nil || a.Schema != "aux" || len(a.Columns) != 1 {
		t.Fatalf("aux.t = %+v, %v", a, err)
	}
	if s, err := d.TableDDL(ctx, db, "aux", "t"); err != nil || s != "CREATE TABLE t (id INTEGER PRIMARY KEY);\n" {
		t.Fatalf("aux.t DDL = %q, %v", s, err)
	}
	for _, schema := range []string{"nope", `main".sqlite_master; --`} {
		if _, err := d.TableInfo(ctx, db, schema, "t"); !errors.Is(err, driver.ErrTableNotFound) {
			t.Fatalf("TableInfo(%q) err = %v", schema, err)
		}
		if _, err := d.TableDDL(ctx, db, schema, "t"); !errors.Is(err, driver.ErrTableNotFound) {
			t.Fatalf("TableDDL(%q) err = %v", schema, err)
		}
	}
}

func TestTriggerKind(t *testing.T) {
	for in, want := range map[string][2]string{
		`CREATE TRIGGER x AFTER UPDATE OF a ON t BEGIN END`:       {"AFTER", "UPDATE"},
		`CREATE TRIGGER x INSTEAD  OF insert ON v BEGIN END`:      {"INSTEAD OF", "INSERT"},
		`CREATE TEMP TRIGGER x DELETE ON t BEGIN END`:             {"BEFORE", "DELETE"},
		`CREATE TRIGGER x BEFORE DELETE ON t BEGIN SELECT 1; END`: {"BEFORE", "DELETE"},
		`garbage`: {"", ""},
	} {
		if ti, ev := triggerKind(in); ti != want[0] || ev != want[1] {
			t.Errorf("triggerKind(%q) = %q, %q", in, ti, ev)
		}
	}
	if got := partialPredicate("CREATE INDEX i ON t (a) WHERE a > 1 AND (b) = 2"); got != "a > 1 AND (b) = 2" {
		t.Errorf("partialPredicate = %q", got)
	}
	if got := partialPredicate("CREATE INDEX i ON t (a)"); got != "" {
		t.Errorf("partialPredicate = %q", got)
	}
}
