package main

import (
	"io/fs"

	"dbedit/internal/conn"
	"dbedit/internal/driver/all"
	"dbedit/internal/services"
	"dbedit/internal/store"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// appName is used for the application name and the main window title.
const appName = "dbedit"

// newConnectionService wires the connection store (JSON + OS keychain, with
// the encrypted-file fallback on Linux), the session manager and the driver
// registry, keeping everything under configDir.
func newConnectionService(configDir string) *services.ConnectionService {
	st := store.New(configDir, store.NewSecretStore(configDir))
	drivers := all.Registry()
	return services.NewConnectionService(st, conn.NewManager(st, drivers), drivers)
}

// appOptions builds the Wails application options. It is kept separate from
// main() so it can be unit tested without starting a webview. Bound
// services implementing ServiceShutdown (ConnectionService closes all open
// sessions) are shut down by Wails when the app quits.
func appOptions(assets fs.FS, connections *services.ConnectionService) application.Options {
	return application.Options{
		Name:        appName,
		Description: "A fast, lightweight desktop database client",
		Services: []application.Service{
			application.NewService(services.NewAppService()),
			application.NewService(connections),
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
