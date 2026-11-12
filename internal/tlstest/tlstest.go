// Package tlstest generates throwaway certificate authorities and
// certificates (PEM files) for TLS integration tests.
package tlstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// CA is a certificate authority.
type CA struct {
	Cert *x509.Certificate
	Key  *ecdsa.PrivateKey
	// CertPEM is the CA certificate in PEM form.
	CertPEM []byte
}

var serial int64 = 1

func nextSerial() *big.Int {
	serial++
	return big.NewInt(serial)
}

// NewCA creates a self-signed CA named cn.
func NewCA(cn string) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          nextSerial(),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{Cert: cert, Key: key, CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}, nil
}

// Issue signs a leaf certificate for cn with the given DNS names / IPs
// (server and client usage) and returns the certificate and PKCS#8 key as
// PEM.
func (ca *CA) Issue(cn string, hosts ...string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: nextSerial(),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else {
			tpl.DNSNames = append(tpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.Cert, &key.PublicKey, ca.Key)
	if err != nil {
		return nil, nil, err
	}
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder}), nil
}

// Files are PEM files written by WriteFiles.
type Files struct {
	CA, ServerCert, ServerKey, ClientCert, ClientKey string
}

// WriteFiles writes the CA, a server certificate for hosts and a client
// certificate (CN clientCN) to dir.
func (ca *CA) WriteFiles(dir, clientCN string, hosts ...string) (Files, error) {
	f := Files{
		CA:         filepath.Join(dir, "ca.pem"),
		ServerCert: filepath.Join(dir, "server-cert.pem"),
		ServerKey:  filepath.Join(dir, "server-key.pem"),
		ClientCert: filepath.Join(dir, "client-cert.pem"),
		ClientKey:  filepath.Join(dir, "client-key.pem"),
	}
	sc, sk, err := ca.Issue("server", hosts...)
	if err != nil {
		return Files{}, err
	}
	cc, ck, err := ca.Issue(clientCN)
	if err != nil {
		return Files{}, err
	}
	for path, data := range map[string][]byte{f.CA: ca.CertPEM, f.ServerCert: sc, f.ServerKey: sk, f.ClientCert: cc, f.ClientKey: ck} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return Files{}, err
		}
	}
	return f, nil
}
