package main

import (
	"embed"
	"log"
	"os"

	"lontar/internal/appstate"
	"lontar/internal/migrate"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// The built frontend (frontend/dist) is embedded into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Moves the config directory of the former name (dbedit) first.
	dir, err := configDir(os.UserConfigDir, migrate.Options{})
	if err != nil {
		log.Fatal(err)
	}
	b := newBackend(dir)
	app := application.New(appOptions(assets, b))
	saved, ok := b.state.Window()
	win := app.Window.NewWithOptions(mainWindowOptions(saved, ok))
	trackWindow(app, win, b.state, saved, ok)
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// trackWindow saves the main window's bounds and maximised state to
// state.json (on maximise/un-maximise, on close and on quit), and moves a
// restored window that would be off every screen (a monitor was unplugged)
// back to the centre. The logic lives in window.go.
func trackWindow(app *application.App, win *application.WebviewWindow, st *appstate.Store, saved appstate.Window, ok bool) {
	initial := saved
	if !ok {
		initial = appstate.Window{Width: defaultWidth, Height: defaultHeight}
	}
	tr := newWindowTracker(win, st, initial)
	bounds := func(*application.WindowEvent) { tr.boundsChanged() }
	win.OnWindowEvent(events.Common.WindowDidResize, bounds)
	win.OnWindowEvent(events.Common.WindowDidMove, bounds)
	win.OnWindowEvent(events.Common.WindowMaximise, func(*application.WindowEvent) { tr.maximised(true) })
	win.OnWindowEvent(events.Common.WindowUnMaximise, func(*application.WindowEvent) { tr.maximised(false) })
	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) { tr.save() })
	app.OnShutdown(tr.save)
	if ok && saved.Positioned {
		win.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
			var areas []application.Rect
			for _, s := range app.Screen.GetAll() {
				areas = append(areas, s.WorkArea)
			}
			x, y := win.Position()
			w, h := win.Size()
			if !onScreen(application.Rect{X: x, Y: y, Width: w, Height: h}, areas) {
				win.Center()
			}
		})
	}
}
