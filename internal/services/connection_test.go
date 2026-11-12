package services

import (
	"context"
	"dbedit/internal/sshtunnel"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dbedit/internal/conn"
	"dbedit/internal/driver"
	"dbedit/internal/driver/all"
	"dbedit/internal/store"
)

type fakeSessions struct {
	connectErr error
	getErr     error
	testedPw   string
	testedSec  conn.Secrets
	testedConn store.Connection
	testErr    error
	closedAll  bool
	session    *conn.Session
}

func (f *fakeSessions) Connect(_ context.Context, id string) (string, error) {
	if f.connectErr != nil {
		return "", f.connectErr
	}
	f.session = &conn.Session{ID: "sess-1", ConnectionID: id, Name: "n", Kind: driver.SQLite, ServerVersion: "SQLite 3.50", ConnectedAt: time.Unix(10, 0), SafeMode: store.SafeConfirm}
	return "sess-1", nil
}
func (f *fakeSessions) Disconnect(id string) error {
	if id != "sess-1" {
		return conn.ErrSessionNotFound
	}
	return nil
}
func (f *fakeSessions) Get(string) (*conn.Session, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.session, nil
}
func (f *fakeSessions) Test(_ context.Context, c store.Connection, sec conn.Secrets) (string, error) {
	f.testedConn, f.testedPw, f.testedSec = c, sec.Password, sec
	if f.testErr != nil {
		return "", f.testErr
	}
	return "PostgreSQL 16.4", nil
}
func (f *fakeSessions) DisconnectAll() error { f.closedAll = true; return nil }

func newService(t *testing.T) (*ConnectionService, *store.Store, *store.MemorySecretStore, *fakeSessions) {
	t.Helper()
	sec := store.NewMemorySecretStore()
	st := store.New(t.TempDir(), sec)
	fs := &fakeSessions{}
	return NewConnectionService(st, fs, all.Registry()), st, sec, fs
}

func ptr(s string) *string { return &s }

func TestConnectionServiceCRUD(t *testing.T) {
	svc, _, sec, _ := newService(t)

	if d := svc.Drivers(); len(d) != 4 || d[0].Kind != driver.Postgres || d[0].DefaultPort != 5432 {
		t.Fatalf("Drivers() = %+v", d)
	}

	c, err := svc.Save(store.Connection{Name: "pg", Kind: driver.Postgres, Host: "h"}, store.Secrets{Password: ptr("pw")})
	if err != nil {
		t.Fatal(err)
	}
	if !c.HasPassword {
		t.Fatal("HasPassword should be set")
	}
	if v, _ := sec.Get(c.ID); v != "pw" {
		t.Fatal("password not stored")
	}
	got, err := svc.Get(c.ID)
	if err != nil || got.Name != "pg" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	dup, err := svc.Duplicate(c.ID)
	if err != nil || dup.Name != "pg copy" {
		t.Fatalf("Duplicate = %+v, %v", dup, err)
	}
	list, err := svc.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if err := svc.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sec.Get(c.ID); !errors.Is(err, store.ErrSecretNotFound) {
		t.Fatal("secret not deleted")
	}

	// Errors are readable strings.
	_, err = svc.Save(store.Connection{Kind: driver.Postgres, Host: "h"}, store.Secrets{})
	if err == nil || err.Error() != "name is required" {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectionServiceTestPassword(t *testing.T) {
	svc, _, sec, fs := newService(t)
	c, _ := svc.Save(store.Connection{Name: "pg", Kind: driver.Postgres, Host: "h"}, store.Secrets{Password: ptr("stored")})

	// nil password on a saved connection uses the stored one.
	v, err := svc.Test(context.Background(), c, store.Secrets{})
	if err != nil || v != "PostgreSQL 16.4" || fs.testedPw != "stored" {
		t.Fatalf("Test = %q, %v, pw %q", v, err, fs.testedPw)
	}
	// An explicit password wins, even an empty one.
	_, _ = svc.Test(context.Background(), c, store.Secrets{Password: ptr("typed")})
	if fs.testedPw != "typed" {
		t.Fatalf("pw = %q", fs.testedPw)
	}
	_, _ = svc.Test(context.Background(), c, store.Secrets{Password: ptr("")})
	if fs.testedPw != "" {
		t.Fatalf("pw = %q", fs.testedPw)
	}
	// Unsaved connection with nil password: no password.
	_, _ = svc.Test(context.Background(), store.Connection{Name: "new", Kind: driver.Postgres, Host: "h"}, store.Secrets{})
	if fs.testedPw != "" || fs.testedConn.Name != "new" {
		t.Fatalf("pw = %q", fs.testedPw)
	}
	// Keychain failure is reported.
	sec.Err = errors.New("keychain locked")
	if _, err := svc.Test(context.Background(), c, store.Secrets{}); err == nil || !strings.Contains(err.Error(), "keychain locked") {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectionServiceSessions(t *testing.T) {
	svc, _, _, fs := newService(t)

	info, err := svc.Connect(context.Background(), "conn-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "sess-1" || info.ConnectionID != "conn-1" || info.ServerVersion != "SQLite 3.50" || info.Kind != driver.SQLite || info.ConnectedAt.IsZero() || info.SafeMode != store.SafeConfirm {
		t.Fatalf("info = %+v", info)
	}
	if err := svc.Disconnect("sess-1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Disconnect("other"); !errors.Is(err, conn.ErrSessionNotFound) {
		t.Fatalf("err = %v", err)
	}

	fs.getErr = errors.New("gone")
	if _, err := svc.Connect(context.Background(), "conn-1"); err == nil {
		t.Fatal("expected Get error")
	}
	fs.connectErr = errors.New("password authentication failed")
	if _, err := svc.Connect(context.Background(), "conn-1"); err == nil || err.Error() != "password authentication failed" {
		t.Fatalf("err = %v", err)
	}

	if err := svc.ServiceShutdown(); err != nil || !fs.closedAll {
		t.Fatal("ServiceShutdown must disconnect all sessions")
	}
}

// End to end through the real store and session manager with SQLite.
func TestConnectionServiceSQLite(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir, store.NewMemorySecretStore())
	reg := all.Registry()
	svc := NewConnectionService(st, conn.NewManager(st, reg), reg)

	c, err := svc.Save(store.Connection{Name: "local", Kind: driver.SQLite, FilePath: filepath.Join(dir, "x.db"), CreateIfMissing: true}, store.Secrets{})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := svc.Test(context.Background(), c, store.Secrets{}); err != nil || !strings.HasPrefix(v, "SQLite ") {
		t.Fatalf("Test = %q, %v", v, err)
	}
	info, err := svc.Connect(context.Background(), c.ID)
	if err != nil || info.Name != "local" {
		t.Fatalf("Connect = %+v, %v", info, err)
	}
	if err := svc.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if err := svc.Disconnect(info.ID); !errors.Is(err, conn.ErrSessionNotFound) {
		t.Fatalf("session should be gone after shutdown, err = %v", err)
	}
}

type fakeAccepter struct{ host, fp string }

func (f *fakeAccepter) Accept(host, fp string) error {
	f.host, f.fp = host, fp
	if fp == "bad" {
		return sshtunnel.ErrNoPendingKey
	}
	return nil
}

func TestConnectionServiceSSH(t *testing.T) {
	svc, _, sec, fs := newService(t)
	c := store.Connection{Name: "pg", Kind: driver.Postgres, Host: "h",
		SSH: &store.SSHConfig{Enabled: true, Host: "b", User: "u", AuthMethod: store.SSHAuthKey, KeyPath: "/k"}}
	saved, err := svc.Save(c, store.Secrets{Password: ptr("db"), SSHPassword: ptr("sp"), SSHPassphrase: ptr("pp")})
	if err != nil || !saved.HasSSHPassword || !saved.HasSSHPassphrase {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	// Stored SSH secrets are used when not typed; typed ones win.
	if _, err := svc.Test(context.Background(), saved, store.Secrets{SSHPassphrase: ptr("typed")}); err != nil {
		t.Fatal(err)
	}
	if fs.testedSec != (conn.Secrets{Password: "db", SSHPassword: "sp", SSHPassphrase: "typed"}) {
		t.Fatalf("secrets = %+v", fs.testedSec)
	}
	// Disabled SSH sends no SSH secrets.
	off := saved
	off.SSH = &store.SSHConfig{Enabled: false}
	_, _ = svc.Test(context.Background(), off, store.Secrets{})
	if fs.testedSec.SSHPassword != "" {
		t.Fatal("ssh secrets for a disabled tunnel")
	}
	// Keychain errors on SSH secrets.
	sec.Err = errors.New("locked")
	if _, err := svc.Test(context.Background(), saved, store.Secrets{Password: ptr("x")}); err == nil {
		t.Fatal("expected keychain error")
	}
	sec.Err = nil

	// Host key errors come back unwrapped (structured for the UI).
	unknown := &sshtunnel.HostKeyUnknownError{Kind: "hostKeyUnknown", Host: "b", FingerprintSHA256: "SHA256:x", Message: "unknown"}
	fs.testErr = fmt.Errorf("open: %w", unknown)
	if _, err := svc.Test(context.Background(), saved, store.Secrets{}); err != error(unknown) {
		t.Fatalf("err = %#v", err)
	}
	changed := &sshtunnel.HostKeyChangedError{Kind: "hostKeyChanged", Message: "changed"}
	fs.connectErr = fmt.Errorf("x: %w", changed)
	if _, err := svc.Connect(context.Background(), saved.ID); err != error(changed) {
		t.Fatalf("err = %#v", err)
	}

	if err := svc.AcceptHostKey("b", "SHA256:x"); !errors.Is(err, conn.ErrNoTunnels) {
		t.Fatalf("no accepter: %v", err)
	}
	acc := &fakeAccepter{}
	svc.HostKeys = acc
	if err := svc.AcceptHostKey("b", "SHA256:x"); err != nil || acc.host != "b" || acc.fp != "SHA256:x" {
		t.Fatalf("accept: %v %+v", err, acc)
	}
	if err := svc.AcceptHostKey("b", "bad"); !errors.Is(err, sshtunnel.ErrNoPendingKey) {
		t.Fatalf("err = %v", err)
	}
}
