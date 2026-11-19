package mcpserver

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"lontar/internal/driver"
	"lontar/internal/store"
	"lontar/internal/testutil"

	gomysql "github.com/go-sql-driver/mysql"
)

// fromDSN converts a testutil DSN into a saved connection and its password.
func fromDSN(t *testing.T, kind driver.Kind, dsn string) (store.Connection, string) {
	t.Helper()
	c := store.Connection{Name: "it-" + string(kind), Kind: kind}
	var pw, hostport string
	if kind == driver.Postgres {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		pw, _ = u.User.Password()
		c.User = u.User.Username()
		c.Database = strings.TrimPrefix(u.Path, "/")
		hostport = u.Host
		c.Options = map[string]string{"sslmode": "disable"}
	} else {
		mc, err := gomysql.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		pw, c.User, c.Database, hostport = mc.Passwd, mc.User, mc.DBName, mc.Addr
	}
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		t.Fatal(err)
	}
	c.Host = host
	c.Port, _ = strconv.Atoi(port)
	return c, pw
}

func TestServersIntegration(t *testing.T) {
	cases := []struct {
		kind    driver.Kind
		start   func(testing.TB) testutil.DB
		otherDB string // another database to switch to
		curDB   string // query returning the current database
		escape  string // a write that resets the session's read-only flag
	}{
		{driver.Postgres, testutil.SharedPostgres, "postgres", "SELECT current_database() AS db", "SET default_transaction_read_only = off"},
		{driver.MySQL, testutil.SharedMySQL, "information_schema", "SELECT DATABASE() AS db", "SET SESSION transaction_read_only = 0"},
		{driver.MariaDB, testutil.SharedMariaDB, "information_schema", "SELECT DATABASE() AS db", "SET SESSION tx_read_only = 0"},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			tdb := tc.start(t)
			raw := testutil.Open(t, tdb)
			ctx := context.Background()
			for _, q := range []string{
				"DROP TABLE IF EXISTS mcp_items",
				"CREATE TABLE mcp_items (id INT PRIMARY KEY, name VARCHAR(50), price DECIMAL(10,2))",
				"INSERT INTO mcp_items VALUES (1, 'apple', 1.50), (2, 'pear', NULL)",
			} {
				if _, err := raw.ExecContext(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			count := func() int {
				var n int
				if err := raw.QueryRowContext(ctx, "SELECT count(*) FROM mcp_items").Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			}

			e := newEnv(t, envOpts{})
			c, pw := fromDSN(t, tc.kind, tdb.DSN)
			c.AIAccess = store.AIWrite
			c = e.save(c, pw)
			id := c.ID

			wantContains(t, e.ok("list_databases", map[string]any{"connection": id}), c.Database+" (current)")
			wantContains(t, e.ok("list_tables", map[string]any{"connection": id}), "- mcp_items (table")
			wantContains(t, e.ok("describe_table", map[string]any{"connection": id, "table": "mcp_items"}), "PRIMARY KEY(1)", "- name ", "(50)")
			wantContains(t, e.ok("get_ddl", map[string]any{"connection": id, "name": "mcp_items"}), "mcp_items")
			e.ok("list_objects", map[string]any{"connection": id})
			e.ok("list_schemas", map[string]any{"connection": id})

			out := e.ok("run_query", map[string]any{"connection": id, "sql": "SELECT id, name, price FROM mcp_items ORDER BY id"})
			wantContains(t, out, "2 rows\nid,name,price\n1,apple,1.50\n2,pear,NULL\n")
			wantContains(t, e.ok("run_query", map[string]any{"connection": id, "sql": tc.curDB, "database": tc.otherDB}), "db\n"+tc.otherDB+"\n")
			wantContains(t, e.ok("explain_query", map[string]any{"connection": id, "sql": "SELECT * FROM mcp_items WHERE id = 1"}), "plan (EXPLAIN")

			// The database refuses writes on the read session, even after
			// a statement that tries to lift the read-only flag.
			sess, err := e.srv.session(ctx, c, false, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := runRead(ctx, sess, []string{"INSERT INTO mcp_items VALUES (9, 'x', 0)"}, 10); err == nil {
				t.Fatal("insert on the read session succeeded")
			}
			if _, failed, err := runRead(ctx, sess, []string{tc.escape, "INSERT INTO mcp_items VALUES (9, 'x', 0)"}, 10); err == nil {
				t.Fatal("insert after lifting read-only succeeded")
			} else {
				t.Logf("statement %d: %v", failed+1, err)
			}
			if n := count(); n != 2 {
				t.Fatalf("rows = %d", n)
			}

			// Approved writes run in one transaction.
			wantContains(t, e.ok("execute_write", map[string]any{"connection": id,
				"sql": "INSERT INTO mcp_items VALUES (3, 'plum', 2); UPDATE mcp_items SET price = 0 WHERE price IS NULL"}),
				"Statement 1: 1 row affected", "Statement 2: 1 row affected")
			e.fail("execute_write", map[string]any{"connection": id, "sql": "INSERT INTO mcp_items VALUES (4, 'kiwi', 1); INSERT INTO mcp_items VALUES (1, 'dup', 1)"},
				"rolled back")
			if n := count(); n != 3 {
				t.Fatalf("rows = %d", n)
			}
			e.fail("execute_write", map[string]any{"connection": id, "sql": "DROP TABLE mcp_items"}, "only runs INSERT")
			e.fail("execute_write", map[string]any{"connection": id, "sql": "DELETE FROM mcp_items", "database": "no_such_db"}, "not found")

			h := e.history(id)
			if len(h) < 5 || h[0].Source != "mcp" {
				t.Fatalf("history = %+v", h)
			}
		})
	}
}

// PostgreSQL side effects that a read-only transaction stops even when
// they look like reads.
func TestPostgresReadOnlyFunctions(t *testing.T) {
	tdb := testutil.SharedPostgres(t)
	raw := testutil.Open(t, tdb)
	ctx := context.Background()
	if _, err := raw.ExecContext(ctx, "CREATE SEQUENCE IF NOT EXISTS mcp_seq"); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, envOpts{})
	c, pw := fromDSN(t, driver.Postgres, tdb.DSN)
	c.AIAccess = store.AIRead
	c = e.save(c, pw)
	e.fail("run_query", map[string]any{"connection": c.ID, "sql": "SELECT nextval('mcp_seq')"}, "calls nextval")
	sess, err := e.srv.session(ctx, c, false, "")
	if err != nil {
		t.Fatal(err)
	}
	// Past the word check, the database still refuses.
	if _, _, err := runRead(ctx, sess, []string{"SELECT nextval('mcp_seq')"}, 1); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("nextval err = %v", err)
	}
	if _, _, err := runRead(ctx, sess, []string{"SELECT set_config('transaction_read_only', 'off', false)"}, 1); err == nil {
		t.Fatal("set_config lifted read-only")
	}
	e.fail("execute_write", map[string]any{"connection": c.ID, "sql": "DELETE FROM x"}, "does not allow AI writes")
}
