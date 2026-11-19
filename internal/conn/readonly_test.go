package conn

import (
	"context"
	"testing"

	"lontar/internal/driver/all"
	"lontar/internal/store"
)

func TestConnectReadOnly(t *testing.T) {
	c := sqliteConn(t, "a", true)
	m := NewManager(newFakeSource(c), all.Registry())
	ctx := context.Background()
	defer func() { _ = m.DisconnectAll() }()

	// Create the file with a table through a normal session.
	rw, err := m.Connect(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := m.Get(rw)
	if s.SafeMode != store.SafeOff {
		t.Fatalf("safe mode = %q", s.SafeMode)
	}
	if _, err := s.DB.ExecContext(ctx, "CREATE TABLE t (x INTEGER)"); err != nil {
		t.Fatal(err)
	}

	ro, err := m.ConnectReadOnly(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := m.Get(ro)
	if r.SafeMode != store.SafeReadOnly {
		t.Fatalf("read-only session safe mode = %q", r.SafeMode)
	}
	if _, err := r.DB.ExecContext(ctx, "INSERT INTO t VALUES (1)"); err == nil {
		t.Fatal("write on a read-only session must fail")
	}
	var n int
	if err := r.DB.QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&n); err != nil || n != 0 {
		t.Fatalf("read = %d, %v", n, err)
	}
}
