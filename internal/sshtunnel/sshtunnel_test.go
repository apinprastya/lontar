package sshtunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lontar/internal/sshtest"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type env struct {
	srv     *sshtest.Server
	echo    string
	dir     string
	keys    *HostKeys
	userKH  string
	clientK ed25519.PrivateKey
	signer  ssh.Signer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	srv, err := sshtest.Start(sshtest.Options{User: "u", Password: "pw", AuthorizedKeys: []ssh.PublicKey{signer.PublicKey()}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	echo, stop, err := sshtest.Echo()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	dir := t.TempDir()
	e := &env{srv: srv, echo: echo, dir: dir, clientK: priv, signer: signer,
		userKH: filepath.Join(dir, "home", ".ssh", "known_hosts")}
	e.keys = &HostKeys{UserFiles: []string{e.userKH}, AppFile: filepath.Join(dir, "cfg", "known_hosts")}
	return e
}

// trust writes the server key to the (fake) user known_hosts.
func (e *env) trust(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(e.userKH), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.userKH, []byte(e.srv.KnownHostsLine()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (e *env) dialer(opts Options) *Dialer {
	opts.HostKeys = e.keys
	if opts.HomeDir == nil {
		opts.HomeDir = func() (string, error) { return filepath.Join(e.dir, "home"), nil }
	}
	if opts.AgentDial == nil {
		opts.AgentDial = func() (net.Conn, error) { return nil, ErrNoAgent }
	}
	return New(opts)
}

func (e *env) cfg(auth AuthMethod) Config {
	return Config{Host: "127.0.0.1", Port: e.srv.Port(), User: "u", Auth: auth, Password: "pw"}
}

func roundTrip(t *testing.T, c net.Conn) {
	t.Helper()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("echo = %q, %v", buf, err)
	}
}

func TestPasswordAuthAndDial(t *testing.T) {
	e := newEnv(t)
	e.trust(t)
	ctx := context.Background()
	tun, err := e.dialer(Options{}).Open(ctx, e.cfg(AuthPassword))
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	c, err := tun.Dial(ctx, "tcp", e.echo)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, c)
	if c.RemoteAddr().String() != e.echo || c.RemoteAddr().Network() != "tcp" {
		t.Fatalf("remote addr = %v", c.RemoteAddr())
	}
	// Deadlines work (pgx relies on them to interrupt cancelled queries).
	_ = c.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	var ne net.Error
	if _, err := c.Read(make([]byte, 1)); !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("read err = %v, want timeout", err)
	}
	_ = c.SetReadDeadline(time.Time{})
	roundTrip(t, c)
	_ = c.Close()

	// Rewritten "internal" host names are resolved by the server.
	e.srv.Rewrite["db.internal:5432"] = e.echo
	c, err = tun.Dial(ctx, "tcp", "db.internal:5432")
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, c)
	_ = c.Close()

	// A refused forward is reported without reconnecting.
	if _, err := tun.Dial(ctx, "tcp", "127.0.0.1:1"); err == nil || !strings.Contains(err.Error(), "open 127.0.0.1:1") {
		t.Fatalf("err = %v", err)
	}
	if _, err := tun.Dial(ctx, "udp", e.echo); err == nil {
		t.Fatal("udp accepted")
	}
	if e.srv.Logins() != 1 {
		t.Fatalf("logins = %d", e.srv.Logins())
	}
	if err := tun.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tun.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := tun.Dial(ctx, "tcp", e.echo); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
}

func TestWrongPassword(t *testing.T) {
	e := newEnv(t)
	e.trust(t)
	cfg := e.cfg(AuthPassword)
	cfg.Password = "nope"
	_, err := e.dialer(Options{}).Open(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "SSH authentication failed for u@") {
		t.Fatalf("err = %v", err)
	}
	// Default (empty) method is password.
	cfg.Auth = ""
	if _, err := e.dialer(Options{}).Open(context.Background(), cfg); err == nil {
		t.Fatal("expected failure")
	}
}

func writeKey(t *testing.T, path string, priv ed25519.PrivateKey, passphrase string) {
	t.Helper()
	var block *pem.Block
	var err error
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "test")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "test", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestKeyAuth(t *testing.T) {
	e := newEnv(t)
	e.trust(t)
	ctx := context.Background()
	writeKey(t, filepath.Join(e.dir, "home", ".ssh", "id_plain"), e.clientK, "")
	writeKey(t, filepath.Join(e.dir, "home", ".ssh", "id_enc"), e.clientK, "s3cret")

	cfg := e.cfg(AuthKey)
	cfg.KeyPath = "~/.ssh/id_plain"
	tun, err := e.dialer(Options{}).Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	tun.Close()

	cfg.KeyPath = "~/.ssh/id_enc"
	if _, err := e.dialer(Options{}).Open(ctx, cfg); !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("no passphrase: %v", err)
	}
	cfg.Passphrase = "wrong"
	if _, err := e.dialer(Options{}).Open(ctx, cfg); !errors.Is(err, ErrBadPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	cfg.Passphrase = "s3cret"
	tun, err = e.dialer(Options{}).Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	c, err := tun.Dial(ctx, "tcp", e.echo)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, c)
	tun.Close()

	// Errors: missing path, missing file, garbage, ~ without a home.
	for name, tc := range map[string]struct {
		path string
		home func() (string, error)
		want string
	}{
		"empty":   {"", nil, "no SSH private key"},
		"missing": {"~/.ssh/none", nil, "read SSH key"},
		"home":    {"~/.ssh/id_plain", func() (string, error) { return "", errors.New("no home") }, "expand ~"},
	} {
		cfg.KeyPath = tc.path
		if _, err := e.dialer(Options{HomeDir: tc.home}).Open(ctx, cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := ParseKey([]byte("garbage"), ""); err == nil {
		t.Fatal("garbage key accepted")
	}
}

func TestUnknownHostAcceptRetry(t *testing.T) {
	e := newEnv(t) // nothing trusted
	ctx := context.Background()
	d := e.dialer(Options{})
	_, err := d.Open(ctx, e.cfg(AuthPassword))
	var unknown *HostKeyUnknownError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want HostKeyUnknownError", err)
	}
	wantFP := ssh.FingerprintSHA256(e.srv.HostKey.PublicKey())
	wantHost := knownhosts.Normalize(e.srv.Addr)
	if unknown.Kind != "hostKeyUnknown" || unknown.Host != wantHost || unknown.KeyType != "ssh-ed25519" || unknown.FingerprintSHA256 != wantFP {
		t.Fatalf("unknown = %+v", unknown)
	}
	if err := d.HostKeys().Accept(wantHost, "SHA256:other"); !errors.Is(err, ErrNoPendingKey) {
		t.Fatalf("err = %v", err)
	}
	if err := d.HostKeys().Accept(wantHost, wantFP); err != nil {
		t.Fatal(err)
	}
	// Accepted once; a second accept has nothing pending.
	if err := d.HostKeys().Accept(wantHost, wantFP); !errors.Is(err, ErrNoPendingKey) {
		t.Fatalf("err = %v", err)
	}
	data, _ := os.ReadFile(e.keys.AppFile)
	if !strings.Contains(string(data), wantHost+" ssh-ed25519 ") {
		t.Fatalf("app known_hosts = %q", data)
	}
	if _, err := os.Stat(e.userKH); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the user's known_hosts must never be written")
	}
	tun, err := d.Open(ctx, e.cfg(AuthPassword))
	if err != nil {
		t.Fatal(err)
	}
	tun.Close()
}

func TestChangedHostKeyIsHardError(t *testing.T) {
	e := newEnv(t)
	other := sshtest.NewHostKey()
	if err := os.MkdirAll(filepath.Dir(e.userKH), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{e.srv.Addr}, other.PublicKey())
	if err := os.WriteFile(e.userKH, []byte("# comment\n"+line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(e.userKH)
	d := e.dialer(Options{})
	_, err := d.Open(context.Background(), e.cfg(AuthPassword))
	var changed *HostKeyChangedError
	if !errors.As(err, &changed) {
		t.Fatalf("err = %v, want HostKeyChangedError", err)
	}
	if changed.Kind != "hostKeyChanged" || len(changed.Known) != 1 || !strings.HasSuffix(changed.Known[0], "known_hosts:2") ||
		!strings.Contains(changed.Error(), "CHANGED") {
		t.Fatalf("changed = %+v", changed)
	}
	// It cannot be accepted from the app.
	fp := ssh.FingerprintSHA256(e.srv.HostKey.PublicKey())
	if err := d.HostKeys().Accept(changed.Host, fp); !errors.Is(err, ErrNoPendingKey) {
		t.Fatalf("accept changed: %v", err)
	}
	after, _ := os.ReadFile(e.userKH)
	if string(before) != string(after) {
		t.Fatal("user known_hosts modified")
	}
}

func TestReconnectOnce(t *testing.T) {
	e := newEnv(t)
	e.trust(t)
	ctx := context.Background()
	tun, err := e.dialer(Options{KeepAlive: -1}).Open(ctx, e.cfg(AuthPassword))
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	c, err := tun.Dial(ctx, "tcp", e.echo)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, c)

	e.srv.DropAll()
	// The open connection fails...
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte("x")); err == nil {
		if _, err := c.Read(make([]byte, 1)); err == nil {
			t.Fatal("connection survived the drop")
		}
	}
	waitFor(t, func() bool { return tun.Err() != nil })
	if !strings.Contains(tun.Err().Error(), "lost") {
		t.Fatalf("Err = %v", tun.Err())
	}
	// ...and the next dial reconnects the tunnel.
	c2, err := tun.Dial(ctx, "tcp", e.echo)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, c2)
	if e.srv.Logins() != 2 {
		t.Fatalf("logins = %d", e.srv.Logins())
	}

	// If reconnecting fails too, the error says so.
	_ = e.srv.Close()
	waitFor(t, func() bool {
		tun.mu.Lock()
		defer tun.mu.Unlock()
		return tun.client == nil
	})
	if _, err := tun.Dial(ctx, "tcp", e.echo); err == nil || !strings.Contains(err.Error(), "reconnecting failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestReconnectRace(t *testing.T) {
	e := newEnv(t)
	e.trust(t)
	ctx := context.Background()
	tun, err := e.dialer(Options{KeepAlive: -1}).Open(ctx, e.cfg(AuthPassword))
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	tun.mu.Lock()
	live := tun.client
	tun.mu.Unlock()
	// Another goroutine already replaced a dead client: reuse it.
	if c, err := tun.reconnect(ctx, &ssh.Client{}); err != nil || c != live {
		t.Fatalf("reconnect = %v, %v", c, err)
	}
	// Closed while reconnecting.
	tun.mu.Lock()
	tun.client = nil
	tun.mu.Unlock()
	_ = tun.Close()
	if _, err := tun.reconnect(ctx, nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v", err)
	}
}

func TestKeepAlive(t *testing.T) {
	e := newEnv(t)
	e.trust(t)
	tun, err := e.dialer(Options{KeepAlive: 10 * time.Millisecond}).Open(context.Background(), e.cfg(AuthPassword))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if tun.Err() != nil {
		t.Fatalf("keepalive broke a healthy tunnel: %v", tun.Err())
	}
	// A dead server is noticed by the keepalive.
	e.srv.DropAll()
	waitFor(t, func() bool { return tun.Err() != nil })
	tun.Close()
}

func TestConnectErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	d := e.dialer(Options{Timeout: time.Second})
	if _, err := d.Open(ctx, Config{}); err == nil || !strings.Contains(err.Error(), "no SSH host") {
		t.Fatalf("err = %v", err)
	}
	cfg := e.cfg("kerberos")
	if _, err := d.Open(ctx, cfg); err == nil || !strings.Contains(err.Error(), "unknown SSH authentication") {
		t.Fatalf("err = %v", err)
	}
	if _, err := d.Open(ctx, Config{Host: "127.0.0.1", Port: 1, User: "u"}); err == nil || !strings.Contains(err.Error(), "connect to") {
		t.Fatalf("err = %v", err)
	}
	// Not an SSH server.
	if _, err := d.Open(ctx, Config{Host: "127.0.0.1", Port: portOf(e.echo), User: "u"}); err == nil {
		t.Fatal("echo server accepted as SSH")
	}
	// Unreadable known_hosts (a directory where a file is expected is
	// skipped; an unreadable path is an error).
	if err := os.MkdirAll(e.userKH, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := e.keys.files(); err != nil {
		t.Fatalf("directory should be skipped: %v", err)
	}
	bad := &HostKeys{UserFiles: []string{"\x00bad"}}
	if _, _, err := bad.Callback("h:22"); err == nil {
		t.Fatal("bad path accepted")
	}
	garbage := filepath.Join(e.dir, "garbage")
	_ = os.WriteFile(garbage, []byte("@cert-authority\n"), 0o600)
	if _, _, err := (&HostKeys{UserFiles: []string{garbage}}).Callback("h:22"); err == nil {
		t.Fatal("garbage known_hosts accepted")
	}
	if err := (&HostKeys{}).Accept("h", "x"); !errors.Is(err, ErrNoPendingKey) {
		t.Fatal(err)
	}
	h := &HostKeys{}
	h.remember("h", "fp", sshtest.NewHostKey().PublicKey())
	if err := h.Accept("h", "fp"); err == nil {
		t.Fatal("accept without an app file")
	}
	h.AppFile = filepath.Join(garbage, "x") // parent is a file
	h.remember("h", "fp", sshtest.NewHostKey().PublicKey())
	if err := h.Accept("h", "fp"); err == nil {
		t.Fatal("accept into an impossible path")
	}
}

func TestKnownAlgorithms(t *testing.T) {
	dir := t.TempDir()
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rpub, _ := ssh.NewPublicKey(&rk.PublicKey)
	f := filepath.Join(dir, "kh")
	lines := knownhosts.Line([]string{"h:22"}, rpub) + "\n" + knownhosts.Line([]string{"h:22"}, sshtest.NewHostKey().PublicKey()) + "\n"
	_ = os.WriteFile(f, []byte(lines), 0o600)
	_, algos, err := (&HostKeys{UserFiles: []string{f}}).Callback("h:22")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA, ssh.KeyAlgoED25519}
	if strings.Join(algos, ",") != strings.Join(want, ",") {
		t.Fatalf("algos = %v", algos)
	}
	if _, algos, _ := (&HostKeys{UserFiles: []string{f}}).Callback("other:22"); algos != nil {
		t.Fatalf("unknown host algos = %v", algos)
	}
	// Revoked key.
	rev := filepath.Join(dir, "rev")
	_ = os.WriteFile(rev, []byte("@revoked * "+string(ssh.MarshalAuthorizedKey(rpub))), 0o600)
	cb, _, _ := (&HostKeys{UserFiles: []string{rev}}).Callback("h:22")
	if err := cb("h:22", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22}, rpub); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("err = %v", err)
	}
}

func TestExpandHome(t *testing.T) {
	home := func() (string, error) { return "/home/x", nil }
	for in, want := range map[string]string{"~": filepath.FromSlash("/home/x"), "~/k": filepath.Join("/home/x", "k"), "/abs": "/abs", "rel~": "rel~"} {
		if got, _ := ExpandHome(in, home); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	if (Config{Host: "h"}).Address() != "h:22" || (Config{Host: "h", Port: 2}).Address() != "h:2" {
		t.Fatal("Address")
	}
	if New(Options{}).HostKeys() == nil {
		t.Fatal("default host keys")
	}
}

func portOf(addr string) int {
	_, p, _ := net.SplitHostPort(addr)
	n := 0
	for _, r := range p {
		n = n*10 + int(r-'0')
	}
	return n
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
