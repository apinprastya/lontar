package postgres

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"

	"dbedit/internal/driver"
)

func TestBuildDSNTLS(t *testing.T) {
	d := New()
	cfg := driver.Config{Host: "db", User: "u", Options: map[string]string{"sslmode": "disable", "sslrootcert": "/old.pem", "sslsni": "0"}}
	// Without Config.TLS the options are used as is.
	dsn, _ := d.BuildDSN(cfg, "")
	u, _ := url.Parse(dsn)
	if u.Query().Get("sslmode") != "disable" || u.Query().Get("sslrootcert") != "/old.pem" {
		t.Fatalf("dsn = %s", dsn)
	}
	// The SSL settings win over the options.
	cfg.TLS = &driver.TLSConfig{Mode: driver.TLSVerifyFull, CAFile: "/ca.pem", CertFile: "/c.pem", KeyFile: "/k.pem"}
	dsn, _ = d.BuildDSN(cfg, "")
	q, _ := url.Parse(dsn)
	v := q.Query()
	if v.Get("sslmode") != "verify-full" || v.Get("sslrootcert") != "/ca.pem" || v.Get("sslcert") != "/c.pem" || v.Get("sslkey") != "/k.pem" || v.Has("sslsni") {
		t.Fatalf("dsn = %s", dsn)
	}
	cfg.TLS = &driver.TLSConfig{Mode: driver.TLSRequire}
	dsn, _ = d.BuildDSN(cfg, "")
	q, _ = url.Parse(dsn)
	if q.Query().Get("sslmode") != "require" || q.Query().Has("sslrootcert") {
		t.Fatalf("dsn = %s", dsn)
	}
	// Invalid modes are refused before connecting.
	cfg.TLS = &driver.TLSConfig{Mode: driver.TLSPreferred}
	if _, err := d.Open(context.Background(), cfg, ""); !errors.Is(err, driver.ErrTLSConfig) {
		t.Fatalf("err = %v", err)
	}
	// A missing CA file fails at parse time.
	cfg.TLS = &driver.TLSConfig{Mode: driver.TLSVerifyCA, CAFile: "/nonexistent/ca.pem"}
	if _, err := d.Open(context.Background(), cfg, ""); err == nil {
		t.Fatal("missing CA accepted")
	}
}

func TestOpenWithDialer(t *testing.T) {
	var dialled []string
	dial := func(_ context.Context, network, addr string) (net.Conn, error) {
		dialled = append(dialled, network+" "+addr)
		return nil, errors.New("tunnel says no")
	}
	db, err := New().Open(context.Background(), driver.Config{Host: "db.internal", Port: 6543, User: "u", Dial: dial,
		Options: map[string]string{"sslmode": "disable"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err == nil {
		t.Fatal("ping should fail through the refusing dialer")
	}
	// The host name is passed through unresolved.
	if len(dialled) == 0 || dialled[0] != "tcp db.internal:6543" {
		t.Fatalf("dialled = %v", dialled)
	}
}
