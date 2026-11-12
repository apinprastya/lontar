package driver_test

import (
	"context"
	"testing"

	"lontar/internal/driver"
	"lontar/internal/testutil"
)

func TestQuoteIdent(t *testing.T) {
	cases := []struct {
		d    driver.Dialect
		in   string
		want string
	}{
		{driver.PostgresDialect{}, "users", `"users"`},
		{driver.PostgresDialect{}, `we"ird`, `"we""ird"`},
		{driver.PostgresDialect{}, `""`, `""""""`},
		{driver.PostgresDialect{}, "Mixed Case", `"Mixed Case"`},
		{driver.PostgresDialect{}, "a`b", "\"a`b\""},
		{driver.SQLiteDialect{}, `x"; DROP TABLE t; --`, `"x""; DROP TABLE t; --"`},
		{driver.SQLiteDialect{}, "order", `"order"`},
		{driver.MySQLDialect{}, "users", "`users`"},
		{driver.MySQLDialect{}, "we`ird", "`we``ird`"},
		{driver.MySQLDialect{}, `a"b`, "`a\"b`"},
		{driver.MySQLDialect{}, "x`; DROP TABLE t; --", "`x``; DROP TABLE t; --`"},
	}
	for _, tc := range cases {
		if got := tc.d.QuoteIdent(tc.in); got != tc.want {
			t.Errorf("%T.QuoteIdent(%q) = %s, want %s", tc.d, tc.in, got, tc.want)
		}
	}
}

func TestDialectHelpers(t *testing.T) {
	pg, my, lite := driver.PostgresDialect{}, driver.MySQLDialect{}, driver.SQLiteDialect{}
	if pg.Placeholder(1) != "$1" || pg.Placeholder(12) != "$12" || my.Placeholder(3) != "?" || lite.Placeholder(3) != "?" {
		t.Fatal("Placeholder")
	}
	for _, d := range []driver.Dialect{pg, my, lite} {
		if got := d.LimitOffset(300, 600); got != " LIMIT 300 OFFSET 600" {
			t.Errorf("LimitOffset = %q", got)
		}
		if got := d.LimitOffset(-1, -5); got != " LIMIT 0 OFFSET 0" {
			t.Errorf("LimitOffset clamp = %q", got)
		}
	}
	if pg.CaseInsensitiveLike() != "ILIKE" || my.CaseInsensitiveLike() != "LIKE" || lite.CaseInsensitiveLike() != "LIKE" {
		t.Fatal("CaseInsensitiveLike")
	}
	if pg.TextExpr(`"a"`) != `CAST("a" AS TEXT)` || my.TextExpr("`a`") != "`a`" || lite.TextExpr(`"a"`) != `"a"` {
		t.Fatal("TextExpr")
	}
	if _, ok := driver.DialectFor(driver.Postgres).(driver.PostgresDialect); !ok {
		t.Fatal("DialectFor postgres")
	}
	if _, ok := driver.DialectFor(driver.MariaDB).(driver.MySQLDialect); !ok {
		t.Fatal("DialectFor mariadb")
	}
	if _, ok := driver.DialectFor(driver.MySQL).(driver.MySQLDialect); !ok {
		t.Fatal("DialectFor mysql")
	}
	if _, ok := driver.DialectFor(driver.SQLite).(driver.SQLiteDialect); !ok {
		t.Fatal("DialectFor sqlite")
	}
	if driver.DialectFor("oracle") != nil {
		t.Fatal("DialectFor unknown")
	}
}

func TestScanHelpers(t *testing.T) {
	db := testutil.Open(t, testutil.SQLiteTemp(t))
	ctx := context.Background()
	got, err := driver.ScanStrings(ctx, db, "SELECT 'a' UNION ALL SELECT ?", "b")
	if err != nil || len(got) != 2 || got[1] != "b" {
		t.Fatalf("ScanStrings = %v, %v", got, err)
	}
	if _, err := driver.ScanStrings(ctx, db, "SELEC"); err == nil {
		t.Fatal("expected syntax error")
	}
	if _, err := driver.ScanStrings(ctx, db, "SELECT NULL"); err == nil {
		t.Fatal("expected scan error for NULL into string")
	}
	s, err := driver.ScanString(ctx, db, "SELECT NULL")
	if err != nil || s != "" {
		t.Fatalf("ScanString NULL = %q, %v", s, err)
	}
	if _, err := driver.ScanString(ctx, db, "SELEC"); err == nil {
		t.Fatal("expected error")
	}
}
