package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dbedit/internal/conn"
	"dbedit/internal/driver/all"
	"dbedit/internal/edits"
	"dbedit/internal/query"
	"dbedit/internal/store"
	"dbedit/internal/values"
)

func changeName(id float64, old values.Cell, name string) edits.ChangeSet {
	return edits.ChangeSet{Schema: "main", Table: "t", Updates: []edits.Update{{
		PK:  map[string]values.Cell{"id": {K: values.Num, V: id}},
		Set: map[string]edits.EditValue{"name": {Kind: edits.ValText, Text: name}},
		Old: map[string]values.Cell{"name": old},
	}}}
}

func TestEditServicePreviewAndApply(t *testing.T) {
	ctx := context.Background()
	m, sid := openSQLiteSession(t)
	svc := NewEditService(m, all.Registry())
	cs := changeName(1, values.Cell{K: values.Str, V: "a"}, "it's")
	cs.Inserts = []edits.Insert{{Values: map[string]edits.EditValue{"name": {Kind: edits.ValText, Text: "new"}}}}
	cs.Deletes = []edits.Delete{{PK: map[string]values.Cell{"id": {K: values.Num, V: 3.0}}}}

	prev, err := svc.Preview(ctx, sid, cs)
	if err != nil || len(prev) != 3 {
		t.Fatalf("Preview = %+v, %v", prev, err)
	}
	if prev[1].SQL != `UPDATE "main"."t" SET "name" = 'it''s' WHERE "id" = 1;` {
		t.Fatalf("preview sql = %s", prev[1].SQL)
	}
	res, err := svc.Apply(ctx, sid, cs)
	if err != nil || !res.OK || res.Failure != nil || res.Result.Updated != 1 || res.Result.Inserted != 1 || res.Result.Deleted != 1 {
		t.Fatalf("Apply = %+v, %v", res, err)
	}
	if k := res.Result.InsertedKeys[0]["id"]; k.V != int64(3) { // deletes run first, so rowid 3 is free again
		t.Fatalf("inserted key = %+v", res.Result.InsertedKeys)
	}

	// A stale change is a structured failure, not an error.
	res, err = svc.Apply(ctx, sid, changeName(99, values.Cell{K: values.Str, V: "c"}, "x"))
	if err != nil || res.OK || res.Failure == nil || res.Failure.Kind != edits.FailStale || res.Failure.Op != edits.OpUpdate {
		t.Fatalf("stale Apply = %+v, %v", res, err)
	}
	// Views are refused before anything runs.
	res, err = svc.Apply(ctx, sid, edits.ChangeSet{Schema: "main", Table: "v", Deletes: []edits.Delete{{}}})
	if err != nil || res.Failure == nil || !errors.Is(res.Failure, edits.ErrNotATable) {
		t.Fatalf("view Apply = %+v, %v", res, err)
	}
	if _, err := svc.Preview(ctx, sid, edits.ChangeSet{Schema: "main", Table: "v"}); !errors.Is(err, edits.ErrNotATable) {
		t.Fatalf("view Preview err = %v", err)
	}

	for _, id := range []string{"nope"} {
		if _, err := svc.Preview(ctx, id, cs); !errors.Is(err, conn.ErrSessionNotFound) {
			t.Fatalf("Preview err = %v", err)
		}
		if _, err := svc.Apply(ctx, id, cs); !errors.Is(err, conn.ErrSessionNotFound) {
			t.Fatalf("Apply err = %v", err)
		}
	}
	// A database error that is not a ChangeError (closed pool) is an error.
	sess, _ := m.Get(sid)
	_ = sess.DB.Close()
	if _, err := svc.Apply(ctx, sid, cs); err == nil {
		t.Fatal("Apply on a closed pool must fail")
	}
}

func TestEditServiceUnknownDriver(t *testing.T) {
	m, sid := openSQLiteSession(t)
	svc := NewEditService(m, failingDrivers{})
	if _, err := svc.Preview(context.Background(), sid, edits.ChangeSet{}); err == nil {
		t.Fatal("Preview without a driver")
	}
	if _, err := svc.Apply(context.Background(), sid, edits.ChangeSet{}); err == nil {
		t.Fatal("Apply without a driver")
	}
}

func TestEditServiceReadOnlySafeMode(t *testing.T) {
	ctx := context.Background()
	m, sid := openSQLiteSessionMode(t, store.SafeReadOnly)
	svc := NewEditService(m, all.Registry())
	res, err := svc.Apply(ctx, sid, changeName(1, values.Cell{K: values.Str, V: "a"}, "b"))
	if err != nil || res.OK || res.Failure == nil || res.Failure.Kind != edits.FailReadOnly {
		t.Fatalf("Apply = %+v, %v", res, err)
	}
	// Preview still works (it never executes anything).
	if _, err := svc.Preview(ctx, sid, changeName(1, values.Cell{K: values.Str, V: "a"}, "b")); err != nil {
		t.Fatal(err)
	}
	// The pool itself is read-only (mode=ro): the editor's writes fail.
	ed := NewEditorService(m, all.Registry(), nil)
	t.Cleanup(func() { _ = ed.ServiceShutdown() })
	eid, _ := ed.OpenEditor(sid)
	out, err := ed.Execute(ctx, eid, "", "UPDATE t SET name = 'z'", query.ExecOptions{})
	if err != nil || out.NeedsConfirm || out.Statements[0].Status != query.StatusError {
		t.Fatalf("Execute = %+v, %v", out, err)
	}
	if len(out.Notices) != 1 || out.Notices[0] != MsgReadOnly {
		t.Fatalf("notices = %q", out.Notices)
	}
	if !strings.Contains(strings.ToLower(out.Statements[0].Error.Message), "readonly") {
		t.Fatalf("error = %+v", out.Statements[0].Error)
	}
	// Reads have no notice.
	out, _ = ed.Execute(ctx, eid, "", "SELECT 1", query.ExecOptions{})
	if len(out.Notices) != 0 || out.Statements[0].Status != query.StatusOK {
		t.Fatalf("read = %+v", out)
	}
}

func TestEditorServiceConfirmMode(t *testing.T) {
	ctx := context.Background()
	m, sid := openSQLiteSessionMode(t, store.SafeConfirm)
	svc := NewEditorService(m, all.Registry(), nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	eid, _ := svc.OpenEditor(sid)

	// Reads run without confirmation.
	res, err := svc.Execute(ctx, eid, "", "SELECT * FROM t", query.ExecOptions{})
	if err != nil || res.NeedsConfirm || len(res.Statements) != 1 {
		t.Fatalf("read = %+v, %v", res, err)
	}
	// A script with writes runs nothing until confirmed.
	script := "SELECT 1; UPDATE t SET name = 'z' WHERE id = 1; DELETE FROM t WHERE id = 3"
	res, err = svc.Execute(ctx, eid, "", script, query.ExecOptions{})
	if err != nil || !res.NeedsConfirm || len(res.Statements) != 0 || len(res.WriteStatements) != 2 || res.WriteStatements[1] != "DELETE FROM t WHERE id = 3" {
		t.Fatalf("unconfirmed = %+v, %v", res, err)
	}
	sess, _ := m.Get(sid)
	var name string
	if err := sess.DB.QueryRow("SELECT name FROM t WHERE id = 1").Scan(&name); err != nil || name != "a" {
		t.Fatalf("nothing may run before confirmation: %q, %v", name, err)
	}
	res, err = svc.Execute(ctx, eid, "", script, query.ExecOptions{Confirmed: true})
	if err != nil || res.NeedsConfirm || len(res.Statements) != 3 || res.FirstError() != nil {
		t.Fatalf("confirmed = %+v, %v", res, err)
	}
	if err := sess.DB.QueryRow("SELECT name FROM t WHERE id = 1").Scan(&name); err != nil || name != "z" {
		t.Fatalf("after confirm: %q, %v", name, err)
	}
	// An unknown editor is still reported by Execute.
	if _, err := svc.Execute(ctx, "nope", "", "UPDATE t SET name = 1", query.ExecOptions{}); !errors.Is(err, query.ErrEditorNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectionServiceSafeMode(t *testing.T) {
	m, sid := openSQLiteSessionMode(t, store.SafeConfirm)
	sess, _ := m.Get(sid)
	if sess.SafeMode != store.SafeConfirm {
		t.Fatalf("session safe mode = %q", sess.SafeMode)
	}
}
