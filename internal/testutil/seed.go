package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
)

// Engine names accepted by SeedBrowse.
const (
	EnginePostgres = "postgres"
	EngineMySQL    = "mysql"
	EngineMariaDB  = "mariadb"
	EngineSQLite   = "sqlite"
)

// Names of the objects created by SeedBrowse.
const (
	SeedItems     = "items"      // all value types, ids 1..3
	SeedView      = "item_names" // view over items
	SeedMatView   = "item_mv"    // PostgreSQL only
	SeedNumbers   = "numbers"    // n = 1..1000 (PK), label 'n<n>', grp = n % 10
	SeedNoPK      = "nopk"       // no primary key
	SeedComposite = "composite"  // PRIMARY KEY (b, a)
	// SeedOdd is a table name with a space, double quotes and mixed case.
	// Its columns are "order" (reserved word, PK), "Mixed Case" and
	// `quote"col`.
	SeedOdd = `Odd "Name" Tbl`
	// SeedOtherSchema holds table other_t on PostgreSQL.
	SeedOtherSchema = "other"
	// BigInt is beyond 2^53; items.big holds BigInt (id 1) and -BigInt (id 3).
	BigInt = "9007199254740993"
	// SeedUUID is items.uid of id 1.
	SeedUUID = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
)

// numbersSQL fills numbers from a 10-row digits table (portable across all
// engines, unlike generate_series or recursive CTE inserts).
var numbersSQL = []string{
	`CREATE TABLE digits (d INT)`,
	`INSERT INTO digits VALUES (0),(1),(2),(3),(4),(5),(6),(7),(8),(9)`,
	`CREATE TABLE numbers (n INT PRIMARY KEY, label VARCHAR(20), grp INT DEFAULT 0)`,
	`INSERT INTO numbers SELECT a.d*100 + b.d*10 + c.d + 1, CONCAT('n', a.d*100 + b.d*10 + c.d + 1), (a.d*100 + b.d*10 + c.d + 1) % 10
	 FROM digits a, digits b, digits c`,
}

var commonTail = []string{
	`CREATE TABLE nopk (a INT, b VARCHAR(10))`,
	`INSERT INTO nopk VALUES (1, 'x'), (2, 'y')`,
	`CREATE TABLE composite (a INT NOT NULL, b INT NOT NULL, v VARCHAR(10), PRIMARY KEY (b, a))`,
	`INSERT INTO composite VALUES (1, 2, 'p'), (2, 1, 'q')`,
}

var seedSQL = map[string][]string{
	EnginePostgres: append(append([]string{
		`CREATE TABLE items (id integer PRIMARY KEY, big bigint, price numeric(12,4), name varchar(100), note text,
		  data bytea, flag boolean, born date, seen_at timestamp, doc jsonb, uid uuid, tags int[], dur interval)`,
		`INSERT INTO items VALUES
		  (1, 9007199254740993, 12.3400, 'alpha', '', '\x00ff10', true, '2024-01-02', '2024-01-02 03:04:05', '{"a": 1}', 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', '{1,2}', '1 day'),
		  (2, 2, NULL, 'Beta', NULL, NULL, false, NULL, NULL, NULL, NULL, NULL, NULL),
		  (3, -9007199254740993, -0.5, 'gamma', 'x', '\xfffe', NULL, '1999-12-31', '1999-12-31 23:59:59', '[1, 2]', NULL, NULL, NULL)`,
		`CREATE VIEW item_names AS SELECT id, name FROM items`,
		`CREATE MATERIALIZED VIEW item_mv AS SELECT id, big FROM items`,
		`CREATE TABLE "Odd ""Name"" Tbl" ("order" integer PRIMARY KEY, "Mixed Case" varchar(20), "quote""col" text)`,
		`INSERT INTO "Odd ""Name"" Tbl" VALUES (1, 'One', 'q1'), (2, 'Two', 'q2')`,
		`CREATE SCHEMA other`,
		`CREATE TABLE other.other_t (id integer)`,
	}, numbersSQL...), append(commonTail, `ANALYZE`)...),
	EngineMySQL: append(append([]string{
		`CREATE TABLE items (id INT PRIMARY KEY, big BIGINT, price DECIMAL(12,4), name VARCHAR(100), note TEXT,
		  data BLOB, flag BOOLEAN, born DATE, seen_at DATETIME, doc JSON, uid CHAR(36))`,
		`INSERT INTO items VALUES
		  (1, 9007199254740993, 12.3400, 'alpha', '', X'00ff10', TRUE, '2024-01-02', '2024-01-02 03:04:05', '{"a": 1}', 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11'),
		  (2, 2, NULL, 'Beta', NULL, NULL, FALSE, NULL, NULL, NULL, NULL),
		  (3, -9007199254740993, -0.5, 'gamma', 'x', X'fffe', NULL, '1999-12-31', '1999-12-31 23:59:59', '[1, 2]', NULL)`,
		"CREATE VIEW item_names AS SELECT id, name FROM items",
		"CREATE TABLE `Odd \"Name\" Tbl` (`order` INT PRIMARY KEY, `Mixed Case` VARCHAR(20), `quote\"col` TEXT)",
		"INSERT INTO `Odd \"Name\" Tbl` VALUES (1, 'One', 'q1'), (2, 'Two', 'q2')",
	}, numbersSQL...), append(commonTail, `ANALYZE TABLE items, numbers, nopk, composite`)...),
	EngineSQLite: append(append([]string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, big INTEGER, price NUMERIC, name VARCHAR(100), note TEXT,
		  data BLOB, flag BOOLEAN, born DATE, seen_at TIMESTAMP, doc JSON, uid TEXT)`,
		`INSERT INTO items VALUES
		  (1, 9007199254740993, '12.3400', 'alpha', '', X'00ff10', 1, '2024-01-02', '2024-01-02 03:04:05', '{"a": 1}', 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11'),
		  (2, 2, NULL, 'Beta', NULL, NULL, 0, NULL, NULL, NULL, NULL),
		  (3, -9007199254740993, -0.5, 'gamma', 'x', X'fffe', NULL, '1999-12-31', '1999-12-31 23:59:59', '[1, 2]', NULL)`,
		`CREATE VIEW item_names AS SELECT id, name FROM items`,
		`CREATE TABLE "Odd ""Name"" Tbl" ("order" INTEGER PRIMARY KEY, "Mixed Case" VARCHAR(20), "quote""col" TEXT)`,
		`INSERT INTO "Odd ""Name"" Tbl" VALUES (1, 'One', 'q1'), (2, 'Two', 'q2')`,
	}, numbersSQL...), commonTail...),
}

func init() { seedSQL[EngineMariaDB] = seedSQL[EngineMySQL] }

// SeedStatements returns the statements SeedBrowse runs for engine.
func SeedStatements(engine string) ([]string, error) {
	s, ok := seedSQL[engine]
	if !ok {
		return nil, fmt.Errorf("testutil: no seed for engine %q", engine)
	}
	return s, nil
}

var (
	seedMu   sync.Mutex
	seedDone = map[string]*seedEntry{}
)

type seedEntry struct {
	once sync.Once
	err  error
}

// SeedBrowse creates the browse fixture (see the Seed* constants) in db.
// It runs once per DSN per test binary, so tests sharing a container can
// all call it.
func SeedBrowse(t testing.TB, engine string, target DB, db *sql.DB) {
	t.Helper()
	stmts, err := SeedStatements(engine)
	if err != nil {
		t.Fatalf("%v", err)
		return
	}
	seedOnce(t, "browse", engine, target, db, stmts)
}

// seedOnce runs stmts once per fixture name and DSN per test binary.
func seedOnce(t testing.TB, name, engine string, target DB, db *sql.DB, stmts []string) {
	t.Helper()
	seedMu.Lock()
	key := name + "\x00" + target.DSN
	e, ok := seedDone[key]
	if !ok {
		e = &seedEntry{}
		seedDone[key] = e
	}
	seedMu.Unlock()
	e.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, s := range stmts {
			if _, err := db.ExecContext(ctx, s); err != nil {
				e.err = fmt.Errorf("seed %s %s: %w\n%s", name, engine, err, s)
				return
			}
		}
	})
	if e.err != nil {
		t.Fatalf("%v", e.err)
	}
}

// AsRoot returns a MySQL/MariaDB DB that connects as root (the containers
// use the same password for root) and without a default database.
func AsRoot(db DB) (DB, error) {
	cfg, err := gomysql.ParseDSN(db.DSN)
	if err != nil {
		return DB{}, err
	}
	cfg.User = "root"
	cfg.DBName = ""
	return DB{Driver: db.Driver, DSN: cfg.FormatDSN()}, nil
}
