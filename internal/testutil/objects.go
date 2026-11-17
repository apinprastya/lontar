package testutil

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"lontar/internal/driver"
)

// ObjSchemaPG is the PostgreSQL schema holding the SeedObjects fixture
// (other engines use SeedSchema).
const ObjSchemaPG = "objs"

// ObjSchema is the schema holding the SeedObjects fixture for engine.
func ObjSchema(engine string) string {
	if engine == EnginePostgres {
		return ObjSchemaPG
	}
	return SeedSchema(engine)
}

var objectsPG = []string{
	`CREATE SCHEMA objs`,
	`CREATE TYPE objs.mood AS ENUM ('sad', 'ok', 'happy')`,
	`CREATE TYPE objs.pair AS (a integer, b text COLLATE "C")`,
	`COMMENT ON TYPE objs.pair IS 'Two values'`,
	`CREATE DOMAIN objs.posint AS integer DEFAULT 1 NOT NULL CONSTRAINT posint_check CHECK (VALUE > 0)`,
	`CREATE FUNCTION objs.add_one(x integer) RETURNS integer LANGUAGE sql IMMUTABLE AS $$ SELECT x + 1 $$`,
	`CREATE FUNCTION objs.add_one(x bigint) RETURNS bigint LANGUAGE sql IMMUTABLE AS $$ SELECT x + 1 $$`,
	`COMMENT ON FUNCTION objs.add_one(integer) IS 'Adds one'`,
	`CREATE FUNCTION objs.touch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.note := COALESCE(NEW.note, ''); RETURN NEW; END $$`,
	`CREATE PROCEDURE objs.reset_tickets(keep bigint) LANGUAGE plpgsql AS $$ BEGIN DELETE FROM objs.tickets WHERE id > keep; END $$`,
	`CREATE AGGREGATE objs.my_sum(integer) (SFUNC = int4pl, STYPE = integer, INITCOND = '0')`,
	`CREATE TABLE objs.tickets (id bigint PRIMARY KEY, mood objs.mood, note text)`,
	`CREATE SEQUENCE objs.ticket_seq AS bigint INCREMENT BY 5 START WITH 10 OWNED BY objs.tickets.id`,
	`COMMENT ON SEQUENCE objs.ticket_seq IS 'Ticket numbers'`,
	`SELECT nextval('objs.ticket_seq')`,
	`CREATE TRIGGER tickets_touch BEFORE INSERT OR UPDATE ON objs.tickets FOR EACH ROW EXECUTE FUNCTION objs.touch()`,
	// Identity sequences belong to their column: not listed.
	`CREATE TABLE objs.ids (id integer GENERATED ALWAYS AS IDENTITY)`,
}

var objectsMySQL = []string{
	`CREATE TABLE obj_log (id INT PRIMARY KEY, note VARCHAR(20))`,
	`CREATE FUNCTION add_one(x INT) RETURNS INT DETERMINISTIC RETURN x + 1`,
	`CREATE PROCEDURE reset_log(IN keep INT) BEGIN DELETE FROM obj_log WHERE id > keep; SELECT COUNT(*) FROM obj_log; END`,
	`CREATE TRIGGER obj_log_bi BEFORE INSERT ON obj_log FOR EACH ROW SET NEW.note = COALESCE(NEW.note, '')`,
	`CREATE EVENT purge_log ON SCHEDULE EVERY 1 DAY DISABLE DO DELETE FROM obj_log`,
}

var objectsSQLite = []string{
	`CREATE TABLE obj_log (id INTEGER PRIMARY KEY, note TEXT)`,
	`CREATE TRIGGER obj_log_ai AFTER INSERT ON obj_log BEGIN
	   UPDATE obj_log SET note = COALESCE(note, '') WHERE id = NEW.id;
	 END`,
}

// ObjectStatements returns the statements SeedObjects runs for engine.
func ObjectStatements(engine string) ([]string, error) {
	switch engine {
	case EnginePostgres:
		return objectsPG, nil
	case EngineMySQL:
		return objectsMySQL, nil
	case EngineMariaDB:
		return append(append([]string{}, objectsMySQL...), `CREATE SEQUENCE ticket_seq START WITH 10 INCREMENT BY 5`), nil
	case EngineSQLite:
		return objectsSQLite, nil
	}
	_, err := SeedStatements(engine)
	return nil, err
}

// trustFunctionCreators lets non-root users create routines and triggers
// on MySQL with binary logging on.
func trustFunctionCreators(t testing.TB, target DB) bool {
	t.Helper()
	root, err := AsRoot(target)
	if err != nil {
		t.Fatalf("%v", err)
		return false
	}
	rdb := Open(t, root)
	if _, err := rdb.ExecContext(context.Background(), `SET GLOBAL log_bin_trust_function_creators = 1`); err != nil {
		t.Fatalf("%v", err)
		return false
	}
	return true
}

// SeedObjects creates the objects fixture (functions, procedures,
// aggregates, sequences, types, triggers and events, as the engine has
// them) once per DSN. See CheckObjects for what it holds.
func SeedObjects(t testing.TB, engine string, target DB, db *sql.DB) {
	t.Helper()
	stmts, err := ObjectStatements(engine)
	if err != nil {
		t.Fatalf("%v", err)
		return
	}
	if engine == EngineMySQL && !trustFunctionCreators(t, target) {
		return
	}
	seedOnce(t, "objects", engine, target, db, stmts)
}

// WantObject is an object CheckObjects expects.
type WantObject struct {
	Kind      driver.ObjectKind
	Name      string
	Signature string // prefix (MySQL/MariaDB type spellings differ)
	Table     string
	Detail    string // prefix
}

// WantObjects lists the objects of the SeedObjects fixture of engine.
func WantObjects(engine string) []WantObject {
	switch engine {
	case EnginePostgres:
		return []WantObject{
			{driver.ObjectFunction, "add_one", "x bigint", "", "returns bigint · sql"},
			{driver.ObjectFunction, "add_one", "x integer", "", "returns integer · sql"},
			{driver.ObjectFunction, "touch", "", "", "returns trigger · plpgsql"},
			{driver.ObjectProcedure, "reset_tickets", "IN keep bigint", "", "plpgsql"},
			{driver.ObjectAggregate, "my_sum", "integer", "", "aggregate returning integer"},
			{driver.ObjectSequence, "ticket_seq", "", "", "bigint · owned by tickets.id"},
			{driver.ObjectEnum, "mood", "", "", "enum (sad, ok, happy)"},
			{driver.ObjectComposite, "pair", "", "", "composite (a integer, b text)"},
			{driver.ObjectDomain, "posint", "", "", "domain over integer"},
			{driver.ObjectTrigger, "tickets_touch", "", "tickets", "BEFORE INSERT OR UPDATE on tickets"},
		}
	case EngineMySQL, EngineMariaDB:
		w := []WantObject{
			{driver.ObjectFunction, "add_one", "x int", "", "returns int"},
			{driver.ObjectProcedure, "reset_log", "IN keep int", "", "procedure"},
			{driver.ObjectTrigger, "obj_log_bi", "", "obj_log", "BEFORE INSERT on obj_log"},
			{driver.ObjectEvent, "purge_log", "", "", "every 1 DAY · disabled"},
		}
		if engine == EngineMariaDB {
			w = append(w, WantObject{driver.ObjectSequence, "ticket_seq", "", "", "increment 5, 1.."})
		}
		return w
	}
	return []WantObject{{driver.ObjectTrigger, "obj_log_ai", "", "obj_log", "AFTER INSERT on obj_log"}}
}

// FindObject returns the listed object matching w (nil when missing).
func FindObject(objs []driver.Object, w WantObject) *driver.Object {
	for i, o := range objs {
		if o.Kind == w.Kind && o.Name == w.Name && o.Table == w.Table && strings.HasPrefix(strings.ToLower(o.Signature), strings.ToLower(w.Signature)) &&
			(w.Signature != "" || o.Signature == "") {
			return &objs[i]
		}
	}
	return nil
}

// CheckObjects asserts that l lists every SeedObjects object of engine
// with the right kind, signature, table and detail, returns a non-empty
// DDL naming it, and refuses forged or unknown refs.
func CheckObjects(t testing.TB, engine string, l driver.ObjectLister, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	schema := ObjSchema(engine)
	objs, err := l.ListObjects(ctx, db, schema)
	if err != nil {
		t.Fatalf("%s: ListObjects: %v", engine, err)
		return
	}
	for _, w := range WantObjects(engine) {
		o := FindObject(objs, w)
		if o == nil {
			t.Errorf("%s: %s %s(%s) not listed in %+v", engine, w.Kind, w.Name, w.Signature, objs)
			continue
		}
		if !strings.HasPrefix(strings.ToLower(o.Detail), strings.ToLower(w.Detail)) {
			t.Errorf("%s: %s detail = %q, want prefix %q", engine, w.Name, o.Detail, w.Detail)
		}
		if o.Schema != schema || o.Properties == nil {
			t.Errorf("%s: %s schema %q, properties %v", engine, w.Name, o.Schema, o.Properties)
		}
		ddl, err := l.ObjectDDL(ctx, db, schema, o.Ref())
		if err != nil || !strings.Contains(ddl, w.Name) || !strings.HasSuffix(ddl, "\n") {
			t.Errorf("%s: ObjectDDL(%s) = %q, %v", engine, w.Name, ddl, err)
		}
	}
	if engine == EnginePostgres && len(objs) != len(WantObjects(engine)) {
		t.Errorf("%s: listed %d objects, want %d: %+v", engine, len(objs), len(WantObjects(engine)), objs)
	}
	if len(objs) == 0 {
		return
	}
	// Forged refs: everything must match a listed object exactly.
	good := objs[0].Ref()
	forged := []driver.ObjectRef{
		{},
		{Kind: good.Kind, Name: "nope"},
		{Kind: good.Kind, Name: good.Name + `"; DROP TABLE x; --`, Signature: good.Signature, Table: good.Table, ID: good.ID},
		{Kind: good.Kind, Name: good.Name, Signature: good.Signature + ")", Table: good.Table, ID: good.ID},
		{Kind: good.Kind, Name: good.Name, Signature: good.Signature, Table: good.Table, ID: good.ID + "0"},
		{Kind: "table", Name: good.Name, Signature: good.Signature, Table: good.Table, ID: good.ID},
	}
	for _, ref := range forged {
		if _, err := l.ObjectDDL(ctx, db, schema, ref); !errors.Is(err, driver.ErrObjectNotFound) {
			t.Errorf("%s: forged ref %+v: err = %v", engine, ref, err)
		}
	}
	if _, err := l.ObjectDDL(ctx, db, schema+"_missing", good); err == nil {
		t.Errorf("%s: ObjectDDL in an unknown schema succeeded", engine)
	}
}
