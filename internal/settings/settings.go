// Package settings stores the application preferences in settings.json in
// the config directory (next to connections.json), written atomically.
//
// The file carries a version. Missing fields take their defaults, so a file
// from an older dbedit loads fine; a file from a newer dbedit is refused
// (ErrNewerVersion) and never overwritten.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"dbedit/internal/store"
)

// FileName is the settings file in the config directory.
const FileName = "settings.json"

// CurrentVersion is the version written to settings.json.
//
//	1: M6 (theme, editor font size, grid page size, editor max rows, CSV
//	   export defaults, confirm closing dirty query tabs)
const CurrentVersion = 1

// ErrNewerVersion is returned for a settings file written by a newer dbedit.
var ErrNewerVersion = errors.New("settings file was created by a newer version of dbedit")

// Theme is the colour theme.
type Theme string

// Themes.
const (
	ThemeSystem Theme = "system" // follows the OS (prefers-color-scheme)
	ThemeDark   Theme = "dark"
	ThemeLight  Theme = "light"
)

// CSV null representations.
const (
	NullEmpty  = "empty"  // an empty field
	NullBackN  = "\\N"    // \N (PostgreSQL COPY, MySQL LOAD DATA)
	NullCustom = "custom" // CSV.NullText
)

// CSV holds the default CSV export options.
type CSV struct {
	Delimiter string `json:"delimiter"`
	Header    bool   `json:"header"`
	Quote     string `json:"quote"`
	// Null is "empty", "\\N" or "custom" (then NullText).
	Null     string `json:"null"`
	NullText string `json:"nullText"`
	// CRLF uses \r\n line endings (default \n).
	CRLF bool `json:"crlf"`
	// BOM writes a UTF-8 byte order mark (for Excel).
	BOM bool `json:"bom"`
}

// Settings are the application preferences.
type Settings struct {
	Version        int   `json:"version"`
	Theme          Theme `json:"theme"`
	EditorFontSize int   `json:"editorFontSize"`
	// GridPageSize is the number of rows per table page.
	GridPageSize int `json:"gridPageSize"`
	// EditorMaxRows is the default "max rows" of SQL editor results.
	EditorMaxRows int `json:"editorMaxRows"`
	CSV           CSV `json:"csvExport"`
	// ConfirmCloseDirtyQuery asks before closing a query tab whose SQL
	// changed since it last ran.
	ConfirmCloseDirtyQuery bool `json:"confirmCloseDirtyQuery"`
}

// Limits.
const (
	MinFontSize, MaxFontSize = 9, 32
	MinPageSize, MaxPageSize = 10, 5000
	MinMaxRows, MaxMaxRows   = 1, 50000
)

// Defaults returns the default settings.
func Defaults() Settings {
	return Settings{
		Version:        CurrentVersion,
		Theme:          ThemeSystem,
		EditorFontSize: 13,
		GridPageSize:   300,
		EditorMaxRows:  1000,
		CSV:            CSV{Delimiter: ",", Header: true, Quote: `"`, Null: NullEmpty},
	}
}

func clamp(v, lo, hi, def int) int {
	switch {
	case v == 0:
		return def
	case v < lo:
		return lo
	case v > hi:
		return hi
	}
	return v
}

// Normalize replaces invalid values with defaults or clamps them.
func (s *Settings) Normalize() {
	d := Defaults()
	s.Version = CurrentVersion
	switch s.Theme {
	case ThemeSystem, ThemeDark, ThemeLight:
	default:
		s.Theme = d.Theme
	}
	s.EditorFontSize = clamp(s.EditorFontSize, MinFontSize, MaxFontSize, d.EditorFontSize)
	s.GridPageSize = clamp(s.GridPageSize, MinPageSize, MaxPageSize, d.GridPageSize)
	s.EditorMaxRows = clamp(s.EditorMaxRows, MinMaxRows, MaxMaxRows, d.EditorMaxRows)
	if len([]rune(s.CSV.Delimiter)) != 1 || s.CSV.Delimiter == "\n" || s.CSV.Delimiter == "\r" {
		s.CSV.Delimiter = d.CSV.Delimiter
	}
	if len([]rune(s.CSV.Quote)) != 1 || s.CSV.Quote == s.CSV.Delimiter {
		s.CSV.Quote = d.CSV.Quote
		if s.CSV.Quote == s.CSV.Delimiter {
			s.CSV.Delimiter = d.CSV.Delimiter
		}
	}
	switch s.CSV.Null {
	case NullEmpty, NullBackN, NullCustom:
	default:
		s.CSV.Null = d.CSV.Null
	}
}

// Store reads and writes settings.json. It is safe for concurrent use.
type Store struct {
	mu  sync.Mutex
	dir string
}

// New returns a store for dir (the config directory).
func New(dir string) *Store { return &Store{dir: dir} }

// Path returns the path of settings.json.
func (s *Store) Path() string { return filepath.Join(s.dir, FileName) }

// Load returns the settings: the defaults when the file does not exist.
// A corrupt file is an error (the caller may fall back to Defaults).
func (s *Store) Load() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() (Settings, error) {
	data, err := os.ReadFile(s.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Defaults(), fmt.Errorf("read settings: %w", err)
	}
	out := Defaults()
	out.Version = 0
	if err := json.Unmarshal(data, &out); err != nil {
		return Defaults(), fmt.Errorf("settings file %s is corrupt: %w", s.Path(), err)
	}
	switch {
	case out.Version > CurrentVersion:
		return Defaults(), fmt.Errorf("%w (file version %d, supported %d)", ErrNewerVersion, out.Version, CurrentVersion)
	case out.Version < 1:
		return Defaults(), fmt.Errorf("settings file %s has an invalid version %d", s.Path(), out.Version)
	}
	out.Normalize()
	return out, nil
}

// Save normalizes and writes v, and returns what was written. A file
// written by a newer dbedit is not overwritten (a corrupt one is).
func (s *Store) Save(v Settings) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.load(); errors.Is(err, ErrNewerVersion) {
		return Settings{}, err
	}
	v.Normalize()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return Settings{}, err
	}
	if err := store.WriteFileAtomic(s.Path(), append(data, '\n')); err != nil {
		return Settings{}, err
	}
	return v, nil
}
