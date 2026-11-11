// Package testutil provides helpers for integration tests that need a real
// database: PostgreSQL 16, MySQL 8 and MariaDB 11 via testcontainers-go, and
// SQLite via a temporary file.
//
// Containers can be started per test (StartPostgres etc., terminated by
// t.Cleanup) or shared by every test in a package (SharedPostgres etc.). Shared
// containers are started lazily on first use and terminated when the package's
// TestMain calls Main:
//
//	func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }
//
//	func TestSomething(t *testing.T) {
//		db := testutil.SharedPostgres(t) // skipped with -short or without Docker
//		conn := testutil.Open(t, db)
//		...
//	}
//
// For TestMain-driven setups that need a DSN up front, MustStart starts a
// container directly and returns its terminate function.
//
// Every container helper skips the test when running with -short or when no
// Docker (or Docker-compatible, e.g. Podman) daemon is reachable.
package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	// database/sql drivers used by dbedit.
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mariadb"
	"github.com/testcontainers/testcontainers-go/modules/mysql"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Kind identifies a database engine that can be started in a container.
type Kind int

// Supported container kinds.
const (
	Postgres Kind = iota
	MySQL
	MariaDB
)

// String returns a human readable name for the kind.
func (k Kind) String() string {
	switch k {
	case Postgres:
		return "postgres"
	case MySQL:
		return "mysql"
	case MariaDB:
		return "mariadb"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// Database/sql driver names registered by the imported drivers.
const (
	DriverPostgres = "pgx"
	DriverMySQL    = "mysql"
	DriverSQLite   = "sqlite"
)

// Credentials used for every container.
const (
	dbName     = "dbedit"
	dbUser     = "dbedit"
	dbPassword = "dbedit"
)

// Images are fully qualified so that Podman does not need short-name
// resolution. They can be overridden (e.g. to test a different minor version).
var Images = map[Kind]string{
	Postgres: "docker.io/library/postgres:16-alpine",
	MySQL:    "docker.io/library/mysql:8.0",
	MariaDB:  "docker.io/library/mariadb:11",
}

// StartTimeout bounds how long starting a single container may take
// (including the image pull on a cold cache).
var StartTimeout = 5 * time.Minute

// DB describes how to connect to a test database.
type DB struct {
	Driver string // database/sql driver name
	DSN    string // data source name for Driver
}

// TerminateFunc stops and removes a container.
type TerminateFunc func(context.Context) error

// Hooks overridable by tests of this package.
var (
	shortMode   = testing.Short
	dockerCheck = providerHealth
)

// providerHealth reports whether a Docker-compatible daemon is reachable.
func providerHealth(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("docker provider panicked: %v", r)
		}
	}()
	p, err := testcontainers.NewDockerProvider()
	if err != nil {
		return err
	}
	defer p.Close()
	return p.Health(ctx)
}

var (
	dockerOnce sync.Once
	dockerErr  error
)

// skipReason returns a non-empty message when container tests cannot run.
func skipReason() string {
	if shortMode() {
		return "skipping container-based integration test in -short mode"
	}
	dockerOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		dockerErr = dockerCheck(ctx)
	})
	if dockerErr != nil {
		return fmt.Sprintf("skipping container-based integration test: Docker is not available (%v)", dockerErr)
	}
	return ""
}

// Start launches a database container of the given kind and returns its
// connection info together with a function that terminates it.
func Start(ctx context.Context, kind Kind) (DB, TerminateFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, StartTimeout)
	defer cancel()

	img := Images[kind]
	var (
		ctr    testcontainers.Container
		db     DB
		dsnErr error
		err    error
	)
	switch kind {
	case Postgres:
		var c *postgres.PostgresContainer
		c, err = postgres.Run(ctx, img,
			postgres.WithDatabase(dbName),
			postgres.WithUsername(dbUser),
			postgres.WithPassword(dbPassword),
			postgres.BasicWaitStrategies(),
		)
		if c != nil {
			ctr = c
		}
		if err == nil {
			db.Driver = DriverPostgres
			db.DSN, dsnErr = c.ConnectionString(ctx, "sslmode=disable")
		}
	case MySQL:
		var c *mysql.MySQLContainer
		c, err = mysql.Run(ctx, img,
			mysql.WithDatabase(dbName),
			mysql.WithUsername(dbUser),
			mysql.WithPassword(dbPassword),
		)
		if c != nil {
			ctr = c
		}
		if err == nil {
			db.Driver = DriverMySQL
			db.DSN, dsnErr = c.ConnectionString(ctx, "parseTime=true", "multiStatements=true")
		}
	case MariaDB:
		var c *mariadb.MariaDBContainer
		c, err = mariadb.Run(ctx, img,
			mariadb.WithDatabase(dbName),
			mariadb.WithUsername(dbUser),
			mariadb.WithPassword(dbPassword),
		)
		if c != nil {
			ctr = c
		}
		if err == nil {
			db.Driver = DriverMySQL
			db.DSN, dsnErr = c.ConnectionString(ctx, "parseTime=true", "multiStatements=true")
		}
	default:
		return DB{}, nil, fmt.Errorf("testutil: unknown container kind %v", kind)
	}

	terminate := func(context.Context) error { return testcontainers.TerminateContainer(ctr) }
	if err == nil {
		err = dsnErr
	}
	if err != nil {
		_ = terminate(context.Background())
		return DB{}, nil, fmt.Errorf("testutil: start %s (%s): %w", kind, img, err)
	}
	return db, terminate, nil
}

// MustStart is Start for use from TestMain: it panics if the container cannot
// be started. The caller must invoke the returned function when done.
func MustStart(ctx context.Context, kind Kind) (DB, func()) {
	db, terminate, err := Start(ctx, kind)
	if err != nil {
		panic(err)
	}
	return db, func() { _ = terminate(context.Background()) }
}

// startForTest starts a container owned by a single test.
func startForTest(t testing.TB, kind Kind) DB {
	t.Helper()
	if reason := skipReason(); reason != "" {
		t.Skip(reason)
		return DB{}
	}
	db, terminate, err := Start(context.Background(), kind)
	if err != nil {
		t.Fatalf("%v", err)
		return DB{}
	}
	t.Cleanup(func() { _ = terminate(context.Background()) })
	return db
}

// StartPostgres starts a PostgreSQL 16 container that lives for the duration
// of the test.
func StartPostgres(t testing.TB) DB { t.Helper(); return startForTest(t, Postgres) }

// StartMySQL starts a MySQL 8 container that lives for the duration of the test.
func StartMySQL(t testing.TB) DB { t.Helper(); return startForTest(t, MySQL) }

// StartMariaDB starts a MariaDB 11 container that lives for the duration of
// the test.
func StartMariaDB(t testing.TB) DB { t.Helper(); return startForTest(t, MariaDB) }

type sharedEntry struct {
	once      sync.Once
	db        DB
	terminate TerminateFunc
	err       error
}

var (
	sharedMu sync.Mutex
	shared   = map[Kind]*sharedEntry{}
)

// sharedForTest returns a container that is started once per test binary
// (i.e. once per package) and reused by all its tests.
func sharedForTest(t testing.TB, kind Kind) DB {
	t.Helper()
	if reason := skipReason(); reason != "" {
		t.Skip(reason)
		return DB{}
	}
	sharedMu.Lock()
	e, ok := shared[kind]
	if !ok {
		e = &sharedEntry{}
		shared[kind] = e
	}
	sharedMu.Unlock()

	e.once.Do(func() {
		e.db, e.terminate, e.err = Start(context.Background(), kind)
	})
	if e.err != nil {
		t.Fatalf("%v", e.err)
		return DB{}
	}
	return e.db
}

// SharedPostgres returns a PostgreSQL 16 container shared by the package.
func SharedPostgres(t testing.TB) DB { t.Helper(); return sharedForTest(t, Postgres) }

// SharedMySQL returns a MySQL 8 container shared by the package.
func SharedMySQL(t testing.TB) DB { t.Helper(); return sharedForTest(t, MySQL) }

// SharedMariaDB returns a MariaDB 11 container shared by the package.
func SharedMariaDB(t testing.TB) DB { t.Helper(); return sharedForTest(t, MariaDB) }

// TerminateShared stops all shared containers started so far.
func TerminateShared() {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	for k, e := range shared {
		if e.terminate != nil {
			_ = e.terminate(context.Background())
		}
		delete(shared, k)
	}
}

// runner is the subset of *testing.M used by Main.
type runner interface{ Run() int }

// Main runs the package's tests and then terminates shared containers. Use it
// from TestMain: os.Exit(testutil.Main(m)).
func Main(m runner) int {
	defer TerminateShared()
	return m.Run()
}

// SQLiteTemp returns a DSN for a fresh SQLite database file in a temporary
// directory that is removed when the test ends.
func SQLiteTemp(t testing.TB) DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	return DB{Driver: DriverSQLite, DSN: path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"}
}

// Open opens db with database/sql, verifies the connection and closes it when
// the test ends.
func Open(t testing.TB, db DB) *sql.DB {
	t.Helper()
	conn, err := sql.Open(db.Driver, db.DSN)
	if err != nil {
		t.Fatalf("open %s: %v", db.Driver, err)
		return nil
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := conn.PingContext(ctx); err != nil {
		t.Fatalf("ping %s: %v", db.Driver, err)
		return nil
	}
	return conn
}
