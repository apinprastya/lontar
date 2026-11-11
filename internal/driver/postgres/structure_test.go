package postgres_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"dbedit/internal/driver"
	"dbedit/internal/driver/postgres"
	"dbedit/internal/testutil"
)

func TestStructure(t *testing.T) {
	target := testutil.SharedPostgres(t)
	db := testutil.Open(t, target)
	testutil.SeedStructure(t, testutil.EnginePostgres, target, db)
	testutil.CheckStructure(t, testutil.EnginePostgres, postgres.New(), db)
}

// TestDDLRoundTrip executes the reconstructed DDL of every fixture relation
// in a fresh schema and checks that the copies have the same structure
// (and the same DDL) as the originals. The DDL is schema-qualified, so the
// schema name is swapped in the text.
func TestDDLRoundTrip(t *testing.T) {
	target := testutil.SharedPostgres(t)
	db := testutil.Open(t, target)
	testutil.SeedStructure(t, testutil.EnginePostgres, target, db)
	ctx := context.Background()
	d := postgres.New()
	const src, dst = "shop", "shop_rt"
	if _, err := db.ExecContext(ctx, `DROP SCHEMA IF EXISTS shop_rt CASCADE; CREATE SCHEMA shop_rt`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS shop_rt CASCADE`) })

	// Parents first: the DDL creates foreign keys inline.
	order := []string{testutil.StructCategories, testutil.StructCustomers, testutil.StructOrders, testutil.StructOrderLines,
		testutil.StructWeird, testutil.StructView, testutil.StructMatView}
	ddls := map[string]string{}
	for _, name := range order {
		ddl, err := d.TableDDL(ctx, db, src, name)
		if err != nil {
			t.Fatal(err)
		}
		ddls[name] = ddl
		moved := strings.ReplaceAll(ddl, src+".", dst+".")
		if _, err := db.ExecContext(ctx, moved); err != nil {
			t.Fatalf("executing the DDL of %s: %v\n%s", name, err, moved)
		}
	}
	norm := func(ti *driver.TableDetail) string {
		ti.EstimatedRows, ti.SizeBytes = nil, nil // data and statistics differ
		b, err := json.MarshalIndent(ti, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(b), dst, src)
	}
	for _, name := range order {
		a, err := d.TableInfo(ctx, db, src, name)
		if err != nil {
			t.Fatal(err)
		}
		b, err := d.TableInfo(ctx, db, dst, name)
		if err != nil {
			t.Fatal(err)
		}
		// Incoming keys from the original schema point at the originals only.
		a.ReferencedBy = keep(a.ReferencedBy, src)
		b.ReferencedBy = keep(b.ReferencedBy, dst)
		if na, nb := norm(a), norm(b); na != nb {
			t.Errorf("%s: structure differs after the round trip:\n--- original\n%s\n--- copy\n%s", name, na, nb)
		}
		again, err := d.TableDDL(ctx, db, dst, name)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.ReplaceAll(again, dst+".", src+"."); got != ddls[name] {
			t.Errorf("%s: DDL differs after the round trip:\n--- original\n%s\n--- copy\n%s", name, ddls[name], got)
		}
	}
}

func keep(fks []driver.ForeignKey, schema string) []driver.ForeignKey {
	out := []driver.ForeignKey{}
	for _, fk := range fks {
		if fk.Schema == schema {
			out = append(out, fk)
		}
	}
	return out
}
