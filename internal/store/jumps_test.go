package store

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func jumpConn(name string, hosts ...string) Connection {
	c := sshConn(name)
	for _, h := range hosts {
		c.SSH.Jumps = append(c.SSH.Jumps, SSHHop{Host: h, User: "j-" + h})
	}
	return c
}

func hopSecret(t *testing.T, sec *MemorySecretStore, connID, hopID, what string) string {
	t.Helper()
	v, err := sec.Get(SecretKey(connID, JumpSecretPurpose(hopID, what)))
	if errors.Is(err, ErrSecretNotFound) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestJumpsRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	c := jumpConn("a", " one ", "two")
	c.SSH.Jumps[1].AuthMethod, c.SSH.Jumps[1].KeyPath, c.SSH.Jumps[1].Port = SSHAuthKey, " ~/.ssh/k ", 2222
	// Flags and IDs sent by the UI for a new connection are ignored.
	c.SSH.Jumps[0].ID, c.SSH.Jumps[0].HasPassword = "forged", true
	saved, err := s.SaveSecrets(c, Secrets{})
	if err != nil {
		t.Fatal(err)
	}
	j := saved.SSH.Jumps
	if len(j) != 2 || j[0].Host != "one" || j[0].Port != DefaultSSHPort || j[0].AuthMethod != SSHAuthPassword ||
		j[1].KeyPath != "~/.ssh/k" || j[1].Port != 2222 || j[0].HasPassword {
		t.Fatalf("jumps = %+v", j)
	}
	if j[0].ID == "" || j[0].ID == "forged" || j[0].ID == j[1].ID {
		t.Fatalf("ids = %q %q", j[0].ID, j[1].ID)
	}
	got, err := s.Get(saved.ID)
	if err != nil || len(got.SSH.Jumps) != 2 || got.SSH.Jumps[1] != j[1] {
		t.Fatalf("get = %+v %v", got.SSH, err)
	}
	// clone copies the slice.
	got.SSH.Jumps[0].Host = "changed"
	if again, _ := s.Get(saved.ID); again.SSH.Jumps[0].Host != "one" {
		t.Fatal("Get shares the jumps slice")
	}

	// A connection without jump hosts has no "jumps" in the file (old
	// files keep working unchanged).
	plain, err := s.SaveSecrets(sshConn("p"), Secrets{})
	if err != nil || plain.SSH.Jumps != nil {
		t.Fatalf("plain = %+v %v", plain.SSH, err)
	}
	var f struct {
		Connections []map[string]json.RawMessage
	}
	data, _ := os.ReadFile(s.Path())
	_ = json.Unmarshal(data, &f)
	for _, raw := range f.Connections {
		var ssh map[string]json.RawMessage
		_ = json.Unmarshal(raw["ssh"], &ssh)
		var name string
		_ = json.Unmarshal(raw["name"], &name)
		if _, ok := ssh["jumps"]; ok != (name == "a") {
			t.Fatalf("%s: jumps present = %v", name, ok)
		}
	}
	// An empty list collapses to nil.
	plain.SSH.Jumps = []SSHHop{}
	if p2, err := s.SaveSecrets(plain, Secrets{}); err != nil || p2.SSH.Jumps != nil {
		t.Fatalf("empty = %+v %v", p2.SSH, err)
	}
	// A disabled tunnel with only jump hosts is kept.
	d := pgConn("d")
	d.SSH = &SSHConfig{Jumps: []SSHHop{{Host: "x"}}}
	d.Normalize()
	if d.SSH == nil || len(d.SSH.Jumps) != 1 {
		t.Fatal("jumps of a disabled tunnel dropped")
	}
}

func TestJumpValidation(t *testing.T) {
	base := func() Connection {
		c := jumpConn("v", "a", "b")
		c.Normalize()
		return c
	}
	cases := []struct {
		field string
		msg   string
		edit  func(*Connection)
	}{
		{"ssh.jumps", "at most 3", func(c *Connection) {
			c.SSH.Jumps = append(c.SSH.Jumps, c.SSH.Jumps...)
		}},
		{"ssh.jumps.1.host", "jump host 2: host is required", func(c *Connection) { c.SSH.Jumps[1].Host = "" }},
		{"ssh.jumps.0.user", "jump host 1: user", func(c *Connection) { c.SSH.Jumps[0].User = "" }},
		{"ssh.jumps.0.keyPath", "private key", func(c *Connection) { c.SSH.Jumps[0].AuthMethod = SSHAuthKey }},
		{"ssh.jumps.1.authMethod", "kerberos", func(c *Connection) { c.SSH.Jumps[1].AuthMethod = "kerberos" }},
		{"ssh.jumps.0.port", "port", func(c *Connection) { c.SSH.Jumps[0].Port = 70000 }},
	}
	for _, tc := range cases {
		c := base()
		tc.edit(&c)
		var ve *ValidationError
		if err := c.Validate(); !errors.As(err, &ve) || ve.Field != tc.field || !strings.Contains(ve.Message, tc.msg) {
			t.Errorf("%s: err = %v", tc.field, err)
		}
	}
	// Exactly three is fine.
	c := jumpConn("v", "a", "b", "c")
	c.Normalize()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	// A disabled tunnel keeps an incomplete hop, but not a bad method.
	c.SSH.Enabled, c.SSH.Jumps[0].Host = false, ""
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.SSH.Jumps[0].AuthMethod = "x"
	if err := c.Validate(); err == nil {
		t.Fatal("bad method accepted on a disabled tunnel")
	}
	// Save refuses too, and refuses secrets for hops that do not exist.
	s, _ := newTestStore(t)
	bad := jumpConn("b", "")
	if _, err := s.SaveSecrets(bad, Secrets{}); err == nil {
		t.Fatal("empty hop host saved")
	}
	if _, err := s.SaveSecrets(jumpConn("b", "x"), Secrets{Jumps: make([]HopSecrets, 2)}); err == nil {
		t.Fatal("secrets for a missing hop accepted")
	}
}

func TestJumpSecretsReorderRemoveDelete(t *testing.T) {
	s, sec := newTestStore(t)
	c, err := s.SaveSecrets(jumpConn("a", "one", "two", "three"), Secrets{
		SSHPassword: ptr("final-pw"),
		Jumps: []HopSecrets{
			{Password: ptr("pw-one")},
			{Password: ptr("pw-two"), Passphrase: ptr("phrase-two")},
			{},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{c.SSH.Jumps[0].ID, c.SSH.Jumps[1].ID, c.SSH.Jumps[2].ID}
	j := c.SSH.Jumps
	if !j[0].HasPassword || j[0].HasPassphrase || !j[1].HasPassword || !j[1].HasPassphrase || j[2].HasPassword {
		t.Fatalf("flags = %+v", j)
	}
	if hopSecret(t, sec, c.ID, ids[1], "passphrase") != "phrase-two" {
		t.Fatal("hop 2 passphrase not stored under its hop ID")
	}
	// Never in connections.json.
	data, _ := os.ReadFile(s.Path())
	for _, secret := range []string{"pw-one", "pw-two", "phrase-two", "final-pw"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("secret %s in connections.json", secret)
		}
	}
	byID, err := s.JumpSecrets(c.ID)
	if err != nil || byID[ids[0]].Password != "pw-one" || byID[ids[1]] != (HopSecret{"pw-two", "phrase-two"}) || byID[ids[2]] != (HopSecret{}) {
		t.Fatalf("JumpSecrets = %+v %v", byID, err)
	}

	// Reorder: three, one, two. The secrets follow the hops (by ID), the
	// UI's flags are ignored, and a secret given by position goes to the
	// hop now at that position.
	r := c
	r.SSH = &SSHConfig{}
	*r.SSH = *c.SSH
	r.SSH.Jumps = []SSHHop{j[2], j[0], j[1]}
	r.SSH.Jumps[1].HasPassword = false
	r2, err := s.SaveSecrets(r, Secrets{Jumps: []HopSecrets{{Password: ptr("pw-three")}}})
	if err != nil {
		t.Fatal(err)
	}
	rj := r2.SSH.Jumps
	if rj[0].ID != ids[2] || rj[1].ID != ids[0] || rj[2].ID != ids[1] {
		t.Fatalf("ids after reorder = %q %q %q", rj[0].ID, rj[1].ID, rj[2].ID)
	}
	if !rj[0].HasPassword || !rj[1].HasPassword || !rj[2].HasPassphrase || rj[0].Host != "three" || rj[1].Host != "one" {
		t.Fatalf("after reorder = %+v", rj)
	}
	byID, _ = s.JumpSecrets(c.ID)
	if byID[ids[0]].Password != "pw-one" || byID[ids[1]].Password != "pw-two" || byID[ids[2]].Password != "pw-three" {
		t.Fatalf("secrets moved with the position: %+v", byID)
	}

	// Remove the middle hop ("one"): its secrets go; the others stay.
	rm := r2
	rm.SSH = &SSHConfig{}
	*rm.SSH = *r2.SSH
	rm.SSH.Jumps = []SSHHop{rj[0], rj[2]}
	if _, err := s.SaveSecrets(rm, Secrets{}); err != nil {
		t.Fatal(err)
	}
	if hopSecret(t, sec, c.ID, ids[0], "password") != "" {
		t.Fatal("removed hop's password left behind")
	}
	if hopSecret(t, sec, c.ID, ids[1], "passphrase") != "phrase-two" || hopSecret(t, sec, c.ID, ids[2], "password") != "pw-three" {
		t.Fatal("remaining hops lost their secrets")
	}

	// A hop with an ID the connection does not have is a new hop: it does
	// not inherit anything, and a duplicated ID is not shared.
	cur, _ := s.Get(c.ID)
	cur.SSH.Jumps = append(cur.SSH.Jumps, SSHHop{ID: cur.SSH.Jumps[0].ID, Host: "dup", User: "u", HasPassword: true})
	cur2, err := s.SaveSecrets(cur, Secrets{Jumps: []HopSecrets{{}, {Passphrase: ptr("")}, {Password: ptr("pw-dup")}}})
	if err != nil {
		t.Fatal(err)
	}
	cj := cur2.SSH.Jumps
	if cj[2].ID == cj[0].ID || !cj[2].HasPassword || cj[1].HasPassphrase {
		t.Fatalf("dup = %+v", cj)
	}
	if hopSecret(t, sec, c.ID, ids[2], "password") != "pw-three" {
		t.Fatal("duplicate ID overwrote the original hop's secret")
	}

	// Duplicate copies the hop secrets.
	dup, err := s.Duplicate(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	dsec, _ := s.JumpSecrets(dup.ID)
	if dsec[cj[0].ID].Password != "pw-three" || dsec[cj[2].ID].Password != "pw-dup" {
		t.Fatalf("dup secrets = %+v", dsec)
	}

	// Disabling SSH entirely (clearing it) removes every hop's secrets.
	off := cur2
	off.SSH = nil
	if _, err := s.SaveSecrets(off, Secrets{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{cj[0].ID, cj[1].ID, cj[2].ID} {
		if hopSecret(t, sec, c.ID, id, "password") != "" || hopSecret(t, sec, c.ID, id, "passphrase") != "" {
			t.Fatal("hop secrets left after removing SSH")
		}
	}

	// Deleting the connection deletes the hop secrets.
	if err := s.Delete(dup.ID); err != nil {
		t.Fatal(err)
	}
	for _, h := range dup.SSH.Jumps {
		if hopSecret(t, sec, dup.ID, h.ID, "password") != "" {
			t.Fatal("hop secret left after delete")
		}
	}
	if m, err := s.JumpSecrets("unknown"); err != nil || len(m) != 0 {
		t.Fatalf("unknown = %v %v", m, err)
	}
}

func TestJumpSecretErrors(t *testing.T) {
	s, sec := newTestStore(t)
	c, err := s.SaveSecrets(jumpConn("a", "one"), Secrets{Jumps: []HopSecrets{{Password: ptr("pw-one")}}})
	if err != nil {
		t.Fatal(err)
	}
	// A hop without saved secrets does not touch the keychain.
	n, err := s.SaveSecrets(jumpConn("n", "x"), Secrets{})
	if err != nil {
		t.Fatal(err)
	}
	sec.Err = errors.New("locked")
	if m, err := s.JumpSecrets(n.ID); err != nil || len(m) != 1 {
		t.Fatalf("no-secret hop = %v %v", m, err)
	}
	if _, err := s.JumpSecrets(c.ID); err == nil || !strings.Contains(err.Error(), "jump host 1 SSH password") ||
		strings.Contains(err.Error(), "pw-one") {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.SaveSecrets(c, Secrets{Jumps: []HopSecrets{{Passphrase: ptr("p")}}}); err == nil ||
		!strings.Contains(err.Error(), "save jump host 1 SSH key passphrase") || strings.Contains(err.Error(), `"p"`) {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.SaveSecrets(c, Secrets{Jumps: []HopSecrets{{Password: ptr("")}}}); err == nil ||
		!strings.Contains(err.Error(), "remove saved jump host 1 SSH password") {
		t.Fatalf("err = %v", err)
	}
	removed := c
	removed.SSH = &SSHConfig{}
	*removed.SSH = *c.SSH
	removed.SSH.Jumps = nil
	if _, err := s.SaveSecrets(removed, Secrets{}); err == nil || !strings.Contains(err.Error(), "jump host secrets") {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Duplicate(c.ID); err == nil || !strings.Contains(err.Error(), "read jump host 1 SSH password") {
		t.Fatalf("Duplicate err = %v", err)
	}
	if err := s.Delete(c.ID); err == nil {
		t.Fatal("Delete should report the keychain error")
	}
	sec.Err = nil
	// A stale flag (secret gone from the keychain) reads as empty and is
	// dropped by Duplicate.
	_ = sec.Delete(SecretKey(n.ID, "x"))
	c2, _ := s.SaveSecrets(jumpConn("c2", "h"), Secrets{Jumps: []HopSecrets{{Password: ptr("x")}}})
	_ = sec.Delete(SecretKey(c2.ID, JumpSecretPurpose(c2.SSH.Jumps[0].ID, "password")))
	if m, err := s.JumpSecrets(c2.ID); err != nil || m[c2.SSH.Jumps[0].ID].Password != "" {
		t.Fatalf("stale = %v %v", m, err)
	}
	if d, err := s.Duplicate(c2.ID); err != nil || d.SSH.Jumps[0].HasPassword {
		t.Fatalf("dup stale = %+v %v", d.SSH, err)
	}

	// A failed hop secret rolls back the secrets already set for a new
	// connection, and for a new hop of an existing one.
	fs := &failNth{MemorySecretStore: NewMemorySecretStore(), n: 2}
	s2 := New(t.TempDir(), fs)
	if _, err := s2.SaveSecrets(jumpConn("c", "x"), Secrets{SSHPassword: ptr("a"), Jumps: []HopSecrets{{Password: ptr("b")}}}); err == nil {
		t.Fatal("expected error")
	}
	if fs.Len() != 0 {
		t.Fatalf("orphaned secrets: %d", fs.Len())
	}
	fs.n = 0
	c3, err := s2.SaveSecrets(jumpConn("c3", "x"), Secrets{})
	if err != nil {
		t.Fatal(err)
	}
	c3.SSH.Jumps = append(c3.SSH.Jumps, SSHHop{Host: "y", User: "u"})
	fs.calls, fs.n = 0, 2
	if _, err := s2.SaveSecrets(c3, Secrets{Jumps: []HopSecrets{{}, {Password: ptr("a"), Passphrase: ptr("b")}}}); err == nil {
		t.Fatal("expected error")
	}
	if fs.Len() != 0 {
		t.Fatalf("orphaned new-hop secrets: %d", fs.Len())
	}
	// Duplicate rolls back copied hop secrets when a later copy fails.
	fs.n = 0
	c4, err := s2.SaveSecrets(jumpConn("c4", "x", "y"), Secrets{Jumps: []HopSecrets{{Password: ptr("a")}, {Password: ptr("b")}}})
	if err != nil {
		t.Fatal(err)
	}
	fs.calls, fs.n = 0, 2
	if _, err := s2.Duplicate(c4.ID); err == nil {
		t.Fatal("expected duplicate error")
	}
	if fs.Len() != 2 {
		t.Fatalf("secrets = %d, want only the original two", fs.Len())
	}
}
