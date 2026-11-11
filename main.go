package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The built frontend (frontend/dist) is embedded into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := application.New(appOptions(assets))
	app.Window.NewWithOptions(mainWindowOptions())
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
