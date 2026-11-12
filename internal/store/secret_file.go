package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Fallback secret store file names (inside the config directory).
const (
	SecretsFile    = "secrets.enc"
	SecretsKeyFile = "secrets.key"
)

const (
	secretsVersion = 1
	secretsKeyLen  = 32 // AES-256
)

// secretsAAD binds the ciphertext to its purpose and format version. It is
// part of the on-disk format, not a display name: it still carries the app's
// former name (dbedit), because files written before the rename move with the
// config directory and must keep decrypting. Change it only together with
// secretsVersion.
var secretsAAD = []byte("dbedit-secrets-v1")

// randReader is replaced in tests.
var randReader io.Reader = rand.Reader

// FileSecretStore keeps secrets in an AES-256-GCM encrypted JSON file
// (secrets.enc). The 32-byte key is generated on first use and stored in a
// separate file (secrets.key); both files are 0600 in a 0700 directory.
//
// SECURITY: this is weaker than an OS keychain. The key lives on the same
// disk, readable by the same user, so the encryption only protects against
// casual disclosure (e.g. someone viewing or copying secrets.enc on its
// own). It is used only when the OS keychain is unavailable; see
// NewSecretStore.
type FileSecretStore struct {
	mu  sync.Mutex
	dir string
}

// NewFileSecretStore returns a FileSecretStore rooted at dir. No files are
// created until the first Set.
func NewFileSecretStore(dir string) *FileSecretStore {
	return &FileSecretStore{dir: dir}
}

type secretsEnvelope struct {
	Version    int    `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func (s *FileSecretStore) dataPath() string { return filepath.Join(s.dir, SecretsFile) }
func (s *FileSecretStore) keyPath() string  { return filepath.Join(s.dir, SecretsKeyFile) }

// key reads the encryption key, generating it when create is set and none
// exists yet.
func (s *FileSecretStore) key(create bool) ([]byte, error) {
	k, err := os.ReadFile(s.keyPath())
	if errors.Is(err, fs.ErrNotExist) && create {
		k = make([]byte, secretsKeyLen)
		if _, err := io.ReadFull(randReader, k); err != nil {
			return nil, fmt.Errorf("generate secrets key: %w", err)
		}
		if err := writeFileAtomic(s.keyPath(), k); err != nil {
			return nil, err
		}
		return k, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read secrets key: %w", err)
	}
	if len(k) != secretsKeyLen {
		return nil, fmt.Errorf("secrets key %s is invalid", s.keyPath())
	}
	return k, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// load decrypts the secrets map. A missing file yields an empty map.
func (s *FileSecretStore) load() (map[string]string, error) {
	data, err := os.ReadFile(s.dataPath())
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read secrets: %w", err)
	}
	var env secretsEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("secrets file %s is corrupt: %w", s.dataPath(), err)
	}
	if env.Version != secretsVersion {
		return nil, fmt.Errorf("secrets file %s has unsupported version %d", s.dataPath(), env.Version)
	}
	key, err := s.key(false)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(env.Nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("secrets file %s is corrupt: bad nonce", s.dataPath())
	}
	plain, err := gcm.Open(nil, env.Nonce, env.Ciphertext, secretsAAD)
	if err != nil {
		return nil, fmt.Errorf("decrypt secrets (wrong key or tampered file): %w", err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(plain, &m); err != nil {
		return nil, fmt.Errorf("decode secrets: %w", err)
	}
	return m, nil
}

// save encrypts m with a fresh nonce and writes it atomically.
func (s *FileSecretStore) save(m map[string]string) error {
	key, err := s.key(true)
	if err != nil {
		return err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(m)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(randReader, nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	env := secretsEnvelope{Version: secretsVersion, Nonce: nonce, Ciphertext: gcm.Seal(nil, nonce, plain, secretsAAD)}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.dataPath(), data)
}

// Get implements SecretStore.
func (s *FileSecretStore) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return "", err
	}
	v, ok := m[key]
	if !ok {
		return "", ErrSecretNotFound
	}
	return v, nil
}

// Set implements SecretStore.
func (s *FileSecretStore) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	m[key] = value
	return s.save(m)
}

// Delete implements SecretStore.
func (s *FileSecretStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.dataPath()); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	m, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := m[key]; !ok {
		return nil
	}
	delete(m, key)
	return s.save(m)
}
