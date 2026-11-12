// Package store persists saved connections and their secrets.
//
// Connections live in <config dir>/lontar/connections.json (see DefaultDir),
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
const AppDirName = "lontar"

// LegacyAppDirName is the config directory of the app's former name. It is
// moved to AppDirName once at startup (see internal/migrate).
const LegacyAppDirName = "dbedit"

// ConnectionsFile is the file name of the connection list.
const ConnectionsFile = "connections.json"

// CurrentVersion is the schema version written to connections.json.
//
//	1: M1 (connections, tags, colours, optional SSH/SSL)
//	2: M4 adds "safeMode" ("off" | "confirm" | "readonly")
//	3: M6 makes SSH/SSL live and adds "hasSshPassword"/"hasSshPassphrase";
//	   the SSL mode "prefer" becomes "preferred" (MySQL/MariaDB) or
//	   "require" (PostgreSQL, which has no preferred mode in Lontar)
const CurrentVersion = 3

// ErrNotFound is returned for unknown connection IDs.
var ErrNotFound = errors.New("connection not found")

// ErrNewerVersion is returned when connections.json was written by a newer
// lontar. The file is left untouched.
var ErrNewerVersion = errors.New("connections file was created by a newer version of Lontar")

// userConfigDir is replaced in tests.
var userConfigDir = os.UserConfigDir

// DefaultDir returns the per-user config directory for Lontar:
// ~/.config/lontar on Linux, %AppData%\lontar on Windows and
// ~/Library/Application Support/lontar on macOS.
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
// CurrentVersion. Older Lontar versions then refuse the file
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
	if f.Version < 3 {
		for i := range f.Connections {
			c := &f.Connections[i]
			if c.SSL != nil && c.SSL.Mode == "prefer" {
				if c.Kind == "postgres" {
					c.SSL.Mode = SSLRequire // never silently weaker
				} else {
					c.SSL.Mode = SSLPreferred
				}
			}
		}
		f.Version = 3
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

// Secret purposes for SecretKey: "" is the database password.
const (
	SecretSSHPassword   = "ssh-password"
	SecretSSHPassphrase = "ssh-passphrase"
)

// secretPurposes lists every secret a connection may have.
var secretPurposes = []string{"", SecretSSHPassword, SecretSSHPassphrase}

// Secrets are the secrets passed to SaveSecrets. For each one, nil keeps
// the stored value, "" removes it and anything else replaces it.
type Secrets struct {
	Password      *string `json:"password"`
	SSHPassword   *string `json:"sshPassword"`
	SSHPassphrase *string `json:"sshPassphrase"`
}

func (s Secrets) value(purpose string) *string {
	switch purpose {
	case SecretSSHPassword:
		return s.SSHPassword
	case SecretSSHPassphrase:
		return s.SSHPassphrase
	}
	return s.Password
}

func hasFlag(c *Connection, purpose string) *bool {
	switch purpose {
	case SecretSSHPassword:
		return &c.HasSSHPassword
	case SecretSSHPassphrase:
		return &c.HasSSHPassphrase
	}
	return &c.HasPassword
}

// Save creates c (when c.ID is empty) or updates the existing connection
// with that ID. password controls the stored secret: nil keeps it, an empty
// string removes it, anything else replaces it. SSH secrets are kept.
func (s *Store) Save(c Connection, password *string) (Connection, error) {
	return s.SaveSecrets(c, Secrets{Password: password})
}

// SaveSecrets is Save with every secret (see Secrets).
func (s *Store) SaveSecrets(c Connection, sec Secrets) (Connection, error) {
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
		c.HasPassword, c.HasSSHPassword, c.HasSSHPassphrase = false, false, false
	} else {
		idx = f.index(c.ID)
		if idx < 0 {
			return Connection{}, fmt.Errorf("%w: %s", ErrNotFound, c.ID)
		}
		old := f.Connections[idx]
		c.CreatedAt = old.CreatedAt
		c.HasPassword, c.HasSSHPassword, c.HasSSHPassphrase = old.HasPassword, old.HasSSHPassword, old.HasSSHPassphrase
	}
	c.UpdatedAt = now

	// Write the secrets first: if that fails, nothing changed on disk.
	var setNew []string
	undo := func() {
		for _, key := range setNew {
			_ = s.secrets.Delete(key) // do not leave orphaned secrets
		}
	}
	for _, purpose := range secretPurposes {
		v := sec.value(purpose)
		if v == nil {
			continue
		}
		key, flag := SecretKey(c.ID, purpose), hasFlag(&c, purpose)
		what := secretName(purpose)
		if *v == "" {
			if err := s.secrets.Delete(key); err != nil {
				undo()
				return Connection{}, fmt.Errorf("remove saved %s: %w", what, err)
			}
			*flag = false
			continue
		}
		if err := s.secrets.Set(key, *v); err != nil {
			undo()
			return Connection{}, fmt.Errorf("save %s: %w", what, err)
		}
		*flag = true
		if idx < 0 {
			setNew = append(setNew, key)
		}
	}

	if idx < 0 {
		f.Connections = append(f.Connections, c)
	} else {
		f.Connections[idx] = c
	}
	if err := s.write(f); err != nil {
		undo()
		return Connection{}, err
	}
	return c.clone(), nil
}

func secretName(purpose string) string {
	switch purpose {
	case SecretSSHPassword:
		return "SSH password"
	case SecretSSHPassphrase:
		return "SSH key passphrase"
	}
	return "password"
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
	var errs []error
	for _, purpose := range secretPurposes {
		if err := s.secrets.Delete(SecretKey(id, purpose)); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("connection deleted, but its password could not be removed: %w", errors.Join(errs...))
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
	var copied []string
	undo := func() {
		for _, key := range copied {
			_ = s.secrets.Delete(key)
		}
	}
	for _, purpose := range secretPurposes {
		flag := hasFlag(&c, purpose)
		if !*flag {
			continue
		}
		v, err := s.secrets.Get(SecretKey(id, purpose))
		switch {
		case errors.Is(err, ErrSecretNotFound):
			*flag = false
		case err != nil:
			undo()
			return Connection{}, fmt.Errorf("read %s: %w", secretName(purpose), err)
		default:
			key := SecretKey(c.ID, purpose)
			if err := s.secrets.Set(key, v); err != nil {
				undo()
				return Connection{}, fmt.Errorf("save %s: %w", secretName(purpose), err)
			}
			copied = append(copied, key)
		}
	}
	f.Connections = append(f.Connections, c)
	if err := s.write(f); err != nil {
		undo()
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
	return s.secret(id, "")
}

// SSHSecrets returns the stored SSH password and key passphrase for id
// ("" when not stored).
func (s *Store) SSHSecrets(id string) (password, passphrase string, err error) {
	if password, err = s.secret(id, SecretSSHPassword); err != nil {
		return "", "", err
	}
	if passphrase, err = s.secret(id, SecretSSHPassphrase); err != nil {
		return "", "", err
	}
	return password, passphrase, nil
}

// secret reads one secret. When the connection's flag says none is saved,
// the keychain is not touched at all (so a missing or locked keychain does
// not break connections that never stored that secret).
func (s *Store) secret(id, purpose string) (string, error) {
	s.mu.Lock()
	f, lerr := s.load()
	s.mu.Unlock()
	if lerr == nil {
		if i := f.index(id); i >= 0 && !*hasFlag(&f.Connections[i], purpose) {
			return "", nil
		}
	}
	v, err := s.secrets.Get(SecretKey(id, purpose))
	if errors.Is(err, ErrSecretNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read saved %s: %w", secretName(purpose), err)
	}
	return v, nil
}
