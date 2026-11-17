package driver_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	"lontar/internal/driver"
)

type objectList struct {
	objs []driver.Object
	err  error
}

func (l objectList) ListObjects(context.Context, *sql.DB, string) ([]driver.Object, error) {
	return l.objs, l.err
}

func (objectList) ObjectDDL(context.Context, *sql.DB, string, driver.ObjectRef) (string, error) {
	return "", nil
}

func TestFindObject(t *testing.T) {
	ctx := context.Background()
	f1 := driver.Object{Kind: driver.ObjectFunction, Name: "f", Signature: "x integer", ID: "1"}
	f2 := driver.Object{Kind: driver.ObjectFunction, Name: "f", Signature: "x bigint", ID: "2"}
	tr := driver.Object{Kind: driver.ObjectTrigger, Name: "t", Table: "orders"}
	l := objectList{objs: []driver.Object{f1, f2, tr}}
	got, err := driver.FindObject(ctx, l, nil, "s", f2.Ref())
	if err != nil || got.ID != "2" {
		t.Fatalf("FindObject = %+v, %v", got, err)
	}
	if got, err := driver.FindObject(ctx, l, nil, "s", tr.Ref()); err != nil || got.Table != "orders" {
		t.Fatalf("FindObject trigger = %+v, %v", got, err)
	}
	for _, ref := range []driver.ObjectRef{
		{},
		{Kind: driver.ObjectFunction, Name: "f"},
		{Kind: driver.ObjectProcedure, Name: "f", Signature: "x integer", ID: "1"},
		{Kind: driver.ObjectFunction, Name: "f", Signature: "x integer", ID: "2"},
		{Kind: driver.ObjectTrigger, Name: "t", Table: "other"},
		{Kind: driver.ObjectFunction, Name: `f"; DROP TABLE x; --`, Signature: "x integer", ID: "1"},
	} {
		if _, err := driver.FindObject(ctx, l, nil, "s", ref); !errors.Is(err, driver.ErrObjectNotFound) {
			t.Errorf("FindObject(%+v) err = %v", ref, err)
		}
	}
	boom := errors.New("boom")
	if _, err := driver.FindObject(ctx, objectList{err: boom}, nil, "s", f1.Ref()); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestSortObjectsAndProps(t *testing.T) {
	objs := []driver.Object{
		{Kind: driver.ObjectTrigger, Name: "a", Table: "z"},
		{Kind: driver.ObjectTrigger, Name: "a", Table: "b"},
		{Kind: driver.ObjectSequence, Name: "s"},
		{Kind: driver.ObjectFunction, Name: "f", Signature: "y"},
		{Kind: driver.ObjectFunction, Name: "f", Signature: "x"},
		{Kind: driver.ObjectFunction, Name: "e"},
		{Kind: driver.ObjectFunction, Name: "e"},
	}
	driver.SortObjects(objs)
	var got []string
	for _, o := range objs {
		got = append(got, string(o.Kind)+":"+o.Name+":"+o.Signature+":"+o.Table)
	}
	want := []string{"function:e::", "function:e::", "function:f:x:", "function:f:y:", "sequence:s::", "trigger:a::b", "trigger:a::z"}
	if !slices.Equal(got, want) {
		t.Fatalf("SortObjects = %v", got)
	}
	p := driver.Props("A", "1", "B", "", "C", "3", "odd")
	if len(p) != 2 || p[0] != (driver.Property{Key: "A", Value: "1"}) || p[1].Key != "C" {
		t.Fatalf("Props = %+v", p)
	}
	if driver.Props() == nil {
		t.Fatal("Props() must not be nil")
	}
}
