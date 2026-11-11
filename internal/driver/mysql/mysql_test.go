package mysql_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"dbedit/internal/driver"
	"dbedit/internal/driver/mysql"
	"dbedit/internal/testutil"

	gomysql "github.com/go-sql-driver/mysql"
)

func TestMain(m *testing.M) { os.Exit(testutil.Main(m)) }

const specialPassword = `p@ss:w/rd'"%`

func TestMetadata(t *testing.T) {
	my, maria := mysql.New(), mysql.NewMariaDB()
	if my.Kind() != driver.MySQL || my.DisplayName() != "MySQL" || my.DefaultPort() != 3306 {
		t.Errorf("mysql metadata: %s %s %d", my.Kind(), my.DisplayName(), my.DefaultPort())
	}
	if maria.Kind() != driver.MariaDB || maria.DisplayName() != "MariaDB" || maria.DefaultPort() != 3306 {
		t.Errorf("mariadb metadata: %s %s %d", maria.Kind(), maria.DisplayName(), maria.DefaultPort())
	}
}

func TestBuildDSNSpecialPassword(t *testing.T) {
	for _, opts := range []map[string]string{nil, {"charset": "utf8mb4", "sql_mode": "'ANSI_QUOTES'"}} {
		cfg := driver.Config{Host: "db.local", Port: 3307, User: "root", Database: "app", Options: opts, ConnectTimeout: 4 * time.Second}
		dsn, err := mysql.New().BuildDSN(cfg, specialPassword)
		if err != nil {
			t.Fatal(err)
		}
		mc, err := gomysql.ParseDSN(dsn)
		if err != nil {
			t.Fatalf("ParseDSN(%q): %v", dsn, err)
		}
		if mc.Passwd != specialPassword {
			t.Errorf("password = %q, want %q (dsn %q)", mc.Passwd, specialPassword, dsn)
		}
		if mc.User != "root" || mc.Addr != "db.local:3307" || mc.DBName != "app" || mc.Net != "tcp" {
			t.Errorf("parsed = %+v", mc)
		}
		if !mc.ParseTime || mc.Timeout != 4*time.Second {
			t.Errorf("parseTime=%v timeout=%v", mc.ParseTime, mc.Timeout)
		}
		if opts != nil && mc.Params["sql_mode"] != "'ANSI_QUOTES'" {
			t.Errorf("params = %v", mc.Params)
		}
	}
}

func TestConfigKeepsIdentityWithOptions(t *testing.T) {
	cfg := driver.Config{Host: "::1", User: "a:b@c", Database: "x/y", Options: map[string]string{"charset": "utf8mb4"}}
	mc, err := mysql.New().Config(cfg, specialPassword)
	if err != nil {
		t.Fatal(err)
	}
	if mc.User != "a:b@c" || mc.DBName != "x/y" || mc.Passwd != specialPassword || mc.Addr != "[::1]:3306" {
		t.Errorf("config = %+v", mc)
	}
}

func TestBuildDSNErrors(t *testing.T) {
	d := mysql.NewMariaDB()
	if _, err := d.BuildDSN(driver.Config{}, ""); err == nil {
		t.Error("missing host should fail")
	}
	_, err := d.BuildDSN(driver.Config{Host: "h", Options: map[string]string{"passwd": "x"}}, "")
	if !errors.Is(err, driver.ErrReservedOption) {
		t.Errorf("err = %v, want ErrReservedOption", err)
	}
	if _, err := d.BuildDSN(driver.Config{Host: "h", Options: map[string]string{"timeout": "notaduration"}}, ""); err == nil {
		t.Error("invalid option value should fail")
	}
	if _, err := d.Open(context.Background(), driver.Config{}, ""); err == nil {
		t.Error("Open with missing host should fail")
	}
}

func TestFormatVersion(t *testing.T) {
	cases := map[string]string{
		"8.0.36":                    "MySQL 8.0.36",
		"8.0.36-0ubuntu0.22.04.1":   "MySQL 8.0.36",
		"11.4.2-MariaDB-ubu2404":    "MariaDB 11.4.2",
		"5.5.5-10.6.12-MariaDB-log": "MariaDB 10.6.12",
		"10.11.6-MariaDB":           "MariaDB 10.11.6",
	}
	for in, want := range cases {
		if got := mysql.FormatVersion(in); got != want {
			t.Errorf("FormatVersion(%q) = %q, want %q", in, got, want)
		}
	}
	if !mysql.IsMariaDB("10.6.12-MariaDB") || mysql.IsMariaDB("8.0.36") {
		t.Error("IsMariaDB is wrong")
	}
}

func TestServerVersionError(t *testing.T) {
	d := mysql.New()
	db, err := d.Open(context.Background(), driver.Config{Host: "127.0.0.1", Port: 1, ConnectTimeout: time.Second}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := d.ServerVersion(context.Background(), db); err == nil {
		t.Fatal("expected an error without a server")
	}
}

func configFrom(t *testing.T, dsn string) (driver.Config, string) {
	t.Helper()
	mc, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, _ := strings.Cut(mc.Addr, ":")
	port, _ := strconv.Atoi(portStr)
	return driver.Config{Host: host, Port: port, User: mc.User, Database: mc.DBName}, mc.Passwd
}

func TestOpenAgainstServers(t *testing.T) {
	cases := []struct {
		name   string
		start  func(testing.TB) testutil.DB
		d      *mysql.Driver
		prefix string
	}{
		{"mysql8", testutil.SharedMySQL, mysql.New(), "MySQL 8."},
		{"mariadb11", testutil.SharedMariaDB, mysql.NewMariaDB(), "MariaDB 11."},
		// A connection saved as "mysql" that points at MariaDB is detected.
		{"mariadb-as-mysql", testutil.SharedMariaDB, mysql.New(), "MariaDB 11."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, pw := configFrom(t, tc.start(t).DSN)
			db, err := tc.d.Open(context.Background(), cfg, pw)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			v, err := tc.d.ServerVersion(context.Background(), db)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(v, tc.prefix) {
				t.Errorf("version = %q, want prefix %q", v, tc.prefix)
			}
		})
	}
}

func TestClientFoundRowsAndReadOnly(t *testing.T) {
	for _, opts := range []map[string]string{nil, {"charset": "utf8mb4", "transaction_read_only": "0", "clientFoundRows": "false"}} {
		for _, d := range []*mysql.Driver{mysql.New(), mysql.NewMariaDB()} {
			mc, err := d.Config(driver.Config{Host: "h", Options: opts}, "")
			if err != nil {
				t.Fatal(err)
			}
			if !mc.ClientFoundRows {
				t.Errorf("%s: clientFoundRows must always be on", d.Kind())
			}
			if mc.Params["transaction_read_only"] == "1" || mc.Params["tx_read_only"] != "" {
				t.Errorf("%s: read-write config has %v", d.Kind(), mc.Params)
			}
			mc, err = d.Config(driver.Config{Host: "h", Options: opts, ReadOnly: true}, "")
			if err != nil {
				t.Fatal(err)
			}
			want, other := mysql.ReadOnlyVariable(d.Kind()), "tx_read_only"
			if want == other {
				other = "transaction_read_only"
			}
			if _, has := mc.Params[other]; mc.Params[want] != "1" || has {
				t.Errorf("%s: read-only params = %v (want only %s=1)", d.Kind(), mc.Params, want)
			}
		}
	}
	if mysql.ReadOnlyVariable(driver.MariaDB) != "tx_read_only" || mysql.ReadOnlyVariable(driver.MySQL) != "transaction_read_only" {
		t.Fatal("ReadOnlyVariable")
	}
}

func TestParseEnum(t *testing.T) {
	cases := map[string][]string{
		"enum('a','b')":          {"a", "b"},
		"ENUM('it''s','x,y')":    {"it's", "x,y"},
		`enum('back\\slash','')`: {`back\slash`, ""},
		"varchar(10)":            nil,
		"enum('a'":               nil,
		"enum('a' 'b')":          nil,
		"enum(a)":                nil,
		"enum('unterminated)":    nil,
		"enum()":                 {},
	}
	for in, want := range cases {
		got := mysql.ParseEnum(in)
		if (got == nil) != (want == nil) || strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("ParseEnum(%q) = %q, want %q", in, got, want)
		}
	}
}
