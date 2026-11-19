package mcpserver

import (
	"context"
	"os"
	"strings"
	"testing"

	"lontar/internal/history"
	"lontar/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestListConnections(t *testing.T) {
	e := newEnv(t, envOpts{})
	out := e.ok("list_connections", nil)
	wantContains(t, out, "No connections are enabled for AI access")

	e.sqlite("hidden", store.AIOff)
	ro := e.sqlite("reader", store.AIRead)
	rw := e.sqlite("writer", store.AIWrite)
	capped := e.save(store.Connection{Name: "capped", Kind: "postgres", Host: "db", Database: "shop", Tag: store.TagProd,
		SafeMode: store.SafeReadOnly, AIAccess: store.AIWrite}, "secret")

	out = e.ok("list_connections", nil)
	if strings.Contains(out, "hidden") {
		t.Fatalf("an AI-off connection is listed:\n%s", out)
	}
	wantContains(t, out,
		"id: "+ro.ID+" | name: reader | engine: sqlite | access: read",
		"id: "+rw.ID+" | name: writer | engine: sqlite | access: write | safe mode: off",
		"id: "+capped.ID+" | name: capped | engine: postgres | database: shop | tag: prod | access: read | safe mode: readonly")
	for _, secret := range []string{"secret", "db |", capped.FilePath + "x", "host"} {
		if strings.Contains(out, secret) {
			t.Fatalf("output leaks %q:\n%s", secret, out)
		}
	}
}

func TestConnectionResolution(t *testing.T) {
	e := newEnv(t, envOpts{})
	a := e.sqlite("Shop", store.AIRead)
	e.sqlite("dup", store.AIRead)
	e.sqlite("dup", store.AIRead)
	off := e.sqlite("off", store.AIOff)

	e.ok("list_tables", map[string]any{"connection": a.ID})
	e.ok("list_tables", map[string]any{"connection": "shop"}) // name, any case
	e.fail("list_tables", map[string]any{"connection": "dup"}, "2 connections are named")
	e.fail("list_tables", map[string]any{"connection": off.ID}, "not enabled for AI access")
	e.fail("list_tables", map[string]any{"connection": "nope"}, "not enabled for AI access")
	e.fail("list_tables", map[string]any{"connection": " "}, "connection is required")
	e.fail("list_tables", map[string]any{"connection": a.ID, "database": "other"}, "single database")
	e.ok("list_tables", map[string]any{"connection": a.ID, "database": "main"})
}

func TestSchemaTools(t *testing.T) {
	e := newEnv(t, envOpts{})
	c := e.sqlite("shop", store.AIRead)
	id := map[string]any{"connection": c.ID}

	wantContains(t, e.ok("list_databases", id), "main (current)")
	wantContains(t, e.ok("list_schemas", id), "main (current)")
	wantContains(t, e.ok("list_tables", id), "Schema main:", "- users (table", "- big_orders (view")

	out := e.ok("describe_table", map[string]any{"connection": c.ID, "table": "orders"})
	wantContains(t, out, "table main.orders", "- id INTEGER NOT NULL PRIMARY KEY(1)", "- user_id INTEGER NOT NULL",
		"DEFAULT 0", "orders_user INDEX (user_id)", "Foreign keys:", "- main.orders (user_id) -> main.users (id) ON DELETE CASCADE")
	wantContains(t, e.ok("describe_table", map[string]any{"connection": c.ID, "table": "users"}),
		"Referenced by:", "UNIQUE (email)", "Triggers:", "users_ai AFTER INSERT")
	e.fail("describe_table", map[string]any{"connection": c.ID, "table": "missing"}, `table "missing" not found`)

	wantContains(t, e.ok("get_ddl", map[string]any{"connection": c.ID, "name": "users"}), "CREATE TABLE users")
	wantContains(t, e.ok("get_ddl", map[string]any{"connection": c.ID, "name": "big_orders", "kind": "view"}), "CREATE VIEW big_orders")
	e.fail("get_ddl", map[string]any{"connection": c.ID, "name": "missing"}, "not found")

	wantContains(t, e.ok("list_objects", id), "- trigger users_ai on users")
	wantContains(t, e.ok("get_ddl", map[string]any{"connection": c.ID, "name": "users_ai", "kind": "trigger"}), "CREATE TRIGGER users_ai")
	e.fail("get_ddl", map[string]any{"connection": c.ID, "name": "nope", "kind": "trigger"}, "call list_objects")
}

func TestEmptySchema(t *testing.T) {
	e := newEnv(t, envOpts{})
	path := e.dir + "/empty.db"
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c := e.save(store.Connection{Name: "empty", Kind: "sqlite", FilePath: path, AIAccess: store.AIRead}, "")
	wantContains(t, e.ok("list_tables", map[string]any{"connection": c.ID}), "has no tables or views")
	wantContains(t, e.ok("list_objects", map[string]any{"connection": c.ID}), "has no functions")
}

func TestRunQuery(t *testing.T) {
	e := newEnv(t, envOpts{})
	c := e.sqlite("shop", store.AIRead)

	out := e.ok("run_query", map[string]any{"connection": c.ID, "sql": "SELECT id, name, note, data FROM users ORDER BY id"})
	want := "3 rows\nid,name,note,data\n1,Alice,NULL,\\xdeadbeef\n2,Bob,\"\",NULL\n3,\"Carol, Jr.\",\"NULL\",NULL\n"
	if out != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}

	out = e.ok("run_query", map[string]any{"connection": c.ID, "sql": "SELECT id FROM users ORDER BY id; SELECT count(*) AS n FROM orders", "max_rows": 2})
	wantContains(t, out, "-- Statement 1 of 2: first 2 rows shown; more rows exist (max_rows=2", "id\n1\n2\n", "-- Statement 2 of 2: 1 row\nn\n3\n")

	// Partial output and the failing statement.
	out = e.fail("run_query", map[string]any{"connection": c.ID, "sql": "SELECT 1 AS one; SELECT * FROM nope"}, "Statement 2 of 2 failed")
	wantContains(t, out, "one\n1\n", "no such table")

	// Recorded in history with the MCP source.
	h := e.history(c.ID)
	if len(h) != 3 || h[0].Source != history.SourceMCP || !h[0].IsError || *h[2].RowCount != 3 {
		t.Fatalf("history = %+v", h)
	}
}

func TestRunQueryRefusals(t *testing.T) {
	e := newEnv(t, envOpts{})
	c := e.sqlite("shop", store.AIWrite)
	for sql, want := range map[string]string{
		"":                                       "no statements",
		"-- only a comment":                      "no statements",
		"SELECT 1; DELETE FROM users":            "statement 2 (DELETE) changes data",
		"UPDATE users SET name = 'x'":            "run_query is read-only",
		"CREATE TABLE t (x int)":                 "changes data",
		"BEGIN":                                  "starts with BEGIN",
		"COMMIT":                                 "starts with COMMIT",
		"PRAGMA foreign_keys = OFF":              "changes data",
		"SELECT load_extension('x')":             "calls load_extension",
		"WITH d AS (DELETE FROM users) SELECT 1": "changes data",
	} {
		e.fail("run_query", map[string]any{"connection": c.ID, "sql": sql}, want)
	}
	if n := countRows(t, c, "SELECT count(*) FROM users"); n != 3 {
		t.Fatalf("users = %d", n)
	}
	if len(e.history(c.ID)) != 0 {
		t.Fatal("refused statements must not be recorded")
	}
}

func TestMaxRowsBounds(t *testing.T) {
	e := newEnv(t, envOpts{})
	c := e.sqlite("shop", store.AIRead)
	gen := "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 1500) SELECT i FROM n"
	out := e.ok("run_query", map[string]any{"connection": c.ID, "sql": gen})
	wantContains(t, out, "first 200 rows shown")
	out = e.ok("run_query", map[string]any{"connection": c.ID, "sql": gen, "max_rows": 5000})
	wantContains(t, out, "first 1000 rows shown", "max_rows=1000")

	// Wide rows hit the output limit before max_rows.
	wide := "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i < 900) SELECT i, printf('%.1500c', 'x') AS pad FROM n"
	out = e.ok("run_query", map[string]any{"connection": c.ID, "sql": wide, "max_rows": 1000})
	wantContains(t, out, "output limit (64 KB) reached", "more rows read but not shown")
	if len(out) > MaxOutputBytes+1024 {
		t.Fatalf("output is %d bytes", len(out))
	}
	// Long cells are clipped.
	out = e.ok("run_query", map[string]any{"connection": c.ID, "sql": "SELECT printf('%.5000c', 'y') AS long"})
	wantContains(t, out, "… (5000 bytes)")
}

// The read session is read-only at the database level: even a statement
// that slipped past the checks fails.
func TestReadSessionIsReadOnly(t *testing.T) {
	e := newEnv(t, envOpts{})
	c := e.sqlite("shop", store.AIWrite)
	sess, err := e.srv.session(context.Background(), c, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runRead(context.Background(), sess, []string{"DELETE FROM users"}, 10); err == nil {
		t.Fatal("a write on the read session must fail")
	}
	if n := countRows(t, c, "SELECT count(*) FROM users"); n != 3 {
		t.Fatalf("users = %d", n)
	}
}

func TestExplainQuery(t *testing.T) {
	e := newEnv(t, envOpts{})
	c := e.sqlite("shop", store.AIRead)
	out := e.ok("explain_query", map[string]any{"connection": c.ID, "sql": "SELECT * FROM orders WHERE user_id = 1"})
	wantContains(t, out, "plan (EXPLAIN QUERY PLAN", "orders_user")
	e.fail("explain_query", map[string]any{"connection": c.ID, "sql": "SELECT 1; SELECT 2"}, "exactly one statement")
	e.fail("explain_query", map[string]any{"connection": c.ID, "sql": "SELECT * FROM nope"}, "no such table")
	e.fail("explain_query", map[string]any{"connection": c.ID, "sql": ""}, "no statements")
	e.fail("explain_query", map[string]any{"connection": "x", "sql": "SELECT 1"}, "not enabled")
	if h := e.history(c.ID); len(h) != 2 || h[0].Source != history.SourceMCP || !h[0].IsError {
		t.Fatalf("history = %+v", h)
	}
}

func TestExecuteWrite(t *testing.T) {
	e := newEnv(t, envOpts{})
	c := e.save(store.Connection{Name: "w", Kind: "sqlite", FilePath: e.sqlite("w0", store.AIOff).FilePath,
		AIAccess: store.AIWrite, Tag: store.TagProd}, "")

	sql := "INSERT INTO users (id, name) VALUES (10, 'Dan'); UPDATE orders SET total = total + 1 WHERE user_id = 1"
	out := e.ok("execute_write", map[string]any{"connection": c.ID, "sql": sql})
	wantContains(t, out, "Committed.", "Statement 1: 1 row affected", "Statement 2: 2 rows affected")
	if len(e.asked) != 1 {
		t.Fatalf("asked %d times", len(e.asked))
	}
	wantContains(t, e.asked[0], `change data on "w" (sqlite`, "tagged prod", sql)
	if n := countRows(t, c, "SELECT count(*) FROM users"); n != 4 {
		t.Fatalf("users = %d", n)
	}
	h := e.history(c.ID)
	if len(h) != 1 || h[0].Source != history.SourceMCP || h[0].RowsAffected == nil || *h[0].RowsAffected != 3 {
		t.Fatalf("history = %+v", h)
	}

	// Declined: nothing runs.
	e.approve = func(string) string { return "decline" }
	e.fail("execute_write", map[string]any{"connection": c.ID, "sql": "DELETE FROM users"}, "did not approve")
	if n := countRows(t, c, "SELECT count(*) FROM users"); n != 4 {
		t.Fatalf("users = %d", n)
	}
	e.approve = func(string) string { return "accept" }

	// A failing statement rolls the whole change back.
	out = e.fail("execute_write", map[string]any{"connection": c.ID, "sql": "DELETE FROM orders; INSERT INTO users (id, name) VALUES (1, 'dup')"},
		"Statement 2 of 2 failed")
	wantContains(t, out, "rolled back")
	if n := countRows(t, c, "SELECT count(*) FROM orders"); n != 3 {
		t.Fatalf("orders = %d", n)
	}
}

func TestExecuteWriteRefusals(t *testing.T) {
	e := newEnv(t, envOpts{})
	ro := e.sqlite("ro", store.AIRead)
	rw := e.sqlite("rw", store.AIWrite)
	capped := e.save(store.Connection{Name: "capped", Kind: "sqlite", FilePath: rw.FilePath, ReadOnly: true, AIAccess: store.AIWrite}, "")

	e.fail("execute_write", map[string]any{"connection": ro.ID, "sql": "DELETE FROM users"}, "does not allow AI writes")
	e.fail("execute_write", map[string]any{"connection": capped.ID, "sql": "DELETE FROM users"}, "does not allow AI writes")
	e.fail("execute_write", map[string]any{"connection": "nope", "sql": "DELETE FROM users"}, "not enabled")
	for sql, want := range map[string]string{
		"":                                      "no statements",
		"DROP TABLE users":                      "only runs INSERT, UPDATE, DELETE",
		"SELECT * FROM users":                   "only runs INSERT",
		"DELETE FROM users; CREATE TABLE x (a)": "statement 2 starts with CREATE",
		"WITH a AS (SELECT 1) SELECT * FROM a":  "only runs INSERT",
		"INSERT INTO users (name) SELECT load_extension('x')":                            "calls load_extension",
		"DELETE FROM users WHERE name = '" + strings.Repeat("x", MaxWriteSQLBytes) + "'": "Split the change",
	} {
		e.fail("execute_write", map[string]any{"connection": rw.ID, "sql": sql}, want)
	}
	e.fail("execute_write", map[string]any{"connection": rw.ID, "sql": "DELETE FROM users", "database": "x"}, "single database")
	if len(e.asked) != 0 {
		t.Fatal("refused writes must not ask the user")
	}
	if n := countRows(t, rw, "SELECT count(*) FROM users"); n != 3 {
		t.Fatalf("users = %d", n)
	}
}

func TestExecuteWriteWithoutElicitation(t *testing.T) {
	e := newEnv(t, envOpts{noElicitation: true})
	c := e.sqlite("rw", store.AIWrite)
	e.fail("execute_write", map[string]any{"connection": c.ID, "sql": "DELETE FROM users"}, "does not support approval prompts")
	if n := countRows(t, c, "SELECT count(*) FROM users"); n != 3 {
		t.Fatalf("users = %d", n)
	}
}

// Access revoked while the user was deciding: nothing runs.
func TestExecuteWriteRevokedDuringApproval(t *testing.T) {
	e := newEnv(t, envOpts{})
	c := e.sqlite("rw", store.AIWrite)
	e.approve = func(string) string {
		c.AIAccess = store.AIRead
		if _, err := e.st.Save(c, nil); err != nil {
			t.Error(err)
		}
		return "accept"
	}
	e.fail("execute_write", map[string]any{"connection": c.ID, "sql": "DELETE FROM users"}, "does not allow AI writes")
	if n := countRows(t, c, "SELECT count(*) FROM users"); n != 3 {
		t.Fatalf("users = %d", n)
	}

	c.AIAccess = store.AIOff
	if _, err := e.st.Save(c, nil); err != nil {
		t.Fatal(err)
	}
	e.approve = func(string) string { return "accept" }
	e.fail("execute_write", map[string]any{"connection": c.ID, "sql": "DELETE FROM users"}, "not enabled")
}

func TestSchemaResource(t *testing.T) {
	e := newEnv(t, envOpts{})
	c := e.sqlite("shop", store.AIRead)
	ctx := context.Background()

	tpl, err := e.cs.ListResourceTemplates(ctx, nil)
	if err != nil || len(tpl.ResourceTemplates) != 1 || tpl.ResourceTemplates[0].URITemplate != "lontar://{connection}/schema" {
		t.Fatalf("templates = %+v, %v", tpl, err)
	}
	res, err := e.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "lontar://" + c.ID + "/schema"})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Contents[0].Text
	wantContains(t, got, `-- shop (sqlite), database "main", schema "main"`, "users [table](id INTEGER, name TEXT", "big_orders [view](")

	for _, uri := range []string{"lontar://" + c.ID + "/other", "lontar://nope/schema"} {
		if _, err := e.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri}); err == nil {
			t.Fatalf("%s: want error", uri)
		}
	}
}

func TestConnectionFromURI(t *testing.T) {
	for uri, want := range map[string]string{
		"lontar://abc/schema":       "abc",
		"lontar://My%20Shop/schema": "My Shop",
		"lontar://a/b/schema":       "",
		"lontar:///schema":          "",
		"other://abc/schema":        "",
		"lontar://abc":              "",
		"lontar://%zz/schema":       "",
	} {
		got, ok := connectionFromURI(uri)
		if got != want && ok || (want != "") != ok {
			t.Errorf("%s: got %q %v, want %q", uri, got, ok, want)
		}
	}
}

func TestToolAnnotations(t *testing.T) {
	e := newEnv(t, envOpts{})
	res, err := e.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tool := range res.Tools {
		seen[tool.Name] = true
		a := tool.Annotations
		if tool.Name == "execute_write" {
			if a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint {
				t.Errorf("execute_write annotations = %+v", a)
			}
			continue
		}
		if !a.ReadOnlyHint {
			t.Errorf("%s must be read-only", tool.Name)
		}
	}
	for _, name := range []string{"list_connections", "list_databases", "list_schemas", "list_tables", "describe_table",
		"list_objects", "get_ddl", "run_query", "explain_query", "execute_write"} {
		if !seen[name] {
			t.Errorf("missing tool %s", name)
		}
	}
	if e.cs.InitializeResult().Instructions == "" {
		t.Error("no instructions")
	}
}
