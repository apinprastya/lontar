// Package services contains thin, Wails-bound facades over the plain Go
// packages in internal/. Services must stay free of business logic so that
// the logic can be unit tested without a running Wails application.
package services

import "runtime"

// Version is the application version. It is overridable at build time with
// -ldflags "-X dbedit/internal/services.Version=1.2.3".
var Version = "0.0.1"

// AppService exposes basic application metadata to the frontend.
type AppService struct {
	goos string
}

// NewAppService returns an AppService that reports the host platform.
func NewAppService() *AppService {
	return &AppService{goos: runtime.GOOS}
}

// Version returns the application version string.
func (s *AppService) Version() string {
	return Version
}

// Platform returns the operating system the app is running on
// ("linux", "windows" or "darwin").
func (s *AppService) Platform() string {
	if s.goos == "" {
		return runtime.GOOS
	}
	return s.goos
}
