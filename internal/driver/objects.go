package driver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
)

// ObjectKind classifies a schema object other than a table or view.
type ObjectKind string

// Object kinds returned by ObjectLister.ListObjects.
const (
	ObjectFunction  ObjectKind = "function"
	ObjectProcedure ObjectKind = "procedure"
	ObjectAggregate ObjectKind = "aggregate" // PostgreSQL
	ObjectSequence  ObjectKind = "sequence"  // PostgreSQL, MariaDB 10.3+
	ObjectEnum      ObjectKind = "enum"      // PostgreSQL
	ObjectComposite ObjectKind = "composite" // PostgreSQL
	ObjectDomain    ObjectKind = "domain"    // PostgreSQL
	ObjectTrigger   ObjectKind = "trigger"
	ObjectEvent     ObjectKind = "event" // MySQL/MariaDB
)

// ObjectKinds lists every object kind (for validation and bindings).
var ObjectKinds = []ObjectKind{
	ObjectFunction, ObjectProcedure, ObjectAggregate, ObjectSequence,
	ObjectEnum, ObjectComposite, ObjectDomain, ObjectTrigger, ObjectEvent,
}

// ErrObjectNotFound is returned by ObjectDDL for a ref that ListObjects
// does not report (unknown, dropped or forged).
var ErrObjectNotFound = errors.New("object not found")

// Property is one key/value line of an object's details.
type Property struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Object is a function, procedure, aggregate, sequence, type, trigger or
// event of a schema, as listed in the sidebar.
type Object struct {
	Schema string     `json:"schema"`
	Kind   ObjectKind `json:"kind"`
	Name   string     `json:"name"`
	// Signature is the argument list of a routine ("a integer, b text");
	// "" for other kinds. PostgreSQL routines are overloaded by it.
	Signature string `json:"signature"`
	// Detail is a one-line summary (return type and language, enum labels,
	// trigger timing, sequence range...).
	Detail string `json:"detail"`
	// Table is the table a trigger belongs to ("" for other kinds).
	Table string `json:"table"`
	// ID identifies the object within the engine where the name does not
	// (PostgreSQL: the catalog OID; MySQL routines: the specific name).
	ID string `json:"id"`
	// Properties are the details shown by the object tab, in order.
	Properties []Property `json:"properties"`
}

// Ref returns the reference the frontend sends back to ask for the DDL.
func (o Object) Ref() ObjectRef {
	return ObjectRef{Kind: o.Kind, Name: o.Name, Signature: o.Signature, Table: o.Table, ID: o.ID}
}

// ObjectRef names an object as ListObjects reported it. It comes from the
// UI, so it is only ever matched against a fresh listing, never put into
// SQL.
type ObjectRef struct {
	Kind      ObjectKind `json:"kind"`
	Name      string     `json:"name"`
	Signature string     `json:"signature"`
	Table     string     `json:"table"`
	ID        string     `json:"id"`
}

// ObjectLister is implemented by drivers that list schema objects besides
// tables and views. It is optional: the service returns no objects for an
// engine without it.
type ObjectLister interface {
	// ListObjects lists every object group of schema in one call, ordered
	// by kind, then name. Slices are never nil.
	ListObjects(ctx context.Context, db *sql.DB, schema string) ([]Object, error)
	// ObjectDDL returns the CREATE statement of the object ref names. The
	// ref is looked up in ListObjects first (FindObject); unknown refs
	// return ErrObjectNotFound.
	ObjectDDL(ctx context.Context, db *sql.DB, schema string, ref ObjectRef) (string, error)
}

// FindObject lists schema's objects and returns the one matching ref
// exactly (kind, name, signature, table and ID), or ErrObjectNotFound.
// Drivers then build their DDL query from the returned object (catalog
// values), never from ref.
func FindObject(ctx context.Context, l ObjectLister, db *sql.DB, schema string, ref ObjectRef) (Object, error) {
	objs, err := l.ListObjects(ctx, db, schema)
	if err != nil {
		return Object{}, err
	}
	return LookupObject(objs, schema, ref)
}

// LookupObject finds ref in objs (exact match) or returns
// ErrObjectNotFound.
func LookupObject(objs []Object, schema string, ref ObjectRef) (Object, error) {
	i := slices.IndexFunc(objs, func(o Object) bool { return ref.Name != "" && o.Ref() == ref })
	if i < 0 {
		return Object{}, fmt.Errorf("%w: %s %s.%s", ErrObjectNotFound, ref.Kind, schema, ref.Name)
	}
	return objs[i], nil
}

// SortObjects orders objects by kind (in ObjectKinds order), name,
// signature and table.
func SortObjects(objs []Object) {
	rank := func(k ObjectKind) int { return slices.Index(ObjectKinds, k) }
	slices.SortStableFunc(objs, func(a, b Object) int {
		if r := rank(a.Kind) - rank(b.Kind); r != 0 {
			return r
		}
		for _, p := range [][2]string{{a.Name, b.Name}, {a.Signature, b.Signature}, {a.Table, b.Table}} {
			if p[0] != p[1] {
				if p[0] < p[1] {
					return -1
				}
				return 1
			}
		}
		return 0
	})
}

// Props builds a property list from key/value pairs, skipping empty
// values.
func Props(kv ...string) []Property {
	out := []Property{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			out = append(out, Property{Key: kv[i], Value: kv[i+1]})
		}
	}
	return out
}
