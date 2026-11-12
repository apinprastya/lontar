// Package all builds a driver.Registry containing every supported driver.
package all

import (
	"lontar/internal/driver"
	"lontar/internal/driver/mysql"
	"lontar/internal/driver/postgres"
	"lontar/internal/driver/sqlite"
)

// Registry returns a new registry with the PostgreSQL, MySQL, MariaDB and
// SQLite drivers.
func Registry() *driver.Registry {
	return driver.NewRegistry(postgres.New(), mysql.New(), mysql.NewMariaDB(), sqlite.New())
}
