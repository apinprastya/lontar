package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"lontar/internal/driver"
)

// Objects other than tables and views (driver.ObjectLister): stored
// functions and procedures, triggers, events and (MariaDB 10.3+)
// sequences. The listing binds the schema; ObjectDDL looks the ref up in
// a fresh listing and quotes the names the server reported, never the
// UI's.

var _ driver.ObjectLister = (*Driver)(nil)

// routineParams maps SPECIFIC_NAME to its parameter list
// ("IN a INT, OUT b TEXT" for procedures, "a INT" for functions).
func routineParams(ctx context.Context, db *sql.DB, schema string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT SPECIFIC_NAME, COALESCE(PARAMETER_MODE, ''), COALESCE(PARAMETER_NAME, ''), DTD_IDENTIFIER, ROUTINE_TYPE
		 FROM information_schema.PARAMETERS
		 WHERE SPECIFIC_SCHEMA = ? AND ORDINAL_POSITION > 0
		 ORDER BY SPECIFIC_NAME, ROUTINE_TYPE, ORDINAL_POSITION`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var spec, mode, name, typ, rtype string
		if err := rows.Scan(&spec, &mode, &name, &typ, &rtype); err != nil {
			return nil, err
		}
		key := rtype + "\x00" + spec
		if rtype == "FUNCTION" {
			mode = "" // always IN (MySQL reports it, MariaDB does not)
		}
		p := strings.TrimSpace(strings.Join([]string{mode, name, typ}, " "))
		if out[key] != "" {
			p = out[key] + ", " + p
		}
		out[key] = p
	}
	return out, rows.Err()
}

func (d *Driver) listRoutines(ctx context.Context, db *sql.DB, schema string) ([]driver.Object, error) {
	params, err := routineParams(ctx, db, schema)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT ROUTINE_NAME, ROUTINE_TYPE, SPECIFIC_NAME, COALESCE(DTD_IDENTIFIER, ''), COALESCE(ROUTINE_BODY, ''),
		        IS_DETERMINISTIC, SQL_DATA_ACCESS, SECURITY_TYPE, DEFINER, COALESCE(ROUTINE_COMMENT, '')
		 FROM information_schema.ROUTINES
		 WHERE ROUTINE_SCHEMA = ? AND ROUTINE_TYPE IN ('FUNCTION', 'PROCEDURE')
		 ORDER BY ROUTINE_NAME`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Object{}
	for rows.Next() {
		var name, typ, spec, returns, body, deterministic, access, security, definer, comment string
		if err := rows.Scan(&name, &typ, &spec, &returns, &body, &deterministic, &access, &security, &definer, &comment); err != nil {
			return nil, err
		}
		o := driver.Object{Schema: schema, Name: name, Signature: params[typ+"\x00"+spec], ID: spec}
		lang := strings.ToLower(body)
		if typ == "FUNCTION" {
			o.Kind = driver.ObjectFunction
			o.Detail = "returns " + returns
			o.Properties = driver.Props("Arguments", o.Signature, "Returns", returns, "Language", lang,
				"Deterministic", strings.ToLower(deterministic), "Data access", access, "Security", strings.ToLower(security),
				"Definer", definer, "Comment", comment)
		} else {
			o.Kind = driver.ObjectProcedure
			o.Detail = "procedure"
			o.Properties = driver.Props("Arguments", o.Signature, "Language", lang,
				"Deterministic", strings.ToLower(deterministic), "Data access", access, "Security", strings.ToLower(security),
				"Definer", definer, "Comment", comment)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (d *Driver) listTriggers(ctx context.Context, db *sql.DB, schema string) ([]driver.Object, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT TRIGGER_NAME, EVENT_OBJECT_TABLE, ACTION_TIMING, EVENT_MANIPULATION, ACTION_ORIENTATION, DEFINER
		 FROM information_schema.TRIGGERS
		 WHERE TRIGGER_SCHEMA = ? ORDER BY TRIGGER_NAME`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Object{}
	for rows.Next() {
		var name, table, timing, event, level, definer string
		if err := rows.Scan(&name, &table, &timing, &event, &level, &definer); err != nil {
			return nil, err
		}
		out = append(out, driver.Object{
			Schema: schema, Kind: driver.ObjectTrigger, Name: name, Table: table,
			Detail: timing + " " + event + " on " + table,
			Properties: driver.Props("Table", table, "Timing", timing, "Events", event,
				"Level", strings.ToLower(level), "Definer", definer),
		})
	}
	return out, rows.Err()
}

func (d *Driver) listEvents(ctx context.Context, db *sql.DB, schema string) ([]driver.Object, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT EVENT_NAME, EVENT_TYPE, COALESCE(CAST(EXECUTE_AT AS CHAR), ''), COALESCE(INTERVAL_VALUE, ''),
		        COALESCE(INTERVAL_FIELD, ''), STATUS, DEFINER, COALESCE(EVENT_COMMENT, '')
		 FROM information_schema.EVENTS
		 WHERE EVENT_SCHEMA = ? ORDER BY EVENT_NAME`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []driver.Object{}
	for rows.Next() {
		var name, typ, at, every, field, status, definer, comment string
		if err := rows.Scan(&name, &typ, &at, &every, &field, &status, &definer, &comment); err != nil {
			return nil, err
		}
		schedule := "at " + at
		if typ == "RECURRING" {
			schedule = "every " + every + " " + field
		}
		out = append(out, driver.Object{
			Schema: schema, Kind: driver.ObjectEvent, Name: name,
			Detail:     schedule + " · " + strings.ToLower(status),
			Properties: driver.Props("Schedule", schedule, "Status", status, "Definer", definer, "Comment", comment),
		})
	}
	return out, rows.Err()
}

// listSequences lists MariaDB sequences (none on MySQL). Their settings
// are read from each sequence, quoted from the listed names.
func (d *Driver) listSequences(ctx context.Context, db *sql.DB, schema string) ([]driver.Object, error) {
	names, err := driver.ScanStrings(ctx, db,
		`SELECT TABLE_NAME FROM information_schema.TABLES
		 WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'SEQUENCE' ORDER BY TABLE_NAME`, schema)
	if err != nil {
		return nil, err
	}
	out := []driver.Object{}
	for _, name := range names {
		o := driver.Object{Schema: schema, Kind: driver.ObjectSequence, Name: name, Properties: []driver.Property{}}
		var next, minv, maxv, start, inc, cache, cycle int64
		err := db.QueryRowContext(ctx,
			"SELECT `next_not_cached_value`, `minimum_value`, `maximum_value`, `start_value`, `increment`, `cache_size`, `cycle_option` FROM "+
				d.QuoteIdent(schema)+"."+d.QuoteIdent(name)).Scan(&next, &minv, &maxv, &start, &inc, &cache, &cycle)
		if err == nil {
			// Reading the settings needs SELECT on the sequence; without
			// it the sequence is listed bare.
			i := func(n int64) string { return strconv.FormatInt(n, 10) }
			o.Detail = "increment " + i(inc) + ", " + i(minv) + ".." + i(maxv)
			o.Properties = driver.Props("Next value (not cached)", i(next), "Start", i(start), "Increment", i(inc),
				"Minimum", i(minv), "Maximum", i(maxv), "Cache", i(cache), "Cycle", map[bool]string{true: "yes", false: "no"}[cycle != 0])
		}
		out = append(out, o)
	}
	return out, nil
}

// ListObjects implements driver.ObjectLister.
func (d *Driver) ListObjects(ctx context.Context, db *sql.DB, schema string) ([]driver.Object, error) {
	out := []driver.Object{}
	for _, list := range []func(context.Context, *sql.DB, string) ([]driver.Object, error){
		d.listRoutines, d.listSequences, d.listTriggers, d.listEvents,
	} {
		objs, err := list(ctx, db, schema)
		if err != nil {
			return nil, fmt.Errorf("%s: list objects: %w", d.kind, err)
		}
		out = append(out, objs...)
	}
	driver.SortObjects(out)
	return out, nil
}

// ObjectDDL implements driver.ObjectLister with SHOW CREATE FUNCTION,
// PROCEDURE, TRIGGER, EVENT or SEQUENCE on the listed (quoted) name.
func (d *Driver) ObjectDDL(ctx context.Context, db *sql.DB, schema string, ref driver.ObjectRef) (string, error) {
	wrap := func(err error) error { return fmt.Errorf("%s: object DDL: %w", d.kind, err) }
	o, err := driver.FindObject(ctx, d, db, schema, ref)
	if err != nil {
		return "", wrap(err)
	}
	name := d.QuoteIdent(o.Schema) + "." + d.QuoteIdent(o.Name)
	var stmt string
	col := 2
	switch o.Kind {
	case driver.ObjectFunction:
		stmt = "SHOW CREATE FUNCTION " + name
	case driver.ObjectProcedure:
		stmt = "SHOW CREATE PROCEDURE " + name
	case driver.ObjectTrigger:
		stmt = "SHOW CREATE TRIGGER " + name
	case driver.ObjectEvent:
		stmt, col = "SHOW CREATE EVENT "+name, 3
	default: // sequence
		stmt, col = "SHOW CREATE SEQUENCE "+name, 1
	}
	ddl, err := showCreate(ctx, db, stmt, col)
	if err != nil {
		return "", wrap(err)
	}
	if strings.TrimSpace(ddl) == "" {
		// SHOW CREATE returns NULL for routines the user may run but
		// not read (not the definer, no SHOW_ROUTINE privilege).
		return "-- The definition is not visible to this user.\n", nil
	}
	return delimited(ddl), nil
}
