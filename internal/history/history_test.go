package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func i64(n int64) *int64 { return &n }

func TestAddListDeleteClear(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "cfg")
	s := New(dir)
	t.Cleanup(func() { _ = s.Close() })

	start := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	id1, err := s.Add(ctx, Entry{ConnectionID: "c1", Database: "shop", SQL: "SELECT * FROM users", StartedAt: start, DurationMs: 1.5, RowCount: i64(3)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, Entry{ConnectionID: "c1", SQL: "UPDATE t SET a = '50%_off!'", RowsAffected: i64(2)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, Entry{ConnectionID: "c2", SQL: "SELEC 1", IsError: true, Error: "syntax error"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{FileName, FileName + "-wal"} {
		if fi, err := os.Stat(filepath.Join(dir, name)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", name, fi, err)
		}
	}

	all, err := s.List(ctx, "c1", "", 0, 0)
	if err != nil || len(all) != 2 {
		t.Fatalf("List = %+v, %v", all, err)
	}
	if all[0].SQL != "UPDATE t SET a = '50%_off!'" || all[1].ID != id1 {
		t.Fatalf("not newest first: %+v", all)
	}
	e := all[1]
	if e.Database != "shop" || !e.StartedAt.Equal(start) || e.DurationMs != 1.5 || e.RowCount == nil || *e.RowCount != 3 || e.RowsAffected != nil || e.IsError {
		t.Fatalf("entry = %+v", e)
	}
	if all[0].StartedAt.IsZero() || all[0].RowsAffected == nil || *all[0].RowsAffected != 2 {
		t.Fatalf("defaults = %+v", all[0])
	}

	for search, want := range map[string]int{"users": 1, "USERS": 1, "%": 1, "_off!": 1, "x%y": 0, "": 2, "'; DROP TABLE history; --": 0} {
		got, err := s.List(ctx, "c1", search, 10, 0)
		if err != nil || len(got) != want {
			t.Errorf("search %q = %d entries (%v), want %d", search, len(got), err, want)
		}
	}
	if got, _ := s.List(ctx, "", "", 10, 0); len(got) != 3 {
		t.Fatalf("all connections = %d", len(got))
	}
	if got, _ := s.List(ctx, "c1", "", 1, 1); len(got) != 1 || got[0].ID != id1 {
		t.Fatalf("paging = %+v", got)
	}
	if got, _ := s.List(ctx, "c2", "", 10, -3); len(got) != 1 || !got[0].IsError || got[0].Error != "syntax error" {
		t.Fatalf("error entry = %+v", got)
	}

	if err := s.Delete(ctx, id1); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, id1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
	n, err := s.Clear(ctx, "c1")
	if err != nil || n != 1 {
		t.Fatalf("Clear = %d, %v", n, err)
	}
	if got, _ := s.List(ctx, "c2", "", 10, 0); len(got) != 1 {
		t.Fatal("Clear removed another connection's entries")
	}
	if n, _ := s.Clear(ctx, ""); n != 1 {
		t.Fatalf("Clear all = %d", n)
	}

	// Reopens after Close.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, Entry{ConnectionID: "c1", SQL: "SELECT 1"}); err != nil {
		t.Fatal(err)
	}
}

func TestPrune(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())
	s.Limit = 3
	t.Cleanup(func() { _ = s.Close() })
	for i := range 5 {
		if _, err := s.Add(ctx, Entry{ConnectionID: "c1", SQL: "SELECT " + string(rune('a'+i))}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Add(ctx, Entry{ConnectionID: "c2", SQL: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.List(ctx, "c1", "", 0, 0)
	if len(got) != 3 || got[0].SQL != "SELECT e" || got[2].SQL != "SELECT c" {
		t.Fatalf("pruned = %+v", got)
	}
	if got, _ := s.List(ctx, "c2", "", 0, 0); len(got) != 3 {
		t.Fatalf("c2 = %d", len(got))
	}
	if New("x").limit() != DefaultLimit {
		t.Fatal("default limit")
	}
}

func TestTruncateLongSQL(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())
	t.Cleanup(func() { _ = s.Close() })
	long := strings.Repeat("ä", MaxSQLBytes) // 2 bytes each
	if _, err := s.Add(ctx, Entry{ConnectionID: "c", SQL: long}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.List(ctx, "c", "", 1, 0)
	if len(got) != 1 || !got[0].Truncated || len(got[0].SQL) != MaxSQLBytes || !strings.HasPrefix(long, got[0].SQL) {
		t.Fatalf("truncated entry len=%d trunc=%v", len(got[0].SQL), got[0].Truncated)
	}
	// Odd cut point: never split a rune.
	text, cutOK := cut("a" + long)
	if !cutOK || len(text) != MaxSQLBytes-1 {
		t.Fatalf("cut = %d %v", len(text), cutOK)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	s := New("")
	if _, err := s.Add(ctx, Entry{ConnectionID: "c"}); err == nil {
		t.Fatal("no dir: want error")
	}
	if _, err := s.List(ctx, "c", "", 0, 0); err == nil {
		t.Fatal("no dir: want error")
	}
	if err := s.Delete(ctx, 1); err == nil {
		t.Fatal("no dir: want error")
	}
	if _, err := s.Clear(ctx, ""); err == nil {
		t.Fatal("no dir: want error")
	}

	// The directory is not writable.
	ro := filepath.Join(t.TempDir(), "ro")
	if err := os.MkdirAll(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() != 0 {
		if _, err := New(ro).List(ctx, "", "", 0, 0); err == nil {
			t.Fatal("read-only dir: want error")
		}
	}
	// The directory is a file.
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(file).List(ctx, "", "", 0, 0); err == nil {
		t.Fatal("dir is a file: want error")
	}
	// The database is not an SQLite file.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(strings.Repeat("garbage!", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir).List(ctx, "", "", 0, 0); err == nil {
		t.Fatal("corrupt db: want error")
	}

	ok := New(t.TempDir())
	t.Cleanup(func() { _ = ok.Close() })
	if _, err := ok.Add(ctx, Entry{}); err == nil {
		t.Fatal("missing connection ID: want error")
	}
	// Closed underlying DB: statements fail.
	db, _ := ok.open()
	_ = db.Close()
	if _, err := ok.Add(ctx, Entry{ConnectionID: "c"}); err == nil {
		t.Fatal("closed db add: want error")
	}
	if _, err := ok.List(ctx, "c", "", 0, 0); err == nil {
		t.Fatal("closed db list: want error")
	}
	if err := ok.Delete(ctx, 1); err == nil {
		t.Fatal("closed db delete: want error")
	}
	if _, err := ok.Clear(ctx, "c"); err == nil {
		t.Fatal("closed db clear: want error")
	}
}
