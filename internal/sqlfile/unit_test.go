package sqlfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lontar/internal/driver"
	"lontar/internal/driver/sqlite"
	"lontar/internal/testutil"
)

func TestPreview(t *testing.T) {
	if got := Preview("  SELECT\n\t a,\r\n  b  FROM t  "); got != "SELECT a, b FROM t" {
		t.Fatalf("%q", got)
	}
	long := "INSERT INTO t VALUES ('" + strings.Repeat("é", 300) + "')"
	got := Preview(long)
	if n := len([]rune(got)); n != PreviewChars+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("len %d %q", n, got)
	}
	if Preview("") != "" {
		t.Fatal("empty")
	}
}

func TestDetectDumpAndMismatch(t *testing.T) {
	tests := []struct{ head, want string }{
		{"--\n-- PostgreSQL database dump\n--\n", DumpPostgres},
		{"-- MySQL dump 10.13  Distrib 8.0", DumpMySQL},
		{"/*M!999999\\- enable the sandbox mode */\n-- MariaDB dump 10.20", DumpMariaDB},
		{"\xef\xbb\xbfPRAGMA foreign_keys=OFF;\nBEGIN TRANSACTION;\n", DumpSQLite},
		{"PRAGMA foreign_keys=OFF;\nCREATE TABLE t(a);", DumpPlain},
		{"SELECT 1;", DumpPlain},
	}
	for _, tc := range tests {
		if got := DetectDump([]byte(tc.head)); got != tc.want {
			t.Errorf("DetectDump(%q) = %q", tc.head, got)
		}
	}
	if mismatch(DumpPostgres, driver.Postgres) != "" || mismatch(DumpPlain, driver.MySQL) != "" || mismatch(DumpMariaDB, driver.MySQL) != "" {
		t.Fatal("false mismatch")
	}
	for dump, kind := range map[string]driver.Kind{DumpPostgres: driver.MySQL, DumpMySQL: driver.SQLite, DumpSQLite: driver.MariaDB, DumpMariaDB: driver.Postgres} {
		if w := mismatch(dump, kind); !strings.Contains(w, dump) || !strings.Contains(w, kindName(kind)) {
			t.Errorf("mismatch(%s, %s) = %q", dump, kind, w)
		}
	}
	if kindName("other") != "other" {
		t.Fatal("kindName")
	}
}

func TestTxControlAndMeta(t *testing.T) {
	for text, want := range map[string]bool{
		"BEGIN": true, "begin transaction": true, "START TRANSACTION": true, "START SLAVE": false, "COMMIT": true,
		"END": true, "ROLLBACK": true, "ROLLBACK TO SAVEPOINT a": false, "ROLLBACK WORK TO a": false, "SAVEPOINT a": false,
		"INSERT INTO t VALUES (1)": false, "-- only a comment": false,
	} {
		if got := isTxControl(driver.Postgres, text); got != want {
			t.Errorf("isTxControl(%q) = %v", text, got)
		}
	}
	for text, want := range map[string]bool{`\restrict abc`: true, `\unrestrict abc`: true, `\connect db`: false, `\c db`: false, `\SET x 1`: true, `\i file.sql`: false} {
		if got := ignoredMeta(text); got != want {
			t.Errorf("ignoredMeta(%q) = %v", text, got)
		}
	}
}

func TestOpenErrors(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	gz := filepath.Join(dir, "dump.sql.gz")
	if err := os.WriteFile(gz, []byte{0x1f, 0x8b, 8, 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(ctx, driver.SQLite, gz); !errors.Is(err, ErrCompressed) {
		t.Fatal(err)
	}
	if _, err := Inspect(ctx, driver.SQLite, dir); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatal(err)
	}
	if _, err := Inspect(ctx, driver.SQLite, filepath.Join(dir, "missing.sql")); err == nil {
		t.Fatal("missing")
	}
	db := testutil.OpenDriver(t, sqlite.New(), testutil.SQLiteTemp(t), false)
	if _, err := Run(ctx, db, driver.SQLite, gz, Options{}, nil); !errors.Is(err, ErrCompressed) {
		t.Fatal(err)
	}
	if _, err := Scan(ctx, driver.SQLite, gz, 1, false, nil); !errors.Is(err, ErrCompressed) {
		t.Fatal(err)
	}
	// A cancelled scan and inspect.
	small := writeScript(t, "SELECT 1;")
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Scan(cctx, driver.SQLite, small, 1, false, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Inspect(cctx, driver.SQLite, small); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Binary content is flagged.
	bin := filepath.Join(dir, "x.sql")
	if err := os.WriteFile(bin, []byte("SELECT 1;\x00\x01"), 0o600); err != nil {
		t.Fatal(err)
	}
	if info, err := Inspect(ctx, driver.SQLite, bin); err != nil || !strings.Contains(info.Warning, "text") {
		t.Fatal(info, err)
	}
}

func TestInspectEstimate(t *testing.T) {
	// A file larger than InspectBytes: the count is extrapolated.
	line := "INSERT INTO t VALUES (1, 'some text to make the line longer');\n"
	n := InspectBytes/len(line) + 50000
	path := filepath.Join(t.TempDir(), "big.sql")
	if err := os.WriteFile(path, []byte(strings.Repeat(line, n)), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := Inspect(context.Background(), driver.MySQL, path)
	if err != nil || info.Exact || info.Name != "big.sql" {
		t.Fatal(info, err)
	}
	if diff := info.Statements - int64(n); diff < -int64(n)/100 || diff > int64(n)/100 {
		t.Fatalf("estimate %d for %d", info.Statements, n)
	}
}

func TestRunLongStatements(t *testing.T) {
	db := testutil.OpenDriver(t, sqlite.New(), testutil.SQLiteTemp(t), false)
	ctx := context.Background()
	// A failing statement longer than MaxErrorSQL keeps only its preview;
	// the engine message does not carry the statement.
	long := "SELECT * FROM missing_table WHERE a = '" + strings.Repeat("x", MaxErrorSQL) + "'"
	res, err := Run(ctx, db, driver.SQLite, writeScript(t, "\xef\xbb\xbfSELECT 1;\n"+long+";"), Options{ContinueOnError: true}, nil)
	if err != nil || res.Run != 1 || res.Failed != 1 || res.Errors[0].SQL != "" || res.Errors[0].Line != 2 || len([]rune(res.Errors[0].Preview)) > PreviewChars+1 {
		t.Fatalf("%+v %v", res, err)
	}
	if strings.Contains(res.Errors[0].Message, strings.Repeat("x", 300)) {
		t.Fatal("message carries the statement")
	}
	// A statement over the 64 MiB limit stops the run, even with
	// continue on error, and rolls a single transaction back.
	path := filepath.Join(t.TempDir(), "huge.sql")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("CREATE TABLE h (a TEXT);\nINSERT INTO h VALUES ('")
	chunk := strings.Repeat("y", 1<<20)
	for i := 0; i < 65; i++ {
		_, _ = f.WriteString(chunk)
	}
	_, _ = f.WriteString("');\nINSERT INTO h VALUES ('after');\n")
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []Options{{ContinueOnError: true}, {SingleTransaction: true}} {
		res, err = Run(ctx, db, driver.SQLite, path, opts, nil)
		if err != nil || !res.Stopped || res.Failed != 1 || !strings.Contains(res.Errors[0].Message, "line 2") || !strings.Contains(res.Errors[0].Message, "64 MiB") {
			t.Fatalf("%+v %v", res, err)
		}
		if res.RolledBack != opts.SingleTransaction {
			t.Fatalf("%+v", res)
		}
		db.Exec("DROP TABLE IF EXISTS h")
	}
}
