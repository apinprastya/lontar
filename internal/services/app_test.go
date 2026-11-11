package services

import (
	"runtime"
	"testing"
)

func TestAppServiceVersion(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })

	s := NewAppService()
	if got := s.Version(); got != old {
		t.Fatalf("Version() = %q, want %q", got, old)
	}
	Version = "9.9.9"
	if got := s.Version(); got != "9.9.9" {
		t.Fatalf("Version() = %q after override, want 9.9.9", got)
	}
}

func TestAppServicePlatform(t *testing.T) {
	if got := NewAppService().Platform(); got != runtime.GOOS {
		t.Fatalf("Platform() = %q, want %q", got, runtime.GOOS)
	}
	// The zero value falls back to the runtime platform.
	var zero AppService
	if got := zero.Platform(); got != runtime.GOOS {
		t.Fatalf("zero Platform() = %q, want %q", got, runtime.GOOS)
	}
}
