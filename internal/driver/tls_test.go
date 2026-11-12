package driver

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lontar/internal/tlstest"
)

// tlsServer serves TLS with the given server certificate on 127.0.0.1 and
// returns its address.
func tlsServer(t *testing.T, f tlstest.Files) string {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(f.ServerCert, f.ServerKey)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = c.(*tls.Conn).Handshake()
				_, _ = io.Copy(io.Discard, c)
				_ = c.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

func handshake(t *testing.T, addr string, cfg *tls.Config) error {
	t.Helper()
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	return tls.Client(raw, cfg).Handshake()
}

func TestClientTLSModes(t *testing.T) {
	dir := t.TempDir()
	ca, _ := tlstest.NewCA("good CA")
	files, err := ca.WriteFiles(dir, "client", "localhost") // no IP SAN
	if err != nil {
		t.Fatal(err)
	}
	other, _ := tlstest.NewCA("other CA")
	otherDir := t.TempDir()
	otherFiles, _ := other.WriteFiles(otherDir, "c", "localhost")
	addr := tlsServer(t, files)

	cases := []struct {
		name       string
		cfg        TLSConfig
		serverName string
		ok         bool
	}{
		{"require no CA", TLSConfig{Mode: TLSRequire}, "anything", true},
		{"require wrong CA verifies chain", TLSConfig{Mode: TLSRequire, CAFile: otherFiles.CA}, "localhost", false},
		{"require right CA", TLSConfig{Mode: TLSRequire, CAFile: files.CA}, "x", true},
		{"verify-ca ignores host name", TLSConfig{Mode: TLSVerifyCA, CAFile: files.CA}, "127.0.0.1", true},
		{"verify-ca wrong CA", TLSConfig{Mode: TLSVerifyCA, CAFile: otherFiles.CA}, "localhost", false},
		{"verify-ca system roots", TLSConfig{Mode: TLSVerifyCA}, "localhost", false},
		{"verify-full ok", TLSConfig{Mode: TLSVerifyFull, CAFile: files.CA}, "localhost", true},
		{"verify-full host mismatch", TLSConfig{Mode: TLSVerifyFull, CAFile: files.CA}, "127.0.0.1", false},
		{"verify-full wrong CA", TLSConfig{Mode: TLSVerifyFull, CAFile: otherFiles.CA}, "localhost", false},
		{"client cert", TLSConfig{Mode: TLSVerifyFull, CAFile: files.CA, CertFile: files.ClientCert, KeyFile: files.ClientKey}, "localhost", true},
		{"preferred", TLSConfig{Mode: TLSPreferred}, "x", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, fallback, err := ClientTLS(&tc.cfg, tc.serverName)
			if err != nil {
				t.Fatal(err)
			}
			if fallback != (tc.cfg.Mode == TLSPreferred) {
				t.Fatalf("fallback = %v", fallback)
			}
			if tc.cfg.CertFile != "" && len(cfg.Certificates) != 1 {
				t.Fatal("client certificate not loaded")
			}
			err = handshake(t, addr, cfg)
			if (err == nil) != tc.ok {
				t.Fatalf("handshake err = %v, want ok=%v", err, tc.ok)
			}
		})
	}

	// disable / inactive → no TLS.
	for _, c := range []*TLSConfig{nil, {}, {Mode: TLSDisable}} {
		if cfg, _, err := ClientTLS(c, "h"); cfg != nil || err != nil {
			t.Fatalf("%+v → %v %v", c, cfg, err)
		}
	}
	// File errors.
	bad := filepath.Join(dir, "missing.pem")
	notPEM := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(notPEM, []byte("hello"), 0o600)
	for name, c := range map[string]TLSConfig{
		"ca missing":   {Mode: TLSVerifyCA, CAFile: bad},
		"ca not pem":   {Mode: TLSVerifyCA, CAFile: notPEM},
		"cert missing": {Mode: TLSRequire, CertFile: bad, KeyFile: files.ClientKey},
		"key missing":  {Mode: TLSRequire, CertFile: files.ClientCert, KeyFile: bad},
		"pair invalid": {Mode: TLSRequire, CertFile: files.ClientCert, KeyFile: otherFiles.ClientKey},
		"bad mode":     {Mode: "sometimes"},
	} {
		if _, _, err := ClientTLS(&c, "h"); !errors.Is(err, ErrTLSConfig) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// verifyChain edge cases.
	if err := verifyChain(nil)(nil, nil); err == nil {
		t.Fatal("no certificate accepted")
	}
	if err := verifyChain(nil)([][]byte{{1, 2}}, nil); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckTLS(t *testing.T) {
	if err := CheckTLS(Postgres, nil); err != nil {
		t.Fatal(err)
	}
	if err := CheckTLS(Postgres, &TLSConfig{Mode: TLSPreferred}); !errors.Is(err, ErrTLSConfig) {
		t.Fatalf("pg preferred: %v", err)
	}
	if err := CheckTLS(MySQL, &TLSConfig{Mode: TLSPreferred}); err != nil {
		t.Fatal(err)
	}
	if err := CheckTLS(MySQL, &TLSConfig{Mode: TLSRequire, CertFile: "c"}); !errors.Is(err, ErrTLSConfig) {
		t.Fatalf("half pair: %v", err)
	}
	if TLSModes(SQLite) != nil || len(TLSModes(MariaDB)) != 5 || len(TLSModes(Postgres)) != 4 {
		t.Fatal("TLSModes")
	}
	if (&TLSConfig{}).Active() || (*TLSConfig)(nil).Active() || !(&TLSConfig{Mode: "require"}).Active() {
		t.Fatal("Active")
	}
}
