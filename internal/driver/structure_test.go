package driver_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"lontar/internal/driver"
)

type tableList struct {
	tables []driver.Table
	err    error
}

func (l tableList) ListTables(context.Context, *sql.DB, string) ([]driver.Table, error) {
	return l.tables, l.err
}

func TestFindTable(t *testing.T) {
	ctx := context.Background()
	l := tableList{tables: []driver.Table{{Schema: "s", Name: "Orders", Type: driver.TypeTable}, {Schema: "s", Name: "v", Type: driver.TypeView}}}
	got, err := driver.FindTable(ctx, l, nil, "s", "v")
	if err != nil || got.Type != driver.TypeView {
		t.Fatalf("FindTable = %+v, %v", got, err)
	}
	// Exact names only: an identifier is only ever quoted after this check.
	for _, name := range []string{"orders", "", "Orders`; DROP TABLE x", `Orders"`} {
		if _, err := driver.FindTable(ctx, l, nil, "s", name); !errors.Is(err, driver.ErrTableNotFound) {
			t.Errorf("FindTable(%q) err = %v", name, err)
		}
	}
	boom := errors.New("boom")
	if _, err := driver.FindTable(ctx, tableList{err: boom}, nil, "s", "x"); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestNewTableDetail(t *testing.T) {
	n := int64(3)
	d := driver.NewTableDetail(driver.Table{Schema: "s", Name: "t", Type: driver.TypeTable, EstimatedRows: &n})
	if d.Columns == nil || d.Indexes == nil || d.ForeignKeys == nil || d.ReferencedBy == nil || d.Checks == nil || d.Triggers == nil ||
		*d.EstimatedRows != 3 || d.Name != "t" {
		t.Fatalf("NewTableDetail = %+v", d)
	}
}

func TestReferentialAction(t *testing.T) {
	for in, want := range map[string]string{"a": "NO ACTION", "": "NO ACTION", "r": "RESTRICT", "c": "CASCADE", "n": "SET NULL", "d": "SET DEFAULT", "CASCADE": "CASCADE"} {
		if got := driver.ReferentialAction(in); got != want {
			t.Errorf("ReferentialAction(%q) = %q", in, got)
		}
	}
}
