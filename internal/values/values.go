// Package values converts database/sql driver values into JSON-safe cells for
// the frontend grid.
//
// # Cell format
//
// A Cell is a tagged union encoded as {"k": kind, "v": value} plus optional
// flags:
//
//	{"k":"null"}                          SQL NULL (no "v"; distinct from "")
//	{"k":"str","v":"text"}                text ("" is an empty string, not NULL)
//	{"k":"num","v":42}                    number that fits a JS double exactly
//	{"k":"num","v":"9007199254740993"}    integer beyond ±2^53, or an exact
//	                                      decimal/numeric, or NaN/±Infinity:
//	                                      the exact text, still a number
//	{"k":"bool","v":true}
//	{"k":"bytes","v":"deadbeef","len":4}  binary as lowercase hex; "len" is
//	                                      the full size in bytes
//	{"k":"time","v":"2024-05-01T10:00:00Z"} ISO 8601 (dates as YYYY-MM-DD;
//	                                      zone-less types without offset)
//	{"k":"json","v":"{\"a\":1}"}          JSON/JSONB document as text
//
// "trunc": true marks a value cut for display: bytes beyond MaxBytes (the
// hex covers only the first MaxBytes bytes) and text beyond MaxText bytes.
// PostgreSQL arrays, UUIDs, intervals and other non-scalar types are sent as
// their text representation ("str").
package values

import (
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Kind is the type tag of a Cell.
type Kind string

// Cell kinds.
const (
	Null  Kind = "null"
	Str   Kind = "str"
	Num   Kind = "num"
	Bool  Kind = "bool"
	Bytes Kind = "bytes"
	Time  Kind = "time"
	JSON  Kind = "json"
)

// Display limits.
const (
	// MaxBytes is how many bytes of a binary value are sent (as hex).
	MaxBytes = 64 << 10
	// MaxText is how many bytes of a text value are sent.
	MaxText = 1 << 20
	// MaxSafeInt is the largest integer a JavaScript number holds exactly.
	MaxSafeInt = 1<<53 - 1
)

// Cell is one JSON-safe value. See the package documentation.
type Cell struct {
	K Kind `json:"k"`
	V any  `json:"v,omitempty"`
	// Len is the full size in bytes of a bytes value.
	Len int `json:"len,omitempty"`
	// Trunc reports that V was cut for display.
	Trunc bool `json:"trunc,omitempty"`
}

// NullCell is the SQL NULL cell.
var NullCell = Cell{K: Null}

// Category is a coarse classification of a database type name, used to
// interpret driver values (notably []byte, which MySQL returns for almost
// everything).
type Category int

// Type categories.
const (
	CatUnknown Category = iota
	CatText
	CatInt
	CatDecimal
	CatFloat
	CatBool
	CatBinary
	CatJSON
	CatDate
	CatTime      // time of day
	CatTimestamp // timestamp/datetime without time zone
	CatTimestampTZ
)

// Categorize classifies a database type name as returned by
// sql.ColumnType.DatabaseTypeName() or an introspected column type
// ("INT4", "VARCHAR", "UNSIGNED BIGINT", "character varying(20)",
// "timestamp with time zone", "_INT4", ...). Matching is case-insensitive.
func Categorize(dbType string) Category {
	t := strings.ToUpper(strings.TrimSpace(dbType))
	if t == "" {
		return CatUnknown
	}
	// PostgreSQL arrays ("_INT4", "integer[]") are shown as text.
	if strings.HasPrefix(t, "_") || strings.HasSuffix(t, "]") {
		return CatText
	}
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = strings.TrimSpace(t[:i]) + strings.TrimSpace(t[strings.IndexByte(t, ')')+1:])
	}
	t = strings.TrimPrefix(t, "UNSIGNED ")
	t = strings.TrimSuffix(t, " UNSIGNED")
	t = strings.TrimSuffix(t, " ZEROFILL")
	switch t {
	case "INT", "INT2", "INT4", "INT8", "INTEGER", "SMALLINT", "BIGINT", "TINYINT", "MEDIUMINT",
		"SERIAL", "BIGSERIAL", "SMALLSERIAL", "YEAR", "OID":
		return CatInt
	case "NUMERIC", "DECIMAL", "NEWDECIMAL", "MONEY":
		return CatDecimal
	case "FLOAT", "FLOAT4", "FLOAT8", "DOUBLE", "DOUBLE PRECISION", "REAL":
		return CatFloat
	case "BOOL", "BOOLEAN":
		return CatBool
	case "BYTEA", "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "BINARY", "VARBINARY", "BIT", "GEOMETRY":
		return CatBinary
	case "JSON", "JSONB":
		return CatJSON
	case "DATE":
		return CatDate
	case "TIME", "TIMETZ", "TIME WITH TIME ZONE", "TIME WITHOUT TIME ZONE":
		return CatTime
	case "TIMESTAMP", "DATETIME", "TIMESTAMP WITHOUT TIME ZONE":
		return CatTimestamp
	case "TIMESTAMPTZ", "TIMESTAMP WITH TIME ZONE":
		return CatTimestampTZ
	}
	switch t {
	case "UUID", "NAME", "CITEXT", "INTERVAL", "INET", "CIDR", "XML", "POINT", "TSVECTOR", "MACADDR":
		return CatText
	}
	// SQLite type affinity rules for declared types.
	switch {
	case strings.Contains(t, "INT"):
		return CatInt
	case strings.Contains(t, "CHAR"), strings.Contains(t, "CLOB"), strings.Contains(t, "TEXT"),
		strings.HasPrefix(t, "ENUM"), strings.HasPrefix(t, "SET"):
		return CatText
	case strings.Contains(t, "REAL"), strings.Contains(t, "FLOA"), strings.Contains(t, "DOUB"):
		return CatFloat
	}
	return CatUnknown
}

// Convert turns a value scanned into *any into a Cell, using the column's
// database type name to interpret it.
func Convert(v any, dbType string) Cell {
	return ConvertCat(v, Categorize(dbType))
}

// ConvertCat is Convert with a precomputed category (use it per column to
// avoid re-categorising every row).
func ConvertCat(v any, cat Category) Cell {
	switch x := v.(type) {
	case nil:
		return NullCell
	case bool:
		return Cell{K: Bool, V: x}
	case int64:
		return intCell(x)
	case int:
		return intCell(int64(x))
	case int32:
		return Cell{K: Num, V: x}
	case int16:
		return Cell{K: Num, V: x}
	case int8:
		return Cell{K: Num, V: x}
	case uint64:
		if x > MaxSafeInt {
			return Cell{K: Num, V: strconv.FormatUint(x, 10)}
		}
		return Cell{K: Num, V: x}
	case uint32:
		return Cell{K: Num, V: x}
	case float64:
		return floatCell(x, 64)
	case float32:
		return floatCell(float64(x), 32)
	case time.Time:
		return timeCell(x, cat)
	case string:
		return stringCell(x, cat)
	case []byte:
		return bytesCell(x, cat)
	case interface{ String() string }:
		return textCell(x.String())
	default:
		return textCell(fallbackString(v))
	}
}

func intCell(x int64) Cell {
	if x > MaxSafeInt || x < -MaxSafeInt {
		return Cell{K: Num, V: strconv.FormatInt(x, 10)}
	}
	return Cell{K: Num, V: x}
}

func floatCell(x float64, bits int) Cell {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return Cell{K: Num, V: strconv.FormatFloat(x, 'g', -1, bits)}
	}
	return Cell{K: Num, V: x}
}

func timeCell(t time.Time, cat Category) Cell {
	switch cat {
	case CatDate:
		return Cell{K: Time, V: t.Format("2006-01-02")}
	case CatTimestamp:
		// Zone-less types: the driver reports them in UTC, but the zone
		// is not part of the value, so do not print one.
		return Cell{K: Time, V: t.Format("2006-01-02T15:04:05.999999999")}
	}
	return Cell{K: Time, V: t.Format(time.RFC3339Nano)}
}

// stringCell handles drivers that return text (pgx for most types).
func stringCell(s string, cat Category) Cell {
	switch cat {
	case CatDecimal, CatInt, CatFloat:
		return numText(s)
	case CatJSON:
		return Cell{K: JSON, V: s}
	case CatBool:
		if b, err := strconv.ParseBool(s); err == nil {
			return Cell{K: Bool, V: b}
		}
	case CatDate, CatTimestamp, CatTimestampTZ:
		return Cell{K: Time, V: s}
	}
	return textCell(s)
}

// numText keeps a numeric value given as text exact. Integers that fit a
// JS double become JSON numbers; everything else (decimals, huge ints,
// "NaN") stays a string.
func numText(s string) Cell {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return intCell(i)
	}
	return Cell{K: Num, V: s}
}

func bytesCell(b []byte, cat Category) Cell {
	switch cat {
	case CatBinary:
		return hexCell(b)
	case CatInt:
		return numText(string(b))
	case CatDecimal, CatFloat:
		return Cell{K: Num, V: string(b)}
	case CatJSON:
		return Cell{K: JSON, V: string(b)}
	case CatBool:
		if s := string(b); s == "0" || s == "1" {
			return Cell{K: Bool, V: s == "1"}
		}
	case CatDate, CatTimestamp, CatTimestampTZ:
		if utf8.Valid(b) {
			return Cell{K: Time, V: string(b)}
		}
	case CatText, CatTime:
		if utf8.Valid(b) {
			return textCell(string(b))
		}
	}
	return hexCell(b)
}

func hexCell(b []byte) Cell {
	c := Cell{K: Bytes, Len: len(b)}
	if len(b) > MaxBytes {
		b = b[:MaxBytes]
		c.Trunc = true
	}
	c.V = hex.EncodeToString(b)
	return c
}

func textCell(s string) Cell {
	if len(s) <= MaxText {
		return Cell{K: Str, V: s}
	}
	cut := MaxText
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return Cell{K: Str, V: s[:cut], Trunc: true}
}

func fallbackString(v any) string {
	switch x := v.(type) {
	case []string:
		return "{" + strings.Join(x, ",") + "}"
	case [16]byte: // UUID
		return formatUUID(x)
	}
	return strings.TrimSpace(strings.ReplaceAll(fmt.Sprint(v), "\n", " "))
}

func formatUUID(u [16]byte) string {
	h := hex.EncodeToString(u[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
