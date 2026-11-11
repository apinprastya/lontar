package main

import (
	"io/fs"

	"dbedit/internal/services"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// appName is used for the application name and the main window title.
const appName = "dbedit"

// appOptions builds the Wails application options. It is kept separate from
// main() so it can be unit tested without starting a webview.
func appOptions(assets fs.FS) application.Options {
	return application.Options{
		Name:        appName,
		Description: "A fast, lightweight desktop database client",
		Services: []application.Service{
			application.NewService(services.NewAppService()),
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
