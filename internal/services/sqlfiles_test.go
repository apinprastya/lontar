package services

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lontar/internal/textfile"
)

// sqlDialogs answers the file dialogs with fixed paths ("" = cancelled).
type sqlDialogs struct {
	open, save         string
	err                error
	title, suggested   string
	filters            []FileFilter
	openCalls, saveCnt int
}

func (d *sqlDialogs) OpenFile(title string, filters []FileFilter) (string, error) {
	d.title, d.filters = title, filters
	d.openCalls++
	return d.open, d.err
}

func (d *sqlDialogs) SaveFile(title, filename string, filters []FileFilter) (string, error) {
	d.title, d.suggested, d.filters = title, filename, filters
	d.saveCnt++
	return d.save, d.err
}

func fileCode(err error) string {
	var fe *textfile.FileError
	if errors.As(err, &fe) {
		return fe.Code
	}
	return ""
}

func TestOpenSQLFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "report.sql")
	if err := os.WriteFile(p, []byte("SELECT 1;\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := &sqlDialogs{open: p}
	svc := NewSQLFileService(d)
	f, err := svc.OpenSQLFile()
	if err != nil || f == nil || f.Text != "SELECT 1;\n" || f.EOL != "\r\n" || f.Name != "report.sql" {
		t.Fatalf("open = %+v, %v", f, err)
	}
	if d.title != "Open SQL file" || len(d.filters) != 2 || d.filters[0].Pattern != "*.sql" || d.filters[1].Pattern != "*" {
		t.Fatalf("dialog = %q %+v", d.title, d.filters)
	}
	// Cancelled.
	d.open = ""
	if f, err := svc.OpenSQLFile(); f != nil || err != nil {
		t.Fatalf("cancelled = %+v, %v", f, err)
	}
	// Dialog failure.
	d.err = errors.New("no display")
	if _, err := svc.OpenSQLFile(); err == nil || err.Error() != "no display" {
		t.Fatalf("err = %v", err)
	}
	// Unreadable choice.
	d.err, d.open = nil, filepath.Join(dir, "gone.sql")
	if _, err := svc.OpenSQLFile(); fileCode(err) != textfile.CodeNotFound {
		t.Fatalf("err = %v", err)
	}
	if _, err := NewSQLFileService(nil).OpenSQLFile(); !errors.Is(err, ErrNoDialogs) {
		t.Fatalf("err = %v", err)
	}
	if f, err := svc.ReadSQLFile(p); err != nil || f.Path != p {
		t.Fatalf("read = %+v, %v", f, err)
	}
}

func TestSaveSQLFile(t *testing.T) {
	dir := t.TempDir()
	d := &sqlDialogs{save: filepath.Join(dir, "new")}
	svc := NewSQLFileService(d)

	// Save as: the dialog suggests the tab title; .sql is appended.
	f, err := svc.SaveSQLFile("", "Query 1", "SELECT 1;\n", "\n", 12345)
	if err != nil || f == nil || f.Path != filepath.Join(dir, "new.sql") || f.Name != "new.sql" {
		t.Fatalf("save as = %+v, %v", f, err)
	}
	if d.title != "Save SQL file" || d.suggested != "Query 1.sql" {
		t.Fatalf("dialog = %q %q", d.title, d.suggested)
	}
	if data, _ := os.ReadFile(f.Path); string(data) != "SELECT 1;\n" {
		t.Fatalf("data = %q", data)
	}

	// Save to the known path with the CRLF of the original.
	f2, err := svc.SaveSQLFile(f.Path, "", "SELECT 2;\n", "\r\n", f.ModTime)
	if err != nil || f2.EOL != "\r\n" {
		t.Fatalf("save = %+v, %v", f2, err)
	}
	if data, _ := os.ReadFile(f.Path); string(data) != "SELECT 2;\r\n" {
		t.Fatalf("data = %q", data)
	}
	if d.saveCnt != 1 {
		t.Fatal("save with a path asked for a file")
	}

	// Changed on disk since it was read.
	later := time.UnixMilli(f2.ModTime).Add(3 * time.Second)
	_ = os.Chtimes(f.Path, later, later)
	if _, err := svc.SaveSQLFile(f.Path, "", "lost", "\n", f2.ModTime); fileCode(err) != textfile.CodeChangedOnDisk {
		t.Fatalf("err = %v", err)
	}
	if f3, err := svc.SaveSQLFile(f.Path, "", "forced", "\n", 0); err != nil || f3.Text != "forced" {
		t.Fatalf("overwrite = %+v, %v", f3, err)
	}

	// Save as onto a file that is newer than the tab's: the user chose it.
	d.save = f.Path
	if _, err := svc.SaveSQLFile("", "x", "as", "\n", 1); err != nil {
		t.Fatalf("save as over existing: %v", err)
	}

	// Cancelled and failing dialogs.
	d.save = ""
	if f, err := svc.SaveSQLFile("", "x", "y", "\n", 0); f != nil || err != nil {
		t.Fatalf("cancelled = %+v, %v", f, err)
	}
	d.err = errors.New("boom")
	if _, err := svc.SaveSQLFile("", "x", "y", "\n", 0); err == nil {
		t.Fatal("dialog error lost")
	}
	if _, err := NewSQLFileService(nil).SaveSQLFile("", "x", "y", "\n", 0); !errors.Is(err, ErrNoDialogs) {
		t.Fatalf("err = %v", err)
	}
	// Without dialogs a known path still saves.
	if _, err := NewSQLFileService(nil).SaveSQLFile(filepath.Join(dir, "plain.sql"), "", "y", "\n", 0); err != nil {
		t.Fatal(err)
	}
	// A write error.
	if _, err := svc.SaveSQLFile(filepath.Join(dir, "no", "dir.sql"), "", "y", "\n", 0); err == nil {
		t.Fatal("write into a missing directory succeeded")
	}
}

func TestSuggestedFileName(t *testing.T) {
	for in, want := range map[string]string{
		"Query 1":        "Query 1.sql",
		"  a/b:c*?  ":    "a_b_c__.sql",
		"report.SQL":     "report.SQL",
		"":               "query.sql",
		"...":            "query.sql",
		"tab\twith\nctl": "tab_with_ctl.sql",
	} {
		if got := suggestedFileName(in); got != want {
			t.Errorf("suggestedFileName(%q) = %q, want %q", in, got, want)
		}
	}
}
