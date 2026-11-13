package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	ok := map[string]Version{
		"v26.11.1":        {26, 11, 1, ""},
		"26.1.12":         {26, 1, 12, ""},
		"v30.12.3-rc.1":   {30, 12, 3, "rc.1"},
		"v26.11.2-beta":   {26, 11, 2, "beta"},
		"v05.6.1":         {5, 6, 1, ""},
		"v26.10.100-rc.2": {26, 10, 100, "rc.2"},
	}
	for in, want := range ok {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "v1.2.3", "v2026.11.1", "v26.01.1", "v26.0.1", "v26.13.1",
		"v26.11.0", "v26.11.01", "v26.11", "v26.11.1-", "v26.11.1-rc..1",
		"v26.11.1+build", "26.11.1 ", "vv26.11.1", "v26.11.99999999999999999999",
	} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) accepted", in)
		}
	}
}

func TestString(t *testing.T) {
	if s := (Version{5, 6, 1, ""}).String(); s != "05.6.1" {
		t.Errorf("got %q", s)
	}
	if s := (Version{26, 11, 3, "rc.1"}).String(); s != "26.11.3-rc.1" {
		t.Errorf("got %q", s)
	}
}

func TestNext(t *testing.T) {
	nov := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		tags []string
		want string
	}{
		{nil, "26.11.1"},
		{[]string{"v26.10.4", "v25.11.7", "v1.2.3", "junk"}, "26.11.1"},
		{[]string{"v26.11.1", "v26.11.3", "v26.11.2"}, "26.11.4"},
		// A pre-release does not use up its number.
		{[]string{"v26.11.1", "v26.11.2-rc.1"}, "26.11.2"},
	}
	for _, c := range cases {
		if got := Next(nov, c.tags).String(); got != c.want {
			t.Errorf("Next(%v) = %s, want %s", c.tags, got, c.want)
		}
	}
}

func fixedNow() time.Time { return time.Date(2026, 11, 13, 23, 0, 0, 0, time.UTC) }

func runCmd(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(args, strings.NewReader(stdin), &out, &errOut, fixedNow)
	return out.String(), errOut.String(), err
}

func TestRunCheck(t *testing.T) {
	out, warn, err := runCmd(t, "", "check", "v26.11.2")
	if err != nil || out != "26.11.2\n" || warn != "" {
		t.Fatalf("got %q %q %v", out, warn, err)
	}
	// Other month: accepted, with a warning.
	out, warn, err = runCmd(t, "", "check", "v26.10.5")
	if err != nil || out != "26.10.5\n" || !strings.Contains(warn, "not a 2026-11 version") {
		t.Fatalf("got %q %q %v", out, warn, err)
	}
	out, warn, err = runCmd(t, "", "check", "-date", "2026-10-01", "v26.10.5")
	if err != nil || out != "26.10.5\n" || warn != "" {
		t.Fatalf("got %q %q %v", out, warn, err)
	}
	if _, _, err := runCmd(t, "", "check", "v1.2.3"); err == nil {
		t.Fatal("semver accepted")
	}
	if _, _, err := runCmd(t, "", "check"); err == nil {
		t.Fatal("missing VERSION accepted")
	}
}

func TestRunNext(t *testing.T) {
	out, _, err := runCmd(t, "v26.11.1\nv26.11.2\nv26.10.9\n", "next")
	if err != nil || out != "26.11.3\n" {
		t.Fatalf("got %q %v", out, err)
	}
	out, _, err = runCmd(t, "v26.11.1\n", "next", "-date", "2026-12-01")
	if err != nil || out != "26.12.1\n" {
		t.Fatalf("got %q %v", out, err)
	}
	if _, _, err := runCmd(t, "", "next", "v26.11.1"); err == nil {
		t.Fatal("argument accepted")
	}
}

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestRunErrors(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run(nil, nil, &out, &errOut, fixedNow); err == nil {
		t.Error("no command accepted")
	}
	if err := run([]string{"bump"}, nil, &out, &errOut, fixedNow); err == nil {
		t.Error("unknown command accepted")
	}
	if err := run([]string{"next", "-bogus"}, nil, &out, &errOut, fixedNow); err == nil {
		t.Error("unknown flag accepted")
	}
	if err := run([]string{"next", "-date", "13/11/2026"}, nil, &out, &errOut, fixedNow); err == nil {
		t.Error("bad date accepted")
	}
	if err := run([]string{"next"}, failReader{}, &out, &errOut, fixedNow); err == nil {
		t.Error("stdin error ignored")
	}
}
