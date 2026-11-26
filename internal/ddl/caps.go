package ddl

import (
	"regexp"
	"strconv"
	"strings"

	"lontar/internal/driver"
)

// Flavor is the engine a change is built for.
type Flavor string

// Flavors.
const (
	Postgres Flavor = "postgres"
	MySQL    Flavor = "mysql"
	MariaDB  Flavor = "mariadb"
	SQLite   Flavor = "sqlite"
)

// Caps says which structure changes an engine supports. The UI disables
// the rest (showing Reason) and Go refuses them.
type Caps struct {
	Flavor  string `json:"flavor"`
	Version string `json:"version"`
	// AlterColumn: change type, nullability, default, auto-increment of an
	// existing column.
	AlterColumn  bool `json:"alterColumn"`
	RenameColumn bool `json:"renameColumn"`
	DropColumn   bool `json:"dropColumn"`
	// Comments on tables and columns.
	Comments bool `json:"comments"`
	// Collation of columns (MySQL/MariaDB).
	Collation bool `json:"collation"`
	// PrimaryKey: replace the key of an existing table.
	PrimaryKey bool `json:"primaryKey"`
	// ForeignKeys: add/drop on an existing table.
	ForeignKeys bool `json:"foreignKeys"`
	// Checks: add/drop on an existing table (and in CREATE TABLE).
	Checks            bool     `json:"checks"`
	RenameIndex       bool     `json:"renameIndex"`
	PartialIndexes    bool     `json:"partialIndexes"`
	ExpressionIndexes bool     `json:"expressionIndexes"`
	IndexPrefix       bool     `json:"indexPrefix"`
	IndexMethods      []string `json:"indexMethods"`
	RenameTable       bool     `json:"renameTable"`
	// DropCascade: DROP TABLE … CASCADE (PostgreSQL).
	DropCascade bool `json:"dropCascade"`
	// UsingExpr: USING on a type change (PostgreSQL).
	UsingExpr bool `json:"usingExpr"`
	// EnumTypes: enum/set column types with a value list (MySQL/MariaDB).
	EnumTypes bool `json:"enumTypes"`
	// GeneratedStored / GeneratedVirtual: kinds of generated column that
	// can be added to an existing table.
	GeneratedStored  bool `json:"generatedStored"`
	GeneratedVirtual bool `json:"generatedVirtual"`
	// MaxNameLength is the identifier limit (0: none); bytes on
	// PostgreSQL, characters on MySQL/MariaDB.
	MaxNameLength int `json:"maxNameLength"`
	// TransactionalDDL: a failed Apply leaves nothing changed.
	TransactionalDDL bool `json:"transactionalDdl"`
	// Types are suggestions for the type input.
	Types []string `json:"types"`
	// Reason explains the disabled changes.
	Reason string `json:"reason"`
}

// SQLiteReason explains SQLite's limits.
const SQLiteReason = "SQLite's ALTER TABLE can only add, rename and drop columns and rename the table. Other changes need the table to be recreated; write that in the SQL editor."

var versionRe = regexp.MustCompile(`(\d+)(?:\.(\d+))?(?:\.(\d+))?`)

// parseVersion reads the first x.y.z of a version string (missing parts
// are 0).
func parseVersion(s string) [3]int {
	var v [3]int
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return v
	}
	for i := range 3 {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v
}

// atLeast compares a parsed version with x.y.z.
func atLeast(v [3]int, x, y, z int) bool {
	if v[0] != x {
		return v[0] > x
	}
	if v[1] != y {
		return v[1] > y
	}
	return v[2] >= z
}

// FlavorOf returns the engine flavor of a session: a MySQL connection to a
// MariaDB server (and vice versa) is detected from the version string.
func FlavorOf(kind driver.Kind, serverVersion string) Flavor {
	switch kind {
	case driver.Postgres:
		return Postgres
	case driver.MySQL, driver.MariaDB:
		if strings.Contains(strings.ToLower(serverVersion), "mariadb") {
			return MariaDB
		}
		if strings.Contains(strings.ToLower(serverVersion), "mysql") || kind == driver.MySQL {
			return MySQL
		}
		return MariaDB
	}
	return SQLite
}

var (
	pgTypes = []string{"integer", "bigint", "smallint", "serial", "bigserial", "numeric(10,2)", "real", "double precision",
		"boolean", "text", "varchar(255)", "char(1)", "date", "time", "timestamp", "timestamptz", "interval", "uuid",
		"json", "jsonb", "bytea", "inet", "text[]", "integer[]"}
	mysqlTypes = []string{"int", "int unsigned", "bigint", "bigint unsigned", "smallint", "tinyint", "tinyint(1)",
		"decimal(10,2)", "float", "double", "varchar(255)", "char(1)", "text", "mediumtext", "longtext", "date",
		"datetime", "timestamp", "time", "year", "json", "blob", "varbinary(255)", "enum", "set"}
	sqliteTypes = []string{"INTEGER", "TEXT", "REAL", "NUMERIC", "BLOB", "BOOLEAN", "DATE", "DATETIME", "VARCHAR(255)"}
)

// Capabilities returns what flavor at serverVersion supports.
func Capabilities(kind driver.Kind, serverVersion string) Caps {
	f := FlavorOf(kind, serverVersion)
	v := parseVersion(serverVersion)
	c := Caps{Flavor: string(f), Version: serverVersion}
	switch f {
	case Postgres:
		c.AlterColumn, c.RenameColumn, c.DropColumn, c.Comments = true, true, true, true
		c.PrimaryKey, c.ForeignKeys, c.Checks, c.RenameIndex = true, true, true, true
		c.PartialIndexes, c.ExpressionIndexes, c.RenameTable, c.DropCascade, c.UsingExpr = true, true, true, true, true
		c.IndexMethods = []string{"btree", "hash", "gin", "gist", "brin", "spgist"}
		c.GeneratedStored = true
		c.MaxNameLength, c.TransactionalDDL = 63, true
		c.Types = pgTypes
	case MySQL, MariaDB:
		c.AlterColumn, c.RenameColumn, c.DropColumn, c.Comments, c.Collation = true, true, true, true, true
		c.PrimaryKey, c.ForeignKeys, c.RenameTable, c.IndexPrefix, c.EnumTypes = true, true, true, true, true
		c.IndexMethods = []string{"BTREE", "HASH", "FULLTEXT", "SPATIAL"}
		c.GeneratedStored, c.GeneratedVirtual = true, true
		c.MaxNameLength = 64
		c.Types = mysqlTypes
		if f == MySQL {
			c.Checks = atLeast(v, 8, 0, 16)
			c.RenameIndex = atLeast(v, 5, 7, 0)
			c.ExpressionIndexes = atLeast(v, 8, 0, 13)
			if !c.Checks {
				c.Reason = "Check constraints need MySQL 8.0.16 or later."
			}
		} else {
			c.Checks = atLeast(v, 10, 2, 1)
			c.RenameIndex = atLeast(v, 10, 5, 2)
			if !c.Checks {
				c.Reason = "Check constraints need MariaDB 10.2.1 or later."
			}
		}
	default:
		c.RenameColumn = atLeast(v, 3, 25, 0)
		c.DropColumn = atLeast(v, 3, 35, 0)
		c.PartialIndexes, c.ExpressionIndexes, c.RenameTable, c.TransactionalDDL = true, true, true, true
		c.GeneratedVirtual = atLeast(v, 3, 31, 0)
		c.Types = sqliteTypes
		c.Reason = SQLiteReason
	}
	return c
}
