package textfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func code(err error) string {
	var fe *FileError
	if errors.As(err, &fe) {
		return fe.Code
	}
	return ""
}

func TestReadLF(t *testing.T) {
	p := filepath.Join(t.TempDir(), "q.sql")
	write(t, p, []byte("SELECT 1;\nSELECT 'ü';\n"))
	f, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if f.Text != "SELECT 1;\nSELECT 'ü';\n" || f.EOL != LF || f.Name != "q.sql" || f.Path != p || f.ModTime != fi.ModTime().UnixMilli() {
		t.Fatalf("file = %+v", f)
	}
}

func TestReadBOMAndCRLFRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "win.sql")
	write(t, p, append([]byte{0xEF, 0xBB, 0xBF}, "SELECT 1;\r\nSELECT 2;\r\n"...))
	f, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if f.Text != "SELECT 1;\nSELECT 2;\n" || f.EOL != CRLF {
		t.Fatalf("file = %+v", f)
	}
	saved, err := Write(p, f.Text+"SELECT 3;\n", f.EOL, f.ModTime)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "SELECT 1;\r\nSELECT 2;\r\nSELECT 3;\r\n" {
		t.Fatalf("saved = %q", data)
	}
	if saved.EOL != CRLF || saved.Text != "SELECT 1;\nSELECT 2;\nSELECT 3;\n" || saved.Name != "win.sql" {
		t.Fatalf("saved = %+v", saved)
	}
	// Text that already has CRLF is not doubled.
	if _, err := Write(p, "a\r\nb", CRLF, 0); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(p); string(data) != "a\r\nb" {
		t.Fatalf("saved = %q", data)
	}
}

func TestReadErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := Read(filepath.Join(dir, "missing.sql")); code(err) != CodeNotFound {
		t.Fatalf("missing: %v", err)
	}
	if _, err := Read(dir); code(err) != CodeNotFound || !strings.Contains(err.Error(), "folder") {
		t.Fatalf("dir: %v", err)
	}
	bin := filepath.Join(dir, "bin.sql")
	write(t, bin, []byte("SELECT\x00secret"))
	_, err := Read(bin)
	if code(err) != CodeNotText {
		t.Fatalf("nul: %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("error contains file contents")
	}
	write(t, bin, []byte{0xff, 0xfe, 'a'})
	if _, err := Read(bin); code(err) != CodeNotText {
		t.Fatalf("latin: %v", err)
	}
	big := filepath.Join(dir, "big.sql")
	write(t, big, []byte(strings.Repeat("x", MaxSize+1)))
	if _, err := Read(big); code(err) != CodeTooLarge || !strings.Contains(err.Error(), "10 MiB") {
		t.Fatalf("big: %v", err)
	}
	write(t, big, []byte(strings.Repeat("x", MaxSize)))
	if _, err := Read(big); err != nil {
		t.Fatalf("exactly the limit: %v", err)
	}
	var fe *FileError
	if !errors.As(func() error { _, err := Read(bin); return err }(), &fe) || fe.Kind != "fileError" || fe.Path != bin {
		t.Fatalf("file error = %+v", fe)
	}
}

func TestReadUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permissions")
	}
	p := filepath.Join(t.TempDir(), "locked.sql")
	write(t, p, []byte("x"))
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(p); err == nil || code(err) != "" {
		t.Fatalf("err = %v", err)
	}
	// A directory that cannot be searched: Stat fails.
	d := filepath.Join(t.TempDir(), "d")
	_ = os.Mkdir(d, 0o700)
	write(t, filepath.Join(d, "f.sql"), []byte("x"))
	_ = os.Chmod(d, 0)
	defer os.Chmod(d, 0o700)
	if _, err := Read(filepath.Join(d, "f.sql")); err == nil || code(err) != "" {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteChangedOnDisk(t *testing.T) {
	p := filepath.Join(t.TempDir(), "q.sql")
	write(t, p, []byte("old"))
	f, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	// Someone else saves later.
	later := time.UnixMilli(f.ModTime).Add(2 * time.Second)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(p, "mine", LF, f.ModTime); code(err) != CodeChangedOnDisk || strings.Contains(err.Error(), "mine") {
		t.Fatalf("err = %v", err)
	}
	if data, _ := os.ReadFile(p); string(data) != "old" {
		t.Fatalf("overwritten: %q", data)
	}
	// Overwrite: expected 0.
	if _, err := Write(p, "mine", LF, 0); err != nil {
		t.Fatal(err)
	}
	// The file was deleted meanwhile: written again.
	_ = os.Remove(p)
	if _, err := Write(p, "again", "weird", f.ModTime); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(p); string(data) != "again" {
		t.Fatalf("data = %q", data)
	}
}

func TestWriteKeepsPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissions")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "new.sql")
	if _, err := Write(p, "x", LF, 0); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
		t.Fatalf("new mode = %v", fi.Mode())
	}
	_ = os.Chmod(p, 0o640)
	if _, err := Write(p, "y", LF, 0); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Fatalf("kept mode = %v", fi.Mode())
	}
}

func TestWriteErrors(t *testing.T) {
	if _, err := Write(filepath.Join(t.TempDir(), "no", "such", "dir.sql"), "x", LF, 0); err == nil || !strings.Contains(err.Error(), "save dir.sql") {
		t.Fatalf("err = %v", err)
	}
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string]string{"": LF, "a": LF, "a\nb\r\n": LF, "a\r\nb\n": CRLF, "\nx": LF} {
		if got := DetectEOL(in); got != want {
			t.Errorf("DetectEOL(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]string{"": "", "/a/q": "/a/q.sql", "/a/q.sql": "/a/q.sql", "/a/q.txt": "/a/q.txt", "/a.b/q": "/a.b/q.sql"} {
		if got := WithSQLExtension(in); got != want {
			t.Errorf("WithSQLExtension(%q) = %q", in, got)
		}
	}
}
