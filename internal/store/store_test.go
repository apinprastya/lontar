package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"dbedit/internal/driver"
)

func ptr(s string) *string { return &s }

func newTestStore(t *testing.T) (*Store, *MemorySecretStore) {
	t.Helper()
	sec := NewMemorySecretStore()
	s := New(filepath.Join(t.TempDir(), "cfg", AppDirName), sec)
	n := 0
	s.now = func() time.Time { n++; return time.Date(2026, 1, 1, 0, 0, n, 0, time.UTC) }
	return s, sec
}

func pgConn(name string) Connection {
	return Connection{Name: name, Kind: driver.Postgres, Host: "localhost", Port: 5432, User: "me", Database: "app"}
}

func TestDefaultDir(t *testing.T) {
	old := userConfigDir
	t.Cleanup(func() { userConfigDir = old })

	userConfigDir = func() (string, error) { return "/cfg", nil }
	d, err := DefaultDir()
	if err != nil || d != filepath.Join("/cfg", "dbedit") {
		t.Fatalf("DefaultDir() = %q, %v", d, err)
	}
	userConfigDir = func() (string, error) { return "", errors.New("no HOME") }
	if _, err := DefaultDir(); err == nil {
		t.Fatal("expected error")
	}
}

func TestCRUD(t *testing.T) {
	s, sec := newTestStore(t)

	list, err := s.List()
	if err != nil || len(list) != 0 {
		t.Fatalf("empty List() = %v, %v", list, err)
	}

	b, err := s.Save(pgConn("  beta "), ptr("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if b.ID == "" || b.Name != "beta" || b.Tag != TagNone || !b.HasPassword {
		t.Fatalf("created = %+v", b)
	}
	if b.CreatedAt.IsZero() || !b.CreatedAt.Equal(b.UpdatedAt) {
		t.Fatalf("timestamps = %v / %v", b.CreatedAt, b.UpdatedAt)
	}
	if pw, _ := s.Password(b.ID); pw != "secret" {
		t.Fatalf("password = %q", pw)
	}

	a, err := s.Save(Connection{Name: "Alpha", Kind: driver.SQLite, FilePath: "/tmp/a.db", Host: "ignored", ReadOnly: true, Tag: TagLocal, Color: "#aabbcc"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Host != "" || a.HasPassword || !a.ReadOnly {
		t.Fatalf("sqlite normalized = %+v", a)
	}

	list, _ = s.List()
	if len(list) != 2 || list[0].Name != "Alpha" || list[1].Name != "beta" {
		t.Fatalf("List() order = %+v", list)
	}

	// Update keeping the password (nil) and CreatedAt; HasPassword from the
	// caller is ignored.
	b.Host = "db.internal"
	b.HasPassword = false
	b.CreatedAt = time.Time{}
	u, err := s.Save(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !u.HasPassword || u.Host != "db.internal" || u.CreatedAt.IsZero() || !u.UpdatedAt.After(u.CreatedAt) {
		t.Fatalf("updated = %+v", u)
	}
	if pw, _ := s.Password(b.ID); pw != "secret" {
		t.Fatalf("nil password must keep the secret, got %q", pw)
	}

	// Replace and then clear the password.
	if _, err := s.Save(u, ptr("new")); err != nil {
		t.Fatal(err)
	}
	if pw, _ := s.Password(b.ID); pw != "new" {
		t.Fatalf("password = %q", pw)
	}
	cleared, err := s.Save(u, ptr(""))
	if err != nil || cleared.HasPassword {
		t.Fatalf("cleared = %+v, %v", cleared, err)
	}
	if pw, _ := s.Password(b.ID); pw != "" {
		t.Fatalf("password should be cleared, got %q", pw)
	}

	got, err := s.Get(a.ID)
	if err != nil || got.Name != "Alpha" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(nope) err = %v", err)
	}
	if _, err := s.Save(Connection{ID: "nope", Name: "x", Kind: driver.SQLite, FilePath: "x"}, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Save(unknown id) err = %v", err)
	}

	// Delete removes the secret.
	if _, err := s.Save(u, ptr("again")); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sec.Get(b.ID); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("secret should be deleted, err = %v", err)
	}
	if err := s.Delete(b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete err = %v", err)
	}
	list, _ = s.List()
	if len(list) != 1 {
		t.Fatalf("List() after delete = %+v", list)
	}
}

func TestPasswordNeverInJSON(t *testing.T) {
	s, _ := newTestStore(t)
	const pw = `p@ss:w/rd'"%-unique-marker`
	if _, err := s.Save(pgConn("x"), ptr(pw)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "unique-marker") {
		t.Fatal("password leaked into connections.json")
	}
	var f map[string]any
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if f["version"] != float64(CurrentVersion) {
		t.Fatalf("version = %v", f["version"])
	}
}

func TestFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions are not used on Windows")
	}
	s, _ := newTestStore(t)
	if err := os.MkdirAll(s.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(pgConn("x"), nil); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", fi.Mode().Perm())
	}
	di, _ := os.Stat(s.Dir())
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", di.Mode().Perm())
	}
}

func TestVersionHandling(t *testing.T) {
	s, _ := newTestStore(t)
	write := func(content string) {
		t.Helper()
		if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(s.Path(), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(`{"version": 4, "connections": []}`)
	if _, err := s.List(); !errors.Is(err, ErrNewerVersion) {
		t.Fatalf("err = %v, want ErrNewerVersion", err)
	}
	if _, err := s.Save(pgConn("x"), nil); !errors.Is(err, ErrNewerVersion) {
		t.Fatalf("Save must refuse to overwrite a newer file, err = %v", err)
	}
	data, _ := os.ReadFile(s.Path())
	if !strings.Contains(string(data), `"version": 4`) {
		t.Fatal("newer file was modified")
	}

	write(`{"connections": []}`)
	if _, err := s.List(); err == nil || !strings.Contains(err.Error(), "invalid version") {
		t.Fatalf("err = %v", err)
	}

	write(`{not json`)
	for name, fn := range map[string]func() error{
		"List":      func() error { _, err := s.List(); return err },
		"Get":       func() error { _, err := s.Get("x"); return err },
		"Delete":    func() error { return s.Delete("x") },
		"Duplicate": func() error { _, err := s.Duplicate("x"); return err },
	} {
		if err := fn(); err == nil || !strings.Contains(err.Error(), "corrupt") {
			t.Errorf("%s err = %v, want corrupt", name, err)
		}
	}

	// Unknown future fields in a v1 file are tolerated (SSH/SSL round trip).
	write(`{"version":1,"connections":[{"id":"1","name":"n","kind":"postgres","host":"h","tag":"prod",
	  "ssh":{"enabled":true,"host":"bastion","user":"me","authMethod":"agent"},"ssl":{"mode":"require"},"futureField":1}]}`)
	c, err := s.Get("1")
	if err != nil {
		t.Fatal(err)
	}
	if c.SSH == nil || c.SSH.Host != "bastion" || c.SSL == nil || c.SSL.Mode != "require" {
		t.Fatalf("ssh/ssl = %+v %+v", c.SSH, c.SSL)
	}
	if _, err := s.Save(c, nil); err != nil {
		t.Fatal(err)
	}
	c2, _ := s.Get("1")
	if c2.SSH == nil || c2.SSH.AuthMethod != "agent" {
		t.Fatal("SSH config lost on save")
	}
}

func TestReadError(t *testing.T) {
	s, _ := newTestStore(t)
	if err := os.MkdirAll(s.Path(), 0o700); err != nil { // a directory where the file should be
		t.Fatal(err)
	}
	if _, err := s.List(); err == nil || !strings.Contains(err.Error(), "read connections") {
		t.Fatalf("err = %v", err)
	}
}

func TestSaveValidation(t *testing.T) {
	s, sec := newTestStore(t)
	if _, err := s.Save(Connection{Kind: driver.Postgres, Host: "h"}, ptr("pw")); err == nil {
		t.Fatal("expected validation error")
	} else {
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Field != "name" {
			t.Fatalf("err = %#v", err)
		}
	}
	if sec.Len() != 0 {
		t.Fatal("secret stored for an invalid connection")
	}
}

func TestSecretErrors(t *testing.T) {
	s, sec := newTestStore(t)
	c, err := s.Save(pgConn("x"), ptr("pw"))
	if err != nil {
		t.Fatal(err)
	}
	sec.Err = errors.New("keychain locked")

	if _, err := s.Save(pgConn("y"), ptr("pw")); err == nil || !strings.Contains(err.Error(), "save password") {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Save(c, ptr("")); err == nil || !strings.Contains(err.Error(), "remove saved password") {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Password(c.ID); err == nil {
		t.Fatal("Password should fail")
	}
	if _, err := s.Duplicate(c.ID); err == nil || !strings.Contains(err.Error(), "read password") {
		t.Fatalf("Duplicate err = %v", err)
	}
	if err := s.Delete(c.ID); err == nil || !strings.Contains(err.Error(), "could not be removed") {
		t.Fatalf("Delete err = %v", err)
	}
	if _, err := s.Get(c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("connection should be deleted even if the secret could not be")
	}
}

// setOnlyFailing fails Set but not Get.
type setOnlyFailing struct{ *MemorySecretStore }

func (setOnlyFailing) Set(string, string) error { return errors.New("set failed") }

func TestDuplicate(t *testing.T) {
	s, sec := newTestStore(t)
	orig := pgConn("Prod DB")
	orig.Options = map[string]string{"sslmode": "require"}
	orig.Tag = TagProd
	c, err := s.Save(orig, ptr("pw"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Duplicate(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID == c.ID || d.Name != "Prod DB copy" || d.Tag != TagProd || d.Options["sslmode"] != "require" || !d.HasPassword {
		t.Fatalf("duplicate = %+v", d)
	}
	if pw, _ := s.Password(d.ID); pw != "pw" {
		t.Fatalf("duplicate password = %q", pw)
	}
	if _, err := s.Duplicate("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}

	// Flag set but secret missing (e.g. keychain wiped): duplicate without one.
	_ = sec.Delete(c.ID)
	d2, err := s.Duplicate(c.ID)
	if err != nil || d2.HasPassword {
		t.Fatalf("d2 = %+v, %v", d2, err)
	}

	// Copying the secret fails.
	_ = sec.Set(c.ID, "pw")
	s.secrets = setOnlyFailing{sec}
	if _, err := s.Duplicate(c.ID); err == nil || !strings.Contains(err.Error(), "save password") {
		t.Fatalf("err = %v", err)
	}
}

func TestCopyName(t *testing.T) {
	long := strings.Repeat("é", MaxNameLen) // 2 bytes per rune
	got := copyName(long)
	if len(got) > MaxNameLen || !strings.HasSuffix(got, " copy") || !strings.HasPrefix(got, "é") {
		t.Fatalf("copyName = %q (%d bytes)", got, len(got))
	}
	if copyName("a") != "a copy" {
		t.Fatal("short name")
	}
}

func TestWriteFailureRollsBack(t *testing.T) {
	s, sec := newTestStore(t)
	c, err := s.Save(pgConn("x"), nil)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.Path())

	old := osRename
	t.Cleanup(func() { osRename = old })
	osRename = func(string, string) error { return errors.New("disk full") }

	if _, err := s.Save(pgConn("y"), ptr("pw")); err == nil {
		t.Fatal("expected write error")
	}
	if sec.Len() != 0 {
		t.Fatal("secret of a failed create must be rolled back")
	}
	if _, err := s.Duplicate(c.ID); err == nil {
		t.Fatal("expected write error")
	}
	if err := s.Delete(c.ID); err == nil {
		t.Fatal("expected write error")
	}
	after, _ := os.ReadFile(s.Path())
	if string(before) != string(after) {
		t.Fatal("failed write modified connections.json")
	}
	entries, _ := os.ReadDir(s.Dir())
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestWriteFileAtomicErrors(t *testing.T) {
	dir := t.TempDir()

	// Directory cannot be created (parent is a file).
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(blocker, "sub", "x.json"), []byte("{}")); err == nil {
		t.Fatal("expected mkdir error")
	}

	// Write failure.
	oldW := osWriteFile
	t.Cleanup(func() { osWriteFile = oldW })
	osWriteFile = func(*os.File, []byte) error { return errors.New("io error") }
	if err := writeFileAtomic(filepath.Join(dir, "y.json"), []byte("{}")); err == nil {
		t.Fatal("expected write error")
	}
	osWriteFile = oldW

	// Success replaces existing content.
	p := filepath.Join(dir, "z.json")
	if err := writeFileAtomic(p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(p, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Fatalf("content = %q", b)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name  string
		c     Connection
		field string
	}{
		{"ok pg", pgConn("x"), ""},
		{"ok default port", Connection{Name: "x", Kind: driver.MySQL, Host: "h"}, ""},
		{"ok sqlite", Connection{Name: "x", Kind: driver.SQLite, FilePath: "a.db"}, ""},
		{"no name", Connection{Kind: driver.MySQL, Host: "h"}, "name"},
		{"long name", Connection{Name: strings.Repeat("x", MaxNameLen+1), Kind: driver.MySQL, Host: "h"}, "name"},
		{"bad kind", Connection{Name: "x", Kind: "oracle", Host: "h"}, "kind"},
		{"bad tag", Connection{Name: "x", Kind: driver.MySQL, Host: "h", Tag: "qa"}, "tag"},
		{"bad color", Connection{Name: "x", Kind: driver.MySQL, Host: "h", Color: "red"}, "color"},
		{"no host", Connection{Name: "x", Kind: driver.MariaDB}, "host"},
		{"port high", Connection{Name: "x", Kind: driver.Postgres, Host: "h", Port: 70000}, "port"},
		{"port negative", Connection{Name: "x", Kind: driver.Postgres, Host: "h", Port: -1}, "port"},
		{"no file", Connection{Name: "x", Kind: driver.SQLite}, "filePath"},
		{"empty option", Connection{Name: "x", Kind: driver.SQLite, FilePath: "a", Options: map[string]string{" ": "v"}}, "options"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.c
			c.Normalize()
			err := c.Validate()
			if tc.field == "" {
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != tc.field || ve.Error() == "" {
				t.Fatalf("err = %v, want field %q", err, tc.field)
			}
		})
	}
}

func TestNormalizeAndDriverConfig(t *testing.T) {
	c := Connection{Name: "x", Kind: driver.Postgres, Host: " h ", FilePath: "f", ReadOnly: true, CreateIfMissing: true, Options: map[string]string{}}
	c.Normalize()
	if c.Host != "h" || c.FilePath != "" || c.ReadOnly || c.CreateIfMissing || c.Options != nil || c.Tag != TagNone {
		t.Fatalf("normalized = %+v", c)
	}
	sq := Connection{Name: "s", Kind: driver.SQLite, FilePath: "/a.db", ReadOnly: true, CreateIfMissing: true, Options: map[string]string{"k": "v"}}
	dc := sq.DriverConfig()
	if dc.Kind != driver.SQLite || dc.FilePath != "/a.db" || !dc.ReadOnly || !dc.CreateIfMissing || dc.Options["k"] != "v" {
		t.Fatalf("DriverConfig = %+v", dc)
	}
	pc := pgConn("p").DriverConfig()
	if pc.Host != "localhost" || pc.Port != 5432 || pc.User != "me" || pc.Database != "app" {
		t.Fatalf("DriverConfig = %+v", pc)
	}
	for _, tag := range Tags {
		if !tag.Valid() {
			t.Errorf("%q invalid", tag)
		}
	}
}

func TestCloneIsDeep(t *testing.T) {
	c := Connection{Options: map[string]string{"a": "1"}, SSH: &SSHConfig{Host: "h"}, SSL: &SSLConfig{Mode: "require"}}
	d := c.clone()
	d.Options["a"] = "2"
	d.SSH.Host = "x"
	d.SSL.Mode = "disable"
	if c.Options["a"] != "1" || c.SSH.Host != "h" || c.SSL.Mode != "require" {
		t.Fatal("clone shares state")
	}
}

func TestMigrateV1ToV2(t *testing.T) {
	s, _ := newTestStore(t)
	if err := os.MkdirAll(s.Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	v1 := `{"version":1,"connections":[
	  {"id":"a","name":"A","kind":"postgres","host":"h","tag":"prod"},
	  {"id":"b","name":"B","kind":"sqlite","filePath":"/x.db","tag":"none","readOnly":true}]}`
	if err := os.WriteFile(s.Path(), []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.SafeMode != SafeOff {
			t.Fatalf("%s: safeMode = %q, want off", c.ID, c.SafeMode)
		}
	}
	// Reading never rewrites the file.
	if data, _ := os.ReadFile(s.Path()); !strings.Contains(string(data), `"version":1`) {
		t.Fatal("load must not rewrite the file")
	}
	// The next write stores version 2 with the field.
	a, _ := s.Get("a")
	a.SafeMode = SafeConfirm
	if _, err := s.Save(a, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(s.Path())
	var f map[string]any
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if f["version"] != float64(CurrentVersion) {
		t.Fatalf("version = %v", f["version"])
	}
	conns := f["connections"].([]any)
	got := map[string]string{}
	for _, c := range conns {
		m := c.(map[string]any)
		got[m["id"].(string)] = m["safeMode"].(string)
	}
	if got["a"] != "confirm" || got["b"] != "off" {
		t.Fatalf("safe modes = %v", got)
	}
}

func TestSafeModeValidation(t *testing.T) {
	c := Connection{Name: "x", Kind: "postgres", Host: "h"}
	c.Normalize()
	if c.SafeMode != SafeOff {
		t.Fatalf("default = %q", c.SafeMode)
	}
	for _, m := range SafeModes {
		c.SafeMode = m
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	c.SafeMode = "paranoid"
	var ve *ValidationError
	if err := c.Validate(); !errors.As(err, &ve) || ve.Field != "safeMode" {
		t.Fatalf("err = %v", err)
	}
	// Read-only safe mode opens the driver read-only on every engine.
	c.SafeMode = SafeReadOnly
	if !c.DriverConfig().ReadOnly {
		t.Fatal("readonly safe mode must set driver ReadOnly")
	}
	c.SafeMode = SafeConfirm
	if c.DriverConfig().ReadOnly {
		t.Fatal("confirm mode must not be read-only")
	}
}
