package mysql

import (
	"context"
	"errors"
	"net"
	"testing"

	"lontar/internal/driver"
	"lontar/internal/tlstest"
)

func TestConfigTLSAndDial(t *testing.T) {
	ca, _ := tlstest.NewCA("ca")
	files, err := ca.WriteFiles(t.TempDir(), "c", "db")
	if err != nil {
		t.Fatal(err)
	}
	d := New()
	dial := func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("no") }
	base := driver.Config{Host: "db", User: "u", Options: map[string]string{"tls": "skip-verify"}, Dial: dial}

	// Without Config.TLS the "tls" option applies.
	mc, err := d.Config(base, "")
	if err != nil || mc.TLS == nil || !mc.TLS.InsecureSkipVerify || mc.DialFunc == nil {
		t.Fatalf("mc = %+v, %v", mc, err)
	}
	cases := []struct {
		mode               string
		nilTLS, fallback   bool
		insecure           bool
		serverName         string
		verifyPeerCallback bool
	}{
		{driver.TLSDisable, true, false, false, "", false},
		{driver.TLSPreferred, false, true, true, "", false},
		{driver.TLSRequire, false, false, true, "", true}, // CA given → chain verified
		{driver.TLSVerifyCA, false, false, true, "", true},
		{driver.TLSVerifyFull, false, false, false, "db", false},
	}
	for _, tc := range cases {
		cfg := base
		cfg.TLS = &driver.TLSConfig{Mode: tc.mode, CAFile: files.CA}
		mc, err := d.Config(cfg, "")
		if err != nil {
			t.Fatalf("%s: %v", tc.mode, err)
		}
		if (mc.TLS == nil) != tc.nilTLS || mc.AllowFallbackToPlaintext != tc.fallback {
			t.Fatalf("%s: tls=%v fallback=%v", tc.mode, mc.TLS, mc.AllowFallbackToPlaintext)
		}
		if mc.TLS != nil && (mc.TLS.InsecureSkipVerify != tc.insecure || mc.TLS.ServerName != tc.serverName ||
			(mc.TLS.VerifyPeerCertificate != nil) != tc.verifyPeerCallback) {
			t.Fatalf("%s: %+v", tc.mode, mc.TLS)
		}
		if tc.nilTLS && mc.TLSConfig != "false" {
			t.Fatalf("disable: TLSConfig = %q", mc.TLSConfig)
		}
	}
	// Errors: mode not for the kind is impossible for MySQL, but a bad
	// mode and unreadable files are reported.
	bad := base
	bad.TLS = &driver.TLSConfig{Mode: "bogus"}
	if _, err := d.Config(bad, ""); !errors.Is(err, driver.ErrTLSConfig) {
		t.Fatalf("err = %v", err)
	}
	bad.TLS = &driver.TLSConfig{Mode: driver.TLSVerifyCA, CAFile: "/missing"}
	if _, err := d.Config(bad, ""); !errors.Is(err, driver.ErrTLSConfig) {
		t.Fatalf("err = %v", err)
	}
	// No options: the dialer still applies.
	plain := driver.Config{Host: "db", Dial: dial}
	if mc, _ := d.Config(plain, ""); mc.DialFunc == nil {
		t.Fatal("dialer lost")
	}
	db, err := d.Open(context.Background(), plain, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err == nil {
		t.Fatal("ping through refusing dialer succeeded")
	}
}
