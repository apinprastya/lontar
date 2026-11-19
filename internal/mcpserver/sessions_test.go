package mcpserver

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"lontar/internal/conn"
	"lontar/internal/driver/all"
	"lontar/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSessionReuseAndReconnect(t *testing.T) {
	e := newEnv(t, envOpts{})
	c := e.sqlite("shop", store.AIWrite)
	ctx := context.Background()

	r1, err := e.srv.session(ctx, c, false, "")
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := e.srv.session(ctx, c, false, "")
	w, _ := e.srv.session(ctx, c, true, "")
	if r1.ID != r2.ID || w.ID == r1.ID {
		t.Fatalf("sessions %s %s %s", r1.ID, r2.ID, w.ID)
	}
	if r1.SafeMode != store.SafeReadOnly || w.SafeMode != store.SafeOff {
		t.Fatalf("safe modes %s %s", r1.SafeMode, w.SafeMode)
	}

	// Editing the connection reconnects.
	c.Tag = store.TagDev
	saved, err := e.st.Save(c, nil)
	if err != nil {
		t.Fatal(err)
	}
	r3, _ := e.srv.session(ctx, saved, false, "")
	if r3.ID == r1.ID {
		t.Fatal("an edited connection must reconnect")
	}
	if _, err := e.sessions.Get(r1.ID); !errors.Is(err, conn.ErrSessionNotFound) {
		t.Fatal("the old session must be closed")
	}

	// A session closed elsewhere is reopened.
	_ = e.sessions.Disconnect(r3.ID)
	r4, err := e.srv.session(ctx, saved, false, "")
	if err != nil || r4.ID == r3.ID {
		t.Fatalf("reopen = %v, %v", r4, err)
	}

	// Idle sessions are swept.
	clock := &fixedNow{t: time.Now()}
	e.srv.now = clock.now
	r5, _ := e.srv.session(ctx, saved, false, "")
	clock.t = clock.t.Add(DefaultIdleTimeout + time.Second)
	e.srv.sweep()
	if _, err := e.sessions.Get(r5.ID); err == nil {
		t.Fatal("idle session not closed")
	}
	if len(e.srv.pools) != 0 {
		t.Fatalf("pools left: %d", len(e.srv.pools))
	}
}

func TestSessionErrors(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	missing := e.save(store.Connection{Name: "gone", Kind: "sqlite", FilePath: e.dir + "/missing.db", AIAccess: store.AIRead}, "")
	e.fail("run_query", map[string]any{"connection": missing.ID, "sql": "SELECT 1"}, "does not exist")
	e.fail("list_tables", map[string]any{"connection": missing.ID}, "does not exist")
	e.fail("execute_write", map[string]any{"connection": e.save(store.Connection{Name: "gone2", Kind: "sqlite",
		FilePath: e.dir + "/missing2.db", AIAccess: store.AIWrite}, "").ID, "sql": "DELETE FROM t"}, "does not exist")

	// A server database that cannot be switched to.
	pg := e.save(store.Connection{Name: "pg", Kind: "postgres", Host: "127.0.0.1", Port: 1, AIAccess: store.AIRead}, "")
	e.srv.opts.Sessions = useFails{e.sessions}
	if _, err := e.srv.session(ctx, pg, false, "other"); err == nil {
		t.Fatal("want connect error")
	}
}

// useFails is a session manager whose Connect fails like a dead server.
type useFails struct{ *conn.Manager }

func (useFails) ConnectReadOnly(context.Context, string) (string, error) {
	return "", errors.New("connection refused")
}

// listFails is a connection store that cannot be read.
type listFails struct{}

func (listFails) List() ([]store.Connection, error) { return nil, errors.New("corrupt file") }

func TestConnectionsListError(t *testing.T) {
	srv := New(Options{Connections: listFails{}, Sessions: conn.NewManager(store.New(t.TempDir(), store.NewMemorySecretStore()), all.Registry()), Drivers: all.Registry()})
	if _, err := srv.visible(); err == nil {
		t.Fatal("want error")
	}
	if _, err := srv.connection("x"); err == nil {
		t.Fatal("want error")
	}
	if srv.opts.Logf == nil || srv.opts.QueryTimeout != DefaultQueryTimeout || srv.opts.IdleTimeout != DefaultIdleTimeout {
		t.Fatal("defaults not applied")
	}
	if _, _, err := srv.listConnections(context.Background(), nil, noArgs{}); err == nil {
		t.Fatal("want error")
	}
}

// Run serves until the client goes away and then closes its sessions.
func TestRun(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir, store.NewMemorySecretStore())
	sessions := conn.NewManager(st, all.Registry())
	srv := New(Options{Connections: st, Sessions: sessions, Drivers: all.Registry()})
	c, err := st.Save(store.Connection{Name: "x", Kind: "sqlite", FilePath: dir + "/x.db", CreateIfMissing: true, AIAccess: store.AIWrite}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Create the file, then close the handle (Windows can't remove an open file).
	setup, err := sessions.Connect(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.Disconnect(setup); err != nil {
		t.Fatal(err)
	}

	ct, stt := mcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- srv.Run(context.Background(), stt) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "run_query", Arguments: map[string]any{"connection": c.ID, "sql": "SELECT 1 AS x"}})
	if err != nil || res.IsError {
		t.Fatalf("call = %+v, %v", res, err)
	}
	if len(srv.pools) != 1 {
		t.Fatalf("pools = %d", len(srv.pools))
	}
	_ = cs.Close()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Logf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	if len(srv.pools) != 0 {
		t.Fatal("Run must close its sessions")
	}
}

func TestSweepLoopStops(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.srv.sweepLoop(ctx, time.Millisecond); close(done) }()
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sweep loop did not stop")
	}
}
