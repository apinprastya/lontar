package sqlite

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dbedit/internal/driver"
)

func TestMetadata(t *testing.T) {
	d := New()
	if d.Kind() != driver.SQLite || d.DisplayName() != "SQLite" || d.DefaultPort() != 0 {
		t.Fatalf("metadata: %s %s %d", d.Kind(), d.DisplayName(), d.DefaultPort())
	}
}

func TestResolvePath(t *testing.T) {
	d := &Driver{homeDir: func() (string, error) { return filepath.FromSlash("/home/me"), nil }}
	got, err := d.ResolvePath("~/data/x.db")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(filepath.FromSlash("/home/me/data/x.db"))
	if got != want {
		t.Errorf("ResolvePath = %q, want %q", got, want)
	}
	if _, err := d.ResolvePath("  "); err == nil {
		t.Error("empty path should fail")
	}
	rel, _ := d.ResolvePath("rel.db")
	if !filepath.IsAbs(rel) {
		t.Errorf("%q is not absolute", rel)
	}
	bad := &Driver{homeDir: func() (string, error) { return "", errors.New("no home") }}
	if _, err := bad.ResolvePath("~"); err == nil {
		t.Error("home dir error should propagate")
	}
}

func TestBuildDSNModes(t *testing.T) {
	d := New()
	dir := t.TempDir()
	path := filepath.Join(dir, `we?ird #name %25.db`)
	cases := []struct {
		cfg  driver.Config
		mode string
	}{
		{driver.Config{FilePath: path}, "rw"},
		{driver.Config{FilePath: path, CreateIfMissing: true}, "rwc"},
		{driver.Config{FilePath: path, ReadOnly: true, CreateIfMissing: true}, "ro"},
	}
	for _, tc := range cases {
		dsn, err := d.BuildDSN(tc.cfg, "ignored")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(dsn, "file:") {
			t.Fatalf("dsn %q is not a file: URI", dsn)
		}
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		if u.Query().Get("mode") != tc.mode {
			t.Errorf("mode = %q, want %q", u.Query().Get("mode"), tc.mode)
		}
		if got := filepath.FromSlash(strings.TrimPrefix(u.Path, pathPrefix())); got != path {
			t.Errorf("path round trip = %q, want %q", got, path)
		}
		if !strings.Contains(dsn, "busy_timeout") {
			t.Errorf("dsn %q lacks busy_timeout", dsn)
		}
	}
}

// pathPrefix is the extra leading slash added before Windows drive letters.
func pathPrefix() string {
	if runtime.GOOS == "windows" {
		return "/"
	}
	return ""
}

func TestFileURIWindowsStyle(t *testing.T) {
	if got := fileURI("C:/data/a b.db"); got != "file:///C:/data/a%20b.db" {
		t.Errorf("fileURI = %q", got)
	}
}

func TestBuildDSNErrors(t *testing.T) {
	d := New()
	if _, err := d.BuildDSN(driver.Config{}, ""); err == nil {
		t.Error("missing path should fail")
	}
	_, err := d.BuildDSN(driver.Config{FilePath: "x.db", Options: map[string]string{"mode": "rwc"}}, "")
	if !errors.Is(err, driver.ErrReservedOption) {
		t.Errorf("err = %v", err)
	}
	if _, err := d.Open(context.Background(), driver.Config{}, ""); err == nil {
		t.Error("Open with missing path should fail")
	}
}

func TestOpenCreateReadOnly(t *testing.T) {
	ctx := context.Background()
	d := New()
	path := filepath.Join(t.TempDir(), "sub dir?", "test.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	// Missing file without CreateIfMissing is an error.
	if _, err := d.Open(ctx, driver.Config{FilePath: path}, ""); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected does-not-exist error, got %v", err)
	}

	// Create it, with an extra option.
	db, err := d.Open(ctx, driver.Config{FilePath: path, CreateIfMissing: true, Options: map[string]string{"_pragma": "journal_mode(WAL)"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	v, err := d.ServerVersion(ctx, db)
	if err != nil || !strings.HasPrefix(v, "SQLite 3.") {
		t.Fatalf("version = %q, %v", v, err)
	}
	_ = db.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file was not created at %s: %v", path, err)
	}

	// Read-only: reads work, writes fail.
	ro, err := d.Open(ctx, driver.Config{FilePath: path, ReadOnly: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	var n int
	if err := ro.QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if _, err := ro.ExecContext(ctx, "INSERT INTO t VALUES (1)"); err == nil {
		t.Fatal("write on a read-only database should fail")
	}

	// ServerVersion error on a closed DB.
	_ = ro.Close()
	if _, err := d.ServerVersion(ctx, ro); err == nil {
		t.Fatal("expected an error on a closed db")
	}
}
