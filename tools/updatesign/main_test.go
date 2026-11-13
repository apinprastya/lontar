package main

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"keygen"}, nil, &out, rand.Reader); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("keygen output: %q", out.String())
	}
	pub, priv := lines[1], lines[3]

	dir := t.TempDir()
	sums := filepath.Join(dir, "SHA256SUMS")
	_ = os.WriteFile(sums, []byte("abc  file\n"), 0o644)
	env := func(k string) string {
		if k == "UPDATE_SIGNING_KEY" {
			return priv
		}
		return ""
	}
	out.Reset()
	if err := run([]string{"sign", sums}, env, &out, nil); err != nil {
		t.Fatal(err)
	}
	sig := filepath.Join(dir, "SHA256SUMS.sig")
	_ = os.WriteFile(sig, out.Bytes(), 0o644)

	out.Reset()
	if err := run([]string{"verify", pub, sums, sig}, nil, &out, nil); err != nil || out.String() != "OK\n" {
		t.Fatalf("verify: %v %q", err, out.String())
	}
	_ = os.WriteFile(sums, []byte("tampered\n"), 0o644)
	if err := run([]string{"verify", pub, sums, sig}, nil, &out, nil); err == nil {
		t.Fatal("tampered file verified")
	}
}

func TestErrors(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	noEnv := func(string) string { return "" }
	var out bytes.Buffer
	cases := [][]string{
		nil, {"bogus"}, {"keygen", "x"}, {"sign"}, {"sign", f}, {"verify", "a"},
		{"verify", "bad", f, f}, {"verify", strings.Repeat("A", 43) + "=", filepath.Join(dir, "missing"), f},
		{"verify", strings.Repeat("A", 43) + "=", f, filepath.Join(dir, "missing")},
		{"verify", strings.Repeat("A", 43) + "=", f, f},
	}
	for _, c := range cases {
		if err := run(c, noEnv, &out, rand.Reader); err == nil {
			t.Errorf("run(%q) succeeded", c)
		}
	}
	key := func(string) string { return strings.Repeat("A", 86) + "==" }
	if err := run([]string{"sign", filepath.Join(dir, "missing")}, key, &out, nil); err == nil {
		t.Fatal("missing file signed")
	}
	if err := run([]string{"keygen"}, nil, &out, failReader{}); err == nil {
		t.Fatal("keygen without randomness")
	}
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }
