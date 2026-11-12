package driver_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"lontar/internal/driver"
)

type fakeDriver struct {
	driver.SQLiteDialect
	driver.Introspector // nil; not used by the registry tests
	kind                driver.Kind
	name                string
}

func (f fakeDriver) Kind() driver.Kind                                            { return f.kind }
func (f fakeDriver) DisplayName() string                                          { return f.name }
func (f fakeDriver) DefaultPort() int                                             { return 1 }
func (f fakeDriver) BuildDSN(driver.Config, string) (string, error)               { return "", nil }
func (f fakeDriver) Open(context.Context, driver.Config, string) (*sql.DB, error) { return nil, nil }
func (f fakeDriver) ServerVersion(context.Context, *sql.DB) (string, error)       { return "", nil }

func TestKindValid(t *testing.T) {
	for _, k := range driver.Kinds {
		if !k.Valid() {
			t.Errorf("%q should be valid", k)
		}
	}
	if driver.Kind("oracle").Valid() {
		t.Error("oracle should not be valid")
	}
	if !driver.SQLite.IsFileBased() || driver.Postgres.IsFileBased() {
		t.Error("IsFileBased is wrong")
	}
}

func TestConfigDefaults(t *testing.T) {
	var c driver.Config
	if c.Timeout() != driver.DefaultConnectTimeout {
		t.Errorf("Timeout() = %v", c.Timeout())
	}
	c.ConnectTimeout = time.Second
	if c.Timeout() != time.Second {
		t.Errorf("Timeout() = %v", c.Timeout())
	}
	if c.PortOr(5432) != 5432 {
		t.Error("PortOr should fall back")
	}
	c.Port = 6000
	if c.PortOr(5432) != 6000 {
		t.Error("PortOr should use Port")
	}
}

func TestCheckOptions(t *testing.T) {
	if err := driver.CheckOptions(map[string]string{"sslmode": "disable"}, "password"); err != nil {
		t.Fatal(err)
	}
	err := driver.CheckOptions(map[string]string{"password": "x"}, "user", "password")
	if !errors.Is(err, driver.ErrReservedOption) {
		t.Fatalf("err = %v, want ErrReservedOption", err)
	}
}

func TestRegistry(t *testing.T) {
	r := driver.NewRegistry(
		fakeDriver{kind: driver.SQLite, name: "SQLite"},
		fakeDriver{kind: "zeta", name: "Zeta"},
		fakeDriver{kind: "alpha", name: "Alpha"},
		fakeDriver{kind: driver.Postgres, name: "PG"},
	)
	d, err := r.Get(driver.Postgres)
	if err != nil || d.DisplayName() != "PG" {
		t.Fatalf("Get = %v, %v", d, err)
	}
	if _, err := r.Get(driver.MySQL); !errors.Is(err, driver.ErrUnknownKind) {
		t.Fatalf("err = %v, want ErrUnknownKind", err)
	}
	r.Register(fakeDriver{kind: driver.Postgres, name: "PostgreSQL"})

	got := r.List()
	want := []driver.Kind{driver.Postgres, driver.SQLite, "alpha", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("List() = %+v", got)
	}
	for i, k := range want {
		if got[i].Kind != k {
			t.Errorf("List()[%d] = %q, want %q", i, got[i].Kind, k)
		}
	}
	if got[0].DisplayName != "PostgreSQL" || got[0].DefaultPort != 1 || got[0].FileBased {
		t.Errorf("List()[0] = %+v", got[0])
	}
	if !got[1].FileBased {
		t.Error("sqlite should be file based")
	}
}
