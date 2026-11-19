package mcpserver

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"lontar/internal/values"
)

// cellText renders a cell for the CSV output. NULL is the bare word NULL;
// text that could be mistaken for it, or for an empty field, is quoted by
// csvField. Binary values are \x-prefixed hex.
func cellText(c values.Cell) (text string, null bool) {
	switch c.K {
	case values.Null:
		return "NULL", true
	case values.Bytes:
		s, _ := c.V.(string)
		s = `\x` + s
		if c.Trunc {
			s += fmt.Sprintf("… (%d bytes)", c.Len)
		}
		return s, false
	case values.Bool:
		if b, _ := c.V.(bool); b {
			return "true", false
		}
		return "false", false
	case values.Num:
		switch v := c.V.(type) {
		case float64:
			return strconv.FormatFloat(v, 'g', -1, 64), false
		case float32:
			return strconv.FormatFloat(float64(v), 'g', -1, 32), false
		}
	}
	s := fmt.Sprint(c.V)
	if c.V == nil {
		s = ""
	}
	if c.Trunc {
		s += "…"
	}
	return s, false
}

// clip cuts s to max bytes on a UTF-8 boundary, saying how long it was.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	n := max
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + fmt.Sprintf("… (%d bytes)", len(s))
}

// csvField quotes a field when needed (RFC 4180). Empty text and text
// reading NULL are quoted so that they differ from an empty field and from
// SQL NULL.
func csvField(s string, null bool) string {
	if null {
		return s
	}
	if s == "" || strings.EqualFold(s, "NULL") || strings.ContainsAny(s, ",\"\r\n") ||
		strings.TrimSpace(s) != s {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// csvLine joins fields into one CSV line (with the trailing newline).
func csvLine(fields []string) string {
	return strings.Join(fields, ",") + "\n"
}

// rowLine renders a result row.
func rowLine(row []values.Cell) string {
	fields := make([]string, len(row))
	for i, c := range row {
		text, null := cellText(c)
		fields[i] = csvField(clip(text, MaxCellBytes), null)
	}
	return csvLine(fields)
}

// budget tracks the remaining output bytes of a tool result.
type budget struct {
	left int
	hit  bool
}

func newBudget() *budget { return &budget{left: MaxOutputBytes} }

// take reserves n bytes; it fails (and remembers it) when they do not fit.
func (b *budget) take(n int) bool {
	if n > b.left {
		b.hit = true
		return false
	}
	b.left -= n
	return true
}

// plural returns "1 row" / "2 rows".
func plural(n int64, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
