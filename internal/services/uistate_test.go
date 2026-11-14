package services

import (
	"errors"
	"testing"

	"lontar/internal/appstate"
)

func TestUIStateService(t *testing.T) {
	dir := t.TempDir()
	svc := NewUIStateService(appstate.New(dir))
	if len(svc.All()) != 0 {
		t.Fatalf("all = %v", svc.All())
	}
	if err := svc.Set("layout.workspace", `{"sidebar":20}`); err != nil {
		t.Fatal(err)
	}
	if got := NewUIStateService(appstate.New(dir)).All(); got["layout.workspace"] != `{"sidebar":20}` {
		t.Fatalf("reloaded = %v", got)
	}
	if err := svc.Set("", "x"); !errors.Is(err, appstate.ErrKey) {
		t.Fatalf("empty key: %v", err)
	}
}
