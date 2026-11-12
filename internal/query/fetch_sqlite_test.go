package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"lontar/internal/driver/sqlite"
	"lontar/internal/testutil"
	"lontar/internal/values"
)

// seedSQLite creates a small table with 1000 rows plus awkward names.
func seedSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db := testutil.Open(t, testutil.SQLiteTemp(t))
	stmts := []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT, qty INTEGER, data BLOB)`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 1000)
		 INSERT INTO items SELECT i, 'item ' || i, i % 10, CASE WHEN i % 100 = 0 THEN NULL ELSE x'00ff' END FROM n`,
		`UPDATE items SET name = NULL WHERE id = 5`,
		`UPDATE items SET name = '' WHERE id = 6`,
		`CREATE TABLE "Odd ""Table""" ("order" TEXT, "Mixed Case" INTEGER)`,
		`INSERT INTO "Odd ""Table""" VALUES ('x', 1), ('y', 2)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return db
}

func TestFetchTableSQLite(t *testing.T) {
	db := seedSQLite(t)
	e := sqlite.New()
	ctx := context.Background()

	p, err := FetchTable(ctx, db, e, Request{Schema: "main", Table: "items"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != DefaultLimit || !p.HasMore || p.TotalCount != nil || p.Offset != 0 || p.Limit != DefaultLimit {
		t.Fatalf("page: rows=%d hasMore=%v total=%v", len(p.Rows), p.HasMore, p.TotalCount)
	}
	if len(p.Columns) != 4 || !p.Columns[0].IsPK || p.Columns[1].Name != "name" || !p.Columns[1].Nullable {
		t.Fatalf("columns = %+v", p.Columns)
	}
	if c := p.Rows[0][0]; c.K != values.Num || c.V != int64(1) {
		t.Fatalf("first id = %+v", c)
	}
	if c := p.Rows[4][1]; c.K != values.Null {
		t.Fatalf("NULL name = %+v", c)
	}
	if c := p.Rows[5][1]; c.K != values.Str || c.V != "" {
		t.Fatalf("empty name = %+v", c)
	}
	if c := p.Rows[0][3]; c.K != values.Bytes || c.V != "00ff" {
		t.Fatalf("blob = %+v", c)
	}

	// Last partial page reports the total.
	p, err = FetchTable(ctx, db, e, Request{Schema: "main", Table: "items", Offset: 900, Limit: 300})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 100 || p.HasMore || p.TotalCount == nil || *p.TotalCount != 1000 {
		t.Fatalf("last page rows=%d hasMore=%v total=%v", len(p.Rows), p.HasMore, p.TotalCount)
	}
	// Exactly-full last page: hasMore false thanks to limit+1.
	p, _ = FetchTable(ctx, db, e, Request{Schema: "main", Table: "items", Offset: 700, Limit: 300})
	if len(p.Rows) != 300 || p.HasMore {
		t.Fatalf("full last page rows=%d hasMore=%v", len(p.Rows), p.HasMore)
	}
	// Past the end.
	p, _ = FetchTable(ctx, db, e, Request{Schema: "main", Table: "items", Offset: 5000})
	if len(p.Rows) != 0 || p.HasMore || p.TotalCount != nil {
		t.Fatalf("past end rows=%d total=%v", len(p.Rows), p.TotalCount)
	}

	// Filters + sort.
	req := Request{Schema: "main", Table: "items",
		Filters: []Filter{{Column: "qty", Op: OpIn, Value: "1,2"}, {Column: "name", Op: OpContains, Value: "ITEM 1"}},
		Sort:    []Sort{{Column: "qty", Desc: true}, {Column: "id"}},
		Limit:   5,
	}
	p, err = FetchTable(ctx, db, e, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(p.Rows[0][0].V, p.Rows[1][0].V); got != "12 102" {
		t.Fatalf("sorted ids = %s", got)
	}
	n, err := CountRows(ctx, db, e, req)
	if err != nil || n != 23 { // 1, 11, 12 and 101,102,111,112,...,191,192
		t.Fatalf("count = %d, %v", n, err)
	}
	n, err = CountRows(ctx, db, e, Request{Schema: "main", Table: "items", RawWhere: "qty = 0 OR id = 1"})
	if err != nil || n != 101 {
		t.Fatalf("raw where count = %d, %v", n, err)
	}

	// Awkward names.
	p, err = FetchTable(ctx, db, e, Request{Schema: "main", Table: `Odd "Table"`,
		Filters: []Filter{{Column: "order", Op: OpEq, Value: "y"}}, Sort: []Sort{{Column: "Mixed Case"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rows) != 1 || p.Rows[0][1].V != int64(2) {
		t.Fatalf("odd rows = %+v", p.Rows)
	}

	// Injection attempts.
	n, err = CountRows(ctx, db, e, Request{Schema: "main", Table: "items", Filters: []Filter{{Column: "name", Op: OpEq, Value: "' OR 1=1 --"}}})
	if err != nil || n != 0 {
		t.Fatalf("injection value count = %d, %v", n, err)
	}
	if _, err := FetchTable(ctx, db, e, Request{Schema: "main", Table: `items" --`}); !errors.Is(err, ErrUnknownTable) {
		t.Fatalf("bad table err = %v", err)
	}
	if _, err := CountRows(ctx, db, e, Request{Schema: "main", Table: "nope"}); !errors.Is(err, ErrUnknownTable) {
		t.Fatalf("bad table err = %v", err)
	}
	if _, err := FetchTable(ctx, db, e, Request{Schema: "main"}); !errors.Is(err, ErrUnknownTable) {
		t.Fatalf("empty table err = %v", err)
	}
	if _, err := FetchTable(ctx, db, e, Request{Schema: "main", Table: "items", Sort: []Sort{{Column: "x"}}}); !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("bad sort err = %v", err)
	}
	if _, err := CountRows(ctx, db, e, Request{Schema: "main", Table: "items", Filters: []Filter{{Column: "x", Op: OpEq}}}); !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("bad filter err = %v", err)
	}
	// A syntax error in the raw WHERE comes back from the database.
	if _, err := FetchTable(ctx, db, e, Request{Schema: "main", Table: "items", RawWhere: "qty ==== 1"}); err == nil {
		t.Fatal("expected SQL error")
	}
	if _, err := CountRows(ctx, db, e, Request{Schema: "main", Table: "items", RawWhere: "qty ==== 1"}); err == nil {
		t.Fatal("expected SQL error")
	}
}

func TestFetchTableIntrospectionError(t *testing.T) {
	db := seedSQLite(t)
	_ = db.Close()
	if _, err := FetchTable(context.Background(), db, sqlite.New(), Request{Schema: "main", Table: "items"}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := CountRows(context.Background(), db, sqlite.New(), Request{Schema: "main", Table: "items"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunner(t *testing.T) {
	r := NewRunner()
	ctx := context.Background()
	if err := r.Run(ctx, "", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	plain := errors.New("boom")
	if err := r.Run(ctx, "a", func(context.Context) error { return plain }); !errors.Is(err, plain) {
		t.Fatalf("err = %v", err)
	}

	// Cancel a running request; duplicates are refused while it runs.
	started := make(chan struct{})
	var wg sync.WaitGroup
	var runErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		runErr = r.Run(ctx, "req", func(c context.Context) error {
			close(started)
			<-c.Done()
			return c.Err()
		})
	}()
	<-started
	if r.Running() != 1 {
		t.Fatalf("running = %d", r.Running())
	}
	if err := r.Run(ctx, "req", func(context.Context) error { return nil }); !errors.Is(err, ErrDuplicateRequest) {
		t.Fatalf("dup err = %v", err)
	}
	if !r.Cancel("req") {
		t.Fatal("Cancel should find the request")
	}
	wg.Wait()
	if !errors.Is(runErr, ErrCanceled) {
		t.Fatalf("err = %v", runErr)
	}
	if r.Cancel("req") || r.Running() != 0 {
		t.Fatal("request should be gone")
	}

	// Timeout.
	r.Timeout = 10 * time.Millisecond
	err := r.Run(ctx, "slow", func(c context.Context) error { <-c.Done(); return c.Err() })
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
	// Parent cancellation is passed through unchanged.
	pctx, cancel := context.WithCancel(ctx)
	cancel()
	err = r.Run(pctx, "p", func(c context.Context) error { <-c.Done(); return c.Err() })
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestCancelSQLiteQuery(t *testing.T) {
	db := seedSQLite(t)
	r := NewRunner()
	errc := make(chan error, 1)
	go func() {
		errc <- r.Run(context.Background(), "slow", func(ctx context.Context) error {
			// A cross join that takes a long time to count.
			_, err := CountRows(ctx, db, sqlite.New(), Request{Schema: "main", Table: "items",
				RawWhere: "(SELECT COUNT(*) FROM items a, items b, items c) > 0"})
			return err
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !r.Cancel("slow") {
		if time.Now().After(deadline) {
			t.Fatal("request never registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case err := <-errc:
		if !errors.Is(err, ErrCanceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not stop the query")
	}
}
