package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestSecretKey(t *testing.T) {
	if SecretKey("id", "") != "id" || SecretKey("id", "ssh") != "id:ssh" {
		t.Fatal("SecretKey")
	}
}

func TestMemorySecretStore(t *testing.T) {
	m := NewMemorySecretStore()
	if _, err := m.Get("k"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatal(err)
	}
	if err := m.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, _ := m.Get("k"); v != "v" || m.Len() != 1 {
		t.Fatal("Get")
	}
	if err := m.Delete("k"); err != nil || m.Len() != 0 {
		t.Fatal("Delete")
	}
	m.Err = errors.New("x")
	if _, err := m.Get("k"); err == nil {
		t.Fatal("Err ignored")
	}
}

func TestKeyringSecretStore(t *testing.T) {
	keyring.MockInit()
	var k KeyringSecretStore
	if _, err := k.Get("id"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("err = %v", err)
	}
	if err := k.Set("id", `p@ss:w/rd'"%`); err != nil {
		t.Fatal(err)
	}
	if v, err := k.Get("id"); err != nil || v != `p@ss:w/rd'"%` {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if err := k.Delete("id"); err != nil {
		t.Fatal(err)
	}
	if err := k.Delete("id"); err != nil {
		t.Fatalf("deleting a missing key: %v", err)
	}

	keyring.MockInitWithError(errors.New("no secret service"))
	t.Cleanup(keyring.MockInit)
	if _, err := k.Get("id"); err == nil || !strings.Contains(err.Error(), "keychain") {
		t.Fatalf("err = %v", err)
	}
	if err := k.Set("id", "v"); err == nil {
		t.Fatal("Set should fail")
	}
	if err := k.Delete("id"); err == nil {
		t.Fatal("Delete should fail")
	}
}

func TestSelectSecretStore(t *testing.T) {
	p, f := NewMemorySecretStore(), NewMemorySecretStore()
	if got := selectSecretStore(false, p, f); got != SecretStore(p) {
		t.Fatalf("without fallback got %T", got)
	}
	fb, ok := selectSecretStore(true, p, f).(*FallbackSecretStore)
	if !ok || fb.Primary != SecretStore(p) || fb.Fallback != SecretStore(f) {
		t.Fatal("fallback not wired")
	}

	// The platform default matches the build tag.
	_, isFallback := NewSecretStore(t.TempDir()).(*FallbackSecretStore)
	if isFallback != (runtime.GOOS == "linux") {
		t.Fatalf("NewSecretStore fallback = %v on %s", isFallback, runtime.GOOS)
	}
}

func TestFallbackSecretStore(t *testing.T) {
	p, f := NewMemorySecretStore(), NewMemorySecretStore()
	s := &FallbackSecretStore{Primary: p, Fallback: f}

	// Keychain works: secrets go there and stale fallback copies vanish.
	_ = f.Set("a", "stale")
	if err := s.Set("a", "1"); err != nil {
		t.Fatal(err)
	}
	if v, _ := p.Get("a"); v != "1" || f.Len() != 0 {
		t.Fatal("primary not used")
	}
	if v, err := s.Get("a"); v != "1" || err != nil {
		t.Fatal("Get from primary")
	}
	if _, err := s.Get("missing"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("err = %v", err)
	}

	// Keychain unavailable: fallback is used for every operation.
	p.Err = errors.New("no secret service")
	if err := s.Set("b", "2"); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.Get("b"); v != "2" {
		t.Fatal("fallback not used")
	}
	if v, err := s.Get("b"); v != "2" || err != nil {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if _, err := s.Get("zzz"); err == nil || !strings.Contains(err.Error(), "no secret service") {
		t.Fatalf("missing key should report the keychain error, got %v", err)
	}
	if err := s.Delete("b"); err != nil {
		t.Fatal(err)
	}
	if f.Len() != 0 {
		t.Fatal("fallback secret not deleted")
	}

	// Keychain back: secrets written to the fallback stay readable.
	_ = f.Set("c", "3")
	p.Err = nil
	if v, err := s.Get("c"); v != "3" || err != nil {
		t.Fatalf("Get = %q, %v", v, err)
	}

	// Both broken.
	p.Err, f.Err = errors.New("p"), errors.New("f")
	if err := s.Delete("c"); err == nil || !strings.Contains(err.Error(), "p") || !strings.Contains(err.Error(), "f") {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Get("c"); err == nil || err.Error() != "f" {
		t.Fatalf("err = %v", err)
	}
	// Only the fallback broken.
	p.Err = nil
	if err := s.Delete("c"); err == nil || err.Error() != "f" {
		t.Fatalf("err = %v", err)
	}
}

func TestFileSecretStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lontar")
	s := NewFileSecretStore(dir)

	if _, err := s.Get("x"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("err = %v", err)
	}
	if err := s.Delete("x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("reads/deletes must not create files")
	}

	const pw = `p@ss:w/rd'"%-marker`
	if err := s.Set("conn-1", pw); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("conn-2", "two"); err != nil {
		t.Fatal(err)
	}

	// A fresh instance (new process) decrypts the same data.
	s2 := NewFileSecretStore(dir)
	if v, err := s2.Get("conn-1"); err != nil || v != pw {
		t.Fatalf("Get = %q, %v", v, err)
	}
	if _, err := s2.Get("conn-3"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("err = %v", err)
	}

	raw, _ := os.ReadFile(filepath.Join(dir, SecretsFile))
	if bytes.Contains(raw, []byte("marker")) {
		t.Fatal("secret stored in plain text")
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{SecretsFile, SecretsKeyFile} {
			fi, _ := os.Stat(filepath.Join(dir, name))
			if fi.Mode().Perm() != 0o600 {
				t.Errorf("%s mode = %v", name, fi.Mode().Perm())
			}
		}
	}
	if k, _ := os.ReadFile(filepath.Join(dir, SecretsKeyFile)); len(k) != 32 {
		t.Fatalf("key length = %d", len(k))
	}

	if err := s2.Delete("conn-1"); err != nil {
		t.Fatal(err)
	}
	if err := s2.Delete("conn-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("conn-1"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatal("not deleted")
	}
	if v, _ := s.Get("conn-2"); v != "two" {
		t.Fatal("other secret lost")
	}
}

func TestFileSecretStoreTamperAndKeyErrors(t *testing.T) {
	dir := t.TempDir()
	s := NewFileSecretStore(dir)
	if err := s.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	dataPath := filepath.Join(dir, SecretsFile)
	keyPath := filepath.Join(dir, SecretsKeyFile)
	good, _ := os.ReadFile(dataPath)
	goodKey, _ := os.ReadFile(keyPath)

	expectErr := func(substr string) {
		t.Helper()
		_, err := s.Get("k")
		if err == nil || !strings.Contains(err.Error(), substr) {
			t.Fatalf("err = %v, want %q", err, substr)
		}
		if err := s.Set("k", "v"); err == nil {
			t.Fatal("Set must not overwrite unreadable secrets")
		}
		if err := s.Delete("k"); err == nil {
			t.Fatal("Delete should fail too")
		}
	}

	// Tampered ciphertext.
	var env secretsEnvelope
	_ = json.Unmarshal(good, &env)
	env.Ciphertext[0] ^= 0xff
	bad, _ := json.Marshal(env)
	_ = os.WriteFile(dataPath, bad, 0o600)
	expectErr("decrypt")

	// Bad nonce, wrong version, garbage.
	env2 := secretsEnvelope{}
	_ = json.Unmarshal(good, &env2)
	env2.Nonce = []byte{1}
	bad, _ = json.Marshal(env2)
	_ = os.WriteFile(dataPath, bad, 0o600)
	expectErr("bad nonce")
	_ = os.WriteFile(dataPath, []byte(`{"version":9}`), 0o600)
	expectErr("unsupported version")
	_ = os.WriteFile(dataPath, []byte(`garbage`), 0o600)
	expectErr("corrupt")

	// Wrong key / missing key / malformed key.
	_ = os.WriteFile(dataPath, good, 0o600)
	_ = os.WriteFile(keyPath, bytes.Repeat([]byte{7}, 32), 0o600)
	expectErr("decrypt")
	_ = os.WriteFile(keyPath, []byte("short"), 0o600)
	expectErr("invalid")
	_ = os.Remove(keyPath)
	expectErr("secrets key")

	// Data path unreadable (a directory).
	_ = os.WriteFile(keyPath, goodKey, 0o600)
	_ = os.Remove(dataPath)
	_ = os.Mkdir(dataPath, 0o700)
	if _, err := s.Get("k"); err == nil || !strings.Contains(err.Error(), "read secrets") {
		t.Fatalf("err = %v", err)
	}

	// Decrypts but the plaintext is not a JSON map.
	_ = os.Remove(dataPath)
	gcm, _ := newGCM(goodKey)
	nonce := make([]byte, gcm.NonceSize())
	env3 := secretsEnvelope{Version: secretsVersion, Nonce: nonce, Ciphertext: gcm.Seal(nil, nonce, []byte("[1]"), secretsAAD)}
	bad, _ = json.Marshal(env3)
	_ = os.WriteFile(dataPath, bad, 0o600)
	if _, err := s.Get("k"); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v", err)
	}
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }

func TestFileSecretStoreRandomFailure(t *testing.T) {
	old := randReader
	t.Cleanup(func() { randReader = old })

	randReader = failReader{}
	s := NewFileSecretStore(t.TempDir())
	if err := s.Set("k", "v"); err == nil || !strings.Contains(err.Error(), "generate secrets key") {
		t.Fatalf("err = %v", err)
	}

	// Key exists, nonce generation fails.
	randReader = old
	if err := s.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	randReader = failReader{}
	if err := s.Set("k", "w"); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("err = %v", err)
	}
}

func TestFileSecretStoreKeyWriteFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	_ = os.WriteFile(blocker, nil, 0o600)
	s := NewFileSecretStore(filepath.Join(blocker, "sub"))
	if err := s.Set("k", "v"); err == nil {
		t.Fatal("expected error")
	}
}

func TestKeyringSecretStoreService(t *testing.T) {
	keyring.MockInit()
	if err := (KeyringSecretStore{Service: "other"}).Set("id", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := (KeyringSecretStore{}).Get("id"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("zero value must use KeyringService, err = %v", err)
	}
	if v, err := keyring.Get("other", "id"); err != nil || v != "x" {
		t.Fatalf("Get = %q, %v", v, err)
	}
}
