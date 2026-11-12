package testutil

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"lontar/internal/driver"
)

// Names of the objects created by SeedStructure (in StructSchema).
const (
	StructCustomers  = "customers"       // serial/auto-increment PK, unique + expression (+ GIN) indexes, comments, enum
	StructOrders     = "orders"          // composite PK, FK → customers ON DELETE CASCADE, generated column, check, partial index, trigger
	StructOrderLines = "order_lines"     // identity/auto-increment PK, composite FK → orders ON DELETE CASCADE ON UPDATE SET NULL
	StructCategories = "categories"      // self-referencing FK
	StructWeird      = `Weird "Q" Tbl`   // awkward names: columns "select", "Mixed Col"; FK → customers
	StructView       = "customer_orders" // view
	StructMatView    = "order_totals"    // PostgreSQL materialized view
	// StructGeoSchema holds regions (cross-schema FK target), the enum type
	// and the trigger function on PostgreSQL.
	StructGeoSchema = "geo"
)

// StructSchema is the schema holding the SeedStructure fixture.
func StructSchema(engine string) string {
	if engine == EnginePostgres {
		return "shop"
	}
	return SeedSchema(engine)
}

var structurePG = []string{
	`CREATE SCHEMA shop`,
	`CREATE SCHEMA geo`,
	`CREATE TYPE geo.status AS ENUM ('active', 'blocked')`,
	`CREATE TABLE geo.regions (code text PRIMARY KEY, name text)`,
	`CREATE FUNCTION geo.touch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.note := COALESCE(NEW.note, ''); RETURN NEW; END $$`,
	`CREATE TABLE shop.categories (id integer PRIMARY KEY, parent_id integer REFERENCES shop.categories (id), name text CHECK (name <> ''))`,
	`CREATE TABLE shop.customers (
	   id serial PRIMARY KEY,
	   email varchar(255) NOT NULL,
	   name varchar(100) COLLATE "C",
	   status geo.status NOT NULL DEFAULT 'active',
	   tags text[],
	   region_code text REFERENCES geo.regions (code),
	   created_at timestamptz DEFAULT now(),
	   CONSTRAINT customers_email_key UNIQUE (email))`,
	`COMMENT ON TABLE shop.customers IS 'People who buy'`,
	`COMMENT ON COLUMN shop.customers.name IS 'Full name'`,
	`CREATE INDEX customers_lower_email ON shop.customers (lower(email))`,
	`CREATE INDEX customers_tags_gin ON shop.customers USING gin (tags)`,
	`CREATE TABLE shop.orders (
	   customer_id integer NOT NULL REFERENCES shop.customers (id) ON DELETE CASCADE,
	   order_no integer NOT NULL,
	   qty integer NOT NULL DEFAULT 1,
	   price numeric(10,2) NOT NULL,
	   total numeric(12,2) GENERATED ALWAYS AS (qty * price) STORED,
	   note text,
	   PRIMARY KEY (customer_id, order_no),
	   CONSTRAINT orders_price_check CHECK (price >= 0))`,
	`CREATE INDEX orders_big_idx ON shop.orders (customer_id) WHERE price > 100`,
	`CREATE TRIGGER orders_touch BEFORE INSERT OR UPDATE ON shop.orders FOR EACH ROW EXECUTE FUNCTION geo.touch()`,
	`CREATE TABLE shop.order_lines (
	   id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	   customer_id integer,
	   order_no integer,
	   "Line Note" text,
	   CONSTRAINT order_lines_order_fk FOREIGN KEY (customer_id, order_no)
	     REFERENCES shop.orders (customer_id, order_no) ON DELETE CASCADE ON UPDATE SET NULL)`,
	`CREATE TABLE shop."Weird ""Q"" Tbl" ("select" integer PRIMARY KEY, "Mixed Col" text, customer_id integer REFERENCES shop.customers (id))`,
	`COMMENT ON COLUMN shop."Weird ""Q"" Tbl"."Mixed Col" IS 'It''s \odd'`,
	`CREATE VIEW shop.customer_orders AS SELECT c.id, c.email, o.order_no FROM shop.customers c JOIN shop.orders o ON o.customer_id = c.id`,
	`CREATE MATERIALIZED VIEW shop.order_totals AS SELECT customer_id, sum(total) AS total FROM shop.orders GROUP BY customer_id`,
	`CREATE INDEX order_totals_cust ON shop.order_totals (customer_id)`,
	`INSERT INTO geo.regions VALUES ('EU', 'Europe')`,
	`INSERT INTO shop.customers (email, name, region_code) VALUES ('a@x.io', 'Ann', 'EU'), ('b@x.io', 'Bob', NULL)`,
	`INSERT INTO shop.orders (customer_id, order_no, price) VALUES (1, 1, 10), (1, 2, 200), (2, 1, 5)`,
	`INSERT INTO shop.order_lines (customer_id, order_no, "Line Note") VALUES (1, 1, 'x'), (1, 2, NULL)`,
	`INSERT INTO shop.categories VALUES (1, NULL, 'root'), (2, 1, 'child')`,
	`REFRESH MATERIALIZED VIEW shop.order_totals`,
	`ANALYZE`,
}

func structureMySQL(functionalIndex bool) []string {
	s := []string{
		`CREATE TABLE categories (id INT PRIMARY KEY, parent_id INT, name VARCHAR(50),
		   CONSTRAINT categories_parent_fk FOREIGN KEY (parent_id) REFERENCES categories (id),
		   CONSTRAINT categories_name_check CHECK (name <> ''))`,
		`CREATE TABLE customers (
		   id INT AUTO_INCREMENT PRIMARY KEY,
		   email VARCHAR(255) NOT NULL,
		   name VARCHAR(100) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin COMMENT 'Full name',
		   status ENUM('active', 'blocked') NOT NULL DEFAULT 'active',
		   score INT UNSIGNED,
		   created_at TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
		   UNIQUE KEY customers_email_key (email)
		 ) ENGINE=InnoDB COMMENT='People who buy'`,
		`CREATE TABLE orders (
		   customer_id INT NOT NULL,
		   order_no INT NOT NULL,
		   qty INT NOT NULL DEFAULT 1,
		   price DECIMAL(10,2) NOT NULL,
		   total DECIMAL(12,2) GENERATED ALWAYS AS (qty * price) STORED,
		   note TEXT,
		   PRIMARY KEY (customer_id, order_no),
		   KEY orders_price_idx (price),
		   CONSTRAINT orders_customer_fk FOREIGN KEY (customer_id) REFERENCES customers (id) ON DELETE CASCADE,
		   CONSTRAINT orders_price_check CHECK (price >= 0))`,
		`CREATE TRIGGER orders_bi BEFORE INSERT ON orders FOR EACH ROW SET NEW.note = COALESCE(NEW.note, '')`,
		`CREATE TABLE order_lines (
		   id BIGINT AUTO_INCREMENT PRIMARY KEY,
		   customer_id INT NULL,
		   order_no INT NULL,
		   ` + "`Line Note`" + ` TEXT,
		   CONSTRAINT order_lines_order_fk FOREIGN KEY (customer_id, order_no)
		     REFERENCES orders (customer_id, order_no) ON DELETE CASCADE ON UPDATE SET NULL)`,
		"CREATE TABLE `Weird \"Q\" Tbl` (`select` INT PRIMARY KEY, `Mixed Col` TEXT, customer_id INT, " +
			"CONSTRAINT weird_customer_fk FOREIGN KEY (customer_id) REFERENCES customers (id))",
		`CREATE VIEW customer_orders AS SELECT c.id, c.email, o.order_no FROM customers c JOIN orders o ON o.customer_id = c.id`,
		`INSERT INTO customers (email, name) VALUES ('a@x.io', 'Ann'), ('b@x.io', 'Bob')`,
		`INSERT INTO orders (customer_id, order_no, price) VALUES (1, 1, 10), (1, 2, 200), (2, 1, 5)`,
		`INSERT INTO order_lines (customer_id, order_no, ` + "`Line Note`" + `) VALUES (1, 1, 'x'), (1, 2, NULL)`,
		`INSERT INTO categories VALUES (1, NULL, 'root'), (2, 1, 'child')`,
	}
	if functionalIndex {
		s = append(s, `CREATE INDEX customers_lower_email ON customers ((lower(email)))`)
	}
	return append(s, `ANALYZE TABLE customers, orders, order_lines, categories`)
}

var structureSQLite = []string{
	`CREATE TABLE categories (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES categories, name TEXT)`,
	`CREATE TABLE customers (
	   id INTEGER PRIMARY KEY AUTOINCREMENT,
	   email TEXT NOT NULL UNIQUE,
	   name TEXT COLLATE NOCASE,
	   status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'blocked')),
	   created_at TEXT DEFAULT CURRENT_TIMESTAMP)`,
	`CREATE INDEX customers_lower_email ON customers (lower(email))`,
	`CREATE TABLE orders (
	   customer_id INTEGER NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
	   order_no INTEGER NOT NULL,
	   qty INTEGER NOT NULL DEFAULT 1,
	   price NUMERIC NOT NULL CHECK (price >= 0),
	   total NUMERIC GENERATED ALWAYS AS (qty * price) STORED,
	   note TEXT,
	   PRIMARY KEY (customer_id, order_no))`,
	`CREATE INDEX orders_big_idx ON orders (customer_id) WHERE price > 100`,
	`CREATE TRIGGER orders_ai AFTER INSERT ON orders BEGIN
	   UPDATE orders SET note = COALESCE(note, '') WHERE customer_id = NEW.customer_id AND order_no = NEW.order_no;
	 END`,
	`CREATE TABLE order_lines (
	   id INTEGER PRIMARY KEY,
	   customer_id INTEGER,
	   order_no INTEGER,
	   "Line Note" TEXT,
	   CONSTRAINT order_lines_order_fk FOREIGN KEY (customer_id, order_no)
	     REFERENCES orders (customer_id, order_no) ON DELETE CASCADE ON UPDATE SET NULL)`,
	`CREATE TABLE "Weird ""Q"" Tbl" ("select" INTEGER PRIMARY KEY, "Mixed Col" TEXT, customer_id INTEGER REFERENCES customers (id))`,
	`CREATE VIEW customer_orders AS SELECT c.id, c.email, o.order_no FROM customers c JOIN orders o ON o.customer_id = c.id`,
	`INSERT INTO customers (email, name) VALUES ('a@x.io', 'Ann'), ('b@x.io', 'Bob')`,
	`INSERT INTO orders (customer_id, order_no, price) VALUES (1, 1, 10), (1, 2, 200), (2, 1, 5)`,
	`INSERT INTO order_lines (customer_id, order_no, "Line Note") VALUES (1, 1, 'x'), (1, 2, NULL)`,
	`INSERT INTO categories VALUES (1, NULL, 'root'), (2, 1, 'child')`,
}

// StructureStatements returns the statements SeedStructure runs for engine.
func StructureStatements(engine string) ([]string, error) {
	switch engine {
	case EnginePostgres:
		return structurePG, nil
	case EngineMySQL:
		return structureMySQL(true), nil
	case EngineMariaDB:
		// MariaDB has no functional indexes.
		return structureMySQL(false), nil
	case EngineSQLite:
		return structureSQLite, nil
	}
	_, err := SeedStatements(engine)
	return nil, err
}

// SeedStructure creates the structure fixture (see the Struct* constants)
// once per DSN, after the browse fixture.
func SeedStructure(t testing.TB, engine string, target DB, db *sql.DB) {
	t.Helper()
	stmts, err := StructureStatements(engine)
	if err != nil {
		t.Fatalf("%v", err)
		return
	}
	SeedBrowse(t, engine, target, db)
	if engine == EngineMySQL {
		// With binary logging on, MySQL lets only privileged users create
		// triggers unless log_bin_trust_function_creators is set.
		root, err := AsRoot(target)
		if err != nil {
			t.Fatalf("%v", err)
			return
		}
		rdb := Open(t, root)
		if _, err := rdb.ExecContext(context.Background(), `SET GLOBAL log_bin_trust_function_creators = 1`); err != nil {
			t.Fatalf("%v", err)
			return
		}
	}
	seedOnce(t, "structure", engine, target, db, stmts)
}

// FindIndex returns the index named name (nil when missing).
func FindIndex(ixs []driver.Index, name string) *driver.Index {
	for i := range ixs {
		if ixs[i].Name == name {
			return &ixs[i]
		}
	}
	return nil
}

// FindFK returns the first foreign key from table to refTable (nil when
// missing).
func FindFK(fks []driver.ForeignKey, table, refTable string) *driver.ForeignKey {
	for i := range fks {
		if fks[i].Table == table && fks[i].RefTable == refTable {
			return &fks[i]
		}
	}
	return nil
}

// FindColumn returns the column named name (nil when missing).
func FindColumn(cols []driver.ColumnDetail, name string) *driver.ColumnDetail {
	for i := range cols {
		if cols[i].Name == name {
			return &cols[i]
		}
	}
	return nil
}

// CheckStructure asserts that in describes the SeedStructure fixture in db
// correctly (TableInfo and TableDDL). It is shared by the driver packages'
// integration tests.
func CheckStructure(t testing.TB, engine string, in driver.Introspector, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	schema := StructSchema(engine)
	pg, sqlite := engine == EnginePostgres, engine == EngineSQLite
	my := engine == EngineMySQL || engine == EngineMariaDB
	check := func(ok bool, format string, args ...any) {
		t.Helper()
		if !ok {
			t.Errorf(engine+": "+format, args...)
		}
	}
	info := func(table string) *driver.TableDetail {
		t.Helper()
		ti, err := in.TableInfo(ctx, db, schema, table)
		if err != nil {
			t.Fatalf("%s: TableInfo(%s): %v", engine, table, err)
		}
		return ti
	}
	ddl := func(table string) string {
		t.Helper()
		s, err := in.TableDDL(ctx, db, schema, table)
		if err != nil {
			t.Fatalf("%s: TableDDL(%s): %v", engine, table, err)
		}
		return s
	}
	has := func(s string, subs ...string) bool {
		for _, sub := range subs {
			if !strings.Contains(s, sub) {
				return false
			}
		}
		return true
	}

	// customers: auto-increment key, comments, collation, enum, indexes.
	cu := info(StructCustomers)
	check(cu.Schema == schema && cu.Name == StructCustomers && cu.Type == driver.TypeTable, "customers = %+v", cu)
	id := FindColumn(cu.Columns, "id")
	check(id != nil && id.IsPrimaryKey && id.PKPosition == 1 && id.AutoIncrement && !id.Nullable && id.Ordinal == 1, "customers.id = %+v", id)
	email := FindColumn(cu.Columns, "email")
	check(email != nil && !email.Nullable && (sqlite || strings.Contains(email.DataType, "255")), "email = %+v", email)
	name := FindColumn(cu.Columns, "name")
	status := FindColumn(cu.Columns, "status")
	check(status != nil && status.Default != nil && strings.Contains(*status.Default, "active"), "status = %+v", status)
	if !sqlite {
		check(cu.Comment == "People who buy", "table comment = %q", cu.Comment)
		check(name != nil && name.Comment == "Full name", "name comment = %+v", name)
		check(status != nil && slices.Equal(status.EnumValues, []string{"active", "blocked"}), "status enum = %+v", status)
		check(cu.SizeBytes != nil && *cu.SizeBytes > 0, "size = %v", cu.SizeBytes)
		check(cu.EstimatedRows != nil, "estimate = %v", cu.EstimatedRows)
	}
	switch {
	case pg:
		check(name != nil && name.Collation == "C", "name collation = %+v", name)
		check(id != nil && id.Default != nil && strings.HasPrefix(*id.Default, "nextval("), "serial default = %+v", id)
		fk := FindFK(cu.ForeignKeys, StructCustomers, "regions")
		check(fk != nil && fk.RefSchema == StructGeoSchema && slices.Equal(fk.Columns, []string{"region_code"}) &&
			slices.Equal(fk.RefColumns, []string{"code"}) && fk.OnDelete == "NO ACTION", "cross-schema fk = %+v", fk)
		gin := FindIndex(cu.Indexes, "customers_tags_gin")
		check(gin != nil && gin.Method == "gin" && slices.Equal(gin.Columns, []string{"tags"}), "gin = %+v", gin)
		lower := FindIndex(cu.Indexes, "customers_lower_email")
		check(lower != nil && len(lower.Columns) == 1 && strings.HasPrefix(lower.Columns[0], "(lower(") &&
			strings.HasPrefix(lower.Definition, "CREATE INDEX customers_lower_email"), "expression index = %+v", lower)
	case my:
		check(cu.Engine == "InnoDB" && cu.Collation != "", "engine = %q collation = %q", cu.Engine, cu.Collation)
		check(name != nil && name.Collation == "utf8mb4_bin", "name collation = %+v", name)
		score := FindColumn(cu.Columns, "score")
		check(score != nil && strings.Contains(score.DataType, "unsigned"), "score = %+v", score)
		created := FindColumn(cu.Columns, "created_at")
		check(created != nil && strings.Contains(strings.ToUpper(created.OnUpdate), "CURRENT_TIMESTAMP"), "on update = %+v", created)
		lower := FindIndex(cu.Indexes, "customers_lower_email")
		if engine == EngineMySQL {
			check(lower != nil && len(lower.Columns) == 1 && strings.Contains(lower.Columns[0], "lower("), "functional index = %+v", lower)
		}
	default:
		lower := FindIndex(cu.Indexes, "customers_lower_email")
		check(lower != nil && slices.Equal(lower.Columns, []string{"(expression)"}) && strings.Contains(lower.Definition, "lower(email)"), "expression index = %+v", lower)
		check(!cu.ChecksSupported && cu.SizeBytes == nil, "sqlite checks/size = %+v", cu)
	}
	uq := slices.IndexFunc(cu.Indexes, func(ix driver.Index) bool {
		return ix.Unique && !ix.Primary && slices.Equal(ix.Columns, []string{"email"})
	})
	check(uq >= 0, "unique index = %+v", cu.Indexes)
	check(len(cu.Indexes) > 0 && (sqlite || cu.Indexes[0].Primary), "primary index first = %+v", cu.Indexes)
	in1 := FindFK(cu.ReferencedBy, StructOrders, StructCustomers)
	check(in1 != nil && slices.Equal(in1.Columns, []string{"customer_id"}) && in1.Schema == schema, "referenced by orders = %+v", cu.ReferencedBy)
	check(FindFK(cu.ReferencedBy, StructWeird, StructCustomers) != nil, "referenced by weird = %+v", cu.ReferencedBy)

	// orders: composite key, generated column, check, partial index, trigger.
	or := info(StructOrders)
	cid, ono := FindColumn(or.Columns, "customer_id"), FindColumn(or.Columns, "order_no")
	check(cid != nil && cid.PKPosition == 1 && ono != nil && ono.PKPosition == 2, "orders pk = %+v %+v", cid, ono)
	total := FindColumn(or.Columns, "total")
	check(total != nil && total.Generated == "STORED" && total.Default == nil, "total = %+v", total)
	if !sqlite {
		check(total != nil && total.GenerationExpr != nil && strings.Contains(*total.GenerationExpr, "qty"), "generation expr = %+v", total)
		ci := slices.IndexFunc(or.Checks, func(c driver.Check) bool { return c.Name == "orders_price_check" })
		check(or.ChecksSupported && ci >= 0 && strings.Contains(or.Checks[ci].Expression, "price"), "checks = %+v", or.Checks)
	}
	ofk := FindFK(or.ForeignKeys, StructOrders, StructCustomers)
	check(ofk != nil && ofk.OnDelete == "CASCADE" && slices.Equal(ofk.RefColumns, []string{"id"}) && ofk.RefSchema == schema, "orders fk = %+v", ofk)
	check(len(or.Triggers) == 1, "orders triggers = %+v", or.Triggers)
	if len(or.Triggers) == 1 {
		tr := or.Triggers[0]
		switch {
		case pg:
			check(tr.Name == "orders_touch" && tr.Timing == "BEFORE" && tr.Event == "INSERT OR UPDATE" && strings.Contains(tr.Definition, "geo.touch()"), "trigger = %+v", tr)
		case my:
			check(tr.Name == "orders_bi" && tr.Timing == "BEFORE" && tr.Event == "INSERT" && strings.Contains(tr.Definition, "COALESCE"), "trigger = %+v", tr)
		default:
			check(tr.Name == "orders_ai" && tr.Timing == "AFTER" && tr.Event == "INSERT", "trigger = %+v", tr)
		}
	}
	if pg || sqlite {
		big := FindIndex(or.Indexes, "orders_big_idx")
		check(big != nil && strings.Contains(big.Predicate, "price") && slices.Equal(big.Columns, []string{"customer_id"}), "partial index = %+v", big)
	}

	// order_lines: composite FK with actions; identity key.
	ol := info(StructOrderLines)
	lfk := FindFK(ol.ForeignKeys, StructOrderLines, StructOrders)
	check(lfk != nil && slices.Equal(lfk.Columns, []string{"customer_id", "order_no"}) &&
		slices.Equal(lfk.RefColumns, []string{"customer_id", "order_no"}) &&
		lfk.OnDelete == "CASCADE" && lfk.OnUpdate == "SET NULL", "composite fk = %+v", lfk)
	check(sqlite || (lfk != nil && lfk.Name == "order_lines_order_fk"), "fk name = %+v", lfk)
	lid := FindColumn(ol.Columns, "id")
	check(lid != nil && lid.AutoIncrement, "order_lines.id = %+v", lid)
	if pg {
		check(lid != nil && lid.Identity == "ALWAYS" && lid.Default == nil, "identity = %+v", lid)
	}
	check(FindColumn(ol.Columns, "Line Note") != nil, "Line Note column = %+v", ol.Columns)
	check(FindFK(or.ReferencedBy, StructOrderLines, StructOrders) != nil, "orders referenced by = %+v", or.ReferencedBy)

	// categories: self reference (SQLite: REFERENCES without columns).
	ca := info(StructCategories)
	sfk := FindFK(ca.ForeignKeys, StructCategories, StructCategories)
	check(sfk != nil && slices.Equal(sfk.Columns, []string{"parent_id"}) && slices.Equal(sfk.RefColumns, []string{"id"}), "self fk = %+v", sfk)
	check(FindFK(ca.ReferencedBy, StructCategories, StructCategories) != nil, "self referenced by = %+v", ca.ReferencedBy)

	// Awkward names.
	we := info(StructWeird)
	check(len(we.Columns) == 3 && we.Columns[0].Name == "select" && we.Columns[0].IsPrimaryKey && we.Columns[1].Name == "Mixed Col", "weird columns = %+v", we.Columns)
	check(FindFK(we.ForeignKeys, StructWeird, StructCustomers) != nil, "weird fk = %+v", we.ForeignKeys)

	// Views.
	vw := info(StructView)
	check(vw.Type == driver.TypeView && len(vw.Columns) == 3 && len(vw.ForeignKeys) == 0, "view = %+v", vw)

	// Unknown tables.
	for _, bad := range []string{"no such table", "customers`; DROP TABLE orders; --", `customers"; DROP TABLE orders; --`} {
		if _, err := in.TableInfo(ctx, db, schema, bad); !errors.Is(err, driver.ErrTableNotFound) {
			t.Errorf("%s: TableInfo(%q) err = %v", engine, bad, err)
		}
		if _, err := in.TableDDL(ctx, db, schema, bad); !errors.Is(err, driver.ErrTableNotFound) {
			t.Errorf("%s: TableDDL(%q) err = %v", engine, bad, err)
		}
	}

	// DDL.
	cd, od, wd, vd := ddl(StructCustomers), ddl(StructOrders), ddl(StructWeird), ddl(StructView)
	switch {
	case pg:
		check(has(cd, "CREATE TABLE shop.customers (", "id serial NOT NULL", `COLLATE pg_catalog."C"`,
			"status geo.status DEFAULT 'active'::geo.status NOT NULL", "CONSTRAINT customers_email_key UNIQUE (email)",
			"REFERENCES geo.regions(code)", "CREATE INDEX customers_tags_gin ON shop.customers USING gin (tags);",
			"COMMENT ON TABLE shop.customers IS 'People who buy';", "COMMENT ON COLUMN shop.customers.name IS 'Full name';"), "customers DDL:\n%s", cd)
		check(!strings.Contains(cd, "nextval"), "serial default in DDL:\n%s", cd)
		check(has(od, "GENERATED ALWAYS AS (((qty)::numeric * price)) STORED", "CONSTRAINT orders_price_check CHECK",
			"ON DELETE CASCADE", "WHERE (price > (100)::numeric);", "CREATE TRIGGER orders_touch BEFORE INSERT OR UPDATE ON shop.orders"), "orders DDL:\n%s", od)
		check(has(wd, `CREATE TABLE shop."Weird ""Q"" Tbl" (`, `"select" integer NOT NULL`, `"Mixed Col" text`,
			`COMMENT ON COLUMN shop."Weird ""Q"" Tbl"."Mixed Col" IS E'It''s \\odd';`), "weird DDL:\n%s", wd)
		check(has(vd, "CREATE VIEW shop.customer_orders AS\n", "FROM shop.customers c"), "view DDL:\n%s", vd)
		md := ddl(StructMatView)
		check(has(md, "CREATE MATERIALIZED VIEW shop.order_totals AS\n", "WITH DATA;", "CREATE INDEX order_totals_cust"), "matview DDL:\n%s", md)
		ld := ddl(StructOrderLines)
		check(has(ld, "id bigint GENERATED ALWAYS AS IDENTITY NOT NULL", "ON UPDATE SET NULL ON DELETE CASCADE"), "order_lines DDL:\n%s", ld)
	case my:
		check(has(cd, "CREATE TABLE `customers`", "AUTO_INCREMENT", "COMMENT='People who buy'", "COMMENT 'Full name'"), "customers DDL:\n%s", cd)
		check(has(od, "CREATE TABLE `orders`", "CONSTRAINT `orders_price_check` CHECK", " TRIGGER ", "orders_bi", "BEFORE INSERT ON"), "orders DDL:\n%s", od)
		check(has(wd, "CREATE TABLE `Weird \"Q\" Tbl`", "`select` int"), "weird DDL:\n%s", wd)
		check(has(vd, "VIEW `customer_orders` AS"), "view DDL:\n%s", vd)
	default:
		check(has(cd, "CREATE TABLE customers (", "AUTOINCREMENT", "CREATE INDEX customers_lower_email ON customers (lower(email));"), "customers DDL:\n%s", cd)
		check(has(od, "CREATE INDEX orders_big_idx", "CREATE TRIGGER orders_ai AFTER INSERT ON orders BEGIN", "END;"), "orders DDL:\n%s", od)
		check(has(wd, `CREATE TABLE "Weird ""Q"" Tbl"`), "weird DDL:\n%s", wd)
		check(has(vd, "CREATE VIEW customer_orders AS"), "view DDL:\n%s", vd)
	}
}
