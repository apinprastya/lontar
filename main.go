package main

import (
	"embed"
	"log"

	"dbedit/internal/store"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The built frontend (frontend/dist) is embedded into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	dir, err := store.DefaultDir()
	if err != nil {
		log.Fatal(err)
	}
	app := application.New(appOptions(assets, newConnectionService(dir)))
	app.Window.NewWithOptions(mainWindowOptions())
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
