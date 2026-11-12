package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const plist = `<plist version="1.0">
	<dict>
		<key>CFBundleShortVersionString</key>
		<string>0.0.0</string>
		<key>CFBundleVersion</key>
		    <string>0.0.0</string>
		<key>CFBundleName</key>
		<string>lontar</string>
	</dict>
</plist>`

const info = `{
	"fixed": {"file_version": "0.0.0"},
	"info": {"0409": {"ProductVersion": "0.0.0", "CompanyName": "Lontar contributors"}}
}`

func TestPlist(t *testing.T) {
	out, err := Plist([]byte(plist), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Count(s, "<string>1.2.3</string>") != 2 || strings.Contains(s, "0.0.0") {
		t.Fatalf("not stamped:\n%s", s)
	}
	if !strings.Contains(s, "<string>lontar</string>") {
		t.Fatal("other keys must be kept")
	}
	if _, err := Plist([]byte(`<dict><key>CFBundleShortVersionString</key><string>1</string></dict>`), "1.2.3"); err == nil {
		t.Fatal("missing CFBundleVersion must fail")
	}
}

func TestWinInfo(t *testing.T) {
	out, err := WinInfo([]byte(info), "1.2.3-rc.1+abc", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Fixed map[string]string            `json:"fixed"`
		Info  map[string]map[string]string `json:"info"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Fixed["file_version"] != "1.2.3" || doc.Fixed["product_version"] != "1.2.3" {
		t.Fatalf("fixed = %v", doc.Fixed)
	}
	l := doc.Info["0409"]
	if l["ProductVersion"] != "1.2.3-rc.1+abc" || l["FileVersion"] != "1.2.3" || l["CompanyName"] != "Lontar contributors" {
		t.Fatalf("info = %v", l)
	}

	// A missing "fixed" block is created.
	if out, err := WinInfo([]byte(`{"info":{"0409":{}}}`), "1.0.0", "1.0.0"); err != nil || !strings.Contains(string(out), "file_version") {
		t.Fatalf("no fixed: %s, %v", out, err)
	}
	for _, bad := range []string{`nope`, `{"fixed":{}}`, `{"info":{"0409":"x"}}`} {
		if _, err := WinInfo([]byte(bad), "1.0.0", "1.0.0"); err == nil {
			t.Errorf("WinInfo(%s) should fail", bad)
		}
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "Info.plist")
	in := filepath.Join(dir, "info.json")
	out := filepath.Join(dir, "out.json")
	if err := os.WriteFile(p, []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"plist", "-version", "2.0.0-dev", "-num", "2.0.0", p}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "<string>2.0.0</string>") {
		t.Fatalf("plist not stamped: %s", b)
	}
	if err := run([]string{"wininfo", "-version", "2.0.0-dev", "-num", "2.0.0", "-in", in, "-out", out}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); !strings.Contains(string(b), `"2.0.0-dev"`) {
		t.Fatalf("info not stamped: %s", b)
	}

	bad := [][]string{
		nil,
		{"plist", "-bogus"},
		{"plist", "-num", "1.0.0", p}, // no -version
		{"plist", "-version", "1", "-num", "1.0", p},     // bad -num
		{"plist", "-version", "1", "-num", "1.0.0"},      // no file
		{"wininfo", "-version", "1", "-num", "1.0.0"},    // no -in/-out
		{"other", "-version", "1", "-num", "1.0.0"},      // unknown
		{"plist", "-version", "1", "-num", "1.0.0", out}, // not a plist
		{"plist", "-version", "1", "-num", "1.0.0", dir + "/missing"},
	}
	for _, args := range bad {
		if err := run(args, io.Discard); err == nil {
			t.Errorf("run(%q) should fail", args)
		}
	}
}
