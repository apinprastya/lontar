package export

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"dbedit/internal/values"
)

// Value is one exported value at full precision.
type Value struct {
	// Kind is null, str, num, bool, bytes, time or json.
	Kind values.Kind
	// Text holds str, num (exact decimal text), time and json values.
	Text string
	Bool bool
	// Bytes holds bytes values.
	Bytes []byte
	// Safe reports a num that a JSON number holds exactly (integers within
	// ±2^53 and floats). Decimals and big integers are not Safe.
	Safe bool
}

// Null is the NULL value.
var Null = Value{Kind: values.Null}

// ErrTruncated is returned when exporting loaded rows that were cut for
// display.
var ErrTruncated = errors.New("some values were cut for display; use Export all rows to get them in full")

// FromDriver converts a value scanned from database/sql (into *any) using
// the column's type category. Nothing is truncated.
func FromDriver(v any, cat values.Category) Value {
	if t, ok := v.(time.Time); ok {
		return Value{Kind: values.Time, Text: formatTime(t, cat)}
	}
	switch x := v.(type) {
	case float64:
		return floatValue(x, 64)
	case float32:
		return floatValue(float64(x), 32)
	}
	c := values.ConvertFull(v, cat)
	if c.K == values.Bytes {
		// ConvertFull hex-encodes; keep the raw bytes instead.
		switch b := v.(type) {
		case []byte:
			return Value{Kind: values.Bytes, Bytes: b}
		}
	}
	out, _ := FromCell(c)
	return out
}

func floatValue(f float64, bits int) Value {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Value{Kind: values.Num, Text: strconv.FormatFloat(f, 'g', -1, bits)}
	}
	return Value{Kind: values.Num, Text: strconv.FormatFloat(f, 'g', -1, bits), Safe: true}
}

// formatTime renders a time like the SQL literal of its type: dates as
// 2006-01-02, zone-less timestamps without an offset, and timestamps with a
// time zone with one.
func formatTime(t time.Time, cat values.Category) string {
	switch cat {
	case values.CatDate:
		return t.Format("2006-01-02")
	case values.CatTimestamp:
		return t.Format("2006-01-02 15:04:05.999999999")
	case values.CatTime:
		return t.Format("15:04:05.999999999")
	}
	return t.Format("2006-01-02 15:04:05.999999999Z07:00")
}

// FromCell converts a grid cell (rows already loaded in the UI). A cell cut
// for display (Trunc) is refused with ErrTruncated.
func FromCell(c values.Cell) (Value, error) {
	if c.Trunc {
		return Value{}, ErrTruncated
	}
	switch c.K {
	case values.Null, "":
		return Null, nil
	case values.Bool:
		b, _ := c.V.(bool)
		return Value{Kind: values.Bool, Bool: b}, nil
	case values.Num:
		return numValue(c.V)
	case values.Bytes:
		s, _ := c.V.(string)
		b, err := hex.DecodeString(s)
		if err != nil {
			return Value{}, fmt.Errorf("invalid bytes cell: %w", err)
		}
		return Value{Kind: values.Bytes, Bytes: b}, nil
	case values.Str, values.Time, values.JSON:
		return Value{Kind: c.K, Text: fmt.Sprint(c.V)}, nil
	}
	return Value{}, fmt.Errorf("unknown cell kind %q", c.K)
}

func numValue(v any) (Value, error) {
	switch x := v.(type) {
	case string:
		// Big integers, exact decimals, NaN/Infinity.
		return Value{Kind: values.Num, Text: x}, nil
	case float64:
		if x == math.Trunc(x) && math.Abs(x) <= values.MaxSafeInt {
			return Value{Kind: values.Num, Text: strconv.FormatInt(int64(x), 10), Safe: true}, nil
		}
		return floatValue(x, 64), nil
	case int64:
		return intValue(x), nil
	case int:
		return intValue(int64(x)), nil
	case int32:
		return intValue(int64(x)), nil
	case int16:
		return intValue(int64(x)), nil
	case int8:
		return intValue(int64(x)), nil
	case uint64:
		return Value{Kind: values.Num, Text: strconv.FormatUint(x, 10), Safe: x <= values.MaxSafeInt}, nil
	case uint32:
		return intValue(int64(x)), nil
	case interface{ String() string }: // json.Number
		s := x.String()
		_, err := strconv.ParseInt(s, 10, 64)
		return Value{Kind: values.Num, Text: s, Safe: err == nil}, nil
	}
	return Value{}, fmt.Errorf("unexpected number %T", v)
}

func intValue(i int64) Value {
	return Value{Kind: values.Num, Text: strconv.FormatInt(i, 10), Safe: i <= values.MaxSafeInt && i >= -values.MaxSafeInt}
}
