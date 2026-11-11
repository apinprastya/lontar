package store

import (
	"errors"
	"fmt"
	"sync"

	"github.com/zalando/go-keyring"
)

// KeyringService is the service name used for OS keychain entries. Entries
// are keyed by connection ID (see SecretKey).
const KeyringService = "dbedit"

// ErrSecretNotFound is returned by SecretStore.Get when no secret exists.
var ErrSecretNotFound = errors.New("secret not found")

// SecretStore stores secrets (passwords) outside connections.json.
type SecretStore interface {
	// Get returns the secret for key or ErrSecretNotFound.
	Get(key string) (string, error)
	// Set stores value under key, replacing any previous value.
	Set(key, value string) error
	// Delete removes key. Deleting a missing key is not an error.
	Delete(key string) error
}

// SecretKey returns the secret key for a connection. purpose "" is the
// database password; later milestones use e.g. "ssh" for tunnel secrets.
func SecretKey(connectionID, purpose string) string {
	if purpose == "" {
		return connectionID
	}
	return connectionID + ":" + purpose
}

// NewSecretStore returns the platform's secret store: the OS keychain
// (macOS Keychain, Windows Credential Manager, Linux Secret Service) via
// go-keyring. On Linux, where a Secret Service (gnome-keyring, KWallet) may
// not be running, it falls back to an AES-GCM encrypted file in dir.
//
// SECURITY: the fallback file is weaker than an OS keychain. Its key is
// stored next to it (secrets.key, mode 0600), so it only keeps passwords out
// of connections.json and out of casual view; anyone who can read your files
// can decrypt it.
func NewSecretStore(dir string) SecretStore {
	return selectSecretStore(fallbackEnabled, KeyringSecretStore{}, NewFileSecretStore(dir))
}

// selectSecretStore wires primary and fallback according to useFallback.
func selectSecretStore(useFallback bool, primary, fallback SecretStore) SecretStore {
	if !useFallback {
		return primary
	}
	return &FallbackSecretStore{Primary: primary, Fallback: fallback}
}

// KeyringSecretStore stores secrets in the OS keychain via go-keyring under
// KeyringService.
type KeyringSecretStore struct{}

// Get implements SecretStore.
func (KeyringSecretStore) Get(key string) (string, error) {
	v, err := keyring.Get(KeyringService, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrSecretNotFound
	}
	if err != nil {
		return "", fmt.Errorf("keychain: %w", err)
	}
	return v, nil
}

// Set implements SecretStore.
func (KeyringSecretStore) Set(key, value string) error {
	if err := keyring.Set(KeyringService, key, value); err != nil {
		return fmt.Errorf("keychain: %w", err)
	}
	return nil
}

// Delete implements SecretStore.
func (KeyringSecretStore) Delete(key string) error {
	err := keyring.Delete(KeyringService, key)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return fmt.Errorf("keychain: %w", err)
}

// FallbackSecretStore uses Primary and switches to Fallback whenever Primary
// fails with anything other than ErrSecretNotFound (e.g. no Secret Service
// on the session bus). Reads consult both, so secrets written while the
// keychain was unavailable stay readable after it comes back.
type FallbackSecretStore struct {
	Primary  SecretStore
	Fallback SecretStore
}

// Get implements SecretStore.
func (s *FallbackSecretStore) Get(key string) (string, error) {
	v, perr := s.Primary.Get(key)
	if perr == nil {
		return v, nil
	}
	v, ferr := s.Fallback.Get(key)
	if ferr == nil {
		return v, nil
	}
	if errors.Is(ferr, ErrSecretNotFound) && !errors.Is(perr, ErrSecretNotFound) {
		// Not in the fallback; report why the keychain failed.
		return "", perr
	}
	return "", ferr
}

// Set implements SecretStore.
func (s *FallbackSecretStore) Set(key, value string) error {
	if err := s.Primary.Set(key, value); err != nil {
		return s.Fallback.Set(key, value)
	}
	// Remove a stale copy written while the keychain was unavailable.
	_ = s.Fallback.Delete(key)
	return nil
}

// Delete implements SecretStore. It removes key from both stores.
func (s *FallbackSecretStore) Delete(key string) error {
	perr := s.Primary.Delete(key)
	ferr := s.Fallback.Delete(key)
	if perr != nil && ferr != nil {
		return errors.Join(perr, ferr)
	}
	if ferr != nil {
		return ferr
	}
	return nil
}

// MemorySecretStore is an in-memory SecretStore for tests.
type MemorySecretStore struct {
	mu   sync.Mutex
	data map[string]string
	// Err, when set, is returned by every method.
	Err error
}

// NewMemorySecretStore returns an empty MemorySecretStore.
func NewMemorySecretStore() *MemorySecretStore {
	return &MemorySecretStore{data: map[string]string{}}
}

// Get implements SecretStore.
func (m *MemorySecretStore) Get(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return "", m.Err
	}
	v, ok := m.data[key]
	if !ok {
		return "", ErrSecretNotFound
	}
	return v, nil
}

// Set implements SecretStore.
func (m *MemorySecretStore) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return m.Err
	}
	m.data[key] = value
	return nil
}

// Delete implements SecretStore.
func (m *MemorySecretStore) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return m.Err
	}
	delete(m.data, key)
	return nil
}

// Len returns the number of stored secrets.
func (m *MemorySecretStore) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.data)
}
