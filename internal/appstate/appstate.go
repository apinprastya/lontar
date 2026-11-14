// Package appstate remembers how the user left the application: the main
// window's bounds and maximised state, and small UI values the frontend
// keeps (panel layouts, table column widths, panel toggles). It lives in
// state.json in the config directory, separate from settings.json: it is
// written often, by the app rather than the user, and losing it only resets
// the layout.
//
// The file carries a version. A file from a newer Lontar is ignored and
// never overwritten; a corrupt one is ignored and replaced on the next save.
package appstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"lontar/internal/store"
)

// FileName is the state file in the config directory.
const FileName = "state.json"

// CurrentVersion is the version written to state.json.
const CurrentVersion = 1

// Limits on the UI map, so a misbehaving frontend cannot grow the file
// without bound.
const (
	MaxKeyLen   = 512
	MaxValueLen = 64 << 10
	MaxKeys     = 4000
)

// Errors.
var (
	ErrNewerVersion = errors.New("state file was created by a newer version of Lontar")
	ErrKey          = errors.New("invalid UI state key")
	ErrValue        = errors.New("UI state value is too large")
	ErrFull         = errors.New("too many UI state entries")
)

// Window is the main window's last normal (not maximised, minimised or
// fullscreen) bounds and whether it was maximised. Minimised is not kept:
// the window always comes back visible.
type Window struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
	// Positioned is set when X and Y are known (they are not, for example,
	// on Wayland).
	Positioned bool `json:"positioned"`
	Maximised  bool `json:"maximised"`
}

// Valid reports whether w has a usable size.
func (w Window) Valid() bool { return w.Width > 0 && w.Height > 0 }

// State is the content of state.json.
type State struct {
	Version int               `json:"version"`
	Window  *Window           `json:"window,omitempty"`
	UI      map[string]string `json:"ui,omitempty"`
}

// Store reads and writes state.json. It keeps the state in memory after the
// first load and is safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	dir    string
	loaded bool
	state  State
	// readOnly is set when the file was written by a newer Lontar.
	readOnly bool
	err      error
}

// New returns a store for dir (the config directory).
func New(dir string) *Store { return &Store{dir: dir} }

// Path returns the path of state.json.
func (s *Store) Path() string { return filepath.Join(s.dir, FileName) }

// Err returns why state.json could not be read, if it could not.
func (s *Store) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure()
	return s.err
}

func (s *Store) ensure() {
	if s.loaded {
		return
	}
	s.loaded = true
	s.state = State{Version: CurrentVersion}
	data, err := os.ReadFile(s.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		s.err = fmt.Errorf("read state: %w", err)
		return
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		s.err = fmt.Errorf("state file %s is corrupt: %w", s.Path(), err)
		return
	}
	if st.Version > CurrentVersion {
		s.readOnly = true
		s.err = fmt.Errorf("%w (file version %d, supported %d)", ErrNewerVersion, st.Version, CurrentVersion)
		return
	}
	st.Version = CurrentVersion
	if st.Window != nil && !st.Window.Valid() {
		st.Window = nil
	}
	s.state = st
}

func (s *Store) save() error {
	if s.readOnly {
		return s.err
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	return store.WriteFileAtomic(s.Path(), append(data, '\n'))
}

// Window returns the saved window state, or false when there is none.
func (s *Store) Window() (Window, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure()
	if s.state.Window == nil {
		return Window{}, false
	}
	return *s.state.Window, true
}

// SaveWindow stores w (ignored when it has no size).
func (s *Store) SaveWindow(w Window) error {
	if !w.Valid() {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure()
	if s.state.Window != nil && *s.state.Window == w {
		return nil
	}
	s.state.Window = &w
	return s.save()
}

// UI returns a copy of the UI values.
func (s *Store) UI() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure()
	out := make(map[string]string, len(s.state.UI))
	maps.Copy(out, s.state.UI)
	return out
}

// SetUI stores value under key; an empty value removes the key.
func (s *Store) SetUI(key, value string) error {
	if key == "" || len(key) > MaxKeyLen {
		return ErrKey
	}
	if len(value) > MaxValueLen {
		return ErrValue
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensure()
	old, exists := s.state.UI[key]
	switch {
	case value == "" && !exists, exists && old == value:
		return nil
	case value == "":
		delete(s.state.UI, key)
	default:
		if !exists && len(s.state.UI) >= MaxKeys {
			return ErrFull
		}
		if s.state.UI == nil {
			s.state.UI = map[string]string{}
		}
		s.state.UI[key] = value
	}
	return s.save()
}
