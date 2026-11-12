package main

import (
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
)

const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100" viewBox="0 0 100 100">
  <rect x="0" y="0" width="50" height="100" fill="#ff0000"/>
  <ellipse cx="75" cy="50" rx="20" ry="10" fill="#0000ff"/>
</svg>`

func writeSVG(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.svg")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseTarget(t *testing.T) {
	for in, want := range map[string]target{
		"16=a.png":      {16, 16, "a.png"},
		"540x380=b.png": {540, 380, "b.png"},
		"svg=c.svg":     {0, 0, "c.svg"},
	} {
		got, err := parseTarget(in)
		if err != nil || got != want {
			t.Errorf("parseTarget(%q) = %+v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"16", "16=", "x=a", "0=a", "16x=a", "9999=a"} {
		if _, err := parseTarget(bad); err == nil {
			t.Errorf("parseTarget(%q) should fail", bad)
		}
	}
}

func TestRender(t *testing.T) {
	in := writeSVG(t, svg)
	for _, size := range []int{16, 1024} {
		img, err := Render(in, size, size)
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != size {
			t.Fatalf("size %d: got %v", size, img.Bounds())
		}
		// Left half red, right half has the blue ellipse, corners of the
		// right half transparent.
		if r, _, _, a := img.At(size/4, size/2).RGBA(); r < 0xf000 || a < 0xf000 {
			t.Errorf("size %d: left half not red", size)
		}
		if _, _, b, _ := img.At(size*3/4, size/2).RGBA(); b < 0xf000 {
			t.Errorf("size %d: ellipse not blue", size)
		}
		if _, _, _, a := img.At(size-1, 0).RGBA(); a != 0 {
			t.Errorf("size %d: corner not transparent", size)
		}
	}
	if _, err := Render(filepath.Join(t.TempDir(), "missing.svg"), 8, 8); err == nil {
		t.Fatal("missing file should fail")
	}
	if _, err := Render(writeSVG(t, `<svg><text>x</text></svg>`), 8, 8); err == nil {
		t.Fatal("unsupported element should fail in strict mode")
	}
}

func TestRun(t *testing.T) {
	in := writeSVG(t, svg)
	dir := t.TempDir()
	out := filepath.Join(dir, "sub", "icon.png")
	cp := filepath.Join(dir, "scalable", "apps", "x.svg")
	if err := run([]string{"-in", in, "32=" + out, "20x10=" + filepath.Join(dir, "wide.png"), "svg=" + cp}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(cp); err != nil || string(b) != svg {
		t.Fatalf("svg copy = %q, %v", b, err)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil || cfg.Width != 32 || cfg.Height != 32 {
		t.Fatalf("png = %+v, %v", cfg, err)
	}

	for _, args := range [][]string{
		{"-bogus"},
		{"16=x.png"},        // no -in
		{"-in", in},         // no targets
		{"-in", in, "nope"}, // bad target
		{"-in", in + ".missing", "8=" + filepath.Join(dir, "m.png")},
		{"-in", in + ".missing", "svg=" + filepath.Join(dir, "m.svg")},
		{"-in", in, "svg=" + filepath.Join(out, "under-a-file.svg")},
		{"-in", in, "8=" + filepath.Join(out, "under-a-file.png")}, // unwritable
	} {
		if err := run(args, io.Discard); err == nil {
			t.Errorf("run(%q) should fail", args)
		}
	}
}
