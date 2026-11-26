package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"lontar/internal/conn"
	"lontar/internal/ddl"
	"lontar/internal/driver/all"
	"lontar/internal/history"
	"lontar/internal/store"
)

func TestStructureEditService(t *testing.T) {
	ctx := context.Background()
	m, sid := openSQLiteSession(t)
	hist := history.New(t.TempDir())
	// Closed before TempDir removes it (Windows cannot delete an open file).
	t.Cleanup(func() { _ = hist.Close() })
	svc := NewStructureEditService(m, all.Registry(), hist)
	sess, _ := m.Get(sid)

	caps, err := svc.Capabilities(sid)
	if err != nil || caps.Flavor != "sqlite" || caps.AlterColumn {
		t.Fatalf("caps = %+v %v", caps, err)
	}
	base, err := NewSchemaService(m, all.Registry()).TableInfo(ctx, sid, "main", "t")
	if err != nil {
		t.Fatal(err)
	}
	tc := ddl.TableChange{Schema: "main", Table: "t", Base: base, Columns: []ddl.ColumnChange{{Op: ddl.OpAdd, Name: "note", Type: &ddl.TypeSpec{Text: "TEXT"}}}}

	pv, err := svc.Preview(ctx, sid, tc)
	if err != nil || pv.Failure != nil || len(pv.Statements) != 1 || pv.Statements[0].SQL != `ALTER TABLE "main"."t" ADD COLUMN "note" TEXT` {
		t.Fatalf("preview = %+v %v", pv, err)
	}
	// A refused change is a structured failure.
	bad := tc
	bad.Columns = []ddl.ColumnChange{{Op: ddl.OpAlter, Name: "name", Nullable: new(bool)}}
	if pv, err := svc.Preview(ctx, sid, bad); err != nil || pv.Failure == nil || pv.Failure.Kind != ddl.FailUnsupported {
		t.Fatalf("refused preview = %+v %v", pv, err)
	}
	if res, err := svc.Apply(ctx, sid, bad); err != nil || res.OK || res.Failure.Section != ddl.SectionColumns {
		t.Fatalf("refused apply = %+v %v", res, err)
	}

	res, err := svc.Apply(ctx, sid, tc)
	if err != nil || !res.OK || len(res.Result.Statements) != 1 {
		t.Fatalf("apply = %+v %v", res, err)
	}
	// A database failure is recorded too.
	base2, _ := NewSchemaService(m, all.Registry()).TableInfo(ctx, sid, "main", "t")
	dup := ddl.TableChange{Schema: "main", Table: "t", Base: base2, Indexes: []ddl.IndexChange{{Op: ddl.OpAdd, Name: "t", Columns: []ddl.IndexColumn{{Column: "id"}}}}}
	if res, err := svc.Apply(ctx, sid, dup); err != nil || res.Failure == nil || res.Failure.Kind != ddl.FailDatabase {
		t.Fatalf("db failure = %+v %v", res, err)
	}

	pv, err = svc.DropTablePreview(ctx, sid, "main", "t", false)
	if err != nil || len(pv.Statements) != 1 || pv.Statements[0].SQL != `DROP TABLE "main"."t"` {
		t.Fatalf("drop preview = %+v %v", pv, err)
	}
	if pv, err := svc.DropTablePreview(ctx, sid, "main", "v", false); err != nil || pv.Failure == nil {
		t.Fatalf("drop view preview = %+v %v", pv, err)
	}
	if res, err := svc.DropTable(ctx, sid, "main", "nope", false); err != nil || res.Failure == nil || res.Failure.Kind != ddl.FailNotFound {
		t.Fatalf("drop missing = %+v %v", res, err)
	}
	if _, err := m.Get(sid); err != nil {
		t.Fatal(err)
	}
	// The view depends on t; SQLite drops t anyway.
	if res, err := svc.DropTable(ctx, sid, "main", "t", false); err != nil || !res.OK {
		t.Fatalf("drop = %+v %v", res, err)
	}

	entries, err := NewHistoryService(hist).List(ctx, sess.ConnectionID, "", 10, 0)
	if err != nil || len(entries) != 3 {
		t.Fatalf("history = %+v %v", entries, err)
	}
	if entries[0].SQL != `DROP TABLE "main"."t";` || entries[0].Source != history.SourceStructure || !entries[1].IsError || !strings.Contains(entries[2].SQL, "ADD COLUMN") {
		t.Fatalf("entries = %+v", entries)
	}

	// Unknown sessions are Go errors.
	for _, err := range []error{
		func() error { _, err := svc.Capabilities("nope"); return err }(),
		func() error { _, err := svc.Preview(ctx, "nope", tc); return err }(),
		func() error { _, err := svc.Apply(ctx, "nope", tc); return err }(),
		func() error { _, err := svc.DropTablePreview(ctx, "nope", "main", "t", false); return err }(),
		func() error { _, err := svc.DropTable(ctx, "nope", "main", "t", false); return err }(),
	} {
		if !errors.Is(err, conn.ErrSessionNotFound) {
			t.Fatalf("unknown session = %v", err)
		}
	}
	// Non-structured errors are returned as errors.
	if ce, err := failure(errors.New("boom")); ce != nil || err == nil {
		t.Fatal("failure()")
	}
}

func TestStructureEditServiceReadOnly(t *testing.T) {
	ctx := context.Background()
	m, sid := openSQLiteSessionMode(t, store.SafeReadOnly)
	svc := NewStructureEditService(m, all.Registry(), nil)
	base, _ := NewSchemaService(m, all.Registry()).TableInfo(ctx, sid, "main", "t")
	res, err := svc.Apply(ctx, sid, ddl.TableChange{Schema: "main", Table: "t", Base: base, Columns: []ddl.ColumnChange{{Op: ddl.OpAdd, Name: "x", Type: &ddl.TypeSpec{Text: "TEXT"}}}})
	if err != nil || res.Failure == nil || res.Failure.Kind != ddl.FailReadOnly {
		t.Fatalf("read-only = %+v %v", res, err)
	}
	if res, err := svc.DropTable(ctx, sid, "main", "t", false); err != nil || res.Failure == nil || res.Failure.Kind != ddl.FailReadOnly {
		t.Fatalf("read-only drop = %+v %v", res, err)
	}
	if res, err := svc.DropTable(ctx, sid, "main", "t", true); err != nil || res.Failure == nil {
		t.Fatalf("cascade on sqlite = %+v %v", res, err)
	}
}
