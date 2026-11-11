package main

import (
	"testing"
	"testing/fstest"
)

func TestAppOptions(t *testing.T) {
	b := newBackend(t.TempDir())
	opts := appOptions(fstest.MapFS{"index.html": {Data: []byte("<html></html>")}}, b)
	if opts.Name != "dbedit" {
		t.Fatalf("Name = %q, want dbedit", opts.Name)
	}
	if len(opts.Services) != 7 {
		t.Fatalf("expected 7 bound services, got %d", len(opts.Services))
	}
	if opts.Services[1].Instance() != b.connections {
		t.Fatal("ConnectionService not registered")
	}
	if opts.Services[2].Instance() != b.schema || opts.Services[3].Instance() != b.browse {
		t.Fatal("SchemaService/BrowseService not registered")
	}
	if opts.Services[6].Instance() != b.edit {
		t.Error("edit service not bound")
	}
	if opts.Services[4].Instance() != b.editor || opts.Services[5].Instance() != b.history {
		t.Fatal("EditorService/HistoryService not registered")
	}
	if opts.Assets.Handler == nil {
		t.Fatal("asset handler must be set")
	}
	if !opts.Mac.ApplicationShouldTerminateAfterLastWindowClosed {
		t.Fatal("app should quit when the last window closes on macOS")
	}
}

func TestNewBackend(t *testing.T) {
	b := newBackend(t.TempDir())
	if b.schema == nil || b.browse == nil || b.editor == nil || b.history == nil {
		t.Fatal("services missing")
	}
	svc := b.connections
	if got := svc.Drivers(); len(got) != 4 {
		t.Fatalf("Drivers() = %+v", got)
	}
	list, err := svc.List()
	if err != nil || len(list) != 0 {
		t.Fatalf("List() = %+v, %v", list, err)
	}
	if err := svc.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if err := b.editor.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestMainWindowOptions(t *testing.T) {
	w := mainWindowOptions()
	if w.Title != "dbedit" {
		t.Fatalf("Title = %q, want dbedit", w.Title)
	}
	if w.URL != "/" {
		t.Fatalf("URL = %q, want /", w.URL)
	}
	if w.Width < w.MinWidth || w.Height < w.MinHeight {
		t.Fatalf("default size %dx%d is below the minimum %dx%d", w.Width, w.Height, w.MinWidth, w.MinHeight)
	}
}
