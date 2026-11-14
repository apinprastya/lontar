package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"lontar/internal/appstate"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestMainWindowOptions(t *testing.T) {
	w := mainWindowOptions(appstate.Window{}, false)
	if w.Title != "Lontar" {
		t.Fatalf("Title = %q, want Lontar", w.Title)
	}
	if w.URL != "/" {
		t.Fatalf("URL = %q, want /", w.URL)
	}
	if w.Width < w.MinWidth || w.Height < w.MinHeight {
		t.Fatalf("default size %dx%d is below the minimum %dx%d", w.Width, w.Height, w.MinWidth, w.MinHeight)
	}
	if w.InitialPosition != application.WindowCentered || w.StartState != application.WindowStateNormal {
		t.Fatalf("first start is not centred and normal: %+v", w)
	}
}

func TestMainWindowOptionsRestore(t *testing.T) {
	w := mainWindowOptions(appstate.Window{X: 40, Y: 50, Width: 1400, Height: 900, Positioned: true, Maximised: true}, true)
	if w.Width != 1400 || w.Height != 900 || w.X != 40 || w.Y != 50 || w.InitialPosition != application.WindowXY || w.StartState != application.WindowStateMaximised {
		t.Fatalf("restored = %+v", w)
	}
	w = mainWindowOptions(appstate.Window{Width: 300, Height: 200}, true)
	if w.Width != minWidth || w.Height != minHeight || w.InitialPosition != application.WindowCentered || w.StartState != application.WindowStateNormal {
		t.Fatalf("small, unpositioned = %+v", w)
	}
	if w := mainWindowOptions(appstate.Window{Width: 0, Height: 10}, true); w.Width != defaultWidth {
		t.Fatalf("invalid saved size used: %+v", w)
	}
}

type fakeWindow struct {
	w, h, x, y, rx, ry              int
	maximised, minimised, fullscreen bool
}

func (f *fakeWindow) Size() (int, int)             { return f.w, f.h }
func (f *fakeWindow) Position() (int, int)         { return f.x, f.y }
func (f *fakeWindow) RelativePosition() (int, int) { return f.rx, f.ry }
func (f *fakeWindow) IsMaximised() bool            { return f.maximised }
func (f *fakeWindow) IsMinimised() bool            { return f.minimised }
func (f *fakeWindow) IsFullscreen() bool           { return f.fullscreen }

func TestWindowPosition(t *testing.T) {
	f := &fakeWindow{x: 1, y: 2, rx: 3, ry: 4}
	if x, y := windowPosition("linux", f); x != 3 || y != 4 {
		t.Fatalf("linux = %d,%d", x, y)
	}
	if x, y := windowPosition("windows", f); x != 1 || y != 2 {
		t.Fatalf("windows = %d,%d", x, y)
	}
}

func TestWindowTracker(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	st := appstate.New(dir)
	f := &fakeWindow{w: 1000, h: 700, x: 30, y: 40}
	now := time.Unix(1000, 0)
	tr := newWindowTracker(f, st, appstate.Window{Width: 1200, Height: 760})
	tr.goos = "windows"
	tr.now = func() time.Time { return now }

	tr.boundsChanged()
	want := appstate.Window{X: 30, Y: 40, Width: 1000, Height: 700, Positioned: true}
	if got := tr.current(); got != want {
		t.Fatalf("after resize = %+v", got)
	}

	// Minimised, fullscreen and zero-size bounds are ignored.
	f.minimised, f.w = true, 10
	tr.boundsChanged()
	f.minimised, f.fullscreen = false, true
	tr.boundsChanged()
	f.fullscreen, f.w, f.h = false, 0, 0
	tr.boundsChanged()
	if got := tr.current(); got != want {
		t.Fatalf("ignored changes recorded: %+v", got)
	}

	// Maximising: the platform reports the maximised size first, then the
	// maximise; the normal bounds survive.
	now = now.Add(time.Hour)
	f.w, f.h, f.x, f.y = 1920, 1080, 0, 0
	tr.boundsChanged()
	now = now.Add(100 * time.Millisecond)
	f.maximised = true
	tr.maximised(true)
	want.Maximised = true
	if got := tr.current(); got != want {
		t.Fatalf("after maximise = %+v", got)
	}
	if saved, ok := appstate.New(dir).Window(); !ok || saved != want {
		t.Fatalf("maximise not saved: %+v", saved)
	}
	tr.boundsChanged() // still maximised: bounds are not kept
	if got := tr.current(); got != want {
		t.Fatalf("maximised resize recorded: %+v", got)
	}

	// Un-maximise, then a later maximise without a fresh resize.
	f.maximised = false
	tr.maximised(false)
	want.Maximised = false
	if got := tr.current(); got != want {
		t.Fatalf("after unmaximise = %+v", got)
	}
	f.w, f.h, f.x, f.y = 900, 600, 0, 0
	tr.boundsChanged()
	now = now.Add(time.Hour)
	tr.maximised(true)
	if got := tr.current(); got.Width != 900 || got.Positioned || !got.Maximised {
		t.Fatalf("late maximise rolled back the resize: %+v", got)
	}

	tr.save()
	if saved, _ := appstate.New(dir).Window(); saved != tr.current() {
		t.Fatalf("saved = %+v", saved)
	}
}

func TestWindowTrackerSaveError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, appstate.FileName), []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := newWindowTracker(&fakeWindow{}, appstate.New(dir), appstate.Window{Width: 1, Height: 1})
	tr.save() // logged, no panic
}

func TestOnScreen(t *testing.T) {
	areas := []application.Rect{{X: 0, Y: 0, Width: 1920, Height: 1080}, {X: 1920, Y: 0, Width: 1280, Height: 1024}}
	cases := []struct {
		win  application.Rect
		want bool
	}{
		{application.Rect{X: 100, Y: 100, Width: 800, Height: 600}, true},
		{application.Rect{X: 2500, Y: 100, Width: 800, Height: 600}, true},  // second screen
		{application.Rect{X: 3150, Y: 100, Width: 800, Height: 600}, false}, // 50 px visible
		{application.Rect{X: -5000, Y: 0, Width: 800, Height: 600}, false},
		{application.Rect{X: 100, Y: 1050, Width: 800, Height: 600}, false},
	}
	for _, c := range cases {
		if got := onScreen(c.win, areas); got != c.want {
			t.Errorf("onScreen(%+v) = %v, want %v", c.win, got, c.want)
		}
	}
	if !onScreen(application.Rect{X: -5000}, nil) {
		t.Fatal("no screens known: should not move the window")
	}
}
