package sqlfile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"lontar/internal/driver"
	"lontar/internal/driver/mysql"
	"lontar/internal/driver/postgres"
	"lontar/internal/driver/sqlite"
	"lontar/internal/store"
	"lontar/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

// engine is a database to run fixtures against.
type engine struct {
	name  string
	drv   driver.Driver
	start func(testing.TB) testutil.DB
	// dump is the testdata fixture written by the engine's dump tool.
	dump string
	// reset drops what the dump creates.
	reset []string
}

var engines = []engine{
	{"sqlite", sqlite.New(), testutil.SQLiteTemp, "sqlite.dump.sql",
		[]string{"DROP VIEW IF EXISTS item_names", "DROP TABLE IF EXISTS child", "DROP TABLE IF EXISTS items", "DROP TABLE IF EXISTS audit"}},
	{"postgres", postgres.New(), testutil.SharedPostgres, "pg_dump.sql", []string{"DROP SCHEMA IF EXISTS dumpfx CASCADE"}},
	{"mysql", mysql.New(), testutil.SharedMySQL, "mysqldump.sql", myReset},
	{"mariadb", mysql.NewMariaDB(), testutil.SharedMariaDB, "mariadb-dump.sql", myReset},
}

var myReset = []string{"DROP VIEW IF EXISTS item_names", "DROP TABLE IF EXISTS child", "DROP TABLE IF EXISTS items",
	"DROP TABLE IF EXISTS audit", "DROP PROCEDURE IF EXISTS count_items"}

type env struct {
	e      engine
	target testutil.DB
	db     *sql.DB
}

func (v *env) kind() driver.Kind { return v.e.drv.Kind() }

func (v *env) exec(t *testing.T, q string) {
	t.Helper()
	if _, err := v.db.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("%s: %v\n%s", v.e.name, err, q)
	}
}

func (v *env) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := v.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func forEach(t *testing.T, fn func(t *testing.T, v *env)) {
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			target := e.start(t)
			db := testutil.OpenDriver(t, e.drv, target, false)
			v := &env{e: e, target: target, db: db}
			if e.drv.Kind() == driver.MySQL || e.drv.Kind() == driver.MariaDB {
				// Triggers and routines need this with binary logging.
				root, err := testutil.AsRoot(target)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := testutil.Open(t, root).Exec("SET GLOBAL log_bin_trust_function_creators = 1"); err != nil {
					t.Fatal(err)
				}
			}
			fn(t, v)
		})
	}
}

func writeScript(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "script.sql")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// progressLog records progress callbacks.
type progressLog struct {
	mu   sync.Mutex
	list []Progress
}

func (p *progressLog) fn(pr Progress) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.list = append(p.list, pr)
}

func (p *progressLog) last() Progress {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.list) == 0 {
		return Progress{}
	}
	return p.list[len(p.list)-1]
}

func TestRunDumpFixtures(t *testing.T) {
	forEach(t, func(t *testing.T, v *env) {
		ctx := context.Background()
		path := filepath.Join("testdata", v.e.dump)
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, single := range []bool{false, true} {
			t.Run(fmt.Sprintf("single=%v", single), func(t *testing.T) {
				for _, q := range v.e.reset {
					v.exec(t, q)
				}
				pl := &progressLog{}
				res, err := Run(ctx, v.db, v.kind(), path, Options{SingleTransaction: single}, pl.fn)
				if err != nil {
					t.Fatal(err)
				}
				if res.Failed != 0 || res.Stopped || res.Cancelled || res.RolledBack {
					t.Fatalf("result %+v", res)
				}
				p := pl.last()
				if p.BytesRead != fi.Size() || p.TotalBytes != fi.Size() || p.Statements != res.Run || res.Run == 0 {
					t.Fatalf("progress %+v, result %+v, size %d", p, res, fi.Size())
				}
				checkFixture(t, v, res, single)
			})
		}
		// The dump is classified for safe mode: it has writes, and a
		// read-only check stops at the first one.
		sc, err := Scan(ctx, v.kind(), path, 3, false, nil)
		if err != nil || sc.Writes == 0 || len(sc.First) != 3 || sc.Statements < sc.Writes {
			t.Fatalf("scan %+v %v", sc, err)
		}
		info, err := Inspect(ctx, v.kind(), path)
		if err != nil || !info.Exact || info.Statements != sc.Statements || info.Warning != "" || info.Size != fi.Size() {
			t.Fatalf("inspect %+v %v (scan %d)", info, err, sc.Statements)
		}
		wantDump := map[string]string{"sqlite": DumpSQLite, "postgres": DumpPostgres, "mysql": DumpMySQL, "mariadb": DumpMariaDB}[v.e.name]
		if info.DumpType != wantDump || info.HasCopy != (v.kind() == driver.Postgres) {
			t.Fatalf("inspect %+v", info)
		}
	})
}

// checkFixture verifies what the dump fixtures hold (see testdata).
func checkFixture(t *testing.T, v *env, res *Result, single bool) {
	t.Helper()
	ctx := context.Background()
	prefix := ""
	if v.kind() == driver.Postgres {
		prefix = "dumpfx."
	}
	if n := v.count(t, prefix+"items"); n != 5 {
		t.Fatalf("items = %d", n)
	}
	if n := v.count(t, prefix+"child"); n != 2 {
		t.Fatalf("child = %d", n)
	}
	if n := v.count(t, prefix+"item_names"); n != 5 {
		t.Fatalf("view = %d", n)
	}
	var name, note string
	if err := v.db.QueryRowContext(ctx, "SELECT name FROM "+prefix+"items WHERE id = 4").Scan(&name); err != nil || name != "ünïcødé 日本" {
		t.Fatalf("unicode name %q %v", name, err)
	}
	if err := v.db.QueryRowContext(ctx, "SELECT name FROM "+prefix+"items WHERE id = 2").Scan(&name); err != nil || name != "tab\there" {
		t.Fatalf("tab name %q %v", name, err)
	}
	if err := v.db.QueryRowContext(ctx, "SELECT note FROM "+prefix+"items WHERE id = 4").Scan(&note); err != nil || !strings.HasPrefix(note, "\r\nCRLF") {
		t.Fatalf("crlf note %q %v", note, err)
	}
	switch v.kind() {
	case driver.Postgres:
		if res.RowsCopied != 7 {
			t.Fatalf("rows copied %d", res.RowsCopied)
		}
		// \restrict and \unrestrict are psql-only and skipped.
		if res.Skipped != 2 {
			t.Fatalf("skipped %d", res.Skipped)
		}
		if err := v.db.QueryRowContext(ctx, "SELECT note FROM dumpfx.items WHERE id = 3").Scan(&note); err != nil || note != `\.` {
			t.Fatalf("escaped terminator %q %v", note, err)
		}
		var n int
		if err := v.db.QueryRowContext(ctx, "SELECT dumpfx.bump(1)").Scan(&n); err != nil || n != 2 {
			t.Fatalf("function %d %v", n, err)
		}
		if err := v.db.QueryRowContext(ctx, "SELECT nextval('dumpfx.items_id_seq')").Scan(&n); err != nil || n != 6 {
			t.Fatalf("sequence %d %v", n, err)
		}
	case driver.MySQL, driver.MariaDB:
		// The trigger was created after the rows (see the fixture).
		if n := v.count(t, "audit"); n != 0 {
			t.Fatalf("audit = %d", n)
		}
		var proc int
		if err := v.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.routines WHERE routine_schema = DATABASE() AND routine_name = 'count_items'").Scan(&proc); err != nil || proc != 1 {
			t.Fatalf("procedure %d %v", proc, err)
		}
		var data []byte
		if err := v.db.QueryRowContext(ctx, "SELECT data FROM items WHERE id = 1").Scan(&data); err != nil || fmt.Sprintf("%x", data) != "deadbeef" {
			t.Fatalf("binary %x %v", data, err)
		}
		var trig int
		if err := v.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema = DATABASE() AND trigger_name = 'items_ai'").Scan(&trig); err != nil || trig != 1 {
			t.Fatalf("trigger %d %v", trig, err)
		}
	case driver.SQLite:
		if n := v.count(t, "audit"); n != 5 {
			t.Fatalf("audit = %d", n)
		}
		// The dump's BEGIN TRANSACTION and COMMIT are skipped in single
		// transaction mode.
		if want := map[bool]int64{false: 0, true: 2}[single]; res.Skipped != want {
			t.Fatalf("skipped %d", res.Skipped)
		}
		var trig int
		if err := v.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger'").Scan(&trig); err != nil || trig != 1 {
			t.Fatalf("trigger %d %v", trig, err)
		}
	}
}

// dmlScript inserts n rows into lontar_rb, one statement each, with a
// failing statement after bad (0: none).
func dmlScript(n, bad int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "INSERT INTO lontar_rb (id, name) VALUES (%d, 'row; %d');\n", i, i)
		if i == bad {
			b.WriteString("-- next one fails\nINSERT INTO lontar_missing VALUES (1);\n")
		}
	}
	return b.String()
}

func (v *env) freshTable(t *testing.T) {
	t.Helper()
	v.exec(t, "DROP TABLE IF EXISTS lontar_rb")
	v.exec(t, "CREATE TABLE lontar_rb (id INT PRIMARY KEY, name VARCHAR(50))")
}

func TestRunErrors(t *testing.T) {
	forEach(t, func(t *testing.T, v *env) {
		ctx := context.Background()
		path := writeScript(t, dmlScript(10, 4))

		// Single transaction: the error rolls everything back.
		v.freshTable(t)
		res, err := Run(ctx, v.db, v.kind(), path, Options{SingleTransaction: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !res.RolledBack || !res.Stopped || res.Run != 4 || res.Failed != 1 || len(res.Errors) != 1 {
			t.Fatalf("result %+v", res)
		}
		e := res.Errors[0]
		if e.Line != 6 || e.Preview != "INSERT INTO lontar_missing VALUES (1)" || e.SQL != e.Preview || e.Message == "" {
			t.Fatalf("error %+v", e)
		}
		if n := v.count(t, "lontar_rb"); n != 0 {
			t.Fatalf("rows after rollback %d", n)
		}

		// Default: stop at the error, keep what ran.
		v.freshTable(t)
		res, err = Run(ctx, v.db, v.kind(), path, Options{}, nil)
		if err != nil || res.RolledBack || !res.Stopped || res.Run != 4 || res.Failed != 1 {
			t.Fatalf("result %+v %v", res, err)
		}
		if n := v.count(t, "lontar_rb"); n != 4 {
			t.Fatalf("rows %d", n)
		}

		// Continue on error: every statement runs.
		v.freshTable(t)
		path2 := writeScript(t, dmlScript(10, 3)+dmlScript(0, 0)+"INSERT INTO lontar_missing VALUES (2);\nINSERT INTO lontar_rb VALUES (1, 'dup');\nINSERT INTO lontar_rb VALUES (11, 'ok');")
		res, err = Run(ctx, v.db, v.kind(), path2, Options{ContinueOnError: true}, nil)
		if err != nil || res.Stopped || res.Run != 11 || res.Failed != 3 || len(res.Errors) != 3 {
			t.Fatalf("result %+v %v", res, err)
		}
		if res.Errors[0].Line != 5 || res.Errors[1].Line != 13 || res.Errors[2].Line != 14 {
			t.Fatalf("errors %+v", res.Errors)
		}
		if n := v.count(t, "lontar_rb"); n != 11 {
			t.Fatalf("rows %d", n)
		}

		// Options cannot be combined.
		if _, err := Run(ctx, v.db, v.kind(), path, Options{SingleTransaction: true, ContinueOnError: true}, nil); !errors.Is(err, ErrOptions) {
			t.Fatal(err)
		}
	})
}

func TestRunCancel(t *testing.T) {
	forEach(t, func(t *testing.T, v *env) {
		path := writeScript(t, dmlScript(200, 0))
		for _, single := range []bool{true, false} {
			v.freshTable(t)
			ctx, cancel := context.WithCancel(context.Background())
			res, err := Run(ctx, v.db, v.kind(), path, Options{SingleTransaction: single}, func(p Progress) {
				if p.Statements >= 50 {
					cancel()
				}
			})
			cancel()
			if err != nil || !res.Cancelled || res.RolledBack != single || res.Run < 50 || res.Run >= 200 {
				t.Fatalf("single=%v result %+v %v", single, res, err)
			}
			n := v.count(t, "lontar_rb")
			if single && n != 0 || !single && int64(n) != res.Run {
				t.Fatalf("single=%v rows after cancel %d (run %d)", single, n, res.Run)
			}
		}
		// Cancelled before it starts.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Run(ctx, v.db, v.kind(), path, Options{}, nil); err == nil {
			t.Fatal("no error for a cancelled context")
		}
	})
}

func TestSafeMode(t *testing.T) {
	forEach(t, func(t *testing.T, v *env) {
		ctx := context.Background()
		reads := writeScript(t, "SELECT 1;\n-- comment\nSELECT 2;\n")
		path := filepath.Join("testdata", v.e.dump)

		// Read-only: refused at the first write, nothing runs.
		_, err := CheckSafeMode(ctx, v.kind(), path, store.SafeReadOnly, false, 20, nil)
		var ro *ReadOnlyError
		if !errors.As(err, &ro) || ro.Line == 0 || ro.Preview == "" || !strings.Contains(err.Error(), "read-only") {
			t.Fatalf("readonly: %v", err)
		}
		// confirmed does not lift read-only.
		if _, err := CheckSafeMode(ctx, v.kind(), path, store.SafeReadOnly, true, 20, nil); !errors.As(err, &ro) {
			t.Fatalf("readonly confirmed: %v", err)
		}
		if sc, err := CheckSafeMode(ctx, v.kind(), reads, store.SafeReadOnly, false, 20, nil); err != nil || sc != nil {
			t.Fatalf("readonly reads: %v %v", sc, err)
		}

		// Confirm: the writes are listed until confirmed.
		pl := &progressLog{}
		sc, err := CheckSafeMode(ctx, v.kind(), path, store.SafeConfirm, false, 20, pl.fn)
		if err != nil || sc == nil || sc.Writes < 5 || len(sc.First) != min(20, int(sc.Writes)) || len(pl.list) == 0 {
			t.Fatalf("confirm: %+v %v", sc, err)
		}
		if sc, err := CheckSafeMode(ctx, v.kind(), path, store.SafeConfirm, true, 20, nil); err != nil || sc != nil {
			t.Fatalf("confirmed: %v %v", sc, err)
		}
		if sc, err := CheckSafeMode(ctx, v.kind(), reads, store.SafeConfirm, false, 20, nil); err != nil || sc != nil {
			t.Fatalf("confirm reads: %v %v", sc, err)
		}
		if sc, err := CheckSafeMode(ctx, v.kind(), path, store.SafeOff, false, 20, nil); err != nil || sc != nil {
			t.Fatalf("off: %v %v", sc, err)
		}

		// A read-only connection runs a read-only file.
		rodb := testutil.OpenDriver(t, v.e.drv, v.target, true)
		res, err := Run(ctx, rodb, v.kind(), reads, Options{SingleTransaction: v.kind() != driver.SQLite}, nil)
		if err != nil || res.Run != 2 || res.Failed != 0 {
			t.Fatalf("readonly run %+v %v", res, err)
		}
		// Errors reading the file.
		if _, err := CheckSafeMode(ctx, v.kind(), filepath.Join(t.TempDir(), "missing.sql"), store.SafeReadOnly, false, 1, nil); err == nil {
			t.Fatal("missing file")
		}
		if _, err := CheckSafeMode(ctx, v.kind(), filepath.Join(t.TempDir(), "missing.sql"), store.SafeConfirm, false, 1, nil); err == nil {
			t.Fatal("missing file")
		}
	})
}

func TestPostgresSpecifics(t *testing.T) {
	for _, e := range engines {
		if e.name != "postgres" {
			continue
		}
		target := e.start(t)
		db := testutil.OpenDriver(t, e.drv, target, false)
		v := &env{e: e, target: target, db: db}
		ctx := context.Background()
		v.exec(t, "DROP TABLE IF EXISTS lontar_cp")
		v.exec(t, "CREATE TABLE lontar_cp (id int PRIMARY KEY, name text)")

		// Bad data in a COPY block names its statement; continue on
		// error goes on after the block.
		script := "COPY lontar_cp (id, name) FROM stdin;\n1\ta\nnot-a-number\tb\n\\.\n" +
			"COPY lontar_cp (id, name) FROM stdin;\r\n2\tx\r\n3\t\\N\r\n\\.\r\n" +
			"\\connect other\nSELECT 1;\n"
		res, err := Run(ctx, db, driver.Postgres, writeScript(t, script), Options{ContinueOnError: true}, nil)
		if err != nil || res.Failed != 2 || res.Run != 2 || res.RowsCopied != 2 {
			t.Fatalf("result %+v %v", res, err)
		}
		if res.Errors[0].Line != 1 || !strings.Contains(res.Errors[0].Message, "integer") ||
			res.Errors[1].Line != 9 || !strings.Contains(res.Errors[1].Message, `\connect`) {
			t.Fatalf("errors %+v", res.Errors)
		}
		if n := v.count(t, "lontar_cp"); n != 2 {
			t.Fatalf("rows %d", n)
		}
		// An unterminated COPY block fails; in one transaction nothing
		// stays.
		res, err = Run(ctx, db, driver.Postgres, writeScript(t, "INSERT INTO lontar_cp VALUES (10, 'x');\nCOPY lontar_cp FROM stdin;\n11\ty\n"), Options{SingleTransaction: true}, nil)
		if err != nil || !res.RolledBack || res.Failed == 0 || !strings.Contains(res.Errors[0].Message, `\.`) {
			t.Fatalf("result %+v %v", res, err)
		}
		if n := v.count(t, "lontar_cp"); n != 2 {
			t.Fatalf("rows %d", n)
		}
		// A large COPY streams through (progress reports copied rows).
		var b strings.Builder
		b.WriteString("TRUNCATE lontar_cp;\nCOPY lontar_cp (id, name) FROM stdin;\n")
		for i := 1; i <= 60000; i++ {
			fmt.Fprintf(&b, "%d\tname number %d with some padding text\n", i, i)
		}
		b.WriteString("\\.\n")
		pl := &progressLog{}
		res, err = Run(ctx, db, driver.Postgres, writeScript(t, b.String()), Options{}, pl.fn)
		if err != nil || res.RowsCopied != 60000 || res.Run != 2 {
			t.Fatalf("result %+v %v", res, err)
		}
		mid := false
		for _, p := range pl.list {
			if p.BytesRead > 0 && p.BytesRead < p.TotalBytes {
				mid = true
			}
		}
		if !mid {
			t.Fatalf("no progress during COPY: %+v", pl.list)
		}
		// The connection's session settings do not leak into the pool.
		res, err = Run(ctx, db, driver.Postgres, writeScript(t, "SET search_path = nowhere;\n"), Options{}, nil)
		if err != nil || res.Run != 1 {
			t.Fatal(res, err)
		}
		var sp string
		for i := 0; i < 5; i++ {
			if err := db.QueryRowContext(ctx, "SHOW search_path").Scan(&sp); err != nil || sp == "nowhere" {
				t.Fatalf("search_path leaked: %q %v", sp, err)
			}
		}
	}
}

func TestCopyOnOtherEngines(t *testing.T) {
	db := testutil.OpenDriver(t, sqlite.New(), testutil.SQLiteTemp(t), false)
	res, err := Run(context.Background(), db, driver.SQLite, writeScript(t, "COPY t FROM stdin;\n"), Options{}, nil)
	if err != nil || res.Failed != 1 || res.RowsCopied != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}
