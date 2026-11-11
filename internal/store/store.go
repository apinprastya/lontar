// Package store persists saved connections and their secrets.
//
// Connections live in <config dir>/dbedit/connections.json (see DefaultDir),
// written atomically with 0600 permissions inside a 0700 directory. The file
// carries a schema version so later milestones (SSH/SSL, ...) can migrate it.
//
// Passwords are never written to connections.json. They go to a SecretStore:
// the OS keychain via go-keyring, or on Linux without a Secret Service an
// AES-GCM encrypted fallback file (see NewSecretStore for its limits).
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// AppDirName is the directory created inside os.UserConfigDir().
const AppDirName = "dbedit"

// ConnectionsFile is the file name of the connection list.
const ConnectionsFile = "connections.json"

// CurrentVersion is the schema version written to connections.json.
//
//	1: M1 (connections, tags, colours, optional SSH/SSL)
//	2: M4 adds "safeMode" ("off" | "confirm" | "readonly")
const CurrentVersion = 2

// ErrNotFound is returned for unknown connection IDs.
var ErrNotFound = errors.New("connection not found")

// ErrNewerVersion is returned when connections.json was written by a newer
// dbedit. The file is left untouched.
var ErrNewerVersion = errors.New("connections file was created by a newer version of dbedit")

// userConfigDir is replaced in tests.
var userConfigDir = os.UserConfigDir

// DefaultDir returns the per-user config directory for dbedit:
// ~/.config/dbedit on Linux, %AppData%\dbedit on Windows and
// ~/Library/Application Support/dbedit on macOS.
func DefaultDir() (string, error) {
	base, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config directory: %w", err)
	}
	return filepath.Join(base, AppDirName), nil
}

// file is the on-disk format of connections.json.
type file struct {
	Version     int          `json:"version"`
	Connections []Connection `json:"connections"`
}

// Store manages saved connections. It is safe for concurrent use within one
// process.
type Store struct {
	mu      sync.Mutex
	dir     string
	secrets SecretStore
	now     func() time.Time
	newID   func() string
}

// New returns a store that keeps its files in dir and passwords in secrets.
func New(dir string, secrets SecretStore) *Store {
	return &Store{
		dir:     dir,
		secrets: secrets,
		now:     func() time.Time { return time.Now().UTC().Truncate(time.Millisecond) },
		newID:   uuid.NewString,
	}
}

// Dir returns the store's directory.
func (s *Store) Dir() string { return s.dir }

// Path returns the path of connections.json.
func (s *Store) Path() string { return filepath.Join(s.dir, ConnectionsFile) }

func (s *Store) load() (*file, error) {
	data, err := os.ReadFile(s.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return &file{Version: CurrentVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read connections: %w", err)
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("connections file %s is corrupt: %w", s.Path(), err)
	}
	switch {
	case f.Version > CurrentVersion:
		return nil, fmt.Errorf("%w (file version %d, supported %d)", ErrNewerVersion, f.Version, CurrentVersion)
	case f.Version < 1:
		return nil, fmt.Errorf("connections file %s has an invalid version %d", s.Path(), f.Version)
	}
	migrate(&f)
	return &f, nil
}

// migrate upgrades an older file in memory; the next write stores it with
// CurrentVersion. Older dbedit versions then refuse the file
// (ErrNewerVersion) instead of silently dropping the new fields.
func migrate(f *file) {
	if f.Version < 2 {
		// v1 had no safe mode: every connection was unrestricted.
		for i := range f.Connections {
			if f.Connections[i].SafeMode == "" {
				f.Connections[i].SafeMode = SafeOff
			}
		}
		f.Version = 2
	}
}

func (s *Store) write(f *file) error {
	f.Version = CurrentVersion
	if f.Connections == nil {
		f.Connections = []Connection{}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode connections: %w", err)
	}
	return writeFileAtomic(s.Path(), append(data, '\n'))
}

func (f *file) index(id string) int {
	for i, c := range f.Connections {
		if c.ID == id {
			return i
		}
	}
	return -1
}

// List returns all connections sorted by name (case-insensitive).
func (s *Store) List() ([]Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Connection, len(f.Connections))
	for i, c := range f.Connections {
		out[i] = c.clone()
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// Get returns the connection with the given ID.
func (s *Store) Get(id string) (Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Connection{}, err
	}
	i := f.index(id)
	if i < 0 {
		return Connection{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return f.Connections[i].clone(), nil
}

// Save creates c (when c.ID is empty) or updates the existing connection
// with that ID. password controls the stored secret: nil keeps it, an empty
// string removes it, anything else replaces it.
func (s *Store) Save(c Connection, password *string) (Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Connection{}, err
	}
	c = c.clone()
	c.Normalize()
	if err := c.Validate(); err != nil {
		return Connection{}, err
	}

	idx := -1
	now := s.now()
	if c.ID == "" {
		c.ID = s.newID()
		c.CreatedAt = now
		c.HasPassword = false
	} else {
		idx = f.index(c.ID)
		if idx < 0 {
			return Connection{}, fmt.Errorf("%w: %s", ErrNotFound, c.ID)
		}
		c.CreatedAt = f.Connections[idx].CreatedAt
		c.HasPassword = f.Connections[idx].HasPassword
	}
	c.UpdatedAt = now

	// Write the secret first: if that fails, nothing changed on disk.
	setNew := false
	if password != nil {
		if *password == "" {
			if err := s.secrets.Delete(c.ID); err != nil {
				return Connection{}, fmt.Errorf("remove saved password: %w", err)
			}
			c.HasPassword = false
		} else {
			if err := s.secrets.Set(c.ID, *password); err != nil {
				return Connection{}, fmt.Errorf("save password: %w", err)
			}
			c.HasPassword = true
			setNew = idx < 0
		}
	}

	if idx < 0 {
		f.Connections = append(f.Connections, c)
	} else {
		f.Connections[idx] = c
	}
	if err := s.write(f); err != nil {
		if setNew {
			_ = s.secrets.Delete(c.ID) // do not leave an orphaned secret
		}
		return Connection{}, err
	}
	return c.clone(), nil
}

// Delete removes the connection and its stored password.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	i := f.index(id)
	if i < 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	f.Connections = append(f.Connections[:i], f.Connections[i+1:]...)
	if err := s.write(f); err != nil {
		return err
	}
	if err := s.secrets.Delete(id); err != nil {
		return fmt.Errorf("connection deleted, but its password could not be removed: %w", err)
	}
	return nil
}

// Duplicate copies the connection (including its password) under a new ID
// and the name "<name> copy".
func (s *Store) Duplicate(id string) (Connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Connection{}, err
	}
	i := f.index(id)
	if i < 0 {
		return Connection{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	c := f.Connections[i].clone()
	c.ID = s.newID()
	c.Name = copyName(c.Name)
	c.CreatedAt = s.now()
	c.UpdatedAt = c.CreatedAt
	if c.HasPassword {
		pw, err := s.secrets.Get(id)
		switch {
		case errors.Is(err, ErrSecretNotFound):
			c.HasPassword = false
		case err != nil:
			return Connection{}, fmt.Errorf("read password: %w", err)
		default:
			if err := s.secrets.Set(c.ID, pw); err != nil {
				return Connection{}, fmt.Errorf("save password: %w", err)
			}
		}
	}
	f.Connections = append(f.Connections, c)
	if err := s.write(f); err != nil {
		_ = s.secrets.Delete(c.ID)
		return Connection{}, err
	}
	return c.clone(), nil
}

func copyName(name string) string {
	const suffix = " copy"
	if len(name)+len(suffix) > MaxNameLen {
		name = strings.ToValidUTF8(name[:MaxNameLen-len(suffix)], "")
	}
	return name + suffix
}

// Password returns the stored password for id, or "" when none is stored.
func (s *Store) Password(id string) (string, error) {
	pw, err := s.secrets.Get(id)
	if errors.Is(err, ErrSecretNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read saved password: %w", err)
	}
	return pw, nil
}
