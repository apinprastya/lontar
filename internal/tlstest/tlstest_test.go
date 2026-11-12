package tlstest

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"testing"
)

func TestFiles(t *testing.T) {
	ca, err := NewCA("test CA")
	if err != nil {
		t.Fatal(err)
	}
	f, err := ca.WriteFiles(t.TempDir(), "client", "localhost", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(f.ServerCert, f.ServerKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	pool := x509.NewCertPool()
	caPEM, _ := os.ReadFile(f.CA)
	pool.AppendCertsFromPEM(caPEM)
	for _, h := range []string{"localhost", "127.0.0.1"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: h}); err != nil {
			t.Fatalf("%s: %v", h, err)
		}
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "other"}); err == nil {
		t.Fatal("wrong name verified")
	}
	if _, err := tls.LoadX509KeyPair(f.ClientCert, f.ClientKey); err != nil {
		t.Fatal(err)
	}
	if _, err := ca.WriteFiles("/nonexistent/dir", "c"); err == nil {
		t.Fatal("write to a missing dir")
	}
}
