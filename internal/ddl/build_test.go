package ddl

import (
	"errors"
	"strings"
	"testing"

	"lontar/internal/driver"
)

func ptr[T any](v T) *T { return &v }

// fixture: users(id PK auto, email NOT NULL, name, note) with an index,
// a unique constraint index, a FK, a check and a trigger.
func fixture() *driver.TableDetail {
	d := driver.NewTableDetail(driver.Table{Schema: "s", Name: "users", Type: driver.TypeTable})
	d.Columns = []driver.ColumnDetail{
		{Name: "id", Ordinal: 1, DataType: "integer", IsPrimaryKey: true, PKPosition: 1, AutoIncrement: true, Identity: "BY DEFAULT"},
		{Name: "email", Ordinal: 2, DataType: "varchar(255)", Nullable: false},
		{Name: "name", Ordinal: 3, DataType: "text", Nullable: true, Default: ptr("'x'::text")},
		{Name: "org_id", Ordinal: 4, DataType: "integer", Nullable: true},
	}
	d.Indexes = []driver.Index{
		{Name: "users_pkey", Columns: []string{"id"}, Unique: true, Primary: true},
		{Name: "users_email_key", Columns: []string{"email"}, Unique: true},
		{Name: "users_name_idx", Columns: []string{"name"}},
	}
	d.ForeignKeys = []driver.ForeignKey{{Name: "users_org_fk", Schema: "s", Table: "users", Columns: []string{"org_id"}, RefSchema: "s", RefTable: "orgs", RefColumns: []string{"id"}}}
	d.Checks = []driver.Check{{Name: "users_email_check", Expression: "email <> ''"}}
	d.Triggers = []driver.Trigger{{Name: "users_touch", Timing: "BEFORE", Event: "UPDATE"}}
	return d
}

func input(kind driver.Kind, version string, tc TableChange) Input {
	caps := Capabilities(kind, version)
	in := Input{Kind: kind, Dialect: driver.DialectFor(kind), Caps: caps, Lit: Literals{Flavor: Flavor(caps.Flavor)}, Change: tc}
	if !tc.Create {
		in.Current = fixture()
		if in.Change.Schema == "" {
			in.Change.Schema = "s"
		}
		if in.Change.Table == "" {
			in.Change.Table = "users"
		}
	}
	in.Refs = func(schema, table string) ([]string, error) {
		if table == "orgs" {
			return []string{"id", "code"}, nil
		}
		return nil, driver.ErrTableNotFound
	}
	return in
}

func sqls(t *testing.T, in Input) []string {
	t.Helper()
	stmts, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out := make([]string, len(stmts))
	for i, s := range stmts {
		out[i] = s.SQL
	}
	return out
}

func wantSQL(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n;\n") != strings.Join(want, "\n;\n") {
		t.Fatalf("got:\n%s\n\nwant:\n%s", strings.Join(got, "\n;\n"), strings.Join(want, "\n;\n"))
	}
}

func failure(t *testing.T, in Input) *ChangeError {
	t.Helper()
	_, err := Build(in)
	var ce *ChangeError
	if !errors.As(err, &ce) {
		t.Fatalf("Build = %v, want a ChangeError", err)
	}
	return ce
}

func TestPostgresAlter(t *testing.T) {
	in := input(driver.Postgres, "PostgreSQL 16.4", TableChange{
		Comment: ptr("People's table"),
		NewName: ptr("people"),
		Columns: []ColumnChange{
			{Op: OpDrop, Name: "org_id"},
			{Op: OpAlter, Name: "email", NewName: "mail", Type: &TypeSpec{Text: "text"}, Using: ptr("email::text"), Nullable: ptr(true), Comment: ptr("")},
			{Op: OpAlter, Name: "name", Default: &DefaultSpec{Kind: DefaultValue, Text: `a\b`}, Nullable: ptr(false)},
			{Op: OpAlter, Name: "id", AutoIncrement: ptr(false)},
			{Op: OpAdd, Name: "created", Type: &TypeSpec{Text: "timestamptz"}, Default: &DefaultSpec{Kind: DefaultExpression, Text: "now()"}, Nullable: ptr(false), Comment: ptr("when")},
			{Op: OpAdd, Name: "total", Type: &TypeSpec{Text: "numeric(10,2)"}, Generated: &GeneratedSpec{Expr: "1 + 1", Stored: true}},
			{Op: OpAdd, Name: "seq", Type: &TypeSpec{Text: "bigint"}, AutoIncrement: ptr(true), Default: &DefaultSpec{Kind: DefaultExpression, Text: "lower('A')"}},
		},
		PrimaryKey:   &[]string{"id", "mail"},
		Indexes:      []IndexChange{{Op: OpDrop, Name: "users_email_key"}, {Op: OpDrop, Name: "users_name_idx"}, {Op: OpAdd, Name: "people_lower", Columns: []IndexColumn{{Expr: "lower(mail)"}, {Column: "id", Desc: true}}, Unique: true, Method: "BTREE", Predicate: "id > 0"}},
		ForeignKeys:  []FKChange{{Op: OpDrop, Name: "users_org_fk"}, {Op: OpAdd, Name: "fk2", Columns: []string{"id"}, RefTable: "orgs", RefColumns: []string{"id"}, OnDelete: "cascade", OnUpdate: "set  null"}},
		Checks:       []CheckChange{{Op: OpDrop, Name: "users_email_check"}, {Op: OpAdd, Name: "c2", Expression: "length(mail) > 3"}},
		DropTriggers: []string{"users_touch"},
	})
	in.Extra = Extra{PKName: "users_pkey", ConstraintIndexes: map[string]string{"users_email_key": "users_email_key"}}
	wantSQL(t, sqls(t, in),
		`ALTER TABLE "s"."users" DROP CONSTRAINT "users_org_fk"`,
		`ALTER TABLE "s"."users" DROP CONSTRAINT "users_email_check"`,
		`ALTER TABLE "s"."users" DROP CONSTRAINT "users_email_key"`,
		`DROP INDEX "s"."users_name_idx"`,
		`DROP TRIGGER "users_touch" ON "s"."users"`,
		`ALTER TABLE "s"."users" DROP CONSTRAINT "users_pkey"`,
		`ALTER TABLE "s"."users" DROP COLUMN "org_id"`,
		`ALTER TABLE "s"."users" ALTER COLUMN "email" TYPE text USING email::text`,
		`ALTER TABLE "s"."users" ALTER COLUMN "email" DROP NOT NULL`,
		`COMMENT ON COLUMN "s"."users"."email" IS NULL`,
		`ALTER TABLE "s"."users" RENAME COLUMN "email" TO "mail"`,
		`ALTER TABLE "s"."users" ALTER COLUMN "name" SET DEFAULT E'a\\b'`,
		`ALTER TABLE "s"."users" ALTER COLUMN "name" SET NOT NULL`,
		`ALTER TABLE "s"."users" ALTER COLUMN "id" DROP IDENTITY`,
		`ALTER TABLE "s"."users" ADD COLUMN "created" timestamptz DEFAULT now() NOT NULL`,
		`COMMENT ON COLUMN "s"."users"."created" IS 'when'`,
		`ALTER TABLE "s"."users" ADD COLUMN "total" numeric(10,2) GENERATED ALWAYS AS (1 + 1) STORED`,
		`ALTER TABLE "s"."users" ADD COLUMN "seq" bigint DEFAULT (lower('A')) GENERATED BY DEFAULT AS IDENTITY`,
		`ALTER TABLE "s"."users" ADD PRIMARY KEY ("id", "mail")`,
		`CREATE UNIQUE INDEX "people_lower" ON "s"."users" USING btree ((lower(mail)), "id" DESC) WHERE id > 0`,
		`ALTER TABLE "s"."users" ADD CONSTRAINT "fk2" FOREIGN KEY ("id") REFERENCES "s"."orgs" ("id") ON UPDATE SET NULL ON DELETE CASCADE`,
		`ALTER TABLE "s"."users" ADD CONSTRAINT "c2" CHECK (length(mail) > 3)`,
		`COMMENT ON TABLE "s"."users" IS 'People''s table'`,
		`ALTER TABLE "s"."users" RENAME TO "people"`,
	)

	// Serial (no identity): auto-increment off drops the default; identity on.
	in = input(driver.Postgres, "PostgreSQL 16", TableChange{Columns: []ColumnChange{
		{Op: OpAlter, Name: "id", AutoIncrement: ptr(false)},
		{Op: OpAlter, Name: "org_id", AutoIncrement: ptr(true), Default: &DefaultSpec{Kind: DefaultNone}},
	}, Indexes: []IndexChange{{Op: OpRename, Name: "users_name_idx", NewName: "n_idx"}}, PrimaryKey: &[]string{}})
	in.Current.Columns[0].Identity = ""
	wantSQL(t, sqls(t, in),
		`ALTER TABLE "s"."users" ALTER COLUMN "id" DROP DEFAULT`,
		`ALTER TABLE "s"."users" ALTER COLUMN "org_id" DROP DEFAULT`,
		`ALTER TABLE "s"."users" ALTER COLUMN "org_id" ADD GENERATED BY DEFAULT AS IDENTITY`,
		`ALTER INDEX "s"."users_name_idx" RENAME TO "n_idx"`,
	)
}

func TestPostgresCreate(t *testing.T) {
	in := input(driver.Postgres, "PostgreSQL 16", TableChange{
		Create: true, Schema: "s", Table: "orgs2", Comment: ptr("Orgs"),
		Columns: []ColumnChange{
			{Op: OpAdd, Name: "id", Type: &TypeSpec{Text: "bigint"}, AutoIncrement: ptr(true), Nullable: ptr(false)},
			{Op: OpAdd, Name: "code", Type: &TypeSpec{Text: "text"}, Comment: ptr("short")},
			{Op: OpAdd, Name: "parent", Type: &TypeSpec{Text: "bigint"}},
		},
		PrimaryKey:  &[]string{"id"},
		Indexes:     []IndexChange{{Op: OpAdd, Name: "orgs2_code", Columns: []IndexColumn{{Column: "code"}}}},
		ForeignKeys: []FKChange{{Op: OpAdd, Columns: []string{"parent"}, RefTable: "orgs2", RefColumns: []string{"id"}}},
		Checks:      []CheckChange{{Op: OpAdd, Expression: "code <> ''"}},
	})
	wantSQL(t, sqls(t, in),
		"CREATE TABLE \"s\".\"orgs2\" (\n  \"id\" bigint NOT NULL GENERATED BY DEFAULT AS IDENTITY,\n  \"code\" text,\n  \"parent\" bigint,\n  PRIMARY KEY (\"id\"),\n  FOREIGN KEY (\"parent\") REFERENCES \"s\".\"orgs2\" (\"id\"),\n  CHECK (code <> '')\n)",
		`CREATE INDEX "orgs2_code" ON "s"."orgs2" ("code")`,
		`COMMENT ON TABLE "s"."orgs2" IS 'Orgs'`,
		`COMMENT ON COLUMN "s"."orgs2"."code" IS 'short'`,
	)
}

func mysqlInput(version string, tc TableChange) Input {
	in := input(driver.MySQL, version, tc)
	in.Extra.MySQLColumns = map[string]MySQLColumn{
		"id":     {Type: "int unsigned", Extra: "auto_increment"},
		"email":  {Type: "varchar(255)", Collation: "utf8mb4_bin", Comment: "it's", Default: ptr(`a\b`)},
		"name":   {Type: "timestamp", Nullable: true, Default: ptr("CURRENT_TIMESTAMP"), Extra: "DEFAULT_GENERATED on update CURRENT_TIMESTAMP"},
		"org_id": {Type: "int", Nullable: true, Default: ptr(`concat(_utf8mb4\'a\',1)`), Extra: "DEFAULT_GENERATED INVISIBLE"},
	}
	return in
}

func TestMySQLAlter(t *testing.T) {
	in := mysqlInput("MySQL 8.0.36", TableChange{
		Comment: ptr(""), NewName: ptr("people"),
		Columns: []ColumnChange{
			{Op: OpAlter, Name: "email", NewName: "mail", Nullable: ptr(true)},
			{Op: OpAlter, Name: "name", Comment: ptr("n")},
			{Op: OpAlter, Name: "org_id", Type: &TypeSpec{Text: "bigint"}, Default: &DefaultSpec{Kind: DefaultNull}},
			{Op: OpAlter, Name: "id", AutoIncrement: ptr(false), Collation: ptr("")},
			{Op: OpAdd, Name: "kind", Type: &TypeSpec{Text: "enum", EnumValues: []string{"a", "b'c"}}, Nullable: ptr(false), Default: &DefaultSpec{Kind: DefaultValue, Text: "a"}, Collation: ptr("utf8mb4_bin")},
			{Op: OpAdd, Name: "g", Type: &TypeSpec{Text: "int"}, Generated: &GeneratedSpec{Expr: "id * 2"}},
		},
		PrimaryKey:   &[]string{"id"},
		Indexes:      []IndexChange{{Op: OpDrop, Name: "users_name_idx"}, {Op: OpAdd, Name: "ft", Columns: []IndexColumn{{Column: "mail", Length: 10}}, Method: "fulltext"}, {Op: OpAdd, Name: "u", Unique: true, Method: "hash", Columns: []IndexColumn{{Column: "kind", Desc: true}}}, {Op: OpRename, Name: "users_email_key", NewName: "mail_key"}},
		ForeignKeys:  []FKChange{{Op: OpDrop, Name: "users_org_fk"}, {Op: OpAdd, Name: "fk2", Columns: []string{"org_id"}, RefSchema: "other", RefTable: "orgs", RefColumns: []string{"code"}, OnDelete: "SET NULL"}},
		Checks:       []CheckChange{{Op: OpDrop, Name: "users_email_check"}, {Op: OpAdd, Name: "c2", Expression: "id > 0"}},
		DropTriggers: []string{"users_touch"},
	})
	wantSQL(t, sqls(t, in),
		"ALTER TABLE `s`.`users` DROP FOREIGN KEY `users_org_fk`",
		"DROP TRIGGER `s`.`users_touch`",
		"ALTER TABLE `s`.`users`\n  DROP CHECK `users_email_check`,\n  DROP INDEX `users_name_idx`,\n  DROP PRIMARY KEY,\n"+
			"  CHANGE COLUMN `email` `mail` varchar(255) COLLATE utf8mb4_bin NULL DEFAULT 'a\\\\b' COMMENT 'it''s',\n"+
			"  MODIFY COLUMN `name` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT 'n',\n"+
			"  MODIFY COLUMN `org_id` bigint NULL DEFAULT NULL INVISIBLE,\n"+
			"  MODIFY COLUMN `id` int unsigned NOT NULL,\n"+
			"  ADD COLUMN `kind` enum('a','b''c') COLLATE utf8mb4_bin NOT NULL DEFAULT 'a',\n"+
			"  ADD COLUMN `g` int GENERATED ALWAYS AS (id * 2) VIRTUAL NULL,\n"+
			"  ADD PRIMARY KEY (`id`),\n"+
			"  ADD FULLTEXT INDEX `ft` (`mail`(10)),\n"+
			"  ADD UNIQUE INDEX `u` (`kind` DESC) USING HASH,\n"+
			"  RENAME INDEX `users_email_key` TO `mail_key`,\n"+
			"  ADD CONSTRAINT `c2` CHECK (id > 0),\n"+
			"  COMMENT = ''",
		"ALTER TABLE `s`.`users` ADD CONSTRAINT `fk2` FOREIGN KEY (`org_id`) REFERENCES `other`.`orgs` (`code`) ON DELETE SET NULL",
		"RENAME TABLE `s`.`users` TO `s`.`people`",
	)
}

func TestMySQLRawDefaults(t *testing.T) {
	in := mysqlInput("MySQL 8.0.36", TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "org_id", Comment: ptr("")}}})
	got := sqls(t, in)
	wantSQL(t, got, "ALTER TABLE `s`.`users`\n  MODIFY COLUMN `org_id` int NULL DEFAULT (concat(_utf8mb4'a',1)) INVISIBLE")

	// NO_BACKSLASH_ESCAPES keeps backslashes.
	in = mysqlInput("MySQL 8.0.36", TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "email", Nullable: ptr(false)}}})
	in.Lit.NoBackslashEscapes = true
	wantSQL(t, sqls(t, in), "ALTER TABLE `s`.`users`\n  MODIFY COLUMN `email` varchar(255) COLLATE utf8mb4_bin NOT NULL DEFAULT 'a\\b' COMMENT 'it''s'")

	// MariaDB reports SQL defaults.
	p := &plan{f: MariaDB, in: Input{Lit: Literals{Flavor: MariaDB}}}
	for raw, want := range map[string]string{"NULL": "NULL", "'x'": "'x'", "12.5": "12.5", "current_timestamp()": "current_timestamp()", "b'1'": "b'1'", "uuid()": "(uuid())"} {
		if got := p.mysqlRawDefault(MySQLColumn{Default: ptr(raw)}); got != want {
			t.Errorf("MariaDB default %q = %q, want %q", raw, got, want)
		}
	}
	p.f = MySQL
	for _, c := range []struct {
		col  MySQLColumn
		want string
	}{
		{MySQLColumn{}, ""},
		{MySQLColumn{Default: ptr("CURRENT_TIMESTAMP"), Type: "datetime"}, "CURRENT_TIMESTAMP"},
		{MySQLColumn{Default: ptr("b'101'"), Type: "bit(3)"}, "b'101'"},
		{MySQLColumn{Default: ptr("b'101'"), Type: "varchar(5)"}, "'b''101'''"},
		{MySQLColumn{Default: ptr("0"), Type: "int"}, "'0'"},
	} {
		if got := p.mysqlRawDefault(c.col); got != c.want {
			t.Errorf("MySQL default %+v = %q, want %q", c.col, got, c.want)
		}
	}
	// Generated columns are restated.
	in = mysqlInput("MariaDB 11.4.2", TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "name", Comment: ptr("")}}})
	in.Kind = driver.MariaDB
	in.Extra.MySQLColumns["name"] = MySQLColumn{Type: "int", Nullable: true, GenExpr: `concat(\'a\')`, Extra: "STORED GENERATED"}
	wantSQL(t, sqls(t, in), "ALTER TABLE `s`.`users`\n  MODIFY COLUMN `name` int GENERATED ALWAYS AS (concat('a')) STORED")
	// MariaDB drops checks with DROP CONSTRAINT; a missing raw column is reported.
	in = mysqlInput("MariaDB 11.4.2", TableChange{Checks: []CheckChange{{Op: OpDrop, Name: "users_email_check"}}, Columns: []ColumnChange{{Op: OpAlter, Name: "email", Comment: ptr("")}}})
	delete(in.Extra.MySQLColumns, "email")
	if ce := failure(t, in); ce.Kind != FailNotFound {
		t.Fatalf("missing raw column: %+v", ce)
	}
}

func TestMySQLCreate(t *testing.T) {
	in := input(driver.MySQL, "MySQL 8.0.36", TableChange{
		Create: true, Schema: "s", Table: "t", Comment: ptr("T"),
		Columns: []ColumnChange{
			{Op: OpAdd, Name: "id", Type: &TypeSpec{Text: "int"}, AutoIncrement: ptr(true), Nullable: ptr(false), Comment: ptr("key")},
			{Op: OpAdd, Name: "org", Type: &TypeSpec{Text: "int"}},
		},
		PrimaryKey:  &[]string{"id"},
		Indexes:     []IndexChange{{Op: OpAdd, Name: "i", Columns: []IndexColumn{{Column: "org"}}, Method: "BTREE"}},
		ForeignKeys: []FKChange{{Op: OpAdd, Name: "f", Columns: []string{"org"}, RefTable: "orgs", RefColumns: []string{"id"}}},
		Checks:      []CheckChange{{Op: OpAdd, Name: "c", Expression: "org > 0"}},
	})
	wantSQL(t, sqls(t, in),
		"CREATE TABLE `s`.`t` (\n  `id` int NOT NULL AUTO_INCREMENT COMMENT 'key',\n  `org` int NULL,\n  PRIMARY KEY (`id`),\n  INDEX `i` (`org`) USING BTREE,\n"+
			"  CONSTRAINT `f` FOREIGN KEY (`org`) REFERENCES `s`.`orgs` (`id`),\n  CONSTRAINT `c` CHECK (org > 0)\n) COMMENT='T'")
}

func TestSQLite(t *testing.T) {
	in := input(driver.SQLite, "SQLite 3.46.0", TableChange{
		NewName: ptr("people"),
		Columns: []ColumnChange{
			{Op: OpDrop, Name: "org_id"},
			{Op: OpAlter, Name: "email", NewName: "mail"},
			{Op: OpAdd, Name: "age", Type: &TypeSpec{Text: "INTEGER"}, Nullable: ptr(false), Default: &DefaultSpec{Kind: DefaultValue, Text: "0"}},
			{Op: OpAdd, Name: "v", Type: &TypeSpec{Text: "TEXT"}, Generated: &GeneratedSpec{Expr: "upper(name)"}},
		},
		Indexes:      []IndexChange{{Op: OpDrop, Name: "users_name_idx"}, {Op: OpAdd, Name: "age_idx", Columns: []IndexColumn{{Column: "age"}}, Predicate: "age > 0", Unique: true}},
		DropTriggers: []string{"users_touch"},
	})
	wantSQL(t, sqls(t, in),
		`DROP INDEX "s"."users_name_idx"`,
		`DROP TRIGGER "s"."users_touch"`,
		`ALTER TABLE "s"."users" DROP COLUMN "org_id"`,
		`ALTER TABLE "s"."users" RENAME COLUMN "email" TO "mail"`,
		`ALTER TABLE "s"."users" ADD COLUMN "age" INTEGER DEFAULT '0' NOT NULL`,
		`ALTER TABLE "s"."users" ADD COLUMN "v" TEXT GENERATED ALWAYS AS (upper(name)) VIRTUAL`,
		`CREATE UNIQUE INDEX "s"."age_idx" ON "users" ("age") WHERE age > 0`,
		`ALTER TABLE "s"."users" RENAME TO "people"`,
	)

	create := input(driver.SQLite, "SQLite 3.46.0", TableChange{
		Create: true, Schema: "main", Table: "t",
		Columns: []ColumnChange{
			{Op: OpAdd, Name: "id", Type: &TypeSpec{Text: "INTEGER"}, AutoIncrement: ptr(true)},
			{Op: OpAdd, Name: "p", Type: &TypeSpec{Text: "INTEGER"}, Default: &DefaultSpec{Kind: DefaultExpression, Text: "CURRENT_TIMESTAMP"}},
			{Op: OpAdd, Name: "s", Type: &TypeSpec{Text: "INTEGER"}, Generated: &GeneratedSpec{Expr: "p + 1", Stored: true}},
		},
		PrimaryKey:  &[]string{"id"},
		ForeignKeys: []FKChange{{Op: OpAdd, Columns: []string{"p"}, RefTable: "t", RefColumns: []string{"id"}, OnDelete: "CASCADE"}},
		Checks:      []CheckChange{{Op: OpAdd, Name: "c", Expression: "p > 0"}},
		Indexes:     []IndexChange{{Op: OpAdd, Name: "pi", Columns: []IndexColumn{{Expr: "p + 1"}}}},
	})
	wantSQL(t, sqls(t, create),
		"CREATE TABLE \"main\".\"t\" (\n  \"id\" INTEGER PRIMARY KEY AUTOINCREMENT,\n  \"p\" INTEGER DEFAULT CURRENT_TIMESTAMP,\n  \"s\" INTEGER GENERATED ALWAYS AS (p + 1) STORED,\n"+
			"  FOREIGN KEY (\"p\") REFERENCES \"t\" (\"id\") ON DELETE CASCADE,\n  CONSTRAINT \"c\" CHECK (p > 0)\n)",
		`CREATE INDEX "main"."pi" ON "t" ((p + 1))`,
	)
	// A composite key without auto-increment; AUTOINCREMENT on a non-key is refused.
	create.Change.Columns[0].AutoIncrement = nil
	if got := sqls(t, create); !strings.Contains(got[0], `PRIMARY KEY ("id")`) {
		t.Fatalf("pk: %s", got[0])
	}
	create.Change.Columns[1].AutoIncrement = ptr(true)
	if ce := failure(t, create); ce.Field != "autoIncrement" {
		t.Fatalf("autoincrement: %+v", ce)
	}
}

func TestValidation(t *testing.T) {
	pg := func(tc TableChange) Input { return input(driver.Postgres, "PostgreSQL 16", tc) }
	lite := func(tc TableChange) Input { return input(driver.SQLite, "SQLite 3.46.0", tc) }
	my := func(v string, tc TableChange) Input { return mysqlInput(v, tc) }
	add := func(c ColumnChange) TableChange { c.Op = OpAdd; return TableChange{Columns: []ColumnChange{c}} }
	text := &TypeSpec{Text: "text"}
	long := strings.Repeat("x", 64)

	cases := []struct {
		name  string
		in    Input
		kind  string
		field string
	}{
		{"no changes", pg(TableChange{}), FailInvalid, ""},
		{"view", func() Input { in := pg(TableChange{Comment: ptr("")}); in.Current.Type = driver.TypeView; return in }(), FailUnsupported, ""},
		{"missing table", func() Input { in := pg(TableChange{Comment: ptr("")}); in.Current = nil; return in }(), FailNotFound, ""},
		{"empty name", pg(add(ColumnChange{Name: " ", Type: text})), FailInvalid, "name"},
		{"NUL name", pg(add(ColumnChange{Name: "a\x00", Type: text})), FailInvalid, "name"},
		{"bad utf8 name", pg(add(ColumnChange{Name: "a\xff", Type: text})), FailInvalid, "name"},
		{"long pg name", pg(add(ColumnChange{Name: long, Type: text})), FailInvalid, "name"},
		{"long mysql name", my("MySQL 8.0.36", add(ColumnChange{Name: long + "y", Type: text})), FailInvalid, "name"},
		{"existing name", pg(add(ColumnChange{Name: "email", Type: text})), FailInvalid, "name"},
		{"duplicate new names", pg(TableChange{Columns: []ColumnChange{{Op: OpAdd, Name: "a", Type: text}, {Op: OpAdd, Name: "a", Type: text}}}), FailInvalid, "name"},
		{"no type", pg(add(ColumnChange{Name: "a"})), FailInvalid, "type"},
		{"empty type", pg(add(ColumnChange{Name: "a", Type: &TypeSpec{Text: " "}})), FailInvalid, "type"},
		{"injected type", pg(add(ColumnChange{Name: "a", Type: &TypeSpec{Text: "int; DROP TABLE x"}})), FailInvalid, "type"},
		{"comment type", pg(add(ColumnChange{Name: "a", Type: &TypeSpec{Text: "int -- x"}})), FailInvalid, "type"},
		{"unbalanced type", pg(add(ColumnChange{Name: "a", Type: &TypeSpec{Text: "numeric(10"}})), FailInvalid, "type"},
		{"closing first type", pg(add(ColumnChange{Name: "a", Type: &TypeSpec{Text: "numeric)10("}})), FailInvalid, "type"},
		{"quoted type", pg(add(ColumnChange{Name: "a", Type: &TypeSpec{Text: "enum('a')"}})), FailInvalid, "type"},
		{"odd quotes type", pg(add(ColumnChange{Name: "a", Type: &TypeSpec{Text: `"char`}})), FailInvalid, "type"},
		{"empty enum", my("MySQL 8.0.36", add(ColumnChange{Name: "a", Type: &TypeSpec{Text: "enum"}})), FailInvalid, "type"},
		{"NUL enum", my("MySQL 8.0.36", add(ColumnChange{Name: "a", Type: &TypeSpec{Text: "set", EnumValues: []string{"\x00"}}})), FailInvalid, "type"},
		{"using on add", pg(add(ColumnChange{Name: "a", Type: text, Using: ptr("1")})), FailInvalid, "using"},
		{"bad default kind", pg(add(ColumnChange{Name: "a", Type: text, Default: &DefaultSpec{Kind: "x"}})), FailInvalid, "default"},
		{"NUL default", pg(add(ColumnChange{Name: "a", Type: text, Default: &DefaultSpec{Kind: DefaultValue, Text: "\x00"}})), FailInvalid, "default"},
		{"bad utf8 default", pg(add(ColumnChange{Name: "a", Type: text, Default: &DefaultSpec{Kind: DefaultValue, Text: "\xff"}})), FailInvalid, "default"},
		{"two statements", pg(add(ColumnChange{Name: "a", Type: text, Default: &DefaultSpec{Kind: DefaultExpression, Text: "1; DROP TABLE t"}})), FailInvalid, "default"},
		{"delimiter", pg(add(ColumnChange{Name: "a", Type: text, Default: &DefaultSpec{Kind: DefaultExpression, Text: "1;"}})), FailInvalid, "default"},
		{"trailing comment", pg(add(ColumnChange{Name: "a", Type: text, Default: &DefaultSpec{Kind: DefaultExpression, Text: "1 -- x"}})), FailInvalid, "default"},
		{"empty expression", pg(add(ColumnChange{Name: "a", Type: text, Default: &DefaultSpec{Kind: DefaultExpression, Text: " "}})), FailInvalid, "default"},
		{"unbalanced expression", pg(add(ColumnChange{Name: "a", Type: text, Default: &DefaultSpec{Kind: DefaultExpression, Text: "f(1"}})), FailInvalid, "default"},
		{"bad using", pg(TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "name", Type: text, Using: ptr("x;y")}}}), FailInvalid, "using"},
		{"using without type", pg(TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "name", Using: ptr("x")}}}), FailInvalid, "using"},
		{"bad generated", pg(add(ColumnChange{Name: "a", Type: text, Generated: &GeneratedSpec{Expr: "", Stored: true}})), FailInvalid, "generated"},
		{"virtual on pg", pg(add(ColumnChange{Name: "a", Type: text, Generated: &GeneratedSpec{Expr: "1"}})), FailUnsupported, "generated"},
		{"generated default", pg(add(ColumnChange{Name: "a", Type: text, Generated: &GeneratedSpec{Expr: "1", Stored: true}, Default: &DefaultSpec{Kind: DefaultNull}})), FailInvalid, "default"},
		{"make generated", pg(TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "name", Generated: &GeneratedSpec{Expr: "1"}}}}), FailInvalid, "generated"},
		{"bad comment", pg(TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "name", Comment: ptr("\x00")}}}), FailInvalid, "comment"},
		{"bad table comment", pg(TableChange{Comment: ptr("\x00")}), FailInvalid, "comment"},
		{"collation on pg", pg(TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "name", Collation: ptr("C")}}}), FailUnsupported, "collation"},
		{"bad collation", my("MySQL 8.0.36", TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "name", Collation: ptr("x y")}}}), FailInvalid, "collation"},
		{"unknown column", pg(TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "nope", Nullable: ptr(true)}}}), FailNotFound, "name"},
		{"changed twice", pg(TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "name", Nullable: ptr(true)}, {Op: OpDrop, Name: "name"}}}), FailInvalid, "name"},
		{"unknown op", pg(TableChange{Columns: []ColumnChange{{Op: "x", Name: "name"}}}), FailInvalid, ""},
		{"rename onto existing", pg(TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "name", NewName: "email"}}}), FailInvalid, "name"},
		{"bad rename", pg(TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "name", NewName: "a\x00"}}}), FailInvalid, "name"},
		{"drop all", pg(TableChange{Columns: []ColumnChange{{Op: OpDrop, Name: "id"}, {Op: OpDrop, Name: "email"}, {Op: OpDrop, Name: "name"}, {Op: OpDrop, Name: "org_id"}}}), FailInvalid, ""},
		{"sqlite alter", lite(TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "name", Nullable: ptr(true)}}}), FailUnsupported, ""},
		{"sqlite comment", lite(TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "name", Comment: ptr("x")}}}), FailUnsupported, "comment"},
		{"sqlite table comment", lite(TableChange{Comment: ptr("x")}), FailUnsupported, "comment"},
		{"old sqlite drop", input(driver.SQLite, "SQLite 3.30.0", TableChange{Columns: []ColumnChange{{Op: OpDrop, Name: "name"}}}), FailUnsupported, ""},
		{"old sqlite rename", input(driver.SQLite, "SQLite 3.20.0", TableChange{Columns: []ColumnChange{{Op: OpAlter, Name: "name", NewName: "n"}}}), FailUnsupported, "name"},
		{"sqlite add autoinc", lite(add(ColumnChange{Name: "a", Type: text, AutoIncrement: ptr(true)})), FailUnsupported, "autoIncrement"},
		{"sqlite add stored", lite(add(ColumnChange{Name: "a", Type: text, Generated: &GeneratedSpec{Expr: "1", Stored: true}})), FailUnsupported, "generated"},
		{"sqlite add expr default", lite(add(ColumnChange{Name: "a", Type: text, Default: &DefaultSpec{Kind: DefaultExpression, Text: "CURRENT_TIMESTAMP"}})), FailUnsupported, "default"},
		{"sqlite not null no default", lite(add(ColumnChange{Name: "a", Type: text, Nullable: ptr(false)})), FailInvalid, "default"},
		{"sqlite pk", lite(TableChange{PrimaryKey: &[]string{"id"}}), FailUnsupported, ""},
		{"sqlite fk", lite(TableChange{ForeignKeys: []FKChange{{Op: OpDrop, Name: "users_org_fk"}}}), FailUnsupported, ""},
		{"sqlite check", lite(TableChange{Checks: []CheckChange{{Op: OpDrop, Name: "users_email_check"}}}), FailUnsupported, ""},
		{"sqlite rename index", lite(TableChange{Indexes: []IndexChange{{Op: OpRename, Name: "users_name_idx", NewName: "x"}}}), FailUnsupported, "name"},
		{"sqlite fk other schema", input(driver.SQLite, "SQLite 3.46.0", TableChange{Create: true, Schema: "main", Table: "t", Columns: []ColumnChange{{Op: OpAdd, Name: "a", Type: text}}, ForeignKeys: []FKChange{{Op: OpAdd, Columns: []string{"a"}, RefSchema: "aux", RefTable: "orgs", RefColumns: []string{"id"}}}}), FailUnsupported, "refTable"},
		{"pk unknown column", pg(TableChange{PrimaryKey: &[]string{"nope"}}), FailNotFound, "columns"},
		{"pk twice", pg(TableChange{PrimaryKey: &[]string{"id", "id"}}), FailInvalid, "columns"},
		{"pk on dropped", pg(TableChange{PrimaryKey: &[]string{"name"}, Columns: []ColumnChange{{Op: OpDrop, Name: "name"}}}), FailNotFound, "columns"},
		{"index unknown", pg(TableChange{Indexes: []IndexChange{{Op: OpDrop, Name: "nope"}}}), FailNotFound, "name"},
		{"index primary", pg(TableChange{Indexes: []IndexChange{{Op: OpDrop, Name: "users_pkey"}}}), FailInvalid, "name"},
		{"index exists", pg(TableChange{Indexes: []IndexChange{{Op: OpAdd, Name: "users_name_idx", Columns: []IndexColumn{{Column: "id"}}}}}), FailInvalid, "name"},
		{"rename index onto existing", pg(TableChange{Indexes: []IndexChange{{Op: OpRename, Name: "users_name_idx", NewName: "users_email_key"}}}), FailInvalid, "name"},
		{"rename index bad name", pg(TableChange{Indexes: []IndexChange{{Op: OpRename, Name: "users_name_idx", NewName: ""}}}), FailInvalid, "name"},
		{"index no name", pg(TableChange{Indexes: []IndexChange{{Op: OpAdd, Columns: []IndexColumn{{Column: "id"}}}}}), FailInvalid, "name"},
		{"index no columns", pg(TableChange{Indexes: []IndexChange{{Op: OpAdd, Name: "i"}}}), FailInvalid, "columns"},
		{"index unknown op", pg(TableChange{Indexes: []IndexChange{{Op: "x", Name: "i"}}}), FailInvalid, ""},
		{"index bad method", pg(TableChange{Indexes: []IndexChange{{Op: OpAdd, Name: "i", Method: "fulltext", Columns: []IndexColumn{{Column: "id"}}}}}), FailInvalid, "method"},
		{"unique fulltext", my("MySQL 8.0.36", TableChange{Indexes: []IndexChange{{Op: OpAdd, Name: "i", Unique: true, Method: "FULLTEXT", Columns: []IndexColumn{{Column: "name"}}}}}), FailInvalid, "unique"},
		{"index both", pg(TableChange{Indexes: []IndexChange{{Op: OpAdd, Name: "i", Columns: []IndexColumn{{Column: "id", Expr: "1"}}}}}), FailInvalid, "columns"},
		{"index unknown column", pg(TableChange{Indexes: []IndexChange{{Op: OpAdd, Name: "i", Columns: []IndexColumn{{Column: "nope"}}}}}), FailNotFound, "columns"},
		{"index bad expr", pg(TableChange{Indexes: []IndexChange{{Op: OpAdd, Name: "i", Columns: []IndexColumn{{Expr: "x; y"}}}}}), FailInvalid, "columns"},
		{"old mysql expr index", my("MySQL 8.0.12", TableChange{Indexes: []IndexChange{{Op: OpAdd, Name: "i", Columns: []IndexColumn{{Expr: "x"}}}}}), FailUnsupported, "columns"},
		{"prefix on pg", pg(TableChange{Indexes: []IndexChange{{Op: OpAdd, Name: "i", Columns: []IndexColumn{{Column: "id", Length: 3}}}}}), FailInvalid, "columns"},
		{"partial on mysql", my("MySQL 8.0.36", TableChange{Indexes: []IndexChange{{Op: OpAdd, Name: "i", Predicate: "x", Columns: []IndexColumn{{Column: "id"}}}}}), FailUnsupported, "predicate"},
		{"bad predicate", pg(TableChange{Indexes: []IndexChange{{Op: OpAdd, Name: "i", Predicate: "x)", Columns: []IndexColumn{{Column: "id"}}}}}), FailInvalid, "predicate"},
		{"old mariadb rename index", my("MariaDB 10.4.0", TableChange{Indexes: []IndexChange{{Op: OpRename, Name: "users_name_idx", NewName: "x"}}}), FailUnsupported, "name"},
		{"create drops index", input(driver.Postgres, "16", TableChange{Create: true, Table: "t", Columns: []ColumnChange{{Op: OpAdd, Name: "a", Type: text}}, Indexes: []IndexChange{{Op: OpDrop, Name: "x"}}}), FailInvalid, ""},
		{"fk unknown", pg(TableChange{ForeignKeys: []FKChange{{Op: OpDrop, Name: "nope"}}}), FailNotFound, "name"},
		{"fk unknown op", pg(TableChange{ForeignKeys: []FKChange{{Op: "x"}}}), FailInvalid, ""},
		{"fk bad name", pg(TableChange{ForeignKeys: []FKChange{{Op: OpAdd, Name: "\x00", Columns: []string{"id"}, RefTable: "orgs", RefColumns: []string{"id"}}}}), FailInvalid, "name"},
		{"fk no columns", pg(TableChange{ForeignKeys: []FKChange{{Op: OpAdd, RefTable: "orgs"}}}), FailInvalid, "columns"},
		{"fk no table", pg(TableChange{ForeignKeys: []FKChange{{Op: OpAdd, Columns: []string{"id"}}}}), FailInvalid, "refTable"},
		{"fk count", pg(TableChange{ForeignKeys: []FKChange{{Op: OpAdd, Columns: []string{"id"}, RefTable: "orgs"}}}), FailInvalid, "refColumns"},
		{"fk missing table", pg(TableChange{ForeignKeys: []FKChange{{Op: OpAdd, Columns: []string{"id"}, RefTable: "nope", RefColumns: []string{"id"}}}}), FailNotFound, "refTable"},
		{"fk missing ref column", pg(TableChange{ForeignKeys: []FKChange{{Op: OpAdd, Columns: []string{"id"}, RefTable: "orgs", RefColumns: []string{"nope"}}}}), FailNotFound, "refColumns"},
		{"fk self bad column", pg(TableChange{ForeignKeys: []FKChange{{Op: OpAdd, Columns: []string{"id"}, RefTable: "users", RefColumns: []string{"nope"}}}}), FailNotFound, "refColumns"},
		{"fk bad action", pg(TableChange{ForeignKeys: []FKChange{{Op: OpAdd, Columns: []string{"id"}, RefTable: "orgs", RefColumns: []string{"id"}, OnUpdate: "explode"}}}), FailInvalid, "onUpdate"},
		{"fk bad delete action", pg(TableChange{ForeignKeys: []FKChange{{Op: OpAdd, Columns: []string{"id"}, RefTable: "orgs", RefColumns: []string{"id"}, OnDelete: "x"}}}), FailInvalid, "onDelete"},
		{"create drops fk", input(driver.Postgres, "16", TableChange{Create: true, Table: "t", Columns: []ColumnChange{{Op: OpAdd, Name: "a", Type: text}}, ForeignKeys: []FKChange{{Op: OpDrop, Name: "x"}}}), FailInvalid, ""},
		{"check unknown", pg(TableChange{Checks: []CheckChange{{Op: OpDrop, Name: "nope"}}}), FailNotFound, "name"},
		{"check unknown op", pg(TableChange{Checks: []CheckChange{{Op: "x"}}}), FailInvalid, ""},
		{"check bad name", pg(TableChange{Checks: []CheckChange{{Op: OpAdd, Name: "\x00", Expression: "1"}}}), FailInvalid, "name"},
		{"check bad expr", pg(TableChange{Checks: []CheckChange{{Op: OpAdd, Expression: ""}}}), FailInvalid, "expression"},
		{"old mysql check", my("MySQL 8.0.15", TableChange{Checks: []CheckChange{{Op: OpAdd, Expression: "1"}}}), FailUnsupported, ""},
		{"old mariadb check", my("MariaDB 10.1.0", TableChange{Checks: []CheckChange{{Op: OpAdd, Expression: "1"}}}), FailUnsupported, ""},
		{"create drops check", input(driver.Postgres, "16", TableChange{Create: true, Table: "t", Columns: []ColumnChange{{Op: OpAdd, Name: "a", Type: text}}, Checks: []CheckChange{{Op: OpDrop, Name: "x"}}}), FailInvalid, ""},
		{"trigger unknown", pg(TableChange{DropTriggers: []string{"nope"}}), FailNotFound, "name"},
		{"rename table bad", pg(TableChange{NewName: ptr("")}), FailInvalid, "name"},
		{"create bad name", input(driver.Postgres, "16", TableChange{Create: true, Table: "", Columns: []ColumnChange{{Op: OpAdd, Name: "a", Type: text}}}), FailInvalid, "name"},
		{"create no columns", input(driver.Postgres, "16", TableChange{Create: true, Table: "t"}), FailInvalid, ""},
		{"create with rename", input(driver.Postgres, "16", TableChange{Create: true, Table: "t", NewName: ptr("x"), Columns: []ColumnChange{{Op: OpAdd, Name: "a", Type: text}}}), FailInvalid, ""},
		{"create alters", input(driver.Postgres, "16", TableChange{Create: true, Table: "t", Columns: []ColumnChange{{Op: OpAlter, Name: "a"}}}), FailInvalid, ""},
		{"create empty pk", input(driver.Postgres, "16", TableChange{Create: true, Table: "t", Columns: []ColumnChange{{Op: OpAdd, Name: "a", Type: text}}, PrimaryKey: &[]string{}}), FailInvalid, "columns"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ce := failure(t, c.in)
			if ce.Kind != c.kind || ce.Field != c.field || ce.Message == "" {
				t.Fatalf("got %+v, want kind %s field %q", ce, c.kind, c.field)
			}
			if ce.Error() != ce.Message || ce.Unwrap() != nil {
				t.Fatal("Error/Unwrap")
			}
		})
	}
}

func TestCapabilities(t *testing.T) {
	if c := Capabilities(driver.SQLite, "SQLite 3.46.0"); c.AlterColumn || !c.DropColumn || !c.RenameColumn || c.Reason == "" || !c.TransactionalDDL {
		t.Fatalf("sqlite caps = %+v", c)
	}
	if c := Capabilities(driver.MySQL, "MariaDB 10.6.1"); c.Flavor != "mariadb" || !c.Checks || !c.RenameIndex {
		t.Fatalf("mariadb on mysql kind = %+v", c)
	}
	if c := Capabilities(driver.MariaDB, "MySQL 8.0.36"); c.Flavor != "mysql" || !c.Checks || !c.ExpressionIndexes {
		t.Fatalf("mysql on mariadb kind = %+v", c)
	}
	if c := Capabilities(driver.MariaDB, "11.0"); c.Flavor != "mariadb" {
		t.Fatalf("mariadb default = %+v", c)
	}
	if c := Capabilities(driver.MySQL, "MySQL 5.6.0"); c.Checks || c.RenameIndex || c.Reason == "" {
		t.Fatalf("old mysql = %+v", c)
	}
	if c := Capabilities(driver.Postgres, "PostgreSQL 16"); !c.AlterColumn || !c.DropCascade || c.MaxNameLength != 63 {
		t.Fatalf("pg = %+v", c)
	}
	if v := parseVersion("no version"); v != [3]int{} {
		t.Fatalf("parseVersion = %v", v)
	}
	if !atLeast([3]int{8, 0, 16}, 8, 0, 16) || atLeast([3]int{8, 0, 15}, 8, 0, 16) || !atLeast([3]int{9}, 8, 5, 0) || atLeast([3]int{7, 9, 9}, 8, 0, 0) {
		t.Fatal("atLeast")
	}
}

func TestLiterals(t *testing.T) {
	for _, c := range []struct {
		l    Literals
		in   string
		want string
	}{
		{Literals{Flavor: Postgres}, "it's", "'it''s'"},
		{Literals{Flavor: Postgres}, `a\'b`, `E'a\\''b'`},
		{Literals{Flavor: MySQL}, `a\'b`, `'a\\''b'`},
		{Literals{Flavor: MariaDB, NoBackslashEscapes: true}, `a\'b`, `'a\''b'`},
		{Literals{Flavor: SQLite}, `a\'b`, `'a\''b'`},
	} {
		if got, err := c.l.quote(c.in); err != nil || got != c.want {
			t.Errorf("%+v quote(%q) = %q %v, want %q", c.l, c.in, got, err, c.want)
		}
	}
}

func TestFingerprint(t *testing.T) {
	a, b := fixture(), fixture()
	if Fingerprint(a) != Fingerprint(b) || Fingerprint(nil) != "" {
		t.Fatal("same structure, different fingerprints")
	}
	b.EstimatedRows = ptr(int64(5))
	b.Indexes[0].Columns = []string{"id"}
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("estimates must not count")
	}
	// nil and empty slices are the same.
	a.Columns[0].EnumValues = nil
	b.Columns[0].EnumValues = []string{}
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("nil vs empty")
	}
	b.Columns[1].Default = ptr("x")
	if Fingerprint(a) == Fingerprint(b) {
		t.Fatal("a changed default must count")
	}
}

func TestEmpty(t *testing.T) {
	if !(TableChange{}).Empty() || (TableChange{Create: true}).Empty() || (TableChange{DropTriggers: []string{"x"}}).Empty() {
		t.Fatal("Empty")
	}
}
