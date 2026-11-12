package export

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"lontar/internal/driver"
	"lontar/internal/values"
)

// Literal renders v as an SQL literal for kind's dialect. Unlike the
// preview renderer of package edits (display only), these literals are
// meant to be executed, so every form is independent of server settings:
//
//   - PostgreSQL: '…' with quotes doubled when the text has no backslash
//     and no control character; otherwise E'…' with backslash escapes.
//     Neither depends on standard_conforming_strings. Text cannot contain
//     NUL in PostgreSQL, so none is ever exported. Bytes are
//     decode('…','hex').
//   - MySQL/MariaDB: '…' with quotes doubled. A string with a backslash,
//     NUL or Ctrl-Z is written as _utf8mb4 X'…' (hex) instead, because a
//     backslash means different things with and without the
//     NO_BACKSLASH_ESCAPES SQL mode; the hex form reads the same in both.
//     Bytes are X'…'.
//   - SQLite: '…' with quotes doubled; text containing NUL is
//     CAST(X'…' AS TEXT). Bytes are X'…'.
//
// Numbers are bare when they are plain decimal numbers (exact text, so big
// integers and decimals survive); NaN/Infinity are quoted. Booleans are
// TRUE/FALSE on PostgreSQL and 1/0 elsewhere. NULL is NULL.
func Literal(kind driver.Kind, v Value) string {
	switch v.Kind {
	case values.Null:
		return "NULL"
	case values.Bool:
		if kind == driver.Postgres {
			if v.Bool {
				return "TRUE"
			}
			return "FALSE"
		}
		if v.Bool {
			return "1"
		}
		return "0"
	case values.Num:
		if plainNumber(v.Text) {
			return v.Text
		}
		return StringLiteral(kind, v.Text)
	case values.Bytes:
		return BytesLiteral(kind, v.Bytes)
	}
	return StringLiteral(kind, v.Text)
}

// plainNumber reports a decimal number ("-12", "3.50", "1e+21") that every
// dialect reads as a number.
func plainNumber(s string) bool {
	if s == "" || strings.ContainsAny(s, "xXpP_iInN") {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	if err == nil {
		return true
	}
	// Out of float range but still a valid numeric literal.
	var ne *strconv.NumError
	return errorsAs(err, &ne) && ne.Err == strconv.ErrRange
}

// BytesLiteral renders binary data.
func BytesLiteral(kind driver.Kind, b []byte) string {
	h := hex.EncodeToString(b)
	if kind == driver.Postgres {
		return "decode('" + h + "', 'hex')"
	}
	return "X'" + h + "'"
}

// StringLiteral renders text (see Literal).
func StringLiteral(kind driver.Kind, s string) string {
	switch kind {
	case driver.MySQL, driver.MariaDB:
		if strings.ContainsAny(s, "\\\x00\x1a") {
			if utf8.ValidString(s) {
				return "_utf8mb4 X'" + hex.EncodeToString([]byte(s)) + "'"
			}
			return "X'" + hex.EncodeToString([]byte(s)) + "'"
		}
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	case driver.SQLite:
		if strings.IndexByte(s, 0) >= 0 {
			return "CAST(X'" + hex.EncodeToString([]byte(s)) + "' AS TEXT)"
		}
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	if !strings.ContainsFunc(s, func(r rune) bool { return r == '\\' || r < 0x20 || r == 0x7f }) {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	var b strings.Builder
	b.WriteString("E'")
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; {
		case ch == '\\':
			b.WriteString(`\\`)
		case ch == '\'':
			b.WriteString(`\'`)
		case ch == '\n':
			b.WriteString(`\n`)
		case ch == '\r':
			b.WriteString(`\r`)
		case ch == '\t':
			b.WriteString(`\t`)
		case ch < 0x20 || ch == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, ch)
		default:
			b.WriteByte(ch)
		}
	}
	b.WriteByte('\'')
	return b.String()
}
