package query

import (
	"strings"
	"testing"

	"dbedit/internal/driver"
)

func TestBuildSelectAllAndReturnsRows(t *testing.T) {
	cols := []driver.Column{{Name: "id", IsPrimaryKey: true, PKPosition: 1}, {Name: "v"}}
	b, err := BuildSelectAll(driver.PostgresDialect{}, cols, Request{Schema: "s", Table: "t", Filters: []Filter{{Column: "v", Op: OpEq, Value: "x"}}, Offset: 10, Limit: 5})
	if err != nil || b.SQL != `SELECT "id", "v" FROM "s"."t" WHERE "v" = $1 ORDER BY "id" ASC` || len(b.Args) != 1 {
		t.Fatalf("%q %v %v", b.SQL, b.Args, err)
	}
	if strings.Contains(b.SQL, "LIMIT") {
		t.Fatal("LIMIT in a full select")
	}
	if _, err := BuildSelectAll(driver.PostgresDialect{}, nil, Request{}); err == nil {
		t.Fatal("no columns")
	}
	for s, want := range map[string]bool{"SELECT 1": true, "show tables": true, "INSERT INTO t VALUES (1)": false,
		"DELETE FROM t RETURNING id": true, "": false, "-- only a comment": false} {
		if got := ReturnsRows(driver.MySQL, s); got != want {
			t.Errorf("%q = %v", s, got)
		}
	}
}
