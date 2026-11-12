package settings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsAndRoundTrip(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "cfg"))
	got, err := s.Load()
	if err != nil || got != Defaults() {
		t.Fatalf("load missing = %+v, %v", got, err)
	}
	if d := Defaults(); d.GridPageSize != 300 || d.Theme != ThemeSystem || d.CSV.Delimiter != "," || !d.CSV.Header || d.CSV.CRLF {
		t.Fatalf("defaults = %+v", d)
	}
	v := Defaults()
	v.Theme = ThemeLight
	v.EditorFontSize = 15
	v.GridPageSize = 500
	v.EditorMaxRows = 2000
	v.ConfirmCloseDirtyQuery = true
	v.CSV = CSV{Delimiter: ";", Header: false, Quote: "'", Null: NullCustom, NullText: "NULL", CRLF: true, BOM: true}
	saved, err := s.Save(v)
	if err != nil || saved != v {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	got, err = s.Load()
	if err != nil || got != v {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	st, _ := os.Stat(s.Path())
	if st.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Fatalf("mode = %v", st.Mode())
	}
}

func TestNormalize(t *testing.T) {
	v := Settings{Theme: "neon", EditorFontSize: 2, GridPageSize: 999999, EditorMaxRows: -5,
		CSV: CSV{Delimiter: "ab", Quote: "", Null: "?"}}
	v.Normalize()
	if v.Theme != ThemeSystem || v.EditorFontSize != MinFontSize || v.GridPageSize != MaxPageSize || v.EditorMaxRows != MinMaxRows ||
		v.CSV.Delimiter != "," || v.CSV.Quote != `"` || v.CSV.Null != NullEmpty || v.Version != CurrentVersion {
		t.Fatalf("normalized = %+v", v)
	}
	// Quote equal to the delimiter.
	v.CSV.Delimiter, v.CSV.Quote = `"`, `"`
	v.Normalize()
	if v.CSV.Delimiter != "," || v.CSV.Quote != `"` {
		t.Fatalf("csv = %+v", v.CSV)
	}
	v.CSV.Delimiter = "\n"
	v.Normalize()
	if v.CSV.Delimiter != "," {
		t.Fatal("newline delimiter kept")
	}
}

func TestVersionsAndErrors(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(s.Path(), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Missing fields take defaults; out-of-range values are clamped.
	write(`{"version":1,"theme":"dark","gridPageSize":5}`)
	got, err := s.Load()
	if err != nil || got.Theme != ThemeDark || got.GridPageSize != MinPageSize || got.EditorMaxRows != 1000 || !got.CSV.Header {
		t.Fatalf("partial = %+v, %v", got, err)
	}
	write(`{"version":2}`)
	if _, err := s.Load(); !errors.Is(err, ErrNewerVersion) {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Save(Defaults()); !errors.Is(err, ErrNewerVersion) {
		t.Fatalf("save over newer: %v", err)
	}
	if data, _ := os.ReadFile(s.Path()); !strings.Contains(string(data), `"version":2`) {
		t.Fatal("newer file overwritten")
	}
	write(`{}`)
	if _, err := s.Load(); err == nil || !strings.Contains(err.Error(), "invalid version") {
		t.Fatalf("err = %v", err)
	}
	write(`{nope`)
	if got, err := s.Load(); err == nil || !strings.Contains(err.Error(), "corrupt") || got != Defaults() {
		t.Fatalf("corrupt = %+v, %v", got, err)
	}
	// A corrupt file can be replaced.
	if _, err := s.Save(Defaults()); err != nil {
		t.Fatal(err)
	}
	// Read error (a directory in place of the file).
	bad := New(t.TempDir())
	_ = os.Mkdir(bad.Path(), 0o700)
	if _, err := bad.Load(); err == nil || !strings.Contains(err.Error(), "read settings") {
		t.Fatalf("err = %v", err)
	}
	if _, err := bad.Save(Defaults()); err == nil {
		t.Fatal("save over a directory succeeded")
	}
}
