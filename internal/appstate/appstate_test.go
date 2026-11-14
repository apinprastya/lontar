package appstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	s := New(dir)
	if _, ok := s.Window(); ok || s.Err() != nil {
		t.Fatalf("empty store has a window or error: %v", s.Err())
	}
	if err := s.SaveWindow(Window{}); err != nil {
		t.Fatalf("invalid window: %v", err)
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Fatalf("invalid window was written: %v", err)
	}
	w := Window{X: 10, Y: 20, Width: 1300, Height: 800, Positioned: true, Maximised: true}
	if err := s.SaveWindow(w); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveWindow(w); err != nil { // unchanged: no write needed
		t.Fatal(err)
	}
	got, ok := New(dir).Window()
	if !ok || got != w {
		t.Fatalf("reloaded window = %+v, %v", got, ok)
	}
}

func TestUI(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := s.SetUI("a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUI("a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUI("b", `{"x":2}`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUI("missing", ""); err != nil {
		t.Fatal(err)
	}
	ui := New(dir).UI()
	if len(ui) != 2 || ui["a"] != "1" || ui["b"] != `{"x":2}` {
		t.Fatalf("ui = %v", ui)
	}
	ui["a"] = "changed" // a copy
	if s.UI()["a"] != "1" {
		t.Fatal("UI returned the internal map")
	}
	if err := s.SetUI("a", ""); err != nil {
		t.Fatal(err)
	}
	if ui := New(dir).UI(); len(ui) != 1 {
		t.Fatalf("after delete = %v", ui)
	}
}

func TestUILimits(t *testing.T) {
	s := New(t.TempDir())
	if err := s.SetUI("", "x"); !errors.Is(err, ErrKey) {
		t.Fatalf("empty key: %v", err)
	}
	if err := s.SetUI(strings.Repeat("k", MaxKeyLen+1), "x"); !errors.Is(err, ErrKey) {
		t.Fatalf("long key: %v", err)
	}
	if err := s.SetUI("k", strings.Repeat("v", MaxValueLen+1)); !errors.Is(err, ErrValue) {
		t.Fatalf("long value: %v", err)
	}
	s.ensure()
	s.state.UI = map[string]string{}
	for i := range MaxKeys {
		s.state.UI[fmt.Sprintf("k%d", i)] = "v"
	}
	if err := s.SetUI("new-key", "v"); !errors.Is(err, ErrFull) {
		t.Fatalf("full: %v", err)
	}
	if err := s.SetUI("k7", "updated"); err != nil {
		t.Fatalf("updating an existing key when full: %v", err)
	}
}

func TestCorruptAndInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	_ = os.WriteFile(path, []byte("{bad"), 0o600)
	s := New(dir)
	if _, ok := s.Window(); ok || s.Err() == nil {
		t.Fatalf("corrupt file: window=%v err=%v", ok, s.Err())
	}
	if err := s.SetUI("a", "1"); err != nil { // a corrupt file is replaced
		t.Fatal(err)
	}
	if New(dir).UI()["a"] != "1" {
		t.Fatal("corrupt file was not replaced")
	}

	_ = os.WriteFile(path, []byte(`{"version":1,"window":{"width":0,"height":5}}`), 0o600)
	if _, ok := New(dir).Window(); ok {
		t.Fatal("window without a size was accepted")
	}

	// An unreadable path (a directory) is an error, not a crash.
	bad := t.TempDir()
	_ = os.Mkdir(filepath.Join(bad, FileName), 0o700)
	if New(bad).Err() == nil {
		t.Fatal("reading a directory succeeded")
	}
}

func TestNewerVersionIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	newer := []byte(`{"version":99,"ui":{"a":"1"}}`)
	_ = os.WriteFile(path, newer, 0o600)
	s := New(dir)
	if !errors.Is(s.Err(), ErrNewerVersion) || len(s.UI()) != 0 {
		t.Fatalf("newer file: err=%v ui=%v", s.Err(), s.UI())
	}
	if err := s.SetUI("b", "2"); !errors.Is(err, ErrNewerVersion) {
		t.Fatalf("set on newer file: %v", err)
	}
	if err := s.SaveWindow(Window{Width: 1, Height: 1}); !errors.Is(err, ErrNewerVersion) {
		t.Fatalf("save window on newer file: %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != string(newer) {
		t.Fatalf("newer file was overwritten: %s", data)
	}
}
