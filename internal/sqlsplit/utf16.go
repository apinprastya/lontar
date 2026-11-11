package sqlsplit

import (
	"unicode/utf16"
	"unicode/utf8"
)

// OffsetMap converts byte offsets of a string into UTF-16 offsets
// (JavaScript string indices, as used by the editor), plus a base.
type OffsetMap struct {
	base int
	u16  []int32 // nil for ASCII strings (identity)
}

// NewOffsetMap builds the map for s. Invalid UTF-8 bytes count as one
// unit each (they arrive as U+FFFD).
func NewOffsetMap(s string, base int) OffsetMap {
	m := OffsetMap{base: base}
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return m
	}
	m.u16 = make([]int32, len(s)+1)
	n := int32(0)
	for i, r := range s {
		_, size := utf8.DecodeRuneInString(s[i:])
		for k := 0; k < size; k++ {
			m.u16[i+k] = n
		}
		n += int32(max(1, utf16.RuneLen(r)))
	}
	m.u16[len(s)] = n
	return m
}

// UTF16 returns base + the UTF-16 offset of byte offset b (clamped to the
// string).
func (m OffsetMap) UTF16(b int) int {
	if m.u16 == nil {
		return m.base + max(0, b)
	}
	b = max(0, min(b, len(m.u16)-1))
	return m.base + int(m.u16[b])
}

// ByteOffset converts a UTF-16 offset in s into a byte offset (clamped to
// the string; an offset inside a surrogate pair maps to the rune start).
func ByteOffset(s string, u16 int) int {
	if u16 <= 0 {
		return 0
	}
	n := 0
	for i, r := range s {
		w := max(1, utf16.RuneLen(r))
		if n+w > u16 {
			return i
		}
		n += w
	}
	return len(s)
}
