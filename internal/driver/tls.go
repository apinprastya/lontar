package driver

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"slices"
)

// SSL/TLS modes of a connection (TLSConfig.Mode).
//
//   - disable: plain TCP.
//   - preferred (MySQL/MariaDB only): TLS when the server offers it,
//     otherwise plain; the certificate is not verified.
//   - require: TLS or fail. The certificate is not verified, unless a CA
//     file is given: then the chain is verified like verify-ca (libpq's
//     rule, applied to MySQL/MariaDB too).
//   - verify-ca: TLS, and the server certificate must chain to the CA file
//     (or the system roots when none is given). The host name is not
//     checked.
//   - verify-full: verify-ca plus the host name must match the
//     certificate. The name checked is the connection's host, also when
//     connecting through an SSH tunnel.
const (
	TLSDisable    = "disable"
	TLSPreferred  = "preferred"
	TLSRequire    = "require"
	TLSVerifyCA   = "verify-ca"
	TLSVerifyFull = "verify-full"
)

// TLSConfig holds a connection's SSL/TLS settings. The files are PEM:
// CAFile is the trusted root(s), CertFile/KeyFile an optional client
// certificate (both or neither).
type TLSConfig struct {
	Mode     string
	CAFile   string
	CertFile string
	KeyFile  string
}

// Active reports whether t sets a mode (and so overrides the driver
// options).
func (t *TLSConfig) Active() bool { return t != nil && t.Mode != "" }

// TLSModes lists the modes valid for kind, or nil when TLS does not apply.
func TLSModes(kind Kind) []string {
	switch kind {
	case Postgres:
		return []string{TLSDisable, TLSRequire, TLSVerifyCA, TLSVerifyFull}
	case MySQL, MariaDB:
		return []string{TLSDisable, TLSPreferred, TLSRequire, TLSVerifyCA, TLSVerifyFull}
	}
	return nil
}

// ErrTLSConfig is wrapped by errors about TLS settings (bad mode, unreadable
// or invalid certificate files).
var ErrTLSConfig = errors.New("invalid SSL settings")

// CheckTLS validates t for kind without reading any file.
func CheckTLS(kind Kind, t *TLSConfig) error {
	if !t.Active() {
		return nil
	}
	if !slices.Contains(TLSModes(kind), t.Mode) {
		return fmt.Errorf("%w: SSL mode %q is not supported for %s", ErrTLSConfig, t.Mode, kind)
	}
	if (t.CertFile == "") != (t.KeyFile == "") {
		return fmt.Errorf("%w: a client certificate needs both the certificate and the key file", ErrTLSConfig)
	}
	return nil
}

// readFile is replaced in tests.
var readFile = os.ReadFile

// ClientTLS builds the *tls.Config for t (see the mode constants). It
// returns nil for disable. serverName is the host name verify-full checks.
// allowFallback reports the "preferred" mode: the caller should connect
// without TLS when the server does not support it.
func ClientTLS(t *TLSConfig, serverName string) (cfg *tls.Config, allowFallback bool, err error) {
	if !t.Active() || t.Mode == TLSDisable {
		return nil, false, nil
	}
	cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	if t.CertFile != "" || t.KeyFile != "" {
		certPEM, err := readFile(t.CertFile)
		if err != nil {
			return nil, false, fmt.Errorf("%w: read client certificate: %w", ErrTLSConfig, err)
		}
		keyPEM, err := readFile(t.KeyFile)
		if err != nil {
			return nil, false, fmt.Errorf("%w: read client key: %w", ErrTLSConfig, err)
		}
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, false, fmt.Errorf("%w: client certificate: %w", ErrTLSConfig, err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	var roots *x509.CertPool
	if t.CAFile != "" {
		pem, err := readFile(t.CAFile)
		if err != nil {
			return nil, false, fmt.Errorf("%w: read CA certificate: %w", ErrTLSConfig, err)
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, false, fmt.Errorf("%w: %s contains no PEM certificate", ErrTLSConfig, t.CAFile)
		}
	}
	switch t.Mode {
	case TLSPreferred:
		cfg.InsecureSkipVerify = true // encryption only, like mysql --ssl-mode=PREFERRED
		return cfg, true, nil
	case TLSRequire:
		cfg.InsecureSkipVerify = true
		if roots != nil {
			cfg.VerifyPeerCertificate = verifyChain(roots)
		}
	case TLSVerifyCA:
		// Chain only, no host name: Go has no switch for that, so the
		// built-in check is off and verifyChain does it instead.
		cfg.InsecureSkipVerify = true
		cfg.VerifyPeerCertificate = verifyChain(roots)
	case TLSVerifyFull:
		cfg.RootCAs = roots // nil = system roots
		cfg.ServerName = serverName
	default:
		return nil, false, fmt.Errorf("%w: unknown SSL mode %q", ErrTLSConfig, t.Mode)
	}
	return cfg, false, nil
}

// verifyChain returns a VerifyPeerCertificate callback that checks that the
// server's certificate chains to roots (nil: the system roots) and is valid
// for server authentication, without checking the host name.
func verifyChain(roots *x509.CertPool) func([][]byte, [][]*x509.Certificate) error {
	return func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) == 0 {
			return errors.New("tls: server sent no certificate")
		}
		certs := make([]*x509.Certificate, len(raw))
		for i, der := range raw {
			c, err := x509.ParseCertificate(der)
			if err != nil {
				return fmt.Errorf("tls: parse server certificate: %w", err)
			}
			certs[i] = c
		}
		inter := x509.NewCertPool()
		for _, c := range certs[1:] {
			inter.AddCert(c)
		}
		_, err := certs[0].Verify(x509.VerifyOptions{
			Roots:         roots,
			Intermediates: inter,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		})
		if err != nil {
			return fmt.Errorf("tls: verify server certificate (verify-ca): %w", err)
		}
		return nil
	}
}
