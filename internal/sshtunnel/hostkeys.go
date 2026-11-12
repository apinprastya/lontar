package sshtunnel

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// HostKeyUnknownError is returned when the SSH server's host key is in no
// known_hosts file. The UI shows the fingerprint and, if the user accepts
// it, calls HostKeys.Accept (which appends the key to Lontar's own
// known_hosts) and retries.
type HostKeyUnknownError struct {
	// Kind is always "hostKeyUnknown" (for the frontend).
	Kind string `json:"kind"`
	// Host is the server in known_hosts form: "host", or "[host]:port"
	// for a port other than 22.
	Host string `json:"host"`
	// Address is the "host:port" that was dialled.
	Address           string `json:"address"`
	KeyType           string `json:"keyType"`
	FingerprintSHA256 string `json:"fingerprintSHA256"`
	Message           string `json:"message"`
}

func (e *HostKeyUnknownError) Error() string { return e.Message }

// HostKeyChangedError is returned when the server presents a key that
// differs from the one recorded for it. This can mean a man-in-the-middle
// attack, so it is never accepted from the UI: the user has to remove the
// old entry from the named known_hosts file by hand.
type HostKeyChangedError struct {
	// Kind is always "hostKeyChanged".
	Kind              string `json:"kind"`
	Host              string `json:"host"`
	Address           string `json:"address"`
	KeyType           string `json:"keyType"`
	FingerprintSHA256 string `json:"fingerprintSHA256"`
	// Known lists the recorded entries as "file:line".
	Known   []string `json:"known"`
	Message string   `json:"message"`
}

func (e *HostKeyChangedError) Error() string { return e.Message }

// ErrNoPendingKey is returned by Accept when no unknown key with that
// fingerprint was seen for the host (the user must connect first, so that
// exactly the key they were shown is recorded).
var ErrNoPendingKey = errors.New("no host key with this fingerprint is waiting to be accepted; connect again first")

// HostKeys verifies SSH host keys against the user's known_hosts files
// (read-only) plus Lontar's own known_hosts file, which is the only file
// it ever writes.
type HostKeys struct {
	// UserFiles are read but never written (e.g. ~/.ssh/known_hosts).
	// Missing files are skipped.
	UserFiles []string
	// AppFile is Lontar's own known_hosts (<config dir>/known_hosts).
	AppFile string

	mu      sync.Mutex
	pending map[string]ssh.PublicKey // host + "\x00" + fingerprint → key
}

// files returns the known_hosts files that exist.
func (h *HostKeys) files() ([]string, error) {
	var out []string
	for _, f := range append(append([]string{}, h.UserFiles...), h.AppFile) {
		if f == "" {
			continue
		}
		st, err := os.Stat(f)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, fmt.Errorf("known_hosts: %w", err)
		case st.IsDir():
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// database returns a knownhosts callback for the current file contents
// (reloaded for every connection, so an accepted key applies at once).
func (h *HostKeys) database() (ssh.HostKeyCallback, error) {
	files, err := h.files()
	if err != nil {
		return nil, err
	}
	cb, err := knownhosts.New(files...)
	if err != nil {
		return nil, fmt.Errorf("known_hosts: %w", err)
	}
	return cb, nil
}

// Callback returns the host key callback for one connection attempt and
// the host key algorithms to offer (the types already recorded for the
// host, so that a server with several keys presents the known one; nil
// when the host is unknown).
func (h *HostKeys) Callback(address string) (ssh.HostKeyCallback, []string, error) {
	db, err := h.database()
	if err != nil {
		return nil, nil, err
	}
	algos := knownAlgorithms(db, address)
	cb := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := db(hostname, remote, key)
		if err == nil {
			return nil
		}
		host := knownhosts.Normalize(hostname)
		fp := ssh.FingerprintSHA256(key)
		var ke *knownhosts.KeyError
		var re *knownhosts.RevokedError
		switch {
		case errors.As(err, &ke) && len(ke.Want) == 0:
			h.remember(host, fp, key)
			return &HostKeyUnknownError{
				Kind: "hostKeyUnknown", Host: host, Address: hostname, KeyType: key.Type(), FingerprintSHA256: fp,
				Message: fmt.Sprintf("the authenticity of SSH host %s can't be established (%s key fingerprint %s)", host, key.Type(), fp),
			}
		case errors.As(err, &ke):
			known := make([]string, len(ke.Want))
			for i, k := range ke.Want {
				known[i] = fmt.Sprintf("%s:%d", k.Filename, k.Line)
			}
			return &HostKeyChangedError{
				Kind: "hostKeyChanged", Host: host, Address: hostname, KeyType: key.Type(), FingerprintSHA256: fp, Known: known,
				Message: fmt.Sprintf("WARNING: the SSH host key of %s has CHANGED (now %s %s). Someone could be intercepting the connection "+
					"(man-in-the-middle), or the server's key was replaced. Lontar will not connect. If the change is expected, "+
					"remove the old entry (%s) from the known_hosts file by hand and connect again.",
					host, key.Type(), fp, strings.Join(known, ", ")),
			}
		case errors.As(err, &re):
			return fmt.Errorf("SSH host key of %s is marked as revoked in %s:%d", host, re.Revoked.Filename, re.Revoked.Line)
		}
		return err
	}
	return cb, algos, nil
}

var probeKey = sync.OnceValue(func() ssh.PublicKey {
	pub, _, _ := ed25519.GenerateKey(nil)
	k, _ := ssh.NewPublicKey(pub)
	return k
})

// knownAlgorithms asks db for the keys recorded for address (by checking a
// throwaway key that never matches) and maps their types to host key
// algorithms.
func knownAlgorithms(db ssh.HostKeyCallback, address string) []string {
	err := db(address, &net.TCPAddr{IP: net.IPv4zero}, probeKey())
	var ke *knownhosts.KeyError
	if !errors.As(err, &ke) || len(ke.Want) == 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(a string) {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	for _, k := range ke.Want {
		if k.Key.Type() == ssh.KeyAlgoRSA {
			add(ssh.KeyAlgoRSASHA512)
			add(ssh.KeyAlgoRSASHA256)
		}
		add(k.Key.Type())
	}
	return out
}

func (h *HostKeys) remember(host, fp string, key ssh.PublicKey) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pending == nil {
		h.pending = map[string]ssh.PublicKey{}
	}
	h.pending[host+"\x00"+fp] = key
}

// Accept records the key with fingerprint fp that host presented in a
// previous, refused connection attempt. host is the known_hosts form from
// HostKeyUnknownError.Host. The line is appended to AppFile only.
func (h *HostKeys) Accept(host, fp string) error {
	h.mu.Lock()
	key, ok := h.pending[host+"\x00"+fp]
	h.mu.Unlock()
	if !ok {
		return ErrNoPendingKey
	}
	if h.AppFile == "" {
		return errors.New("known_hosts: no file to record the key in")
	}
	if err := os.MkdirAll(filepath.Dir(h.AppFile), 0o700); err != nil {
		return fmt.Errorf("known_hosts: %w", err)
	}
	f, err := os.OpenFile(h.AppFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("known_hosts: %w", err)
	}
	line := knownhosts.Line([]string{host}, key) + "\n"
	if _, err := f.WriteString(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("known_hosts: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("known_hosts: %w", err)
	}
	h.mu.Lock()
	delete(h.pending, host+"\x00"+fp)
	h.mu.Unlock()
	return nil
}
