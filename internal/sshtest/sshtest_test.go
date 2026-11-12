package sshtest

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func dial(t *testing.T, s *Server, auth ssh.AuthMethod) (*ssh.Client, error) {
	t.Helper()
	return ssh.Dial("tcp", s.Addr, &ssh.ClientConfig{
		User: "u", Auth: []ssh.AuthMethod{auth},
		HostKeyCallback: ssh.FixedHostKey(s.HostKey.PublicKey()),
		Timeout:         5 * time.Second,
	})
}

func TestServer(t *testing.T) {
	key := NewHostKey()
	s, err := Start(Options{User: "u", Password: "pw", AuthorizedKeys: []ssh.PublicKey{key.PublicKey()}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Port() == 0 || !strings.Contains(s.KnownHostsLine(), "ssh-ed25519") {
		t.Fatal("port / known hosts line")
	}
	echo, stop, err := Echo()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	if _, err := dial(t, s, ssh.Password("nope")); err == nil {
		t.Fatal("wrong password accepted")
	}
	if _, err := dial(t, s, ssh.PublicKeys(NewHostKey())); err == nil {
		t.Fatal("unknown key accepted")
	}
	c, err := dial(t, s, ssh.PublicKeys(key))
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	c, err = dial(t, s, ssh.Password("pw"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if s.Logins() != 2 {
		t.Fatalf("logins = %d", s.Logins())
	}
	if ok, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err != nil || !ok {
		t.Fatalf("keepalive = %v %v", ok, err)
	}

	// Forward with a rewrite to the echo server.
	s.Rewrite["db.internal:5432"] = echo
	conn, err := c.Dial("tcp", "db.internal:5432")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo = %q %v", buf, err)
	}
	conn.Close()
	if s.Forwards() != 1 {
		t.Fatalf("forwards = %d", s.Forwards())
	}
	// Unreachable target and a non-tcpip channel are rejected.
	if _, err := c.Dial("tcp", "127.0.0.1:1"); err == nil {
		t.Fatal("dial to closed port succeeded")
	}
	if _, _, err := c.OpenChannel("session", nil); err == nil {
		t.Fatal("session channel accepted")
	}

	s.DropAll()
	if err := c.Wait(); err == nil {
		// Wait returns when the connection is gone.
		_ = err
	}
	if _, err := s.Close(), s.Close(); err == nil {
		_ = err
	}
}

func TestParseDirect(t *testing.T) {
	if _, err := parseDirect([]byte{0, 0, 0, 9, 'x'}); err == nil {
		t.Fatal("short string accepted")
	}
	if _, err := parseDirect([]byte{0, 0, 0, 1, 'x', 0}); err == nil {
		t.Fatal("short port accepted")
	}
	if _, _, ok := sshString([]byte{1}); ok {
		t.Fatal("short length accepted")
	}
	if _, err := Start(Options{Listen: "256.0.0.1:0"}); err == nil {
		t.Fatal("bad listen address accepted")
	}
	_ = net.IPv4zero
}
