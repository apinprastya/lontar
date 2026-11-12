// Package services contains thin, Wails-bound facades over the plain Go
// packages in internal/. Services must stay free of business logic so that
// the logic can be unit tested without a running Wails application.
package services

import "runtime"

// DevVersion is the version of a binary built without version stamping
// (plain `go build`/`go run`, tests).
const DevVersion = "0.0.0-dev"

// Version is the application version. Every Taskfile build and package
// stamps it with -ldflags "-X lontar/internal/services.Version=<VERSION>",
// where VERSION comes from `git describe` or LONTAR_VERSION (see the root
// Taskfile.yml).
var Version = DevVersion

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
