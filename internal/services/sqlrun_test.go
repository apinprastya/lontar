package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"lontar/internal/sqlfile"
	"lontar/internal/store"
)

type runEmitter struct {
	mu     sync.Mutex
	events []SQLRunProgress
}

func (r *runEmitter) Emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := data.(SQLRunProgress); ok && name == EventSQLRunProgress {
		r.events = append(r.events, p)
	}
}

func sqlScript(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "run.sql")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSQLRunService(t *testing.T) {
	m, id := openSQLiteSession(t)
	em := &runEmitter{}
	path := sqlScript(t, "-- MySQL dump 10.13\nINSERT INTO t VALUES (10, 'x');\nINSERT INTO t VALUES (11, 'y');\nSELECT 1;\n")
	d := &sqlDialogs{open: path}
	svc := NewSQLRunService(m, d, em)
	ctx := context.Background()

	info, err := svc.PickAndInspect(ctx, id)
	if err != nil || info == nil || info.Statements != 3 || info.DumpType != sqlfile.DumpMySQL || info.Warning == "" || d.title != "Run SQL file" {
		t.Fatalf("inspect %+v %v", info, err)
	}
	d.open = ""
	if info, err := svc.PickAndInspect(ctx, id); err != nil || info != nil {
		t.Fatal("cancelled dialog", info, err)
	}
	if _, err := NewSQLRunService(m, nil, nil).PickAndInspect(ctx, id); !errors.Is(err, ErrNoDialogs) {
		t.Fatal(err)
	}
	if _, err := svc.Inspect(ctx, "nope", path); err == nil {
		t.Fatal("unknown session")
	}

	res, err := svc.RunFile(ctx, "job1", id, path, sqlfile.Options{SingleTransaction: true}, false)
	if err != nil || res.NeedsConfirm || res.Result == nil || res.Result.Run != 3 || res.Result.Failed != 0 {
		t.Fatalf("run %+v %v", res, err)
	}
	if len(em.events) == 0 || em.events[0].JobID != "job1" || em.events[0].Phase != "run" {
		t.Fatalf("events %+v", em.events)
	}
	// Failures are part of the result.
	res, err = svc.RunFile(ctx, "job2", id, path, sqlfile.Options{SingleTransaction: true}, false)
	if err != nil || res.Result.Failed != 1 || !res.Result.RolledBack || !strings.Contains(res.Result.Errors[0].Message, "UNIQUE") {
		t.Fatalf("run %+v %v", res, err)
	}
	if _, err := svc.RunFile(ctx, "", id, path, sqlfile.Options{SingleTransaction: true, ContinueOnError: true}, false); !errors.Is(err, sqlfile.ErrOptions) {
		t.Fatal(err)
	}
	if _, err := svc.RunFile(ctx, "", "nope", path, sqlfile.Options{}, false); err == nil {
		t.Fatal("unknown session")
	}
	if _, err := svc.RunFile(ctx, "", id, filepath.Join(t.TempDir(), "missing.sql"), sqlfile.Options{}, false); err == nil {
		t.Fatal("missing file")
	}
	if svc.Cancel("nothing") {
		t.Fatal("cancelled a job that does not run")
	}
}

func TestSQLRunServiceSafeModes(t *testing.T) {
	ctx := context.Background()
	writes := sqlScript(t, "SELECT 1;\nUPDATE t SET name = 'z' WHERE id = 1;\nDELETE FROM t WHERE id = 3;\n")
	reads := sqlScript(t, "SELECT 1;\nSELECT name FROM t;\n")

	// Read-only: refused before anything runs.
	m, id := openSQLiteSessionMode(t, store.SafeReadOnly)
	svc := NewSQLRunService(m, nil, &runEmitter{})
	_, err := svc.RunFile(ctx, "r1", id, writes, sqlfile.Options{}, true)
	var ro *sqlfile.ReadOnlyError
	if !errors.As(err, &ro) || ro.Line != 2 || !strings.HasPrefix(ro.Preview, "UPDATE t") {
		t.Fatalf("readonly %v", err)
	}
	if res, err := svc.RunFile(ctx, "r2", id, reads, sqlfile.Options{}, false); err != nil || res.Result.Run != 2 {
		t.Fatalf("readonly reads %+v %v", res, err)
	}

	// Confirm: the writes are listed, nothing runs until confirmed.
	m, id = openSQLiteSessionMode(t, store.SafeConfirm)
	em := &runEmitter{}
	svc = NewSQLRunService(m, nil, em)
	res, err := svc.RunFile(ctx, "c1", id, writes, sqlfile.Options{}, false)
	if err != nil || !res.NeedsConfirm || res.Writes != 2 || len(res.WriteStatements) != 2 || res.Result != nil || res.WriteStatements[1].Line != 3 {
		t.Fatalf("confirm %+v %v", res, err)
	}
	sess, _ := m.Get(id)
	var name string
	if err := sess.DB.QueryRow("SELECT name FROM t WHERE id = 1").Scan(&name); err != nil || name != "a" {
		t.Fatalf("ran before confirming: %q %v", name, err)
	}
	if len(em.events) == 0 || em.events[0].Phase != "scan" {
		t.Fatalf("events %+v", em.events)
	}
	res, err = svc.RunFile(ctx, "c2", id, writes, sqlfile.Options{}, true)
	if err != nil || res.NeedsConfirm || res.Result.Run != 3 {
		t.Fatalf("confirmed %+v %v", res, err)
	}
	if res, err := svc.RunFile(ctx, "c3", id, reads, sqlfile.Options{}, false); err != nil || res.NeedsConfirm || res.Result.Run != 2 {
		t.Fatalf("confirm reads %+v %v", res, err)
	}
	if _, err := svc.RunFile(ctx, "c4", id, filepath.Join(t.TempDir(), "missing.sql"), sqlfile.Options{}, false); err == nil {
		t.Fatal("missing file")
	}
}

func TestSQLRunServiceCancel(t *testing.T) {
	m, id := openSQLiteSession(t)
	var b strings.Builder
	for i := 0; i < 20000; i++ {
		b.WriteString("INSERT INTO t (name) VALUES ('n');\n")
	}
	path := sqlScript(t, b.String())
	var svc *SQLRunService
	cancelled := make(chan bool, 1)
	var once sync.Once
	svc = NewSQLRunService(m, nil, EmitterFunc(func(name string, data any) {
		once.Do(func() { cancelled <- svc.Cancel("job") })
	}))
	res, err := svc.RunFile(context.Background(), "job", id, path, sqlfile.Options{SingleTransaction: true}, false)
	if err != nil || !<-cancelled || !res.Result.Cancelled || !res.Result.RolledBack {
		t.Fatalf("%+v %v", res, err)
	}
	sess, _ := m.Get(id)
	var n int
	if err := sess.DB.QueryRow("SELECT COUNT(*) FROM t").Scan(&n); err != nil || n != 3 {
		t.Fatalf("rows after cancel %d %v", n, err)
	}
}
