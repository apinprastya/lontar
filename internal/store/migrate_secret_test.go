package store

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestMigratingSecretStoreKeyring runs the lazy keychain migration against
// go-keyring's in-memory mock with entries under both service names, the
// way NewSecretStore wires it.
func TestMigratingSecretStoreKeyring(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	s := &MigratingSecretStore{
		Current: KeyringSecretStore{Service: KeyringService},
		Legacy:  KeyringSecretStore{Service: LegacyKeyringService},
	}
	inService := func(service, key string) (string, bool) {
		v, err := keyring.Get(service, key)
		return v, err == nil
	}

	// Found in the old service only: moved to the new one, old one removed.
	_ = keyring.Set(LegacyKeyringService, "c1", "old-pw")
	if v, err := s.Get("c1"); err != nil || v != "old-pw" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if v, ok := inService(KeyringService, "c1"); !ok || v != "old-pw" {
		t.Fatal("secret not written under the new service")
	}
	if _, ok := inService(LegacyKeyringService, "c1"); ok {
		t.Fatal("legacy entry not deleted")
	}

	// Found in the new service: the old entry is never consulted.
	_ = keyring.Set(KeyringService, "c2", "new")
	_ = keyring.Set(LegacyKeyringService, "c2", "older")
	if v, err := s.Get("c2"); err != nil || v != "new" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if _, ok := inService(LegacyKeyringService, "c2"); !ok {
		t.Fatal("a hit in the new service must not touch the old one")
	}

	// Replacing a secret removes the old service's copy.
	if err := s.Set("c2", "newer"); err != nil {
		t.Fatal(err)
	}
	if _, ok := inService(LegacyKeyringService, "c2"); ok {
		t.Fatal("Set left the legacy entry")
	}

	// Delete removes both.
	_ = keyring.Set(LegacyKeyringService, "c2", "older")
	if err := s.Delete("c2"); err != nil {
		t.Fatal(err)
	}
	if _, ok := inService(KeyringService, "c2"); ok {
		t.Fatal("Delete left the new entry")
	}
	if _, ok := inService(LegacyKeyringService, "c2"); ok {
		t.Fatal("Delete left the legacy entry")
	}

	// Missing everywhere.
	if _, err := s.Get("nope"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("err = %v", err)
	}
	if err := s.Delete("nope"); err != nil {
		t.Fatalf("deleting a missing key: %v", err)
	}
}

func TestMigratingSecretStoreErrors(t *testing.T) {
	cur, old := NewMemorySecretStore(), NewMemorySecretStore()
	s := &MigratingSecretStore{Current: cur, Legacy: old}

	// The current store failing (no keychain) is reported as is, so the
	// Linux FallbackSecretStore switches to its file.
	cur.Err = errors.New("no secret service")
	if _, err := s.Get("k"); err == nil || err.Error() != "no secret service" {
		t.Fatalf("err = %v", err)
	}
	if err := s.Set("k", "v"); err == nil {
		t.Fatal("Set should fail")
	}
	if err := s.Delete("k"); err == nil {
		t.Fatal("Delete should fail")
	}
	cur.Err = nil

	// A failing legacy read is reported with context.
	old.Err = errors.New("denied")
	if _, err := s.Get("k"); err == nil || !strings.Contains(err.Error(), "denied") || !strings.Contains(err.Error(), LegacyKeyringService) {
		t.Fatalf("err = %v", err)
	}
	// A failing legacy delete does not fail Set (the new value wins anyway).
	if err := s.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	old.Err = nil

	// If the new entry cannot be written, the old one stays for next time.
	_ = old.Set("m", "pw")
	failing := &failingSetStore{MemorySecretStore: NewMemorySecretStore()}
	s2 := &MigratingSecretStore{Current: failing, Legacy: old}
	if v, err := s2.Get("m"); err != nil || v != "pw" {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if v, _ := old.Get("m"); v != "pw" {
		t.Fatal("legacy entry deleted although the copy failed")
	}
}

// failingSetStore fails every Set.
type failingSetStore struct{ *MemorySecretStore }

func (f *failingSetStore) Set(string, string) error { return errors.New("read-only") }

func TestNewSecretStoreMigratesKeychain(t *testing.T) {
	s := NewSecretStore(t.TempDir())
	if fb, ok := s.(*FallbackSecretStore); ok {
		s = fb.Primary
	}
	m, ok := s.(*MigratingSecretStore)
	if !ok {
		t.Fatalf("keychain store is %T", s)
	}
	if m.Current != (KeyringSecretStore{Service: KeyringService}) || m.Legacy != (KeyringSecretStore{Service: LegacyKeyringService}) {
		t.Fatalf("services = %+v / %+v", m.Current, m.Legacy)
	}
	if KeyringService == LegacyKeyringService {
		t.Fatal("services must differ")
	}
}

// TestFileSecretStoreReadsPreRenameFile checks that a secrets.enc written
// before the rename (format v1, AAD "dbedit-secrets-v1") still decrypts once
// the config directory has moved.
func TestFileSecretStoreReadsPreRenameFile(t *testing.T) {
	dir := t.TempDir()
	key := []byte("0123456789abcdef0123456789abcdef")
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	plain, _ := json.Marshal(map[string]string{"c1": "s3cret"})
	env, _ := json.Marshal(secretsEnvelope{Version: 1, Nonce: nonce, Ciphertext: gcm.Seal(nil, nonce, plain, []byte("dbedit-secrets-v1"))})
	if err := os.WriteFile(filepath.Join(dir, SecretsKeyFile), key, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SecretsFile), env, 0o600); err != nil {
		t.Fatal(err)
	}
	if v, err := NewFileSecretStore(dir).Get("c1"); err != nil || v != "s3cret" {
		t.Fatalf("Get = %q, %v", v, err)
	}
}
