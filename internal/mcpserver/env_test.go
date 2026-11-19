package mcpserver

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lontar/internal/conn"
	"lontar/internal/driver"
	"lontar/internal/driver/all"
	"lontar/internal/history"
	"lontar/internal/store"
	"lontar/internal/testutil"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

// env is a server backed by a real store, session manager and history,
// with an in-memory MCP client.
type env struct {
	t        *testing.T
	dir      string
	st       *store.Store
	sessions *conn.Manager
	hist     *history.Store
	srv      *Server
	cs       *mcp.ClientSession
	// approve answers elicitation requests ("accept", "decline", …).
	approve func(msg string) string
	asked   []string
	files   int
}

type envOpts struct {
	noElicitation bool
	// protocol pins the client's protocol version ("" = latest).
	protocol string
	// manualRoundTrip disables the client's automatic multi round-trip
	// handling, so tests see input requests themselves.
	manualRoundTrip bool
}

func newEnv(t *testing.T, o envOpts) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{t: t, dir: dir, approve: func(string) string { return "accept" }}
	e.st = store.New(dir, store.NewMemorySecretStore())
	e.sessions = conn.NewManager(e.st, all.Registry())
	e.hist = history.New(dir)
	e.srv = New(Options{Connections: e.st, Sessions: e.sessions, Drivers: all.Registry(), History: e.hist, Version: "test",
		Logf: func(string, ...any) {}})

	copts := &mcp.ClientOptions{}
	if !o.noElicitation {
		copts.ElicitationHandler = func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			e.asked = append(e.asked, req.Params.Message)
			return &mcp.ElicitResult{Action: e.approve(req.Params.Message)}, nil
		}
	}
	if o.manualRoundTrip {
		copts.MultiRoundTrip = &mcp.MultiRoundTripOptions{Disabled: true}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, copts)
	ct, stt := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := e.srv.MCP().Connect(ctx, stt, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := client.Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: o.protocol})
	if err != nil {
		t.Fatal(err)
	}
	e.cs = cs
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Wait()
		e.srv.Close()
		_ = e.sessions.DisconnectAll()
		_ = e.hist.Close()
	})
	return e
}

// save stores a connection and returns it with its ID.
func (e *env) save(c store.Connection, password string) store.Connection {
	e.t.Helper()
	var pw *string
	if password != "" {
		pw = &password
	}
	saved, err := e.st.Save(c, pw)
	if err != nil {
		e.t.Fatal(err)
	}
	return saved
}

// sqlite creates a seeded SQLite file and saves a connection to it.
func (e *env) sqlite(name string, access store.AIAccess) store.Connection {
	e.t.Helper()
	e.files++
	path := filepath.Join(e.dir, fmt.Sprintf("%s-%d.db", name, e.files))
	db, err := sql.Open("sqlite", path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(sqliteSeed); err != nil {
		e.t.Fatal(err)
	}
	return e.save(store.Connection{Name: name, Kind: driver.SQLite, FilePath: path, AIAccess: access}, "")
}

const sqliteSeed = `
CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT UNIQUE, note TEXT, data BLOB);
CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, total REAL DEFAULT 0);
CREATE INDEX orders_user ON orders(user_id);
CREATE VIEW big_orders AS SELECT * FROM orders WHERE total > 100;
CREATE TABLE audit (msg TEXT);
CREATE TRIGGER users_ai AFTER INSERT ON users BEGIN INSERT INTO audit VALUES ('added ' || NEW.name); END;
INSERT INTO users (id, name, email, note, data) VALUES
  (1, 'Alice', 'a@x', NULL, x'DEADBEEF'),
  (2, 'Bob', 'b@x', '', NULL),
  (3, 'Carol, Jr.', 'c@x', 'NULL', NULL);
INSERT INTO orders (user_id, total) VALUES (1, 10.5), (1, 250), (2, 99);
`

// call runs a tool and returns its text and whether it is an error.
func (e *env) call(name string, args map[string]any) (string, bool) {
	e.t.Helper()
	res, err := e.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

// ok runs a tool that must succeed.
func (e *env) ok(name string, args map[string]any) string {
	e.t.Helper()
	out, isErr := e.call(name, args)
	if isErr {
		e.t.Fatalf("%s(%v) failed: %s", name, args, out)
	}
	return out
}

// fail runs a tool that must fail with a message containing want.
func (e *env) fail(name string, args map[string]any, want string) string {
	e.t.Helper()
	out, isErr := e.call(name, args)
	if !isErr || !strings.Contains(out, want) {
		e.t.Fatalf("%s(%v) = %q (error %v), want error containing %q", name, args, out, isErr, want)
	}
	return out
}

// history returns the MCP history of a connection, newest first.
func (e *env) history(id string) []history.Entry {
	e.t.Helper()
	list, err := e.hist.List(context.Background(), id, "", 100, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	return list
}

// wantContains checks every want is in got.
func wantContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Fatalf("output lacks %q:\n%s", w, got)
		}
	}
}

// count runs a scalar query on a fresh connection to the SQLite file.
func countRows(t *testing.T, c store.Connection, q string) int {
	t.Helper()
	db, err := sql.Open("sqlite", c.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// fixedNow lets tests move the server's clock.
type fixedNow struct{ t time.Time }

func (f *fixedNow) now() time.Time { return f.t }
