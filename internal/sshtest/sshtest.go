// Package sshtest runs an in-process SSH server for tests: password and
// public-key authentication, "direct-tcpip" forwarding (what an SSH tunnel
// uses) and keepalive replies. It never reads ~/.ssh.
package sshtest

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Server is a running test SSH server.
type Server struct {
	// Addr is "127.0.0.1:port".
	Addr    string
	HostKey ssh.Signer

	// Rewrite maps forward targets ("host:port" as requested by the
	// client) to the address actually dialled, e.g. a fake internal name
	// to a container's mapped port.
	Rewrite map[string]string

	user, password string
	keys           []ssh.PublicKey

	ln       net.Listener
	mu       sync.Mutex
	conns    map[net.Conn]bool
	closed   bool
	forwards atomic.Int64
	logins   atomic.Int64
	wg       sync.WaitGroup
}

// Options configures Start.
type Options struct {
	User     string
	Password string // "" disables password auth
	// AuthorizedKeys are accepted for public-key auth.
	AuthorizedKeys []ssh.PublicKey
	// HostKey is the server key (generated when nil).
	HostKey ssh.Signer
	// Listen is the listen address (default 127.0.0.1:0).
	Listen string
}

// NewHostKey returns a fresh ed25519 signer.
func NewHostKey() ssh.Signer {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		panic(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		panic(err)
	}
	return s
}

// Start starts a server. Call Close when done.
func Start(opts Options) (*Server, error) {
	if opts.HostKey == nil {
		opts.HostKey = NewHostKey()
	}
	if opts.Listen == "" {
		opts.Listen = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		return nil, err
	}
	s := &Server{Addr: ln.Addr().String(), HostKey: opts.HostKey, user: opts.User, password: opts.Password,
		keys: opts.AuthorizedKeys, ln: ln, conns: map[net.Conn]bool{}, Rewrite: map[string]string{}}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Port returns the listening port.
func (s *Server) Port() int {
	_, p, _ := net.SplitHostPort(s.Addr)
	n, _ := strconv.Atoi(p)
	return n
}

// KnownHostsLine returns a known_hosts line for the server's key.
func (s *Server) KnownHostsLine() string {
	return knownhosts.Line([]string{s.Addr}, s.HostKey.PublicKey())
}

// Forwards returns how many direct-tcpip channels were opened.
func (s *Server) Forwards() int64 { return s.forwards.Load() }

// Logins returns how many clients authenticated.
func (s *Server) Logins() int64 { return s.logins.Load() }

// DropAll closes every client connection (simulating a network drop).
func (s *Server) DropAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		_ = c.Close()
	}
}

// Close stops the server and drops all clients.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	err := s.ln.Close()
	s.DropAll()
	s.wg.Wait()
	return err
}

func (s *Server) config() *ssh.ServerConfig {
	cfg := &ssh.ServerConfig{}
	if s.password != "" {
		cfg.PasswordCallback = func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == s.user && string(pw) == s.password {
				return nil, nil
			}
			return nil, errors.New("wrong password")
		}
	}
	if len(s.keys) > 0 {
		cfg.PublicKeyCallback = func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() != s.user {
				return nil, errors.New("unknown user")
			}
			for _, k := range s.keys {
				if string(k.Marshal()) == string(key.Marshal()) {
					return nil, nil
				}
			}
			return nil, errors.New("unknown key")
		}
	}
	cfg.AddHostKey(s.HostKey)
	return cfg
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = c.Close()
			return
		}
		s.conns[c] = true
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serve(c)
			s.mu.Lock()
			delete(s.conns, c)
			s.mu.Unlock()
		}()
	}
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	sc, chans, reqs, err := ssh.NewServerConn(c, s.config())
	if err != nil {
		return
	}
	defer sc.Close()
	s.logins.Add(1)
	go func() {
		for r := range reqs {
			// keepalive@openssh.com and anything else: reply OK when asked.
			if r.WantReply {
				_ = r.Reply(r.Type == "keepalive@openssh.com", nil)
			}
		}
	}()
	for nc := range chans {
		if nc.ChannelType() != "direct-tcpip" {
			_ = nc.Reject(ssh.UnknownChannelType, "only direct-tcpip")
			continue
		}
		target, err := parseDirect(nc.ExtraData())
		if err != nil {
			_ = nc.Reject(ssh.ConnectionFailed, err.Error())
			continue
		}
		s.mu.Lock()
		if r, ok := s.Rewrite[target]; ok {
			target = r
		}
		s.mu.Unlock()
		up, err := net.Dial("tcp", target)
		if err != nil {
			_ = nc.Reject(ssh.ConnectionFailed, err.Error())
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			_ = up.Close()
			continue
		}
		s.forwards.Add(1)
		go ssh.DiscardRequests(creqs)
		go func() {
			_, _ = io.Copy(up, ch)
			_ = up.Close()
		}()
		go func() {
			_, _ = io.Copy(ch, up)
			_ = ch.Close()
		}()
	}
}

// parseDirect decodes the direct-tcpip payload: string host, uint32 port,
// string origin host, uint32 origin port.
func parseDirect(b []byte) (string, error) {
	host, rest, ok := sshString(b)
	if !ok || len(rest) < 4 {
		return "", fmt.Errorf("bad direct-tcpip payload")
	}
	port := binary.BigEndian.Uint32(rest[:4])
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func sshString(b []byte) (string, []byte, bool) {
	if len(b) < 4 {
		return "", nil, false
	}
	n := binary.BigEndian.Uint32(b[:4])
	if uint32(len(b)-4) < n {
		return "", nil, false
	}
	return string(b[4 : 4+n]), b[4+n:], true
}

// Echo starts a TCP echo server on 127.0.0.1 and returns its address and a
// stop function.
func Echo() (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = io.Copy(c, c)
				_ = c.Close()
			}()
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }, nil
}
