// Package sshtunnel connects to databases through an SSH server (a bastion)
// with golang.org/x/crypto/ssh.
//
// # Approach: in-process dialer
//
// No local port is opened. A Tunnel is an SSH client connection; its Dial
// method opens a "direct-tcpip" channel to the database host for every
// database connection. The drivers take it as their dialer: pgx via
// pgconn.Config.DialFunc (with a LookupFunc that skips local DNS), and
// go-sql-driver/mysql via Config.DialFunc. Channels do not support
// deadlines, which pgx needs to interrupt a cancelled query, so each
// channel is bridged through a net.Pipe that does.
//
// # Host keys
//
// Host keys are always verified (never InsecureIgnoreHostKey) against the
// user's known_hosts (read only) and Lontar's own known_hosts in the config
// directory. An unknown key fails with *HostKeyUnknownError; the UI shows
// its fingerprint and HostKeys.Accept appends exactly that key to Lontar's
// file. A changed key fails with *HostKeyChangedError and can only be
// resolved by editing the file by hand.
//
// # Lifetime
//
// A keepalive request is sent every KeepAlive (30 s). When the SSH
// connection drops, open database connections fail with an error; the next
// Dial (the pool's next new connection) reconnects the tunnel once and
// reports the error if that fails too.
//
// # Jump hosts
//
// Config.Jumps lists up to a few SSH servers to go through before the
// final one (like OpenSSH's ProxyJump). Jumps[0] is dialled directly from
// this machine; every next server, and finally Config itself, is reached
// through a direct-tcpip channel of the previous one. Host keys and
// authentication are checked on every hop, and errors name the hop
// (*HopError, and the Hop/HopLabel fields of the host key errors). The
// tunnel keeps the whole chain: Close closes it from the last client to the
// first, and a lost connection rebuilds the whole chain.
package sshtunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// AuthMethod selects how the tunnel authenticates.
type AuthMethod string

// Authentication methods.
const (
	AuthPassword AuthMethod = "password"
	AuthKey      AuthMethod = "key"
	AuthAgent    AuthMethod = "agent"
)

// DefaultPort is the SSH port used when Config.Port is 0.
const DefaultPort = 22

// Defaults for Options.
const (
	DefaultKeepAlive = 30 * time.Second
	DefaultTimeout   = 15 * time.Second
)

// Config describes one SSH server and the credentials for it.
type Config struct {
	Host string
	Port int
	User string
	Auth AuthMethod
	// KeyPath is the private key file (AuthKey); a leading "~" is the
	// user's home directory.
	KeyPath string
	// Password is the SSH password (AuthPassword).
	Password string
	// Passphrase decrypts an encrypted private key (AuthKey).
	Passphrase string
	// Jumps are the jump hosts to go through, in order: Jumps[0] is
	// dialled from this machine and the last one reaches this server. The
	// Jumps of a jump host are ignored.
	Jumps []Config
}

// HopError is an error of one hop of a chain with jump hosts. Hop is the
// 1-based jump host number, or 0 for the final SSH server.
type HopError struct {
	Hop  int
	Host string
	Err  error
}

// HopLabel names a hop for messages: "jump host 2 (bastion)" or
// "SSH host (db-gw)".
func HopLabel(hop int, host string) string {
	if hop > 0 {
		return fmt.Sprintf("jump host %d (%s)", hop, host)
	}
	return fmt.Sprintf("SSH host (%s)", host)
}

func (e *HopError) Error() string { return HopLabel(e.Hop, e.Host) + ": " + e.Err.Error() }

// Unwrap returns the hop's error.
func (e *HopError) Unwrap() error { return e.Err }

// Address returns "host:port".
func (c Config) Address() string {
	port := c.Port
	if port == 0 {
		port = DefaultPort
	}
	return net.JoinHostPort(c.Host, strconv.Itoa(port))
}

// Errors.
var (
	ErrPassphraseRequired = errors.New("the SSH private key is encrypted: enter its passphrase")
	ErrBadPassphrase      = errors.New("the SSH key passphrase is wrong")
	ErrNoAgent            = errors.New("no SSH agent is running")
	ErrClosed             = errors.New("ssh tunnel is closed")
)

// Options configures a Dialer. Every path is injectable so tests never
// touch the real ~/.ssh.
type Options struct {
	// HostKeys verifies server keys. Required.
	HostKeys *HostKeys
	// HomeDir expands "~" in key paths (default os.UserHomeDir).
	HomeDir func() (string, error)
	// ReadFile reads key files (default os.ReadFile).
	ReadFile func(string) ([]byte, error)
	// AgentDial connects to the SSH agent (default: $SSH_AUTH_SOCK on
	// Unix, the OpenSSH named pipe on Windows).
	AgentDial func() (net.Conn, error)
	// KeepAlive is the keepalive interval; 0 means DefaultKeepAlive and a
	// negative value disables keepalives.
	KeepAlive time.Duration
	// Timeout bounds dialling plus the SSH handshake; 0 means
	// DefaultTimeout.
	Timeout time.Duration
	// TimeoutFunc, when set and positive, replaces Timeout; it is re-read
	// for every connect (a settings change applies to the next one).
	TimeoutFunc func() time.Duration
}

// Dialer opens tunnels.
type Dialer struct {
	opts Options
	// onClose observes every client closed by closeChain (tests).
	onClose func(*ssh.Client)
}

// New returns a Dialer.
func New(opts Options) *Dialer {
	if opts.HomeDir == nil {
		opts.HomeDir = os.UserHomeDir
	}
	if opts.ReadFile == nil {
		opts.ReadFile = os.ReadFile
	}
	if opts.AgentDial == nil {
		opts.AgentDial = dialAgent
	}
	if opts.KeepAlive == 0 {
		opts.KeepAlive = DefaultKeepAlive
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.HostKeys == nil {
		opts.HostKeys = &HostKeys{}
	}
	return &Dialer{opts: opts}
}

// timeout returns the current connect timeout.
func (d *Dialer) timeout() time.Duration {
	if d.opts.TimeoutFunc != nil {
		if t := d.opts.TimeoutFunc(); t > 0 {
			return t
		}
	}
	return d.opts.Timeout
}

// HostKeys returns the dialer's host key store.
func (d *Dialer) HostKeys() *HostKeys { return d.opts.HostKeys }

// ExpandHome replaces a leading "~" in p with the home directory.
func ExpandHome(p string, home func() (string, error)) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p, nil
	}
	h, err := home()
	if err != nil {
		return "", fmt.Errorf("expand ~: %w", err)
	}
	return filepath.Join(h, p[1:]), nil
}

// authMethods returns the SSH auth methods for cfg and a cleanup function
// (closing the agent connection after the handshake).
func (d *Dialer) authMethods(cfg Config) ([]ssh.AuthMethod, func(), error) {
	noop := func() {}
	switch cfg.Auth {
	case AuthPassword, "":
		pw := cfg.Password
		return []ssh.AuthMethod{
			ssh.Password(pw),
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = pw
				}
				return answers, nil
			}),
		}, noop, nil
	case AuthKey:
		if strings.TrimSpace(cfg.KeyPath) == "" {
			return nil, noop, errors.New("no SSH private key file is set")
		}
		path, err := ExpandHome(cfg.KeyPath, d.opts.HomeDir)
		if err != nil {
			return nil, noop, err
		}
		pem, err := d.opts.ReadFile(path)
		if err != nil {
			return nil, noop, fmt.Errorf("read SSH key: %w", err)
		}
		signer, err := ParseKey(pem, cfg.Passphrase)
		if err != nil {
			return nil, noop, err
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, noop, nil
	case AuthAgent:
		conn, err := d.opts.AgentDial()
		if err != nil {
			return nil, noop, fmt.Errorf("SSH agent: %w", err)
		}
		client := agent.NewClient(conn)
		return []ssh.AuthMethod{ssh.PublicKeysCallback(client.Signers)}, func() { _ = conn.Close() }, nil
	}
	return nil, noop, fmt.Errorf("unknown SSH authentication method %q", cfg.Auth)
}

// ParseKey parses a PEM/OpenSSH private key, decrypting it with
// passphrase when it is encrypted.
func ParseKey(pem []byte, passphrase string) (ssh.Signer, error) {
	signer, err := ssh.ParsePrivateKey(pem)
	var missing *ssh.PassphraseMissingError
	switch {
	case err == nil:
		return signer, nil
	case errors.As(err, &missing):
		if passphrase == "" {
			return nil, ErrPassphraseRequired
		}
		signer, err = ssh.ParsePrivateKeyWithPassphrase(pem, []byte(passphrase))
		if err != nil {
			if strings.Contains(err.Error(), "decryption password incorrect") || strings.Contains(err.Error(), "incorrect") {
				return nil, ErrBadPassphrase
			}
			return nil, fmt.Errorf("SSH key: %w", err)
		}
		return signer, nil
	}
	return nil, fmt.Errorf("SSH key: %w", err)
}

// closeClient closes one client of a chain.
func (d *Dialer) closeClient(c *ssh.Client) error {
	if d.onClose != nil {
		d.onClose(c)
	}
	return c.Close()
}

// closeChain closes the clients of a chain from the last to the first, so
// that each one's transport (a channel of the previous client) is still up
// while it says goodbye. It returns the first error.
func (d *Dialer) closeChain(chain []*ssh.Client) error {
	var first error
	for i := len(chain) - 1; i >= 0; i-- {
		if err := d.closeClient(chain[i]); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// connect dials the SSH server, through its jump hosts if any, and
// authenticates on every hop. It returns the chain of clients (the last one
// is connected to cfg's server). Options.Timeout bounds the whole chain.
func (d *Dialer) connect(ctx context.Context, cfg Config) ([]*ssh.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout())
	defer cancel()
	final := cfg
	final.Jumps = nil
	hops := append(append([]Config{}, cfg.Jumps...), final)
	chain := make([]*ssh.Client, 0, len(hops))
	for i, h := range hops {
		h.Jumps = nil
		var via *ssh.Client
		if i > 0 {
			via = chain[i-1]
		}
		c, err := d.connectHop(ctx, h, via)
		if err != nil {
			_ = d.closeChain(chain)
			if len(cfg.Jumps) == 0 {
				return nil, err
			}
			hop := i + 1
			if i == len(hops)-1 {
				hop = 0
			}
			return nil, hopError(hop, h.Host, err)
		}
		chain = append(chain, c)
	}
	return chain, nil
}

// hopError names the hop in err. The host key errors stay unwrapped (so
// that callers and the UI get their fields) and carry the hop themselves.
func hopError(hop int, host string, err error) error {
	label := HopLabel(hop, host)
	var unknown *HostKeyUnknownError
	var changed *HostKeyChangedError
	switch {
	case errors.As(err, &unknown):
		unknown.Hop, unknown.HopLabel = hop, label
		unknown.Message = label + ": " + unknown.Message
		return unknown
	case errors.As(err, &changed):
		changed.Hop, changed.HopLabel = hop, label
		changed.Message = label + ": " + changed.Message
		return changed
	}
	return &HopError{Hop: hop, Host: host, Err: err}
}

// connectHop dials one SSH server, directly (via == nil) or through a
// direct-tcpip channel of via, and authenticates.
func (d *Dialer) connectHop(ctx context.Context, cfg Config, via *ssh.Client) (*ssh.Client, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, errors.New("no SSH host is set")
	}
	addr := cfg.Address()
	cb, algos, err := d.opts.HostKeys.Callback(addr)
	if err != nil {
		return nil, err
	}
	auth, cleanup, err := d.authMethods(cfg)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	cc := &ssh.ClientConfig{
		User:              cfg.User,
		Auth:              auth,
		HostKeyCallback:   cb,
		HostKeyAlgorithms: algos,
		Timeout:           d.timeout(),
	}
	var raw net.Conn
	if via == nil {
		var nd net.Dialer
		raw, err = nd.DialContext(ctx, "tcp", addr)
	} else {
		raw, err = via.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("SSH: connect to %s: %w", addr, err)
	}
	// Bound the handshake by the context too. A channel of the previous hop
	// has no deadlines: close it instead.
	stop := context.AfterFunc(ctx, func() {
		if raw.SetDeadline(time.Now()) != nil {
			_ = raw.Close()
		}
	})
	c, chans, reqs, err := ssh.NewClientConn(raw, addr, cc)
	stop()
	if err != nil {
		_ = raw.Close()
		var unknown *HostKeyUnknownError
		var changed *HostKeyChangedError
		switch {
		case errors.As(err, &unknown):
			// Return the structured error itself (not wrapped by ssh) so
			// callers can use errors.As and the UI gets its fields.
			return nil, unknown
		case errors.As(err, &changed):
			return nil, changed
		case strings.Contains(err.Error(), "unable to authenticate"):
			return nil, fmt.Errorf("SSH authentication failed for %s@%s: %w", cfg.User, addr, err)
		case ctx.Err() != nil:
			return nil, fmt.Errorf("SSH: %s: %w", addr, ctx.Err())
		}
		return nil, fmt.Errorf("SSH: %s: %w", addr, err)
	}
	_ = raw.SetDeadline(time.Time{})
	return ssh.NewClient(c, chans, reqs), nil
}

// Tunnel is an SSH connection that database connections are dialled
// through. It is safe for concurrent use.
type Tunnel struct {
	d   *Dialer
	cfg Config

	// reconnecting serialises reconnects, so that concurrent dials after
	// a drop share one new SSH connection.
	reconnecting sync.Mutex

	mu sync.Mutex
	// client is the last client of chain (the one connected to the final
	// SSH server); chain holds the jump host clients before it.
	client  *ssh.Client
	chain   []*ssh.Client
	closed  bool
	lastErr error
	done    chan struct{} // closed by Close (stops the keepalive loop)
}

// Open connects to the SSH server (through its jump hosts) and returns the
// tunnel.
func (d *Dialer) Open(ctx context.Context, cfg Config) (*Tunnel, error) {
	chain, err := d.connect(ctx, cfg)
	if err != nil {
		return nil, err
	}
	t := &Tunnel{d: d, cfg: cfg, done: make(chan struct{})}
	t.attach(chain)
	if d.opts.KeepAlive > 0 {
		go t.keepAlive(d.opts.KeepAlive)
	}
	return t, nil
}

// attach makes chain the current one and watches its last client for
// disconnects. A jump host that drops takes the channels through it, and so
// the last client, down with it. When the last client is gone, the rest of
// its chain is closed.
func (t *Tunnel) attach(chain []*ssh.Client) {
	c := chain[len(chain)-1]
	t.mu.Lock()
	t.client, t.chain = c, chain
	t.mu.Unlock()
	go func() {
		err := c.Wait()
		t.mu.Lock()
		current := t.client == c
		if current {
			t.client, t.chain = nil, nil
			if err == nil {
				err = errors.New("connection closed")
			}
			t.lastErr = fmt.Errorf("SSH connection to %s lost: %w", t.cfg.Address(), err)
		}
		t.mu.Unlock()
		if current {
			_ = t.d.closeChain(chain[:len(chain)-1])
		}
	}()
}

// keepAlive sends a keepalive request every interval. A request that is
// not answered within the interval closes the client (so Dial
// reconnects).
func (t *Tunnel) keepAlive(interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-tick.C:
		}
		t.mu.Lock()
		c := t.client
		t.mu.Unlock()
		if c == nil {
			continue
		}
		res := make(chan error, 1)
		go func() {
			_, _, err := c.SendRequest("keepalive@openssh.com", true, nil)
			res <- err
		}()
		select {
		case err := <-res:
			if err != nil {
				_ = c.Close()
			}
		case <-time.After(interval):
			_ = c.Close()
		case <-t.done:
			return
		}
	}
}

// Err returns why the SSH connection was lost last (nil if it never was).
func (t *Tunnel) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastErr
}

// current returns a live client, reconnecting (once) when the connection
// was lost.
func (t *Tunnel) current(ctx context.Context) (*ssh.Client, error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, ErrClosed
	}
	c := t.client
	t.mu.Unlock()
	if c != nil {
		return c, nil
	}
	return t.reconnect(ctx, nil)
}

// reconnect replaces the client chain (unless another goroutine already
// did). The whole chain is rebuilt from the first jump host.
func (t *Tunnel) reconnect(ctx context.Context, dead *ssh.Client) (*ssh.Client, error) {
	t.reconnecting.Lock()
	defer t.reconnecting.Unlock()
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, ErrClosed
	}
	if t.client != nil && t.client != dead {
		c := t.client
		t.mu.Unlock()
		return c, nil
	}
	var old []*ssh.Client
	if dead != nil && t.client == dead {
		old = t.chain
		t.client, t.chain = nil, nil
	}
	lost := t.lastErr
	t.mu.Unlock()
	switch {
	case old != nil:
		_ = t.d.closeChain(old)
	case dead != nil:
		_ = t.d.closeClient(dead)
	}
	chain, err := t.d.connect(ctx, t.cfg)
	if err != nil {
		if lost != nil {
			return nil, fmt.Errorf("%w; reconnecting failed: %w", lost, err)
		}
		return nil, err
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = t.d.closeChain(chain)
		return nil, ErrClosed
	}
	t.mu.Unlock()
	t.attach(chain)
	return chain[len(chain)-1], nil
}

// Dial opens a connection to addr ("host:port", resolved by the SSH
// server) through the tunnel. If the SSH connection is gone, it reconnects
// once.
func (t *Tunnel) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("ssh tunnel: unsupported network %q", network)
	}
	c, err := t.current(ctx)
	if err != nil {
		return nil, err
	}
	ch, err := c.DialContext(ctx, "tcp", addr)
	if err != nil && ctx.Err() == nil && isConnectionLost(err) {
		// The SSH connection died without us noticing yet: reconnect
		// once and retry.
		if c, err = t.reconnect(ctx, c); err != nil {
			return nil, err
		}
		ch, err = c.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("ssh tunnel: open %s: %w", addr, err)
	}
	return bridge(ch, addr), nil
}

// isConnectionLost reports errors that mean the SSH transport is gone (as
// opposed to the server refusing the forward).
func isConnectionLost(err error) bool {
	var oce *ssh.OpenChannelError
	if errors.As(err, &oce) {
		return false
	}
	return true
}

// Close closes the SSH connection (the chain from the last client to the
// first); open database connections through it fail afterwards.
func (t *Tunnel) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	chain := t.chain
	t.client, t.chain = nil, nil
	close(t.done)
	t.mu.Unlock()
	return t.d.closeChain(chain)
}
