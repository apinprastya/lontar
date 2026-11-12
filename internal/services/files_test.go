package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"dbedit/internal/driver/all"
	"dbedit/internal/export"
	"dbedit/internal/importer"
	"dbedit/internal/query"
	"dbedit/internal/settings"
	"dbedit/internal/store"
	"dbedit/internal/values"
)

type recEmitter struct {
	mu     sync.Mutex
	events []JobProgress
}

func (r *recEmitter) Emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := data.(JobProgress); ok && strings.Contains(name, "progress") {
		r.events = append(r.events, p)
	}
}

type fakeDialogs struct{ title, filename string }

func (f *fakeDialogs) OpenFile(title string, filters []FileFilter) (string, error) {
	f.title = title
	return "/picked/" + filters[0].Pattern, nil
}
func (f *fakeDialogs) SaveFile(title, filename string, _ []FileFilter) (string, error) {
	f.title, f.filename = title, filename
	return "", nil // cancelled
}

func TestDialogService(t *testing.T) {
	fd := &fakeDialogs{}
	svc := NewDialogService(fd)
	p, err := svc.OpenFile("Key", []FileFilter{{DisplayName: "Keys", Pattern: "*.pem"}})
	if err != nil || p != "/picked/*.pem" || fd.title != "Key" {
		t.Fatalf("open = %q, %v", p, err)
	}
	if p, err := svc.SaveFile("Export", "t.csv", nil); err != nil || p != "" || fd.filename != "t.csv" {
		t.Fatalf("save = %q, %v", p, err)
	}
	none := NewDialogService(nil)
	if _, err := none.OpenFile("x", nil); !errors.Is(err, ErrNoDialogs) {
		t.Fatal(err)
	}
	if _, err := none.SaveFile("x", "y", nil); !errors.Is(err, ErrNoDialogs) {
		t.Fatal(err)
	}
	var got string
	EmitterFunc(func(name string, _ any) { got = name }).Emit("e", nil)
	if got != "e" {
		t.Fatal("EmitterFunc")
	}
}

func TestExportService(t *testing.T) {
	m, id := openSQLiteSession(t)
	em := &recEmitter{}
	svc := NewExportService(m, all.Registry(), em)
	ctx := context.Background()
	dir := t.TempDir()

	// Whole table with filters and sort, CSV.
	path := filepath.Join(dir, "t.csv")
	res, err := svc.Export(ctx, ExportRequest{JobID: "j1", SessionID: id, Path: path, Source: SourceTable,
		Table:   query.Request{Schema: "main", Table: "t", Sort: []query.Sort{{Column: "id", Desc: true}}},
		Options: export.Options{Format: export.FormatCSV, CSV: export.CSVOptions{Header: true, Null: export.NullBackN}}})
	if err != nil || res.Rows != 3 || res.Path != path {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if data, _ := os.ReadFile(path); string(data) != "id,name\n3,c\n2,\\N\n1,a\n" {
		t.Fatalf("csv = %q", data)
	}
	if len(em.events) == 0 || em.events[len(em.events)-1] != (JobProgress{JobID: "j1", Rows: 3}) {
		t.Fatalf("events = %+v", em.events)
	}

	// Table as SQL with the DDL.
	path = filepath.Join(dir, "t.sql")
	if _, err := svc.Export(ctx, ExportRequest{SessionID: id, Path: path, Source: SourceTable, Table: query.Request{Schema: "main", Table: "t"},
		Options: export.Options{Format: export.FormatSQL, SQL: export.SQLOptions{CreateTable: true}}}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT);") || !strings.Contains(string(data), `INSERT INTO "t" ("id", "name") VALUES`) {
		t.Fatalf("sql =\n%s", data)
	}
	// Unknown table for the SQL options.
	if _, err := svc.Export(ctx, ExportRequest{SessionID: id, Path: path, Source: SourceTable, Table: query.Request{Schema: "main", Table: "nope"},
		Options: export.Options{Format: export.FormatSQL}}); err == nil {
		t.Fatal("unknown table")
	}

	// Query re-run: reads only.
	path = filepath.Join(dir, "q.json")
	if res, err := svc.Export(ctx, ExportRequest{SessionID: id, Path: path, Source: SourceQuery, SQL: "SELECT name FROM t WHERE id < 3 ORDER BY id",
		Options: export.Options{Format: export.FormatJSON}}); err != nil || res.Rows != 2 {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if _, err := svc.Export(ctx, ExportRequest{SessionID: id, Path: path, Source: SourceQuery, SQL: "DELETE FROM t",
		Options: export.Options{Format: export.FormatJSON}}); !errors.Is(err, export.ErrNotRerunnable) {
		t.Fatalf("err = %v", err)
	}
	if err := svc.CheckQuery(id, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckQuery(id, "DROP TABLE t"); !errors.Is(err, export.ErrNotRerunnable) {
		t.Fatal(err)
	}
	if err := svc.CheckQuery("nope", "SELECT 1"); err == nil {
		t.Fatal("unknown session")
	}

	// Loaded rows.
	path = filepath.Join(dir, "rows.sql")
	if _, err := svc.Export(ctx, ExportRequest{SessionID: id, Path: path, Source: SourceRows,
		Columns: []export.Column{{Name: "a"}}, Rows: [][]values.Cell{{{K: values.Str, V: "x'y"}}},
		Options: export.Options{Format: export.FormatSQL, SQL: export.SQLOptions{Table: "query_result"}}}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `("a") VALUES`+"\n  ('x''y');") {
		t.Fatalf("rows sql = %s", data)
	}
	if _, err := svc.Export(ctx, ExportRequest{SessionID: id, Path: path, Source: "bogus"}); !errors.Is(err, export.ErrOptions) {
		t.Fatal(err)
	}
	if _, err := svc.Export(ctx, ExportRequest{SessionID: "nope"}); err == nil {
		t.Fatal("unknown session")
	}
	if svc.Cancel("none") {
		t.Fatal("cancelled a job that is not running")
	}

	// Selection text.
	path = filepath.Join(dir, "sel.tsv")
	if err := svc.SaveText(path, "a\tb\n"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "a\tb\n" {
		t.Fatalf("text = %q", data)
	}
}

func TestImportService(t *testing.T) {
	ctx := context.Background()
	csvPath := filepath.Join(t.TempDir(), "in.csv")
	_ = os.WriteFile(csvPath, []byte("id,name\n10,x\n11,y\n"), 0o600)
	req := importer.Request{Path: csvPath, Options: importer.Options{Header: true}, Schema: "main", Table: "t",
		Mapping: []importer.Mapping{{Source: 0, Column: "id"}, {Source: 1, Column: "name"}}}

	m, id := openSQLiteSession(t)
	em := &recEmitter{}
	svc := NewImportService(m, all.Registry(), em)
	p, err := svc.Preview(ctx, id, csvPath, importer.Options{Header: true}, "main", "t")
	if err != nil || len(p.Rows) != 2 || len(p.Mapping) != 2 {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	res, err := svc.Import(ctx, "j", id, req, false)
	if err != nil || !res.OK || res.Rows != 2 {
		t.Fatalf("res = %+v, %v", res, err)
	}
	// A failure is structured (line 2: duplicate key).
	res, err = svc.Import(ctx, "j", id, req, false)
	if err != nil || res.OK || res.Failure == nil || res.Failure.Line != 2 {
		t.Fatalf("res = %+v, %v", res, err)
	}
	// Other errors are Go errors.
	bad := req
	bad.Table = "nope"
	if _, err := svc.Import(ctx, "j", id, bad, false); err == nil {
		t.Fatal("unknown table")
	}
	if _, err := svc.Import(ctx, "j", "nope", req, false); err == nil {
		t.Fatal("unknown session")
	}
	if _, err := svc.Preview(ctx, "nope", csvPath, importer.Options{}, "main", "t"); err == nil {
		t.Fatal("unknown session")
	}
	if svc.Cancel("j") {
		t.Fatal("nothing running")
	}

	// Confirm mode needs confirmation; read-only refuses.
	mc, idc := openSQLiteSessionMode(t, store.SafeConfirm)
	svcC := NewImportService(mc, all.Registry(), nil)
	if res, err := svcC.Import(ctx, "", idc, req, false); err != nil || !res.NeedsConfirm {
		t.Fatalf("confirm: %+v, %v", res, err)
	}
	if res, err := svcC.Import(ctx, "", idc, req, true); err != nil || !res.OK {
		t.Fatalf("confirmed: %+v, %v", res, err)
	}
	mr, idr := openSQLiteSessionMode(t, store.SafeReadOnly)
	if _, err := NewImportService(mr, all.Registry(), nil).Import(ctx, "", idr, req, true); !errors.Is(err, importer.ErrReadOnly) {
		t.Fatalf("readonly: %v", err)
	}
}

func TestSettingsService(t *testing.T) {
	dir := t.TempDir()
	svc := NewSettingsService(settings.New(dir))
	st := svc.Get()
	if st.Error != "" || st.Settings != settings.Defaults() || svc.Defaults() != settings.Defaults() {
		t.Fatalf("get = %+v", st)
	}
	v := st.Settings
	v.Theme = settings.ThemeLight
	saved, err := svc.Save(v)
	if err != nil || saved.Theme != settings.ThemeLight || svc.Get().Settings.Theme != settings.ThemeLight {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	_ = os.WriteFile(filepath.Join(dir, settings.FileName), []byte("{bad"), 0o600)
	if st := svc.Get(); st.Error == "" || st.Settings != settings.Defaults() {
		t.Fatalf("corrupt = %+v", st)
	}
}
