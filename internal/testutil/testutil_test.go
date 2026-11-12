package testutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(Main(m)) }

// selectOne runs SELECT 1 against db and fails the test unless it returns 1.
func selectOne(t *testing.T, db DB) {
	t.Helper()
	conn := Open(t, db)
	var n int
	if err := conn.QueryRow("SELECT 1").Scan(&n); err != nil {
		t.Fatalf("SELECT 1 on %s: %v", db.Driver, err)
	}
	if n != 1 {
		t.Fatalf("SELECT 1 on %s returned %d", db.Driver, n)
	}
}

func TestSQLiteTemp(t *testing.T) {
	db := SQLiteTemp(t)
	if db.Driver != DriverSQLite {
		t.Fatalf("driver = %q, want %q", db.Driver, DriverSQLite)
	}
	selectOne(t, db)

	// The file is writable and pragmas from the DSN are applied.
	conn := Open(t, db)
	if _, err := conn.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	var fk int
	if err := conn.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys pragma = %d, err %v; want 1", fk, err)
	}
}

func TestSharedPostgres(t *testing.T) {
	db := SharedPostgres(t)
	if db.Driver != DriverPostgres {
		t.Fatalf("driver = %q", db.Driver)
	}
	selectOne(t, db)
	if again := SharedPostgres(t); again != db {
		t.Fatalf("shared container was started twice: %v != %v", again, db)
	}
}

func TestSharedMySQL(t *testing.T) {
	db := SharedMySQL(t)
	if db.Driver != DriverMySQL {
		t.Fatalf("driver = %q", db.Driver)
	}
	selectOne(t, db)
	conn := Open(t, db)
	var version string
	if err := conn.QueryRow("SELECT VERSION()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(version, "8.") || strings.Contains(strings.ToLower(version), "mariadb") {
		t.Fatalf("unexpected MySQL version %q", version)
	}
}

func TestSharedMariaDB(t *testing.T) {
	db := SharedMariaDB(t)
	if db.Driver != DriverMySQL {
		t.Fatalf("driver = %q", db.Driver)
	}
	selectOne(t, db)
	conn := Open(t, db)
	var version string
	if err := conn.QueryRow("SELECT VERSION()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(version, "11.") || !strings.Contains(strings.ToLower(version), "mariadb") {
		t.Fatalf("unexpected MariaDB version %q", version)
	}
}

// TestStartPostgresIsolated covers the per-test lifecycle: the container is
// distinct from the shared one and is terminated by t.Cleanup.
func TestStartPostgresIsolated(t *testing.T) {
	var db DB
	t.Run("start", func(t *testing.T) {
		db = StartPostgres(t)
		selectOne(t, db)
	})
	if db == (DB{}) {
		t.Skip("container tests skipped")
	}
	if db == SharedPostgres(t) {
		t.Fatal("isolated container must not reuse the shared one")
	}
}

func TestMustStartAndTerminate(t *testing.T) {
	if reason := skipReason(); reason != "" {
		t.Skip(reason)
	}
	db, stop := MustStart(context.Background(), MariaDB)
	defer stop()
	selectOne(t, db)
}

func TestMustStartPanicsOnError(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for unknown kind")
		}
	}()
	MustStart(context.Background(), Kind(99))
}

func TestStartInvalidImage(t *testing.T) {
	if reason := skipReason(); reason != "" {
		t.Skip(reason)
	}
	for _, k := range []Kind{Postgres, MySQL, MariaDB} {
		t.Run(k.String(), func(t *testing.T) {
			old := Images[k]
			Images[k] = "invalid image reference!"
			t.Cleanup(func() { Images[k] = old })
			if _, _, err := Start(context.Background(), k); err == nil {
				t.Fatal("expected error for invalid image")
			}
		})
	}
}

func TestKindString(t *testing.T) {
	for k, want := range map[Kind]string{Postgres: "postgres", MySQL: "mysql", MariaDB: "mariadb", Kind(7): "Kind(7)"} {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(k), got, want)
		}
	}
}

// fakeTB records Skip/Fatal calls instead of stopping the goroutine.
type fakeTB struct {
	testing.TB
	skipped string
	fatal   string
	cleanup []func()
}

func (f *fakeTB) Helper()                        {}
func (f *fakeTB) Skip(args ...any)               { f.skipped = fmt.Sprint(args...) }
func (f *fakeTB) Fatalf(format string, a ...any) { f.fatal = fmt.Sprintf(format, a...) }
func (f *fakeTB) Cleanup(fn func())              { f.cleanup = append(f.cleanup, fn) }

// withHooks overrides the short-mode and Docker probes for one test.
func withHooks(t *testing.T, short bool, docker error) {
	t.Helper()
	oldShort, oldCheck := shortMode, dockerCheck
	shortMode = func() bool { return short }
	dockerCheck = func(context.Context) error { return docker }
	dockerOnce, dockerErr = sync.Once{}, nil
	t.Cleanup(func() {
		shortMode, dockerCheck = oldShort, oldCheck
		dockerOnce, dockerErr = sync.Once{}, nil
	})
}

func TestSkipInShortMode(t *testing.T) {
	withHooks(t, true, nil)
	for name, fn := range map[string]func(testing.TB) DB{
		"StartPostgres": StartPostgres, "StartMySQL": StartMySQL, "StartMariaDB": StartMariaDB,
		"SharedPostgres": SharedPostgres, "SharedMySQL": SharedMySQL, "SharedMariaDB": SharedMariaDB,
	} {
		f := &fakeTB{}
		if db := fn(f); db != (DB{}) {
			t.Errorf("%s returned %v in short mode", name, db)
		}
		if !strings.Contains(f.skipped, "-short") {
			t.Errorf("%s skip message = %q, want mention of -short", name, f.skipped)
		}
	}
}

func TestSkipWithoutDocker(t *testing.T) {
	withHooks(t, false, errors.New("cannot connect to daemon"))
	f := &fakeTB{}
	SharedPostgres(f)
	if !strings.Contains(f.skipped, "Docker is not available") || !strings.Contains(f.skipped, "cannot connect") {
		t.Fatalf("skip message = %q", f.skipped)
	}
}

func TestStartFailureFailsTest(t *testing.T) {
	withHooks(t, false, nil)
	f := &fakeTB{}
	if db := startForTest(f, Kind(42)); db != (DB{}) || !strings.Contains(f.fatal, "unknown container kind") {
		t.Fatalf("db=%v fatal=%q", db, f.fatal)
	}
	f = &fakeTB{}
	if db := sharedForTest(f, Kind(42)); db != (DB{}) || !strings.Contains(f.fatal, "unknown container kind") {
		t.Fatalf("db=%v fatal=%q", db, f.fatal)
	}
	sharedMu.Lock()
	delete(shared, Kind(42))
	sharedMu.Unlock()
}

func TestOpenErrors(t *testing.T) {
	f := &fakeTB{}
	if conn := Open(f, DB{Driver: "no-such-driver", DSN: "x"}); conn != nil || !strings.Contains(f.fatal, "open no-such-driver") {
		t.Fatalf("conn=%v fatal=%q", conn, f.fatal)
	}
	f = &fakeTB{}
	bad := DB{Driver: DriverSQLite, DSN: t.TempDir() + "/missing-dir/x.db?mode=ro"}
	if conn := Open(f, bad); conn != nil || !strings.Contains(f.fatal, "ping sqlite") {
		t.Fatalf("conn=%v fatal=%q", conn, f.fatal)
	}
	for _, fn := range f.cleanup {
		fn()
	}
}

func TestProviderHealth(t *testing.T) {
	// Exercise the real probe; its result depends on the environment, so only
	// check that it returns without panicking.
	_ = providerHealth(context.Background())
}

type fakeRunner struct{ code int }

func (r fakeRunner) Run() int { return r.code }

func TestMainTerminatesShared(t *testing.T) {
	stopped := false
	sharedMu.Lock()
	shared[Kind(50)] = &sharedEntry{terminate: func(context.Context) error { stopped = true; return nil }}
	sharedMu.Unlock()
	if code := Main(fakeRunner{code: 3}); code != 3 {
		t.Fatalf("Main returned %d, want 3", code)
	}
	if !stopped {
		t.Fatal("shared container was not terminated")
	}
	sharedMu.Lock()
	_, still := shared[Kind(50)]
	sharedMu.Unlock()
	if still {
		t.Fatal("shared entry not removed")
	}
}

func TestSeedBrowseSQLite(t *testing.T) {
	db := SQLiteTemp(t)
	conn := Open(t, db)
	SeedBrowse(t, EngineSQLite, db, conn)
	SeedBrowse(t, EngineSQLite, db, conn) // second call is a no-op
	var n int
	if err := conn.QueryRow("SELECT COUNT(*) FROM numbers").Scan(&n); err != nil || n != 1000 {
		t.Fatalf("numbers = %d, %v", n, err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM "Odd ""Name"" Tbl"`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("odd = %d, %v", n, err)
	}
	for _, e := range []string{EnginePostgres, EngineMySQL, EngineMariaDB} {
		if s, err := SeedStatements(e); err != nil || len(s) < 10 {
			t.Fatalf("SeedStatements(%s) = %d, %v", e, len(s), err)
		}
	}
	if _, err := SeedStatements("oracle"); err == nil {
		t.Fatal("expected error")
	}
}

func TestSeedBrowseFailure(t *testing.T) {
	db := SQLiteTemp(t)
	conn := Open(t, db)
	_ = conn.Close()
	ft := &fakeTB{TB: t}
	SeedBrowse(ft, EngineSQLite, db, conn)
	if ft.fatal == "" {
		t.Fatal("seeding a closed DB should fail")
	}
	ft = &fakeTB{TB: t}
	SeedBrowse(ft, "oracle", db, conn)
	if ft.fatal == "" {
		t.Fatal("unknown engine should fail")
	}
}

func TestAsRoot(t *testing.T) {
	got, err := AsRoot(DB{Driver: DriverMySQL, DSN: "lontar:pw@tcp(localhost:3306)/lontar?parseTime=true"})
	if err != nil || !strings.HasPrefix(got.DSN, "root:pw@tcp(localhost:3306)/?") {
		t.Fatalf("AsRoot = %+v, %v", got, err)
	}
	if _, err := AsRoot(DB{DSN: "::bad"}); err == nil {
		t.Fatal("expected error")
	}
}
