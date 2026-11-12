package main

import (
	"errors"

	"dbedit/internal/services"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// errNoApp is returned when the Wails application is not running.
var errNoApp = errors.New("the application is not running")

// WailsDialogs shows the native Wails v3 file dialogs.
type WailsDialogs struct{}

// OpenFile implements Dialogs.
func (WailsDialogs) OpenFile(title string, filters []services.FileFilter) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errNoApp
	}
	d := app.Dialog.OpenFile().SetTitle(title).CanChooseFiles(true).CanChooseDirectories(false).ShowHiddenFiles(true)
	for _, f := range filters {
		d.AddFilter(f.DisplayName, f.Pattern)
	}
	return d.PromptForSingleSelection()
}

// SaveFile implements Dialogs.
func (WailsDialogs) SaveFile(title, filename string, filters []services.FileFilter) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errNoApp
	}
	d := app.Dialog.SaveFile().SetMessage(title).SetFilename(filename).CanCreateDirectories(true)
	for _, f := range filters {
		d.AddFilter(f.DisplayName, f.Pattern)
	}
	return d.PromptForSingleSelection()
}

// WailsEmitter sends events through the running Wails application.
type WailsEmitter struct{}

// Emit implements Emitter.
func (WailsEmitter) Emit(name string, data any) {
	if app := application.Get(); app != nil {
		app.Event.Emit(name, data)
	}
}
