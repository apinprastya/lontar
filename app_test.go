package main

import (
	"testing"
	"testing/fstest"
)

func TestAppOptions(t *testing.T) {
	opts := appOptions(fstest.MapFS{"index.html": {Data: []byte("<html></html>")}})
	if opts.Name != "dbedit" {
		t.Fatalf("Name = %q, want dbedit", opts.Name)
	}
	if len(opts.Services) != 1 {
		t.Fatalf("expected 1 bound service, got %d", len(opts.Services))
	}
	if opts.Assets.Handler == nil {
		t.Fatal("asset handler must be set")
	}
	if !opts.Mac.ApplicationShouldTerminateAfterLastWindowClosed {
		t.Fatal("app should quit when the last window closes on macOS")
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
