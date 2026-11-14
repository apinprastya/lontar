package main

import (
	"log"
	"runtime"
	"sync"
	"time"

	"lontar/internal/appstate"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Main window size: the default (first start) and the minimum.
const (
	defaultWidth, defaultHeight = 1200, 760
	minWidth, minHeight         = 800, 500
)

// maximiseRollback is how long after a resize a maximise may arrive and
// still be taken as the cause of that resize: the platforms report the new
// (maximised) size before they report the maximise itself.
const maximiseRollback = time.Second

// mainWindowOptions describes the main application window, restoring the
// size, position and maximised state saved when it was last closed.
func mainWindowOptions(saved appstate.Window, ok bool) application.WebviewWindowOptions {
	opts := application.WebviewWindowOptions{
		Title:     appName,
		Width:     defaultWidth,
		Height:    defaultHeight,
		MinWidth:  minWidth,
		MinHeight: minHeight,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 38,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(24, 25, 30),
		URL:              "/",
	}
	if !ok || !saved.Valid() {
		return opts
	}
	opts.Width = max(saved.Width, minWidth)
	opts.Height = max(saved.Height, minHeight)
	if saved.Positioned {
		// Linux places WindowXY relative to the monitor (see windowPosition);
		// Windows and macOS use absolute coordinates.
		opts.InitialPosition = application.WindowXY
		opts.X, opts.Y = saved.X, saved.Y
	}
	if saved.Maximised {
		opts.StartState = application.WindowStateMaximised
	}
	return opts
}

// windowView is the part of a Wails window the tracker reads.
type windowView interface {
	Size() (int, int)
	Position() (int, int)
	RelativePosition() (int, int)
	IsMaximised() bool
	IsMinimised() bool
	IsFullscreen() bool
}

// windowPosition returns the position to save, in the coordinates
// mainWindowOptions restores: relative to the monitor on Linux (where
// WindowXY is relative), absolute elsewhere.
func windowPosition(goos string, w windowView) (int, int) {
	if goos == "linux" {
		return w.RelativePosition()
	}
	return w.Position()
}

// windowTracker follows the main window's bounds and maximised state and
// saves them to state.json. It keeps the last normal bounds, so a window
// closed while maximised comes back maximised and un-maximises to its
// previous size.
type windowTracker struct {
	mu    sync.Mutex
	win   windowView
	store *appstate.Store
	goos  string
	now   func() time.Time

	state  appstate.Window
	prev   appstate.Window // state before the last bounds update
	prevAt time.Time
}

func newWindowTracker(win windowView, st *appstate.Store, initial appstate.Window) *windowTracker {
	return &windowTracker{win: win, store: st, goos: runtime.GOOS, now: time.Now, state: initial, prev: initial}
}

// boundsChanged records the window's bounds after a move or resize, unless
// it is maximised, minimised or fullscreen (those bounds are not kept).
func (t *windowTracker) boundsChanged() {
	if t.win.IsMinimised() || t.win.IsFullscreen() {
		return
	}
	maximised := t.win.IsMaximised()
	t.mu.Lock()
	defer t.mu.Unlock()
	if maximised {
		t.state.Maximised = true
		return
	}
	w, h := t.win.Size()
	if w <= 0 || h <= 0 {
		return
	}
	x, y := windowPosition(t.goos, t.win)
	t.prev, t.prevAt = t.state, t.now()
	// (0, 0) is also what Wayland reports for an unknown position; the
	// window is then centred on the next start rather than put in a corner.
	t.state = appstate.Window{X: x, Y: y, Width: w, Height: h, Positioned: x != 0 || y != 0}
}

// maximised records a maximise or un-maximise and saves.
func (t *windowTracker) maximised(on bool) {
	t.mu.Lock()
	if on && !t.prevAt.IsZero() && t.now().Sub(t.prevAt) < maximiseRollback {
		// The resize just recorded was the maximise itself.
		t.state = t.prev
	}
	t.prevAt = time.Time{}
	t.state.Maximised = on
	t.mu.Unlock()
	t.save()
}

// current returns the state that would be saved.
func (t *windowTracker) current() appstate.Window {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// save writes the current state; failures are only logged (losing the
// window size is harmless).
func (t *windowTracker) save() {
	if err := t.store.SaveWindow(t.current()); err != nil {
		log.Printf("save window state: %v", err)
	}
}

// onScreen reports whether enough of the window rectangle (at least
// 100×50 px) overlaps one of the screen work areas to be grabbed and moved.
// With no screens known it reports true.
func onScreen(win application.Rect, areas []application.Rect) bool {
	if len(areas) == 0 {
		return true
	}
	for _, a := range areas {
		w := min(win.X+win.Width, a.X+a.Width) - max(win.X, a.X)
		h := min(win.Y+win.Height, a.Y+a.Height) - max(win.Y, a.Y)
		if w >= 100 && h >= 50 {
			return true
		}
	}
	return false
}
