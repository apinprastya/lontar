package store

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"lontar/internal/driver"
)

func sshConn(name string) Connection {
	c := pgConn(name)
	c.SSH = &SSHConfig{Enabled: true, Host: "bastion", User: "jump", AuthMethod: SSHAuthKey, KeyPath: "~/.ssh/id_ed25519"}
	return c
}

func TestSSHSecretsSavedKeptCleared(t *testing.T) {
	s, sec := newTestStore(t)
	c, err := s.SaveSecrets(sshConn("a"), Secrets{Password: ptr("db"), SSHPassword: ptr("sshpw"), SSHPassphrase: ptr("s3cr3t-phr")})
	if err != nil {
		t.Fatal(err)
	}
	if !c.HasPassword || !c.HasSSHPassword || !c.HasSSHPassphrase {
		t.Fatalf("flags = %v %v %v", c.HasPassword, c.HasSSHPassword, c.HasSSHPassphrase)
	}
	if c.SSH.Port != DefaultSSHPort {
		t.Fatalf("port = %d", c.SSH.Port)
	}
	pw, phrase, err := s.SSHSecrets(c.ID)
	if err != nil || pw != "sshpw" || phrase != "s3cr3t-phr" {
		t.Fatalf("ssh secrets = %q %q %v", pw, phrase, err)
	}
	// Separate keys from the database password.
	if v, _ := sec.Get(SecretKey(c.ID, SecretSSHPassword)); v != "sshpw" {
		t.Fatalf("ssh key = %q", v)
	}
	if v, _ := s.Password(c.ID); v != "db" {
		t.Fatalf("db password = %q", v)
	}
	// Never in the JSON file.
	data, _ := os.ReadFile(s.Path())
	for _, secret := range []string{"sshpw", "s3cr3t-phr", `"db"`} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("secret %s in connections.json", secret)
		}
	}
	if !strings.Contains(string(data), `"hasSshPassword": true`) {
		t.Fatalf("flag missing:\n%s", data)
	}

	// nil keeps; the flags sent by the UI are ignored.
	c.HasSSHPassword = false
	c2, err := s.SaveSecrets(c, Secrets{})
	if err != nil || !c2.HasSSHPassword || !c2.HasSSHPassphrase || !c2.HasPassword {
		t.Fatalf("keep: %+v %v", c2, err)
	}
	// "" clears only that secret.
	c3, err := s.SaveSecrets(c2, Secrets{SSHPassphrase: ptr("")})
	if err != nil || c3.HasSSHPassphrase || !c3.HasSSHPassword {
		t.Fatalf("clear: %+v %v", c3, err)
	}
	if _, phrase, _ := s.SSHSecrets(c.ID); phrase != "" {
		t.Fatalf("passphrase not cleared: %q", phrase)
	}
	// Plain Save keeps SSH secrets.
	c4, err := s.Save(c3, ptr("new"))
	if err != nil || !c4.HasSSHPassword {
		t.Fatalf("save: %+v %v", c4, err)
	}

	dup, err := s.Duplicate(c.ID)
	if err != nil || !dup.HasSSHPassword || dup.HasSSHPassphrase {
		t.Fatalf("dup: %+v %v", dup, err)
	}
	if pw, _, _ := s.SSHSecrets(dup.ID); pw != "sshpw" {
		t.Fatalf("dup ssh password = %q", pw)
	}
	if err := s.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range secretPurposes {
		if _, err := sec.Get(SecretKey(c.ID, p)); !errors.Is(err, ErrSecretNotFound) {
			t.Fatalf("secret %q left after delete", p)
		}
	}
}

func TestSSHSecretErrors(t *testing.T) {
	s, sec := newTestStore(t)
	c, err := s.SaveSecrets(sshConn("a"), Secrets{SSHPassword: ptr("x")})
	if err != nil {
		t.Fatal(err)
	}
	sec.Err = errors.New("locked")
	if _, err := s.SaveSecrets(sshConn("b"), Secrets{SSHPassphrase: ptr("p")}); err == nil || !strings.Contains(err.Error(), "SSH key passphrase") {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.SaveSecrets(c, Secrets{SSHPassword: ptr("")}); err == nil || !strings.Contains(err.Error(), "remove saved SSH password") {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := s.SSHSecrets(c.ID); err == nil {
		t.Fatal("SSHSecrets should fail")
	}
	if _, err := s.Duplicate(c.ID); err == nil || !strings.Contains(err.Error(), "read SSH password") {
		t.Fatalf("Duplicate err = %v", err)
	}
	sec.Err = nil

	// A failed later secret rolls back the earlier ones of a new connection.
	fs := &failNth{MemorySecretStore: NewMemorySecretStore(), n: 2}
	s2 := New(t.TempDir(), fs)
	if _, err := s2.SaveSecrets(sshConn("c"), Secrets{Password: ptr("a"), SSHPassword: ptr("b")}); err == nil {
		t.Fatal("expected error")
	}
	if fs.Len() != 0 {
		t.Fatalf("orphaned secrets: %d", fs.Len())
	}
	// Duplicate rolls back copied secrets when a later copy fails.
	fs.n = 0
	c5, err := s2.SaveSecrets(sshConn("d"), Secrets{Password: ptr("a"), SSHPassword: ptr("b")})
	if err != nil {
		t.Fatal(err)
	}
	fs.calls, fs.n = 0, 2
	if _, err := s2.Duplicate(c5.ID); err == nil {
		t.Fatal("expected duplicate error")
	}
	if fs.Len() != 2 {
		t.Fatalf("secrets = %d, want only the original two", fs.Len())
	}
}

// failNth fails the n-th Set call (1-based; 0 never).
type failNth struct {
	*MemorySecretStore
	n, calls int
}

func (f *failNth) Set(k, v string) error {
	f.calls++
	if f.calls == f.n {
		return errors.New("set failed")
	}
	return f.MemorySecretStore.Set(k, v)
}

func TestSSHAndSSLValidation(t *testing.T) {
	base := func() Connection {
		c := sshConn("v")
		c.Normalize()
		return c
	}
	cases := []struct {
		field string
		edit  func(*Connection)
	}{
		{"ssh.host", func(c *Connection) { c.SSH.Host = "" }},
		{"ssh.user", func(c *Connection) { c.SSH.User = "" }},
		{"ssh.keyPath", func(c *Connection) { c.SSH.KeyPath = "" }},
		{"ssh.authMethod", func(c *Connection) { c.SSH.AuthMethod = "kerberos" }},
		{"ssh.port", func(c *Connection) { c.SSH.Port = 70000 }},
		{"ssl.mode", func(c *Connection) { c.SSL = &SSLConfig{Mode: SSLPreferred} }}, // PG has no preferred
		{"ssl.mode", func(c *Connection) { c.SSL = &SSLConfig{Mode: "bogus"} }},
		{"ssl.certFile", func(c *Connection) { c.SSL = &SSLConfig{Mode: SSLRequire, CertFile: "c.pem"} }},
	}
	for _, tc := range cases {
		c := base()
		tc.edit(&c)
		var ve *ValidationError
		if err := c.Validate(); !errors.As(err, &ve) || ve.Field != tc.field {
			t.Errorf("%s: err = %v", tc.field, err)
		}
	}
	// A disabled tunnel keeps its settings without being validated fully.
	c := base()
	c.SSH.Enabled, c.SSH.Host = false, ""
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	// MySQL accepts preferred.
	m := Connection{Name: "m", Kind: driver.MySQL, Host: "h", SSL: &SSLConfig{Mode: SSLPreferred}}
	m.Normalize()
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	if tls := m.DriverConfig().TLS; tls == nil || tls.Mode != SSLPreferred {
		t.Fatalf("tls = %+v", tls)
	}
}

func TestNormalizeSSHSSL(t *testing.T) {
	// SQLite drops SSH and SSL.
	c := Connection{Name: "l", Kind: driver.SQLite, FilePath: "/x.db", SSH: &SSHConfig{Enabled: true}, SSL: &SSLConfig{Mode: "require"}}
	c.Normalize()
	if c.SSH != nil || c.SSL != nil || c.SSHEnabled() || c.DriverConfig().TLS != nil {
		t.Fatal("sqlite must not keep ssh/ssl")
	}
	// Empty structs collapse to nil; spaces are trimmed.
	p := pgConn("p")
	p.SSH = &SSHConfig{}
	p.SSL = &SSLConfig{Mode: " "}
	p.Normalize()
	if p.SSH != nil || p.SSL != nil {
		t.Fatalf("ssh/ssl = %+v %+v", p.SSH, p.SSL)
	}
	p.SSL = &SSLConfig{Mode: SSLVerifyFull, CAFile: " /ca.pem "}
	p.SSH = &SSHConfig{Enabled: true, Host: " b ", User: " u "}
	p.Normalize()
	if p.SSL.CAFile != "/ca.pem" || p.SSH.Host != "b" || p.SSH.AuthMethod != SSHAuthPassword || !p.SSHEnabled() {
		t.Fatalf("normalized = %+v %+v", p.SSL, p.SSH)
	}
	tls := p.DriverConfig().TLS
	if tls == nil || tls.Mode != SSLVerifyFull || tls.CAFile != "/ca.pem" {
		t.Fatalf("tls = %+v", tls)
	}
	// An empty mode leaves TLS to the options.
	p.SSL = &SSLConfig{CAFile: "/ca.pem"}
	if p.DriverConfig().TLS != nil {
		t.Fatal("empty mode must not configure TLS")
	}
}

func TestMigrateV2ToV3(t *testing.T) {
	s, _ := newTestStore(t)
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	v2 := `{"version":2,"connections":[
	  {"id":"a","name":"A","kind":"postgres","host":"h","tag":"none","safeMode":"off","ssl":{"mode":"prefer"}},
	  {"id":"b","name":"B","kind":"mysql","host":"h","tag":"none","safeMode":"off","ssl":{"mode":"prefer"}},
	  {"id":"c","name":"C","kind":"mysql","host":"h","tag":"none","safeMode":"off","ssl":{"mode":"verify-full"}}]}`
	if err := os.WriteFile(s.Path(), []byte(v2), 0o600); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": SSLRequire, "b": SSLPreferred, "c": SSLVerifyFull}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.SSL.Mode != want[c.ID] {
			t.Errorf("%s: mode = %q", c.ID, c.SSL.Mode)
		}
	}
	a, _ := s.Get("a")
	if _, err := s.Save(a, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(s.Path())
	var f struct{ Version int }
	_ = json.Unmarshal(data, &f)
	if f.Version != 3 {
		t.Fatalf("version = %d", f.Version)
	}
}

func TestSecretsSkipKeychainWithoutFlag(t *testing.T) {
	s, sec := newTestStore(t)
	c, err := s.SaveSecrets(sshConn("a"), Secrets{Password: ptr("db")})
	if err != nil {
		t.Fatal(err)
	}
	// The keychain is unreachable: secrets that were never saved are not
	// read at all; saved ones report the error.
	sec.Err = errors.New("no secret service")
	if pw, phrase, err := s.SSHSecrets(c.ID); err != nil || pw != "" || phrase != "" {
		t.Fatalf("ssh = %q %q %v", pw, phrase, err)
	}
	if _, err := s.Password(c.ID); err == nil {
		t.Fatal("a saved password must report the keychain error")
	}
	// Unknown connections still go to the keychain (and report it).
	if _, err := s.Password("unknown"); err == nil {
		t.Fatal("expected keychain error")
	}
}
