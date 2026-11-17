package main

import (
	"reflect"
	"testing"

	"lontar/internal/services"
)

func TestDroppedSQLFiles(t *testing.T) {
	var got []any
	em := services.EmitterFunc(func(name string, data any) {
		if name != EventFilesDropped {
			t.Fatalf("event = %q", name)
		}
		got = append(got, data)
	})
	emitDroppedSQLFiles(em, []string{"/a/q.sql", "/a/photo.png", `C:\x\R.SQL`, "/a/noext"})
	emitDroppedSQLFiles(em, []string{"/a/readme.md"})
	emitDroppedSQLFiles(em, nil)
	want := []any{[]string{"/a/q.sql", `C:\x\R.SQL`}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("emitted = %#v", got)
	}
}
