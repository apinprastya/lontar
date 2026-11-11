package main

import (
	"io/fs"

	"dbedit/internal/conn"
	"dbedit/internal/driver/all"
	"dbedit/internal/history"
	"dbedit/internal/query"
	"dbedit/internal/services"
	"dbedit/internal/store"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// appName is used for the application name and the main window title.
const appName = "dbedit"

// backend holds the Wails-bound services.
type backend struct {
	connections *services.ConnectionService
	schema      *services.SchemaService
	browse      *services.BrowseService
	editor      *services.EditorService
	history     *services.HistoryService
	edit        *services.EditService
}

// newBackend wires the connection store (JSON + OS keychain, with the
// encrypted-file fallback on Linux), the session manager, the driver
// registry and the query history (history.db), keeping everything under
// configDir. All services share one session manager.
func newBackend(configDir string) backend {
	st := store.New(configDir, store.NewSecretStore(configDir))
	drivers := all.Registry()
	sessions := conn.NewManager(st, drivers)
	hist := history.New(configDir)
	return backend{
		connections: services.NewConnectionService(st, sessions, drivers),
		schema:      services.NewSchemaService(sessions, drivers),
		browse:      services.NewBrowseService(sessions, drivers, query.NewRunner()),
		editor:      services.NewEditorService(sessions, drivers, hist),
		history:     services.NewHistoryService(hist),
		edit:        services.NewEditService(sessions, drivers),
	}
}

// appOptions builds the Wails application options. It is kept separate from
// main() so it can be unit tested without starting a webview. Bound
// services implementing ServiceShutdown (ConnectionService closes all open
// sessions) are shut down by Wails when the app quits.
func appOptions(assets fs.FS, b backend) application.Options {
	return application.Options{
		Name:        appName,
		Description: "A fast, lightweight desktop database client",
		Services: []application.Service{
			application.NewService(services.NewAppService()),
			application.NewService(b.connections),
			application.NewService(b.schema),
			application.NewService(b.browse),
			application.NewService(b.editor),
			application.NewService(b.history),
			application.NewService(b.edit),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	}
}

// mainWindowOptions describes the main application window.
func mainWindowOptions() application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Title:     appName,
		Width:     1200,
		Height:    760,
		MinWidth:  800,
		MinHeight: 500,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 38,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(24, 25, 30),
		URL:              "/",
	}
}
