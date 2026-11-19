package history

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSourceRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Add(ctx, Entry{ConnectionID: "c", SQL: "SELECT 1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, Entry{ConnectionID: "c", SQL: "SELECT 2", Source: SourceMCP}); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, "c", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Source != SourceMCP || list[1].Source != SourceEditor {
		t.Fatalf("list = %+v", list)
	}
}

// A history.db from before the source column is upgraded in place.
func TestUpgradeAddsSourceColumn(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	old, err := sql.Open("sqlite", filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE history (
		id INTEGER PRIMARY KEY AUTOINCREMENT, connection_id TEXT NOT NULL, database TEXT NOT NULL DEFAULT '',
		sql TEXT NOT NULL, started_at INTEGER NOT NULL, duration_ms REAL NOT NULL DEFAULT 0, row_count INTEGER,
		rows_affected INTEGER, is_error INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '',
		truncated INTEGER NOT NULL DEFAULT 0);
		INSERT INTO history (connection_id, sql, started_at) VALUES ('c', 'SELECT old', 1)`); err != nil {
		t.Fatal(err)
	}
	_ = old.Close()

	s := New(dir)
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Add(ctx, Entry{ConnectionID: "c", SQL: "SELECT new", Source: SourceMCP}); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, "c", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Source != SourceMCP || list[1].Source != "" || list[1].SQL != "SELECT old" {
		t.Fatalf("list = %+v", list)
	}
	// Opening again is a no-op.
	_ = s.Close()
	if _, err := s.List(ctx, "c", "", 10, 0); err != nil {
		t.Fatal(err)
	}
}

func TestAddSourceColumnDuplicateIsFine(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := addSourceColumn(db); err == nil {
		t.Fatal("missing table must fail")
	}
	if _, err := db.Exec(`CREATE TABLE history (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := addSourceColumn(db); err != nil {
		t.Fatal(err)
	}
	if err := addSourceColumn(db); err != nil {
		t.Fatal(err)
	}
}
