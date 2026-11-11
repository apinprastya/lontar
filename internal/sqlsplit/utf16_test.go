package sqlsplit

import "testing"

func TestOffsetMap(t *testing.T) {
	m := NewOffsetMap("abc", 10)
	if m.UTF16(2) != 12 || m.UTF16(-1) != 10 || m.u16 != nil {
		t.Fatal("ascii map")
	}
	s := "a😀b\xffc" // a, 😀 (4 bytes, 2 units), b, invalid byte, c
	m = NewOffsetMap(s, 0)
	for b, want := range map[int]int{0: 0, 1: 1, 5: 3, 6: 4, 7: 5, 8: 6, -1: 0, 99: 6} {
		if got := m.UTF16(b); got != want {
			t.Errorf("UTF16(%d) = %d, want %d", b, got, want)
		}
	}
	for u, want := range map[int]int{-3: 0, 0: 0, 1: 1, 2: 1, 3: 5, 4: 6, 5: 7, 6: 8, 99: 8} {
		if got := ByteOffset(s, u); got != want {
			t.Errorf("ByteOffset(%d) = %d, want %d", u, got, want)
		}
	}
}
