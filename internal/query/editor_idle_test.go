package query

import (
	"testing"

	"lontar/internal/testutil"
)

func TestEditorsIdleHold(t *testing.T) {
	e, db := liteEditors(t)
	other := testutil.Open(t, testutil.SQLiteTemp(t))
	if n := e.IdleHold("s1", db); n != 0 {
		t.Fatalf("no editors: %d", n)
	}
	id := openEditor(t, e)
	if n := e.IdleHold("s1", db); n != 0 {
		t.Fatalf("editor without a connection: %d", n)
	}
	run(t, e, id, "SELECT 1", ExecOptions{})
	if n := e.IdleHold("s1", db); n != 1 {
		t.Fatalf("idle pinned editor: %d", n)
	}
	if n := e.IdleHold("s1", other); n != 0 {
		t.Fatalf("editor pinned from another pool: %d", n)
	}
	if n := e.IdleHold("s2", db); n != 0 {
		t.Fatalf("other session: %d", n)
	}

	// In a transaction.
	run(t, e, id, "BEGIN", ExecOptions{})
	if n := e.IdleHold("s1", db); n != -1 {
		t.Fatalf("in a transaction: %d", n)
	}
	run(t, e, id, "COMMIT", ExecOptions{})
	if n := e.IdleHold("s1", db); n != 1 {
		t.Fatalf("after commit: %d", n)
	}

	// A truncated result keeps its cursor.
	run(t, e, id, seriesSQL(50), ExecOptions{MaxRows: 10})
	if n := e.IdleHold("s1", db); n != -1 {
		t.Fatalf("open cursor: %d", n)
	}
	run(t, e, id, "SELECT 1", ExecOptions{})

	// Running.
	ed, _ := e.get(id)
	ed.run.Lock()
	if n := e.IdleHold("s1", db); n != -1 {
		t.Fatalf("running: %d", n)
	}
	ed.run.Unlock()
}

func TestEditorsSessionSuspended(t *testing.T) {
	e, db := liteEditors(t)
	other := testutil.Open(t, testutil.SQLiteTemp(t))
	id := openEditor(t, e)
	run(t, e, id, "CREATE TEMP TABLE tt (x)", ExecOptions{})

	// Another pool: the editor keeps its connection.
	e.SessionSuspended("s1", other)
	if e.Pinned(db) != 1 {
		t.Fatal("released a connection of another pool")
	}
	e.SessionSuspended("s1", db)
	if e.Pinned(db) != 0 {
		t.Fatal("connection not released")
	}
	res := run(t, e, id, "SELECT * FROM tt", ExecOptions{})
	if !res.SessionReset || len(res.Notices) == 0 || res.Notices[0] != msgSuspended {
		t.Fatalf("after suspend = %+v", res)
	}
	// An editor without a connection is left alone.
	fresh := openEditor(t, e)
	e.SessionSuspended("s1", db)
	if res := run(t, e, fresh, "SELECT 1", ExecOptions{}); res.SessionReset {
		t.Fatalf("fresh editor reset: %+v", res)
	}
}
