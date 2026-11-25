package ddl

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"lontar/internal/driver"
	"lontar/internal/driver/mysql"
	"lontar/internal/driver/postgres"
	"lontar/internal/driver/sqlite"
	"lontar/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

type env struct {
	name    string
	drv     driver.Driver
	db      *sql.DB
	schema  string
	opts    Options
	caps    Caps
	created []string
}

func forEach(t *testing.T, fn func(t *testing.T, v *env)) {
	pg, my, ma, lite := postgres.New(), mysql.New(), mysql.NewMariaDB(), sqlite.New()
	for _, e := range []struct {
		name  string
		drv   driver.Driver
		start func(testing.TB) testutil.DB
	}{
		{"sqlite", lite, testutil.SQLiteTemp},
		{"postgres", pg, testutil.SharedPostgres},
		{"mysql", my, testutil.SharedMySQL},
		{"mariadb", ma, testutil.SharedMariaDB},
	} {
		t.Run(e.name, func(t *testing.T) {
			ctx := context.Background()
			db := testutil.OpenDriver(t, e.drv, e.start(t), false)
			schema, err := e.drv.CurrentSchema(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			version, err := e.drv.ServerVersion(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			v := &env{name: e.name, drv: e.drv, db: db, schema: schema, opts: Options{ServerVersion: version}, caps: Capabilities(e.drv.Kind(), version)}
			t.Cleanup(func() {
				for i := len(v.created) - 1; i >= 0; i-- {
					_, _ = db.Exec("DROP TABLE IF EXISTS " + e.drv.QuoteIdent(schema) + "." + e.drv.QuoteIdent(v.created[i]))
				}
			})
			fn(t, v)
		})
	}
}

func (v *env) info(t *testing.T, table string) *driver.TableDetail {
	t.Helper()
	d, err := v.drv.TableInfo(context.Background(), v.db, v.schema, table)
	if err != nil {
		t.Fatalf("TableInfo(%s): %v", table, err)
	}
	return d
}

func (v *env) apply(t *testing.T, tc TableChange) *Result {
	t.Helper()
	if tc.Schema == "" {
		tc.Schema = v.schema
	}
	if tc.Create {
		v.created = append(v.created, tc.Table)
	} else if tc.Base == nil {
		tc.Base = v.info(t, tc.Table)
	}
	if tc.NewName != nil {
		v.created = append(v.created, *tc.NewName)
	}
	res, err := Apply(context.Background(), v.db, v.drv, tc, v.opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return res
}

func (v *env) applyErr(t *testing.T, tc TableChange) *ChangeError {
	t.Helper()
	if tc.Schema == "" {
		tc.Schema = v.schema
	}
	if !tc.Create && tc.Base == nil {
		tc.Base = v.info(t, tc.Table)
	}
	_, err := Apply(context.Background(), v.db, v.drv, tc, v.opts)
	var ce *ChangeError
	if !errors.As(err, &ce) {
		t.Fatalf("Apply = %v, want a ChangeError", err)
	}
	return ce
}

func col(d *driver.TableDetail, name string) *driver.ColumnDetail {
	for i := range d.Columns {
		if d.Columns[i].Name == name {
			return &d.Columns[i]
		}
	}
	return nil
}

func colNames(d *driver.TableDetail) []string {
	var out []string
	for _, c := range d.Columns {
		out = append(out, c.Name)
	}
	return out
}

func indexNames(d *driver.TableDetail) []string {
	var out []string
	for _, i := range d.Indexes {
		if !i.Primary {
			out = append(out, i.Name)
		}
	}
	slices.Sort(out)
	return out
}

func intType(v *env) string {
	if v.name == "sqlite" {
		return "INTEGER"
	}
	return "integer"
}

func TestIntegrationCreateAndAlter(t *testing.T) {
	forEach(t, func(t *testing.T, v *env) {
		it := &TypeSpec{Text: intType(v)}
		text := &TypeSpec{Text: "varchar(100)"}
		parent := TableChange{Create: true, Table: "ddl_parent", Columns: []ColumnChange{
			{Op: OpAdd, Name: "id", Type: it, Nullable: ptr(false)},
			{Op: OpAdd, Name: "code", Type: text},
		}, PrimaryKey: &[]string{"id"}}
		v.apply(t, parent)

		tc := TableChange{Create: true, Table: "ddl_t", Columns: []ColumnChange{
			{Op: OpAdd, Name: "id", Type: it, Nullable: ptr(false), AutoIncrement: ptr(v.name != "postgres" || true)},
			{Op: OpAdd, Name: "name", Type: text, Nullable: ptr(false), Default: &DefaultSpec{Kind: DefaultValue, Text: `it's \ ok`}},
			{Op: OpAdd, Name: "parent_id", Type: it},
			{Op: OpAdd, Name: "qty", Type: it, Default: &DefaultSpec{Kind: DefaultExpression, Text: "1 + 1"}},
		}, PrimaryKey: &[]string{"id"},
			Indexes:     []IndexChange{{Op: OpAdd, Name: "ddl_t_name", Columns: []IndexColumn{{Column: "name"}}, Unique: true}},
			ForeignKeys: []FKChange{{Op: OpAdd, Name: "ddl_t_parent", Columns: []string{"parent_id"}, RefTable: "ddl_parent", RefColumns: []string{"id"}, OnDelete: "CASCADE"}},
		}
		if v.caps.Checks || v.name == "sqlite" {
			tc.Checks = []CheckChange{{Op: OpAdd, Name: "ddl_t_qty", Expression: "qty >= 0"}}
		}
		if v.caps.Comments {
			tc.Comment = ptr("A table")
			tc.Columns[1].Comment = ptr("The name")
		}
		v.apply(t, tc)
		d := v.info(t, "ddl_t")
		if !slices.Equal(colNames(d), []string{"id", "name", "parent_id", "qty"}) || !col(d, "id").IsPrimaryKey || col(d, "name").Nullable {
			t.Fatalf("created = %+v", d.Columns)
		}
		if !slices.Equal(indexNames(d), []string{"ddl_t_name"}) && v.name != "mysql" && v.name != "mariadb" {
			t.Fatalf("indexes = %v", indexNames(d))
		}
		if len(d.ForeignKeys) != 1 || d.ForeignKeys[0].RefTable != "ddl_parent" {
			t.Fatalf("fks = %+v", d.ForeignKeys)
		}
		if v.caps.Comments && (d.Comment != "A table" || col(d, "name").Comment != "The name") {
			t.Fatalf("comments = %q %q", d.Comment, col(d, "name").Comment)
		}
		// The default round-trips: insert without it and read it back.
		if _, err := v.db.Exec("INSERT INTO " + v.drv.QuoteIdent(v.schema) + "." + v.drv.QuoteIdent("ddl_t") + " (" + v.drv.QuoteIdent("parent_id") + ") VALUES (NULL)"); err != nil {
			t.Fatal(err)
		}
		var name string
		var qty int
		if err := v.db.QueryRow("SELECT "+v.drv.QuoteIdent("name")+", "+v.drv.QuoteIdent("qty")+" FROM "+v.drv.QuoteIdent(v.schema)+"."+v.drv.QuoteIdent("ddl_t")).Scan(&name, &qty); err != nil || name != `it's \ ok` || qty != 2 {
			t.Fatalf("defaults = %q %d %v", name, qty, err)
		}

		// Alter: what the engine supports.
		alter := TableChange{Table: "ddl_t", Columns: []ColumnChange{
			{Op: OpAdd, Name: "note", Type: text, Nullable: ptr(false), Default: &DefaultSpec{Kind: DefaultValue, Text: "-"}},
			{Op: OpDrop, Name: "qty"},
			{Op: OpAlter, Name: "parent_id", NewName: "parent"},
		}, Indexes: []IndexChange{{Op: OpDrop, Name: "ddl_t_name"}, {Op: OpAdd, Name: "ddl_t_note", Columns: []IndexColumn{{Column: "note"}}}}}
		if len(d.Checks) > 0 {
			alter.Checks = []CheckChange{{Op: OpDrop, Name: d.Checks[0].Name}}
		}
		if v.name == "sqlite" {
			// SQLite cannot drop a column that a check uses, nor rename one
			// in a FK... renaming works; dropping qty needs the check gone,
			// which SQLite cannot do: keep qty.
			alter.Columns = alter.Columns[0:1]
			alter.Columns = append(alter.Columns, ColumnChange{Op: OpAlter, Name: "parent_id", NewName: "parent"})
			alter.Checks = nil
		}
		if v.caps.AlterColumn {
			alter.Columns = append(alter.Columns, ColumnChange{Op: OpAlter, Name: "name", Type: &TypeSpec{Text: "varchar(200)"}, Nullable: ptr(true), Default: &DefaultSpec{Kind: DefaultNone}, Comment: ptr("")})
		}
		v.apply(t, alter)
		d = v.info(t, "ddl_t")
		if col(d, "note") == nil || col(d, "parent") == nil || col(d, "parent_id") != nil {
			t.Fatalf("altered = %v", colNames(d))
		}
		if v.name != "sqlite" && col(d, "qty") != nil {
			t.Fatalf("qty not dropped: %v", colNames(d))
		}
		if v.caps.AlterColumn {
			n := col(d, "name")
			if !n.Nullable || (n.Default != nil && *n.Default != "NULL") || !strings.Contains(n.DataType, "200") || n.Comment != "" {
				t.Fatalf("name = %+v", n)
			}
		}
		if !slices.Contains(indexNames(d), "ddl_t_note") || slices.Contains(indexNames(d), "ddl_t_name") {
			t.Fatalf("indexes = %v", indexNames(d))
		}

		// Keys, foreign keys, checks, index rename, comment, table rename.
		more := TableChange{Table: "ddl_t", NewName: ptr("ddl_t2")}
		if v.caps.RenameIndex {
			more.Indexes = []IndexChange{{Op: OpRename, Name: "ddl_t_note", NewName: "ddl_t_note2"}}
		}
		if v.caps.ForeignKeys {
			more.ForeignKeys = []FKChange{{Op: OpDrop, Name: d.ForeignKeys[0].Name}, {Op: OpAdd, Name: "ddl_fk2", Columns: []string{"parent"}, RefTable: "ddl_parent", RefColumns: []string{"id"}}}
		}
		if v.caps.Checks {
			more.Checks = []CheckChange{{Op: OpAdd, Name: "ddl_t_note_check", Expression: "note <> ''"}}
		}
		if v.caps.PrimaryKey {
			more.PrimaryKey = &[]string{"id", "note"}
			if v.name == "mysql" || v.name == "mariadb" {
				// MySQL needs the auto-increment column first in a key: it is.
			}
		}
		if v.caps.Comments {
			more.Comment = ptr("")
		}
		v.apply(t, more)
		d = v.info(t, "ddl_t2")
		if v.caps.RenameIndex && !slices.Contains(indexNames(d), "ddl_t_note2") {
			t.Fatalf("renamed index = %v", indexNames(d))
		}
		if v.caps.ForeignKeys && (len(d.ForeignKeys) != 1 || d.ForeignKeys[0].Name != "ddl_fk2") {
			t.Fatalf("fks = %+v", d.ForeignKeys)
		}
		if v.caps.Checks && !slices.ContainsFunc(d.Checks, func(c driver.Check) bool { return c.Name == "ddl_t_note_check" }) {
			t.Fatalf("checks = %+v", d.Checks)
		}
		if v.caps.PrimaryKey && (!col(d, "note").IsPrimaryKey || col(d, "note").PKPosition != 2) {
			t.Fatalf("pk = %+v", d.Columns)
		}
		if v.caps.Comments && d.Comment != "" {
			t.Fatalf("comment = %q", d.Comment)
		}

		// A stale change set and read-only mode are refused.
		stale := v.info(t, "ddl_t2")
		stale.Columns[0].Comment = "changed elsewhere"
		if ce := v.applyErr(t, TableChange{Table: "ddl_t2", Base: stale, Columns: []ColumnChange{{Op: OpAdd, Name: "z", Type: text}}}); ce.Kind != FailStructureChanged {
			t.Fatalf("stale = %+v", ce)
		}
		ro := v.opts
		ro.ReadOnly = true
		if _, err := Apply(context.Background(), v.db, v.drv, TableChange{Schema: v.schema, Table: "ddl_t2"}, ro); err == nil || err.(*ChangeError).Kind != FailReadOnly {
			t.Fatalf("read-only = %v", err)
		}
		if ce := v.applyErr(t, TableChange{Create: true, Table: "ddl_t2", Columns: []ColumnChange{{Op: OpAdd, Name: "a", Type: text}}}); ce.Kind != FailInvalid || ce.Field != "name" {
			t.Fatalf("create existing = %+v", ce)
		}
		if ce := v.applyErr(t, TableChange{Table: "ddl_t2", NewName: ptr("ddl_parent")}); ce.Kind != FailInvalid {
			t.Fatalf("rename onto existing = %+v", ce)
		}
		if _, err := Apply(context.Background(), v.db, v.drv, TableChange{Schema: v.schema, Table: "nope", Base: stale, Comment: ptr("")}, v.opts); err == nil || err.(*ChangeError).Kind != FailNotFound {
			t.Fatalf("missing = %v", err)
		}

		// Preview returns the statements without running them.
		stmts, err := Preview(context.Background(), v.db, v.drv, TableChange{Schema: v.schema, Table: "ddl_t2", Base: v.info(t, "ddl_t2"), Columns: []ColumnChange{{Op: OpAdd, Name: "z", Type: text}}}, v.opts)
		if err != nil || len(stmts) != 1 || col(v.info(t, "ddl_t2"), "z") != nil {
			t.Fatalf("preview = %+v %v", stmts, err)
		}
		if _, err := Preview(context.Background(), v.db, v.drv, TableChange{Schema: v.schema, Table: "ddl_t2"}, v.opts); err == nil {
			t.Fatal("preview of a stale (nil base) change")
		}
	})
}

func TestIntegrationFailures(t *testing.T) {
	forEach(t, func(t *testing.T, v *env) {
		if v.name == "sqlite" {
			return // see TestIntegrationSQLiteBadType
		}
		it := &TypeSpec{Text: intType(v)}
		v.apply(t, TableChange{Create: true, Table: "ddl_f", Columns: []ColumnChange{{Op: OpAdd, Name: "id", Type: it, Nullable: ptr(false)}, {Op: OpAdd, Name: "x", Type: it}}, PrimaryKey: &[]string{"id"},
			Indexes: []IndexChange{{Op: OpAdd, Name: "ddl_f_x", Columns: []IndexColumn{{Column: "x"}}}}})
		// The index drop works, the bad type fails.
		ce := v.applyErr(t, TableChange{Table: "ddl_f", Indexes: []IndexChange{{Op: OpDrop, Name: "ddl_f_x"}},
			Columns: []ColumnChange{{Op: OpAdd, Name: "y", Type: &TypeSpec{Text: "no_such_type"}}}})
		if ce.Kind != FailDatabase || ce.SQL == "" {
			t.Fatalf("failure = %+v", ce)
		}
		d := v.info(t, "ddl_f")
		if v.caps.TransactionalDDL {
			if ce.Applied != 0 || !slices.Contains(indexNames(d), "ddl_f_x") {
				t.Fatalf("not rolled back: %+v %v", ce, indexNames(d))
			}
		}
	})
}

func TestIntegrationSQLiteBadType(t *testing.T) {
	// SQLite takes any type name; its failure path is a constraint error.
	ctx := context.Background()
	lite := sqlite.New()
	db := testutil.OpenDriver(t, lite, testutil.SQLiteTemp(t), false)
	opts := Options{ServerVersion: "SQLite 3.46.0"}
	if _, err := Apply(ctx, db, lite, TableChange{Schema: "main", Create: true, Table: "a", Columns: []ColumnChange{{Op: OpAdd, Name: "id", Type: &TypeSpec{Text: "INTEGER"}}},
		Indexes: []IndexChange{{Op: OpAdd, Name: "a_id", Columns: []IndexColumn{{Column: "id"}}}}}, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO a VALUES (1), (1)"); err != nil {
		t.Fatal(err)
	}
	base, _ := lite.TableInfo(ctx, db, "main", "a")
	_, err := Apply(ctx, db, lite, TableChange{Schema: "main", Table: "a", Base: base,
		Columns: []ColumnChange{{Op: OpAdd, Name: "b", Type: &TypeSpec{Text: "TEXT"}}},
		Indexes: []IndexChange{{Op: OpAdd, Name: "a_u", Unique: true, Columns: []IndexColumn{{Column: "id"}}}}}, opts)
	var ce *ChangeError
	if !errors.As(err, &ce) || ce.Kind != FailDatabase || ce.Section != SectionIndexes || ce.Index != 0 || ce.Applied != 0 {
		t.Fatalf("failure = %+v", err)
	}
	if d, _ := lite.TableInfo(ctx, db, "main", "a"); col(d, "b") != nil {
		t.Fatal("not rolled back")
	}
}

func TestIntegrationMySQLPartial(t *testing.T) {
	for _, start := range []func(testing.TB) testutil.DB{testutil.SharedMySQL} {
		ctx := context.Background()
		my := mysql.New()
		db := testutil.OpenDriver(t, my, start(t), false)
		schema, _ := my.CurrentSchema(ctx, db)
		version, _ := my.ServerVersion(ctx, db)
		opts := Options{ServerVersion: version}
		q := my.QuoteIdent
		t.Cleanup(func() {
			_, _ = db.Exec("DROP TABLE IF EXISTS " + q(schema) + ".`ddl_c`")
			_, _ = db.Exec("DROP TABLE IF EXISTS " + q(schema) + ".`ddl_p`")
		})
		for _, s := range []string{
			"CREATE TABLE `ddl_p` (id int PRIMARY KEY)",
			"CREATE TABLE `ddl_c` (id int PRIMARY KEY, p int, CONSTRAINT `ddl_c_fk` FOREIGN KEY (p) REFERENCES `ddl_p` (id))",
		} {
			if _, err := db.Exec(s); err != nil {
				t.Fatal(err)
			}
		}
		base, _ := my.TableInfo(ctx, db, schema, "ddl_c")
		// The FK drop runs (and commits); the column change fails.
		_, err := Apply(ctx, db, my, TableChange{Schema: schema, Table: "ddl_c", Base: base,
			ForeignKeys: []FKChange{{Op: OpDrop, Name: "ddl_c_fk"}},
			Columns:     []ColumnChange{{Op: OpAdd, Name: "bad", Type: &TypeSpec{Text: "no_such_type"}}}}, opts)
		var ce *ChangeError
		if !errors.As(err, &ce) || ce.Applied != 1 || !strings.Contains(ce.Message, "applied") {
			t.Fatalf("partial = %+v", err)
		}
		if d, _ := my.TableInfo(ctx, db, schema, "ddl_c"); len(d.ForeignKeys) != 0 {
			t.Fatalf("fk drop was not applied: %+v", d.ForeignKeys)
		}
	}
}

func TestIntegrationMySQLKeepsDefinitions(t *testing.T) {
	forEach(t, func(t *testing.T, v *env) {
		if v.name != "mysql" && v.name != "mariadb" {
			return
		}
		q := v.drv.QuoteIdent
		v.created = append(v.created, "ddl_m")
		if _, err := v.db.Exec("CREATE TABLE " + q("ddl_m") + ` (
			id int unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY,
			s varchar(20) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT 'a\\b''c' COMMENT 'it''s',
			ts timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			e enum('x','y') NOT NULL DEFAULT 'y',
			n decimal(10,2) DEFAULT 1.50,
			g decimal(12,2) GENERATED ALWAYS AS (n * 2) VIRTUAL)`); err != nil {
			t.Fatal(err)
		}
		before := v.info(t, "ddl_m")
		// Touch every column with a comment change: the rest must stay.
		var cols []ColumnChange
		for _, c := range before.Columns {
			if c.Name != "s" {
				cols = append(cols, ColumnChange{Op: OpAlter, Name: c.Name, Comment: ptr("c " + c.Name)})
			}
		}
		v.apply(t, TableChange{Table: "ddl_m", Columns: cols})
		after := v.info(t, "ddl_m")
		for i, b := range before.Columns {
			a := after.Columns[i]
			if b.Name != "s" {
				a.Comment = b.Comment
			}
			if a.DataType != b.DataType || a.Nullable != b.Nullable || (a.Default == nil) != (b.Default == nil) ||
				(a.Default != nil && *a.Default != *b.Default) || a.AutoIncrement != b.AutoIncrement || a.OnUpdate != b.OnUpdate ||
				a.Collation != b.Collation || a.Generated != b.Generated || a.Comment != b.Comment {
				t.Errorf("%s changed:\nbefore %+v\nafter  %+v", b.Name, b, a)
			}
		}
	})
}

func TestIntegrationDropTable(t *testing.T) {
	forEach(t, func(t *testing.T, v *env) {
		ctx := context.Background()
		v.apply(t, TableChange{Create: true, Table: "ddl_d", Columns: []ColumnChange{{Op: OpAdd, Name: "a", Type: &TypeSpec{Text: intType(v)}}}})
		if _, err := DropTableStatement(ctx, v.db, v.drv, v.schema, "nope", false, v.opts); err == nil || err.(*ChangeError).Kind != FailNotFound {
			t.Fatalf("missing = %v", err)
		}
		_, err := DropTableStatement(ctx, v.db, v.drv, v.schema, "ddl_d", true, v.opts)
		if v.caps.DropCascade != (err == nil) {
			t.Fatalf("cascade = %v", err)
		}
		ro := v.opts
		ro.ReadOnly = true
		if _, err := DropTable(ctx, v.db, v.drv, v.schema, "ddl_d", false, ro); err == nil {
			t.Fatal("read-only drop")
		}
		res, err := DropTable(ctx, v.db, v.drv, v.schema, "ddl_d", false, v.opts)
		if err != nil || len(res.Statements) != 1 || !strings.HasPrefix(res.Statements[0].SQL, "DROP TABLE ") {
			t.Fatalf("drop = %+v %v", res, err)
		}
		if _, err := driver.FindTable(ctx, v.drv, v.db, v.schema, "ddl_d"); !errors.Is(err, driver.ErrTableNotFound) {
			t.Fatalf("still there: %v", err)
		}
		// Views are not tables.
		q := v.drv.QuoteIdent
		v.apply(t, TableChange{Create: true, Table: "ddl_vbase", Columns: []ColumnChange{{Op: OpAdd, Name: "a", Type: &TypeSpec{Text: intType(v)}}}})
		if _, err := v.db.Exec("CREATE VIEW " + q(v.schema) + "." + q("ddl_v") + " AS SELECT a FROM " + q(v.schema) + "." + q("ddl_vbase")); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = v.db.Exec("DROP VIEW " + q(v.schema) + "." + q("ddl_v")) })
		if _, err := DropTableStatement(ctx, v.db, v.drv, v.schema, "ddl_v", false, v.opts); err == nil || err.(*ChangeError).Kind != FailUnsupported {
			t.Fatalf("view = %v", err)
		}
		if ce := v.applyErr(t, TableChange{Table: "ddl_v", Comment: ptr("")}); ce.Kind != FailUnsupported {
			t.Fatalf("alter view = %+v", ce)
		}
	})
}
