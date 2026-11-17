package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"lontar/internal/driver"
)

// SQLite has no stored routines, sequences, types or events: the only
// other schema objects are triggers (sqlite_master type 'trigger').

var _ driver.ObjectLister = (*Driver)(nil)

// ListObjects implements driver.ObjectLister: the triggers of schema.
func (d *Driver) ListObjects(ctx context.Context, db *sql.DB, schema string) ([]driver.Object, error) {
	wrap := func(err error) error { return fmt.Errorf("sqlite: list objects: %w", err) }
	master, err := d.master(ctx, db, schema)
	if err != nil {
		return nil, wrap(err)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT name, tbl_name, COALESCE(sql, '') FROM `+master+` WHERE type = 'trigger' ORDER BY name`)
	if err != nil {
		return nil, wrap(err)
	}
	defer rows.Close()
	out := []driver.Object{}
	for rows.Next() {
		var name, table, def string
		if err := rows.Scan(&name, &table, &def); err != nil {
			return nil, wrap(err)
		}
		timing, event := triggerKind(def)
		out = append(out, driver.Object{
			Schema: schema, Kind: driver.ObjectTrigger, Name: name, Table: table,
			Detail:     strings.TrimSpace(timing + " " + event + " on " + table),
			Properties: driver.Props("Table", table, "Timing", timing, "Events", event),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// ObjectDDL implements driver.ObjectLister: the trigger's sqlite_master
// SQL, read by its listed name (bound).
func (d *Driver) ObjectDDL(ctx context.Context, db *sql.DB, schema string, ref driver.ObjectRef) (string, error) {
	wrap := func(err error) error { return fmt.Errorf("sqlite: object DDL: %w", err) }
	o, err := driver.FindObject(ctx, d, db, schema, ref)
	if err != nil {
		return "", wrap(err)
	}
	master, err := d.master(ctx, db, schema)
	if err != nil {
		return "", wrap(err)
	}
	def, err := driver.ScanString(ctx, db, `SELECT sql FROM `+master+` WHERE type = 'trigger' AND name = ?`, o.Name)
	if err != nil {
		return "", wrap(err)
	}
	return strings.TrimRight(strings.TrimSpace(def), ";") + ";\n", nil
}
