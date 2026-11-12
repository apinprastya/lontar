//go:build !windows

package sshtunnel

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/agent"
)

func TestAgentAuth(t *testing.T) {
	e := newEnv(t)
	e.trust(t)
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: e.clientK}); err != nil {
		t.Fatal(err)
	}
	sockDir, err := os.MkdirTemp("", "ag")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	sock := filepath.Join(sockDir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _ = agent.ServeAgent(keyring, c); _ = c.Close() }()
		}
	}()
	ctx := context.Background()
	d := e.dialer(Options{AgentDial: func() (net.Conn, error) { return dialAgentSocket(sock) }})
	tun, err := d.Open(ctx, e.cfg(AuthAgent))
	if err != nil {
		t.Fatal(err)
	}
	tun.Close()

	// The default dialer uses $SSH_AUTH_SOCK (pointed at the test agent).
	t.Setenv("SSH_AUTH_SOCK", sock)
	if c, err := dialAgent(); err != nil {
		t.Fatal(err)
	} else {
		c.Close()
	}
	t.Setenv("SSH_AUTH_SOCK", "")
	if _, err := dialAgent(); !errors.Is(err, ErrNoAgent) {
		t.Fatalf("err = %v", err)
	}
	// No agent.
	if _, err := e.dialer(Options{}).Open(ctx, e.cfg(AuthAgent)); err == nil || !strings.Contains(err.Error(), "SSH agent") {
		t.Fatalf("err = %v", err)
	}
}
