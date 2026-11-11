package values

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

type stringer struct{}

func (stringer) String() string { return "P1D" }

func TestCategorize(t *testing.T) {
	cases := map[string]Category{
		"":                            CatUnknown,
		"INT4":                        CatInt,
		"int8":                        CatInt,
		"UNSIGNED BIGINT":             CatInt,
		"bigint(20) unsigned":         CatInt,
		"int(11) zerofill":            CatInt,
		"MEDIUMINT":                   CatInt,
		"NUMERIC":                     CatDecimal,
		"numeric(10,2)":               CatDecimal,
		"DECIMAL":                     CatDecimal,
		"FLOAT8":                      CatFloat,
		"double precision":            CatFloat,
		"BOOL":                        CatBool,
		"boolean":                     CatBool,
		"BYTEA":                       CatBinary,
		"BLOB":                        CatBinary,
		"varbinary(16)":               CatBinary,
		"JSONB":                       CatJSON,
		"json":                        CatJSON,
		"DATE":                        CatDate,
		"TIME":                        CatTime,
		"time with time zone":         CatTime,
		"TIMESTAMP":                   CatTimestamp,
		"DATETIME":                    CatTimestamp,
		"timestamp without time zone": CatTimestamp,
		"TIMESTAMPTZ":                 CatTimestampTZ,
		"timestamp with time zone":    CatTimestampTZ,
		"VARCHAR":                     CatText,
		"character varying(20)":       CatText,
		"TEXT":                        CatText,
		"UUID":                        CatText,
		"_INT4":                       CatText,
		"integer[]":                   CatText,
		"enum('a','b')":               CatText,
		"INTERVAL":                    CatText,
		"UNSIGNED INT":                CatInt,
		"BIGINTEGER":                  CatInt, // SQLite affinity: contains INT
		"NVARCHAR(10)":                CatText,
		"FLOATING":                    CatFloat,
		"BOX":                         CatUnknown,
	}
	for in, want := range cases {
		if got := Categorize(in); got != want {
			t.Errorf("Categorize(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestConvert(t *testing.T) {
	ts := time.Date(2024, 5, 1, 10, 30, 0, 500, time.UTC)
	big := make([]byte, MaxBytes+10)
	for i := range big {
		big[i] = 0xab
	}
	cases := []struct {
		name string
		v    any
		typ  string
		want Cell
	}{
		{"nil", nil, "TEXT", Cell{K: Null}},
		{"empty string is not null", "", "TEXT", Cell{K: Str, V: ""}},
		{"bool", true, "BOOL", Cell{K: Bool, V: true}},
		{"false", false, "BOOL", Cell{K: Bool, V: false}},
		{"int64 small", int64(42), "INT8", Cell{K: Num, V: int64(42)}},
		{"int64 2^53-1", int64(MaxSafeInt), "INT8", Cell{K: Num, V: int64(MaxSafeInt)}},
		{"int64 2^53+1", int64(1<<53 + 1), "INT8", Cell{K: Num, V: "9007199254740993"}},
		{"int64 -2^53-1", int64(-(1<<53 + 1)), "INT8", Cell{K: Num, V: "-9007199254740993"}},
		{"int", 7, "", Cell{K: Num, V: int64(7)}},
		{"int32", int32(7), "", Cell{K: Num, V: int32(7)}},
		{"int16", int16(7), "", Cell{K: Num, V: int16(7)}},
		{"int8", int8(7), "", Cell{K: Num, V: int8(7)}},
		{"uint32", uint32(7), "", Cell{K: Num, V: uint32(7)}},
		{"uint64 small", uint64(7), "UNSIGNED BIGINT", Cell{K: Num, V: uint64(7)}},
		{"uint64 big", uint64(math.MaxUint64), "UNSIGNED BIGINT", Cell{K: Num, V: "18446744073709551615"}},
		{"float64", 1.5, "FLOAT8", Cell{K: Num, V: 1.5}},
		{"float32", float32(0.5), "FLOAT4", Cell{K: Num, V: 0.5}},
		{"NaN", math.NaN(), "FLOAT8", Cell{K: Num, V: "NaN"}},
		{"+Inf", math.Inf(1), "FLOAT8", Cell{K: Num, V: "+Inf"}},
		{"time tz", ts, "TIMESTAMPTZ", Cell{K: Time, V: "2024-05-01T10:30:00.0000005Z"}},
		{"time unknown", ts, "", Cell{K: Time, V: "2024-05-01T10:30:00.0000005Z"}},
		{"timestamp no tz", ts, "TIMESTAMP", Cell{K: Time, V: "2024-05-01T10:30:00.0000005"}},
		{"date", ts, "DATE", Cell{K: Time, V: "2024-05-01"}},
		{"numeric string", "123.4500", "NUMERIC", Cell{K: Num, V: "123.4500"}},
		{"numeric int string", "12", "NUMERIC", Cell{K: Num, V: int64(12)}},
		{"numeric huge string", "123456789012345678901234567890", "NUMERIC", Cell{K: Num, V: "123456789012345678901234567890"}},
		{"numeric NaN string", "NaN", "NUMERIC", Cell{K: Num, V: "NaN"}},
		{"json string", `{"a":1}`, "JSONB", Cell{K: JSON, V: `{"a":1}`}},
		{"bool string", "t", "BOOL", Cell{K: Bool, V: true}},
		{"bad bool string", "maybe", "BOOL", Cell{K: Str, V: "maybe"}},
		{"timestamp string", "2024-01-01 00:00:00", "TIMESTAMP", Cell{K: Time, V: "2024-01-01 00:00:00"}},
		{"uuid string", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "UUID", Cell{K: Str, V: "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"}},
		{"array string", "{1,2,3}", "_INT4", Cell{K: Str, V: "{1,2,3}"}},
		{"interval string", "1 day", "INTERVAL", Cell{K: Str, V: "1 day"}},
		{"text bytes", []byte("héllo"), "VARCHAR", Cell{K: Str, V: "héllo"}},
		{"invalid utf8 text bytes", []byte{0xff, 0xfe}, "TEXT", Cell{K: Bytes, V: "fffe", Len: 2}},
		{"blob bytes", []byte("abc"), "BLOB", Cell{K: Bytes, V: "616263", Len: 3}},
		{"bytes unknown type", []byte("abc"), "", Cell{K: Bytes, V: "616263", Len: 3}},
		{"empty blob", []byte{}, "BYTEA", Cell{K: Bytes, V: "", Len: 0}},
		{"int bytes", []byte("42"), "BIGINT", Cell{K: Num, V: int64(42)}},
		{"big int bytes", []byte("18446744073709551615"), "UNSIGNED BIGINT", Cell{K: Num, V: "18446744073709551615"}},
		{"decimal bytes", []byte("10.50"), "DECIMAL", Cell{K: Num, V: "10.50"}},
		{"float bytes", []byte("1.25"), "DOUBLE", Cell{K: Num, V: "1.25"}},
		{"json bytes", []byte(`[1]`), "JSON", Cell{K: JSON, V: `[1]`}},
		{"bool bytes 1", []byte("1"), "BOOL", Cell{K: Bool, V: true}},
		{"bool bytes 0", []byte("0"), "BOOLEAN", Cell{K: Bool, V: false}},
		{"bool bytes other", []byte{2}, "BOOL", Cell{K: Bytes, V: "02", Len: 1}},
		{"date bytes", []byte("2024-01-02"), "DATE", Cell{K: Time, V: "2024-01-02"}},
		{"bad date bytes", []byte{0xff}, "DATE", Cell{K: Bytes, V: "ff", Len: 1}},
		{"time bytes", []byte("12:34:56"), "TIME", Cell{K: Str, V: "12:34:56"}},
		{"stringer", stringer{}, "INTERVAL", Cell{K: Str, V: "P1D"}},
		{"string slice", []string{"a", "b"}, "", Cell{K: Str, V: "{a,b}"}},
		{"uuid bytes16", [16]byte{0xa0, 0xee, 0xbc, 0x99, 0x9c, 0x0b, 0x4e, 0xf8, 0xbb, 0x6d, 0x6b, 0xb9, 0xbd, 0x38, 0x0a, 0x11}, "UUID", Cell{K: Str, V: "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"}},
		{"other", struct{ A int }{1}, "", Cell{K: Str, V: "{1}"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Convert(tc.v, tc.typ)
			if got.K != tc.want.K || got.V != tc.want.V || got.Len != tc.want.Len || got.Trunc != tc.want.Trunc {
				t.Fatalf("Convert(%#v, %q) = %#v, want %#v", tc.v, tc.typ, got, tc.want)
			}
		})
	}

	t.Run("big blob is truncated", func(t *testing.T) {
		c := Convert(big, "BLOB")
		if c.K != Bytes || !c.Trunc || c.Len != len(big) || len(c.V.(string)) != 2*MaxBytes {
			t.Fatalf("got K=%v trunc=%v len=%d hex=%d", c.K, c.Trunc, c.Len, len(c.V.(string)))
		}
	})
	t.Run("long text is truncated on a rune boundary", func(t *testing.T) {
		s := strings.Repeat("é", MaxText) // 2 bytes each
		c := Convert(s+"x", "TEXT")
		v := c.V.(string)
		if c.K != Str || !c.Trunc || len(v) > MaxText || !strings.HasSuffix(v, "é") {
			t.Fatalf("got K=%v trunc=%v len=%d", c.K, c.Trunc, len(v))
		}
		// Odd cut point: MaxText-1 bytes of ASCII then a 2-byte rune.
		s2 := strings.Repeat("a", MaxText-1) + "é"
		c2 := Convert(s2, "TEXT")
		if !c2.Trunc || len(c2.V.(string)) != MaxText-1 {
			t.Fatalf("cut = %d", len(c2.V.(string)))
		}
	})
}

func TestCellJSON(t *testing.T) {
	cases := []struct {
		c    Cell
		want string
	}{
		{NullCell, `{"k":"null"}`},
		{Convert("", "TEXT"), `{"k":"str","v":""}`},
		{Convert(false, "BOOL"), `{"k":"bool","v":false}`},
		{Convert(int64(1<<53+1), "INT8"), `{"k":"num","v":"9007199254740993"}`},
		{Convert(int64(0), "INT8"), `{"k":"num","v":0}`},
		{Convert([]byte{1, 2}, "BYTEA"), `{"k":"bytes","v":"0102","len":2}`},
		{Convert(math.Inf(-1), "FLOAT8"), `{"k":"num","v":"-Inf"}`},
	}
	for _, tc := range cases {
		b, err := json.Marshal(tc.c)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != tc.want {
			t.Errorf("json = %s, want %s", b, tc.want)
		}
	}
}
