package mysql_test

import (
	"context"
	"strings"
	"testing"

	"lontar/internal/driver"
	"lontar/internal/driver/mysql"
	"lontar/internal/testutil"
)

func TestStructure(t *testing.T) {
	cases := []struct {
		engine string
		start  func(testing.TB) testutil.DB
		d      *mysql.Driver
	}{
		{testutil.EngineMySQL, testutil.SharedMySQL, mysql.New()},
		{testutil.EngineMariaDB, testutil.SharedMariaDB, mysql.NewMariaDB()},
	}
	for _, tc := range cases {
		t.Run(tc.engine, func(t *testing.T) {
			target := tc.start(t)
			db := testutil.Open(t, target)
			testutil.SeedStructure(t, tc.engine, target, db)
			testutil.CheckStructure(t, tc.engine, tc.d, db)

			// SHOW CREATE sanity: the DDL recreates an equivalent table.
			ctx := context.Background()
			ddl, err := tc.d.TableDDL(ctx, db, testutil.StructSchema(tc.engine), testutil.StructOrderLines)
			if err != nil {
				t.Fatal(err)
			}
			copyDDL := strings.Replace(ddl, "CREATE TABLE `order_lines`", "CREATE TABLE `order_lines_copy`", 1)
			copyDDL = strings.Replace(copyDDL, "CONSTRAINT `order_lines_order_fk`", "CONSTRAINT `order_lines_copy_fk`", 1)
			if _, err := db.ExecContext(ctx, copyDDL); err != nil {
				t.Fatalf("%v\n%s", err, copyDDL)
			}
			t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE `order_lines_copy`") })
			a, err := tc.d.TableInfo(ctx, db, testutil.StructSchema(tc.engine), testutil.StructOrderLines)
			if err != nil {
				t.Fatal(err)
			}
			b, err := tc.d.TableInfo(ctx, db, testutil.StructSchema(tc.engine), "order_lines_copy")
			if err != nil {
				t.Fatal(err)
			}
			if len(a.Columns) != len(b.Columns) || len(b.ForeignKeys) != 1 || b.ForeignKeys[0].OnUpdate != "SET NULL" {
				t.Fatalf("copy = %+v", b)
			}
			for i := range a.Columns {
				if a.Columns[i].DataType != b.Columns[i].DataType || a.Columns[i].AutoIncrement != b.Columns[i].AutoIncrement {
					t.Errorf("column %d: %+v vs %+v", i, a.Columns[i], b.Columns[i])
				}
			}
		})
	}
}

func TestStructureHelpers(t *testing.T) {
	c := driver.ColumnDetail{}
	mysql.ApplyExtra(&c, "DEFAULT_GENERATED on update CURRENT_TIMESTAMP(3)")
	if c.OnUpdate != "CURRENT_TIMESTAMP(3)" || c.AutoIncrement || c.Generated != "" {
		t.Fatalf("extra = %+v", c)
	}
	for extra, want := range map[string]string{"VIRTUAL GENERATED": "VIRTUAL", "STORED GENERATED": "STORED", "PERSISTENT GENERATED": "STORED"} {
		c := driver.ColumnDetail{}
		mysql.ApplyExtra(&c, extra)
		if c.Generated != want {
			t.Errorf("%s: %+v", extra, c)
		}
	}
	c = driver.ColumnDetail{}
	mysql.ApplyExtra(&c, "auto_increment")
	if !c.AutoIncrement {
		t.Fatal("auto_increment")
	}
	if got := mysql.Delimited("SET x = 1;\n"); got != "SET x = 1;\n" {
		t.Errorf("Delimited = %q", got)
	}
	if got := mysql.Delimited("BEGIN SET a = 1; END"); got != "DELIMITER ;;\nBEGIN SET a = 1; END;;\nDELIMITER ;\n" {
		t.Errorf("Delimited = %q", got)
	}
	d := mysql.New()
	for _, tc := range []struct {
		ix   driver.Index
		want string
	}{
		{driver.Index{Name: "PRIMARY", Primary: true, Unique: true, Method: "BTREE"}, "PRIMARY KEY (`a`) USING BTREE"},
		{driver.Index{Name: "u", Unique: true, Method: "HASH"}, "UNIQUE KEY `u` (`a`) USING HASH"},
		{driver.Index{Name: "f", Method: "FULLTEXT"}, "FULLTEXT KEY `f` (`a`)"},
		{driver.Index{Name: "k"}, "KEY `k` (`a`)"},
	} {
		if got := d.IndexDefinition(tc.ix, []string{"`a`"}); got != tc.want {
			t.Errorf("IndexDefinition = %q, want %q", got, tc.want)
		}
	}
}
