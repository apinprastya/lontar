package main

import (
	"bytes"
	"testing"
	"testing/fstest"
)

func TestAppOptions(t *testing.T) {
	b := newBackend(t.TempDir())
	opts := appOptions(fstest.MapFS{"index.html": {Data: []byte("<html></html>")}}, b)
	if opts.Name != "dbedit" {
		t.Fatalf("Name = %q, want dbedit", opts.Name)
	}
	if len(opts.Services) != 11 {
		t.Fatalf("expected 11 bound services, got %d", len(opts.Services))
	}
	for i, inst := range []any{b.dialogs, b.export, b.importer, b.settings} {
		if opts.Services[7+i].Instance() != inst {
			t.Fatalf("service %d not registered", 7+i)
		}
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
	if opts.Linux.ProgramName != "dbedit" {
		t.Fatalf("Linux.ProgramName = %q, want dbedit (StartupWMClass in build/linux/dbedit.desktop)", opts.Linux.ProgramName)
	}
	if !bytes.HasPrefix(opts.Icon, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("Icon must be the embedded build/appicon.png")
	}
}

func TestNewBackend(t *testing.T) {
	b := newBackend(t.TempDir())
	if b.schema == nil || b.browse == nil || b.editor == nil || b.history == nil || b.connections.HostKeys == nil {
		t.Fatal("services missing")
	}
	if st := b.settings.Get(); st.Error != "" || st.Settings.GridPageSize != 300 {
		t.Fatalf("settings = %+v", st)
	}
	if len(userKnownHosts()) != 1 {
		t.Fatal("user known_hosts path")
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

func TestWailsAdaptersWithoutApp(t *testing.T) {
	if _, err := (WailsDialogs{}).OpenFile("t", nil); err == nil {
		t.Fatal("OpenFile without an app")
	}
	if _, err := (WailsDialogs{}).SaveFile("t", "f", nil); err == nil {
		t.Fatal("SaveFile without an app")
	}
	WailsEmitter{}.Emit("x", 1) // no app: dropped, no panic
}
