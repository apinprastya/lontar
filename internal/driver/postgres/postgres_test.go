package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"dbedit/internal/driver"
	"dbedit/internal/driver/postgres"
	"dbedit/internal/testutil"

	"github.com/jackc/pgx/v5"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

const specialPassword = `p@ss:w/rd'"%`

func TestMetadata(t *testing.T) {
	d := postgres.New()
	if d.Kind() != driver.Postgres || d.DisplayName() != "PostgreSQL" || d.DefaultPort() != 5432 {
		t.Fatalf("unexpected metadata: %s %s %d", d.Kind(), d.DisplayName(), d.DefaultPort())
	}
}

func TestBuildDSNSpecialPassword(t *testing.T) {
	d := postgres.New()
	cfg := driver.Config{
		Kind: driver.Postgres, Host: "db.example.com", Port: 6543,
		User: "us er@x", Database: "my db/1",
		Options:        map[string]string{"sslmode": "require", "search_path": "a&b=c"},
		ConnectTimeout: 3 * time.Second,
	}
	dsn, err := d.BuildDSN(cfg, specialPassword)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("pgx cannot parse %q: %v", dsn, err)
	}
	if pc.Password != specialPassword {
		t.Errorf("password = %q, want %q", pc.Password, specialPassword)
	}
	if pc.User != cfg.User || pc.Database != cfg.Database || pc.Host != cfg.Host || pc.Port != 6543 {
		t.Errorf("parsed config = %s@%s:%d/%s", pc.User, pc.Host, pc.Port, pc.Database)
	}
	if pc.ConnectTimeout != 3*time.Second {
		t.Errorf("connect timeout = %v", pc.ConnectTimeout)
	}
	if pc.RuntimeParams["search_path"] != "a&b=c" || pc.RuntimeParams["application_name"] != "dbedit" {
		t.Errorf("runtime params = %v", pc.RuntimeParams)
	}
	if pc.TLSConfig == nil {
		t.Error("sslmode=require should enable TLS")
	}
}

func TestBuildDSNVariants(t *testing.T) {
	d := postgres.New()
	cases := []struct {
		name     string
		cfg      driver.Config
		password string
		check    func(t *testing.T, u *url.URL)
	}{
		{"user only", driver.Config{Host: "h", User: "u"}, "", func(t *testing.T, u *url.URL) {
			if _, set := u.User.Password(); set || u.User.Username() != "u" {
				t.Errorf("user = %v", u.User)
			}
			if u.Port() != "5432" {
				t.Errorf("port = %q", u.Port())
			}
		}},
		{"password only", driver.Config{Host: "h"}, "pw", func(t *testing.T, u *url.URL) {
			if p, _ := u.User.Password(); p != "pw" {
				t.Errorf("password = %q", p)
			}
		}},
		{"no user", driver.Config{Host: "::1", Database: "d"}, "", func(t *testing.T, u *url.URL) {
			if u.User != nil || u.Hostname() != "::1" || u.Path != "/d" {
				t.Errorf("url = %v", u)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dsn, err := d.BuildDSN(tc.cfg, tc.password)
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, u)
		})
	}
}

func TestBuildDSNErrors(t *testing.T) {
	d := postgres.New()
	if _, err := d.BuildDSN(driver.Config{}, ""); err == nil {
		t.Error("missing host should fail")
	}
	_, err := d.BuildDSN(driver.Config{Host: "h", Options: map[string]string{"password": "x"}}, "")
	if !errors.Is(err, driver.ErrReservedOption) {
		t.Errorf("err = %v, want ErrReservedOption", err)
	}
	if _, err := d.Open(context.Background(), driver.Config{}, ""); err == nil {
		t.Error("Open with missing host should fail")
	}
	_, err = d.Open(context.Background(), driver.Config{Host: "h", Options: map[string]string{"sslmode": "bogus"}}, "")
	if err == nil || !strings.Contains(err.Error(), "invalid connection settings") {
		t.Errorf("Open with bad sslmode: %v", err)
	}
}

func TestServerVersionError(t *testing.T) {
	db, err := postgres.New().Open(context.Background(), driver.Config{Host: "127.0.0.1", Port: 1, ConnectTimeout: time.Second}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := postgres.New().ServerVersion(context.Background(), db); err == nil {
		t.Fatal("expected an error without a server")
	}
}

func TestOpenAgainstServer(t *testing.T) {
	tdb := testutil.SharedPostgres(t)
	pc, err := pgx.ParseConfig(tdb.DSN)
	if err != nil {
		t.Fatal(err)
	}
	cfg := driver.Config{
		Host: pc.Host, Port: int(pc.Port), User: pc.User, Database: pc.Database,
		Options: map[string]string{"sslmode": "disable"},
	}
	d := postgres.New()
	db, err := d.Open(context.Background(), cfg, pc.Password)
	if err != nil {
		t.Fatal(err)
	}
	defer func(db *sql.DB) { _ = db.Close() }(db)
	v, err := d.ServerVersion(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v, "PostgreSQL 16.") {
		t.Errorf("version = %q, want PostgreSQL 16.x", v)
	}
}
