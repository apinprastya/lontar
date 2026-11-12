package main

import (
	"embed"
	"log"
	"os"

	"lontar/internal/migrate"

	"github.com/wailsapp/wails/v3/pkg/application"
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
	app := application.New(appOptions(assets, newBackend(dir)))
	app.Window.NewWithOptions(mainWindowOptions())
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
