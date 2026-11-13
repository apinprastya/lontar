package services

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"lontar/internal/update"
)

// EventUpdateProgress reports download progress of an update.
const EventUpdateProgress = "update:progress"

// UpdateProgress is the payload of EventUpdateProgress.
type UpdateProgress struct {
	Done  int64 `json:"done"`
	Total int64 `json:"total"`
}

// UpdateInfo is the result of a check for updates.
type UpdateInfo struct {
	Current     string `json:"current"`
	Latest      string `json:"latest"`
	Available   bool   `json:"available"`
	Name        string `json:"name"`
	Notes       string `json:"notes"`
	URL         string `json:"url"`
	PublishedAt string `json:"publishedAt"`
	// CanInstall says whether Install can replace this installation; Reason
	// explains why not (the UI then offers the release page).
	CanInstall bool   `json:"canInstall"`
	Reason     string `json:"reason"`
}

// ReleasesPage is where every release is listed.
const ReleasesPage = "https://github.com/" + update.Repo + "/releases"

// UpdateService checks for, installs and restarts into new releases. It
// only acts when the user asks (no background checks).
type UpdateService struct {
	updater *update.Updater
	events  Emitter
	// Start launches the installed version; Quit ends this process; Open
	// opens a URL in the browser. Set by the app (nil in server mode).
	Start func(name string, args []string) error
	Quit  func()
	Open  func(url string) error

	mu        sync.Mutex
	release   *update.Release
	cancel    context.CancelFunc
	installed bool
}

// NewUpdateService returns an UpdateService.
func NewUpdateService(u *update.Updater, events Emitter) *UpdateService {
	return &UpdateService{updater: u, events: events}
}

// installBlocker returns why this installation cannot install updates ("" if it can).
func (s *UpdateService) installBlocker() string {
	switch {
	case update.IsDev(Version):
		return "This is a development build; it does not update itself."
	case s.updater.Install.Kind == update.KindManual:
		return s.updater.Install.Reason
	case s.updater.PublicKey == "":
		return update.ErrNoKey.Error()
	}
	return ""
}

// Check asks GitHub for the latest stable release.
func (s *UpdateService) Check(ctx context.Context) (*UpdateInfo, error) {
	r, err := s.updater.Latest(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.release = &r
	s.mu.Unlock()
	info := &UpdateInfo{
		Current:   Version,
		Latest:    r.Version(),
		Available: update.Newer(r.Version(), Version),
		Name:      r.Name,
		Notes:     r.Notes,
		URL:       r.URL,
	}
	if !r.PublishedAt.IsZero() {
		info.PublishedAt = r.PublishedAt.Format(time.RFC3339)
	}
	info.Reason = s.installBlocker()
	info.CanInstall = info.Available && info.Reason == ""
	return info, nil
}

// Install downloads, verifies and installs the release found by the last
// Check. Progress is sent as "update:progress" events; Cancel stops it.
// The running app keeps working; Restart switches to the new version.
func (s *UpdateService) Install(ctx context.Context) error {
	s.mu.Lock()
	r := s.release
	if r == nil {
		s.mu.Unlock()
		return update.ErrNoRelease
	}
	if !update.Newer(r.Version(), Version) {
		s.mu.Unlock()
		return update.ErrNotNewer
	}
	if reason := s.installBlocker(); reason != "" {
		s.mu.Unlock()
		return errors.New(reason)
	}
	if s.cancel != nil {
		s.mu.Unlock()
		return errors.New("an update is already being installed")
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	}()

	var last time.Time
	file, err := s.updater.Download(ctx, *r, func(done, total int64) {
		if s.events != nil && (time.Since(last) >= 100*time.Millisecond || done == total) {
			last = time.Now()
			s.events.Emit(EventUpdateProgress, UpdateProgress{Done: done, Total: total})
		}
	})
	if err != nil {
		return err
	}
	defer os.Remove(file)
	if err := s.updater.Apply(ctx, file); err != nil {
		return err
	}
	s.mu.Lock()
	s.installed = true
	s.mu.Unlock()
	return nil
}

// Cancel stops a running Install; false when none runs.
func (s *UpdateService) Cancel() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel == nil {
		return false
	}
	s.cancel()
	return true
}

// Restart starts the installed update and quits this instance (open
// sessions are closed by the services' shutdown).
func (s *UpdateService) Restart() error {
	s.mu.Lock()
	installed := s.installed
	s.mu.Unlock()
	if !installed {
		return errors.New("no update has been installed")
	}
	if s.Start == nil || s.Quit == nil {
		return errors.New("restart is not available; start Lontar again")
	}
	name, args := s.updater.Install.RestartCommand()
	if err := s.Start(name, args); err != nil {
		return err
	}
	s.Quit()
	return nil
}

// OpenReleasePage opens the checked release (or the release list) in the browser.
func (s *UpdateService) OpenReleasePage() error {
	if s.Open == nil {
		return errors.New("cannot open a browser here; visit " + ReleasesPage)
	}
	url := ReleasesPage
	s.mu.Lock()
	if s.release != nil && strings.HasPrefix(s.release.URL, ReleasesPage+"/") {
		url = s.release.URL
	}
	s.mu.Unlock()
	return s.Open(url)
}
