package spotlight

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

// EncodePlist encodes a string or a list of strings as a binary property
// list (bplist00), the form Spotlight expects in com.apple.metadata:*
// attributes. Only what this package needs is supported.
func EncodePlist(v any) ([]byte, error) {
	var objs [][]byte // object 0 is the top object
	switch t := v.(type) {
	case string:
		objs = append(objs, plistString(t))
	case []string:
		if len(t) >= 255 {
			return nil, fmt.Errorf("plist: %d strings is too many", len(t))
		}
		arr := plistMarker(0xA, len(t))
		for i, s := range t {
			arr = append(arr, byte(i+1)) // one-byte object references
			objs = append(objs, plistString(s))
		}
		objs = append([][]byte{arr}, objs...)
	default:
		return nil, fmt.Errorf("plist: unsupported type %T", v)
	}

	out := []byte("bplist00")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = len(out)
		out = append(out, o...)
	}
	tableAt := len(out)
	size := intSize(tableAt)
	for _, off := range offsets {
		out = appendUint(out, uint64(off), size)
	}
	trailer := make([]byte, 32)
	trailer[6] = byte(size) // offset int size
	trailer[7] = 1          // object reference size
	binary.BigEndian.PutUint64(trailer[8:], uint64(len(objs)))
	binary.BigEndian.PutUint64(trailer[16:], 0) // top object
	binary.BigEndian.PutUint64(trailer[24:], uint64(tableAt))
	return append(out, trailer...), nil
}

// plistString is an ASCII string, or UTF-16BE when it has other runes.
func plistString(s string) []byte {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return append(plistMarker(0x5, len(s)), s...)
	}
	units := utf16.Encode([]rune(s))
	b := plistMarker(0x6, len(units))
	for _, u := range units {
		b = binary.BigEndian.AppendUint16(b, u)
	}
	return b
}

// plistMarker is an object's type nibble and length, with lengths of 15 and
// more following as an integer object.
func plistMarker(kind byte, n int) []byte {
	if n < 15 {
		return []byte{kind<<4 | byte(n)}
	}
	size := intSize(n)
	b := []byte{kind<<4 | 0xF, 0x10 | log2(size)}
	return appendUint(b, uint64(n), size)
}

// intSize is the number of bytes (1, 2, 4 or 8) n needs.
func intSize(n int) int {
	switch {
	case n < 1<<8:
		return 1
	case n < 1<<16:
		return 2
	case n < 1<<32:
		return 4
	}
	return 8
}

func log2(size int) byte {
	return map[int]byte{1: 0, 2: 1, 4: 2, 8: 3}[size]
}

func appendUint(b []byte, v uint64, size int) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return append(b, buf[8-size:]...)
}
