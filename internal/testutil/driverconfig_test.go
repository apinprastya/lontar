package testutil

import (
	"testing"

	"lontar/internal/driver"
	"lontar/internal/driver/sqlite"
)

func TestDriverConfig(t *testing.T) {
	cfg, pw := DriverConfig(t, driver.Postgres, DB{DSN: "postgres://u:p%40ss@127.0.0.1:5433/app?sslmode=disable"})
	if pw != "p@ss" || cfg.User != "u" || cfg.Database != "app" || cfg.Host != "127.0.0.1" || cfg.Port != 5433 || cfg.Options["sslmode"] != "disable" {
		t.Fatalf("postgres cfg = %+v, %q", cfg, pw)
	}
	cfg, pw = DriverConfig(t, driver.MySQL, DB{DSN: "root:secret@tcp(localhost:3307)/shop?parseTime=true"})
	if pw != "secret" || cfg.User != "root" || cfg.Database != "shop" || cfg.Host != "localhost" || cfg.Port != 3307 || cfg.Kind != driver.MySQL {
		t.Fatalf("mysql cfg = %+v, %q", cfg, pw)
	}
	target := SQLiteTemp(t)
	cfg, _ = DriverConfig(t, driver.SQLite, target)
	if cfg.FilePath == "" || cfg.FilePath == target.DSN || !cfg.CreateIfMissing {
		t.Fatalf("sqlite cfg = %+v", cfg)
	}
}

func TestOpenDriverSQLite(t *testing.T) {
	target := SQLiteTemp(t)
	d := sqlite.New()
	db := OpenDriver(t, d, target, false)
	if _, err := db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	ro := OpenDriver(t, d, target, true)
	if _, err := ro.Exec("INSERT INTO t VALUES (1)"); err == nil {
		t.Fatal("read-only open must reject writes")
	}
}
