package spotlight

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

// decodePlist reads back what EncodePlist writes (strings and arrays of
// them), following the trailer and offset table like a real reader.
func decodePlist(t *testing.T, b []byte) any {
	t.Helper()
	if len(b) < 40 || string(b[:8]) != "bplist00" {
		t.Fatalf("not a bplist00: %x", b)
	}
	tr := b[len(b)-32:]
	offSize, refSize := int(tr[6]), int(tr[7])
	n := int(binary.BigEndian.Uint64(tr[8:]))
	top := int(binary.BigEndian.Uint64(tr[16:]))
	table := int(binary.BigEndian.Uint64(tr[24:]))
	readUint := func(at, size int) int {
		v := 0
		for i := 0; i < size; i++ {
			v = v<<8 | int(b[at+i])
		}
		return v
	}
	offset := func(i int) int { return readUint(table+i*offSize, offSize) }
	length := func(at int) (int, int) { // length, start of payload
		if l := int(b[at] & 0xF); l != 0xF {
			return l, at + 1
		}
		size := 1 << (b[at+1] & 0xF)
		return readUint(at+2, size), at + 2 + size
	}
	var obj func(i int) any
	obj = func(i int) any {
		if i >= n {
			t.Fatalf("object %d of %d", i, n)
		}
		at := offset(i)
		l, p := length(at)
		switch b[at] >> 4 {
		case 0x5:
			return string(b[p : p+l])
		case 0x6:
			u := make([]uint16, l)
			for k := range u {
				u[k] = binary.BigEndian.Uint16(b[p+2*k:])
			}
			return string(utf16.Decode(u))
		case 0xA:
			out := make([]string, l)
			for k := range out {
				out[k] = obj(readUint(p+k*refSize, refSize)).(string)
			}
			return out
		}
		t.Fatalf("unexpected marker %x", b[at])
		return nil
	}
	return obj(top)
}

func TestEncodePlistGolden(t *testing.T) {
	// Checked with Python: plistlib.loads(bytes.fromhex(...)) == ['db', 'sql'].
	got, err := EncodePlist([]string{"db", "sql"})
	if err != nil {
		t.Fatal(err)
	}
	const want = "62706c6973743030a201025264625373716c080b0e0000000000000101000000000000000300000000000000000000000000000012"
	if hex.EncodeToString(got) != want {
		t.Fatalf("got %x", got)
	}
}

func TestEncodePlistRoundTrip(t *testing.T) {
	long := strings.Repeat("x", 300)
	many := make([]string, 20)
	for i := range many {
		many[i] = fmt.Sprintf("keyword %d", i)
	}
	for _, v := range []any{"", "db", long, "Café ☕ 🐘", Keywords, many, []string{}, Comment} {
		b, err := EncodePlist(v)
		if err != nil {
			t.Fatal(err)
		}
		if got := decodePlist(t, b); !reflect.DeepEqual(got, v) {
			t.Fatalf("round trip of %q: %q", v, got)
		}
	}
	// A large plist needs two-byte offsets.
	big := make([]string, 200)
	for i := range big {
		big[i] = strings.Repeat("y", 40)
	}
	b, err := EncodePlist(big)
	if err != nil {
		t.Fatal(err)
	}
	if b[len(b)-32+6] != 2 {
		t.Fatalf("offset size %d", b[len(b)-32+6])
	}
	if got := decodePlist(t, b); !reflect.DeepEqual(got, big) {
		t.Fatal("large round trip")
	}
}

func TestEncodePlistErrors(t *testing.T) {
	if _, err := EncodePlist(42); err == nil {
		t.Fatal("int accepted")
	}
	if _, err := EncodePlist(make([]string, 255)); err == nil {
		t.Fatal("too many strings accepted")
	}
}

func TestIntSize(t *testing.T) {
	for n, want := range map[int]int{0: 1, 255: 1, 256: 2, 65535: 2, 65536: 4, 1<<32 - 1: 4, 1 << 32: 8} {
		if got := intSize(n); got != want {
			t.Errorf("intSize(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestBundlePath(t *testing.T) {
	for exe, want := range map[string]string{
		"/Applications/lontar.app/Contents/MacOS/lontar":       "/Applications/lontar.app",
		"/Users/me/Apps/Lontar Beta.app/Contents/MacOS/lontar": "/Users/me/Apps/Lontar Beta.app",
		"/Users/me/src/lontar/bin/lontar":                      "",
		".app/Contents/MacOS/lontar":                           "",
	} {
		got, ok := BundlePath(exe)
		if got != want || ok != (want != "") {
			t.Errorf("BundlePath(%q) = %q, %v", exe, got, ok)
		}
	}
}

type fakeAttrs struct {
	m      map[string][]byte
	sets   int
	getErr map[string]error
	setErr map[string]error
}

func newFake() *fakeAttrs {
	return &fakeAttrs{m: map[string][]byte{}, getErr: map[string]error{}, setErr: map[string]error{}}
}

func (f *fakeAttrs) Get(path, name string) ([]byte, error) {
	if err := f.getErr[name]; err != nil {
		return nil, err
	}
	return f.m[path+"|"+name], nil
}

func (f *fakeAttrs) Set(path, name string, v []byte) error {
	if err := f.setErr[name]; err != nil {
		return err
	}
	f.sets++
	f.m[path+"|"+name] = v
	return nil
}

const bundle = "/Applications/lontar.app"

func TestTagWritesOnceAndKeepsUserComment(t *testing.T) {
	f := newFake()
	changed, err := Tag(f, bundle)
	if err != nil || !changed {
		t.Fatalf("first tag: %v %v", changed, err)
	}
	if got := decodePlist(t, f.m[bundle+"|"+KeywordsAttr]); !reflect.DeepEqual(got, Keywords) {
		t.Fatalf("keywords %q", got)
	}
	if got := decodePlist(t, f.m[bundle+"|"+CommentAttr]); got != Comment {
		t.Fatalf("comment %q", got)
	}
	changed, err = Tag(f, bundle)
	if err != nil || changed || f.sets != 2 {
		t.Fatalf("second tag: %v %v, %d writes", changed, err, f.sets)
	}

	// Old keywords are replaced; a comment the user wrote stays.
	f = newFake()
	old, _ := EncodePlist([]string{"db"})
	mine, _ := EncodePlist("my work database")
	f.m[bundle+"|"+KeywordsAttr] = old
	f.m[bundle+"|"+CommentAttr] = mine
	changed, err = Tag(f, bundle)
	if err != nil || !changed || f.sets != 1 {
		t.Fatalf("update: %v %v, %d writes", changed, err, f.sets)
	}
	if got := decodePlist(t, f.m[bundle+"|"+CommentAttr]); got != "my work database" {
		t.Fatalf("user comment replaced: %q", got)
	}
}

func TestTagErrors(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name        string
		get, set    string
		wantChanged bool
	}{
		{"read keywords", KeywordsAttr, "", false},
		{"write keywords", "", KeywordsAttr, false},
		{"read comment", CommentAttr, "", true},
		{"write comment", "", CommentAttr, true},
	} {
		f := newFake()
		if tc.get != "" {
			f.getErr[tc.get] = boom
		}
		if tc.set != "" {
			f.setErr[tc.set] = boom
		}
		changed, err := Tag(f, bundle)
		if !errors.Is(err, boom) || changed != tc.wantChanged {
			t.Errorf("%s: %v %v", tc.name, changed, err)
		}
	}
}

func TestEnsure(t *testing.T) {
	f := newFake()
	var reindexed []string
	re := func(b string) error { reindexed = append(reindexed, b); return nil }
	if err := ensure("/usr/local/bin/lontar", f, re); err != nil || f.sets != 0 {
		t.Fatalf("outside a bundle: %v, %d writes", err, f.sets)
	}
	exe := bundle + "/Contents/MacOS/lontar"
	if err := ensure(exe, f, re); err != nil {
		t.Fatal(err)
	}
	if err := ensure(exe, f, re); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reindexed, []string{bundle}) {
		t.Fatalf("reindexed %q, want once", reindexed)
	}

	// Errors are returned; a missing reindex is fine.
	f = newFake()
	f.setErr[KeywordsAttr] = errDenied
	if err := ensure(exe, f, re); !errors.Is(err, errDenied) {
		t.Fatalf("got %v", err)
	}
	if err := ensure(exe, newFake(), nil); err != nil {
		t.Fatal(err)
	}
	if err := ensure(exe, newFake(), func(string) error { return errors.New("no mdimport") }); err == nil {
		t.Fatal("reindex error dropped")
	}
}

func TestEnsureUnsupported(t *testing.T) {
	if supported {
		t.Skip("macOS writes real attributes")
	}
	if err := Ensure(bundle + "/Contents/MacOS/lontar"); err != nil {
		t.Fatal(err)
	}
	if _, err := (systemAttrs{}).Get(bundle, KeywordsAttr); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatal(err)
	}
	if err := (systemAttrs{}).Set(bundle, KeywordsAttr, nil); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatal(err)
	}
	if err := reindex(bundle); err != nil {
		t.Fatal(err)
	}
}

var errDenied = errors.New("permission denied")
