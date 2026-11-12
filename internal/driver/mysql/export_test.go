package mysql

import "lontar/internal/driver"

// Exported for the external tests.
var (
	ApplyExtra = applyExtra
	Delimited  = delimited
)

// IndexDefinition exposes indexDefinition to the external tests.
func (d *Driver) IndexDefinition(ix driver.Index, cols []string) string {
	return d.indexDefinition(ix, cols)
}
