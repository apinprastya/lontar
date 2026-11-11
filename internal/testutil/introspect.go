package testutil

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"dbedit/internal/driver"
)

// SeedSchema is the schema holding the SeedBrowse fixture for engine when
// connected to the container's default database.
func SeedSchema(engine string) string {
	switch engine {
	case EnginePostgres:
		return "public"
	case EngineMySQL, EngineMariaDB:
		return dbName
	}
	return "main"
}

// CheckIntrospection asserts that in describes the SeedBrowse fixture in db
// correctly. It is shared by the driver packages' integration tests.
func CheckIntrospection(t testing.TB, engine string, in driver.Introspector, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	server := engine != EngineSQLite
	schema := SeedSchema(engine)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", engine, err)
		}
	}
	check := func(ok bool, format string, args ...any) {
		t.Helper()
		if !ok {
			t.Errorf(engine+": "+format, args...)
		}
	}

	// Databases.
	dbs, err := in.ListDatabases(ctx, db, false)
	must(err)
	allDBs, err := in.ListDatabases(ctx, db, true)
	must(err)
	cur, err := in.CurrentDatabase(ctx, db)
	must(err)
	switch engine {
	case EnginePostgres:
		check(slices.Contains(dbs, dbName) && !slices.Contains(dbs, "template1"), "databases = %v", dbs)
		check(slices.Contains(allDBs, "template1") && !slices.Contains(allDBs, "template0"), "all databases = %v", allDBs)
		check(cur == dbName, "current database = %q", cur)
	case EngineMySQL, EngineMariaDB:
		check(slices.Contains(dbs, dbName) && !slices.Contains(dbs, "information_schema") && !slices.Contains(dbs, "mysql"), "databases = %v", dbs)
		check(slices.Contains(allDBs, "information_schema"), "all databases = %v", allDBs)
		check(cur == dbName, "current database = %q", cur)
	default:
		check(slices.Equal(dbs, []string{"main"}) && cur == "main", "databases = %v current %q", dbs, cur)
	}

	// Schemas.
	schemas, err := in.ListSchemas(ctx, db, false)
	must(err)
	allSchemas, err := in.ListSchemas(ctx, db, true)
	must(err)
	curSchema, err := in.CurrentSchema(ctx, db)
	must(err)
	check(curSchema == schema, "current schema = %q", curSchema)
	check(slices.Contains(schemas, schema), "schemas = %v", schemas)
	if engine == EnginePostgres {
		check(slices.Contains(schemas, SeedOtherSchema), "schemas = %v", schemas)
		check(!slices.ContainsFunc(schemas, func(s string) bool { return strings.HasPrefix(s, "pg_") || s == "information_schema" }), "system schemas shown: %v", schemas)
		check(slices.Contains(allSchemas, "pg_catalog") && slices.Contains(allSchemas, "information_schema"), "all schemas = %v", allSchemas)
		other, err := in.ListTables(ctx, db, SeedOtherSchema)
		must(err)
		check(len(other) == 1 && other[0].Name == "other_t" && other[0].Schema == SeedOtherSchema, "other tables = %+v", other)
	} else {
		check(slices.Equal(schemas, []string{schema}), "schemas = %v", schemas)
	}

	// Tables and views.
	tables, err := in.ListTables(ctx, db, schema)
	must(err)
	byName := map[string]driver.Table{}
	var names []string
	for _, tb := range tables {
		byName[tb.Name] = tb
		names = append(names, tb.Name)
	}
	// Order follows the server's collation, which differs between
	// engines; the UI sorts for display.
	check(len(names) >= 7, "tables = %v", names)
	for _, n := range []string{SeedItems, SeedNumbers, SeedNoPK, SeedComposite, SeedOdd, "digits"} {
		check(byName[n].Type == driver.TypeTable, "table %q = %+v", n, byName[n])
	}
	check(byName[SeedView].Type == driver.TypeView && byName[SeedView].EstimatedRows == nil, "view = %+v", byName[SeedView])
	if engine == EnginePostgres {
		check(byName[SeedMatView].Type == driver.TypeMaterializedView, "matview = %+v", byName[SeedMatView])
		mv, err := in.Columns(ctx, db, schema, SeedMatView)
		must(err)
		check(len(mv) == 2, "matview columns = %+v", mv)
	}
	est := byName[SeedNumbers].EstimatedRows
	if server {
		check(est != nil && *est >= 0, "numbers estimate = %v", est)
		if engine == EnginePostgres && est != nil {
			check(*est == 1000, "numbers estimate = %d", *est)
		}
	} else {
		check(est == nil, "sqlite estimate = %v", est)
	}

	// Columns.
	cols, err := in.Columns(ctx, db, schema, SeedItems)
	must(err)
	check(len(cols) >= 11, "items columns = %d", len(cols))
	if len(cols) >= 4 {
		check(cols[0].Name == "id" && cols[0].IsPrimaryKey && !cols[0].Nullable && cols[0].Ordinal == 1, "id = %+v", cols[0])
		check(cols[3].Name == "name" && !cols[3].IsPrimaryKey && cols[3].Nullable && cols[3].Ordinal == 4, "name = %+v", cols[3])
		check(strings.Contains(strings.ToLower(cols[3].DataType), "char"), "name type = %q", cols[3].DataType)
		check(cols[3].Default == nil || strings.EqualFold(*cols[3].Default, "NULL"), "name default = %v", cols[3].Default)
	}
	num, err := in.Columns(ctx, db, schema, SeedNumbers)
	must(err)
	if len(num) == 3 {
		check(num[2].Default != nil && strings.Contains(*num[2].Default, "0"), "grp default = %v", num[2].Default)
	} else {
		t.Errorf("%s: numbers columns = %+v", engine, num)
	}
	odd, err := in.Columns(ctx, db, schema, SeedOdd)
	must(err)
	var oddNames []string
	for _, c := range odd {
		oddNames = append(oddNames, c.Name)
	}
	check(slices.Equal(oddNames, []string{"order", "Mixed Case", `quote"col`}) && odd[0].IsPrimaryKey, "odd columns = %+v", odd)
	comp, err := in.Columns(ctx, db, schema, SeedComposite)
	must(err)
	check(len(comp) == 3 && comp[0].IsPrimaryKey && comp[1].IsPrimaryKey && !comp[2].IsPrimaryKey &&
		comp[0].PKPosition == 2 && comp[1].PKPosition == 1 && comp[2].PKPosition == 0, "composite columns = %+v", comp)
	none, err := in.Columns(ctx, db, schema, "no such table")
	must(err)
	check(len(none) == 0, "missing table columns = %+v", none)
	view, err := in.Columns(ctx, db, schema, SeedView)
	must(err)
	check(len(view) == 2, "view columns = %+v", view)

	// Primary keys.
	pk, err := in.PrimaryKey(ctx, db, schema, SeedComposite)
	must(err)
	check(slices.Equal(pk, []string{"b", "a"}), "composite pk = %v", pk)
	pk, err = in.PrimaryKey(ctx, db, schema, SeedNoPK)
	must(err)
	check(len(pk) == 0, "nopk pk = %v", pk)
	pk, err = in.PrimaryKey(ctx, db, schema, SeedOdd)
	must(err)
	check(slices.Equal(pk, []string{"order"}), "odd pk = %v", pk)

	// All columns of the schema at once (editor autocompletion).
	all, err := in.SchemaColumns(ctx, db, schema)
	must(err)
	byTable := map[string][]string{}
	for _, c := range all {
		byTable[c.Table] = append(byTable[c.Table], c.Name)
		check(c.DataType != "" || c.Table == SeedView, "schema column without type: %+v", c)
	}
	check(slices.Equal(byTable[SeedOdd], oddNames), "schema columns of odd = %v", byTable[SeedOdd])
	check(len(byTable[SeedItems]) == len(cols) && len(byTable[SeedView]) == 2, "schema columns = %v", byTable)
	none2, err := in.SchemaColumns(ctx, db, "no such schema")
	must(err)
	check(len(none2) == 0, "unknown schema columns = %+v", none2)
}

// CheckIntrospectionErrors asserts every Introspector method fails on a
// closed database.
func CheckIntrospectionErrors(t testing.TB, in driver.Introspector, closed *sql.DB) {
	t.Helper()
	ctx := context.Background()
	errs := map[string]error{}
	_, errs["ListSchemas"] = in.ListSchemas(ctx, closed, false)
	_, errs["ListTables"] = in.ListTables(ctx, closed, "x")
	_, errs["Columns"] = in.Columns(ctx, closed, "x", "y")
	_, errs["PrimaryKey"] = in.PrimaryKey(ctx, closed, "x", "y")
	_, errs["SchemaColumns"] = in.SchemaColumns(ctx, closed, "x")
	_, errs["TableInfo"] = in.TableInfo(ctx, closed, "x", "y")
	_, errs["TableDDL"] = in.TableDDL(ctx, closed, "x", "y")
	for name, err := range errs {
		if err == nil {
			t.Errorf("%s on a closed DB should fail", name)
		}
	}
}
