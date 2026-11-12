// Command stamp writes the build version into platform metadata files whose
// formats Task cannot template portably.
//
//	stamp plist   -version V -num X.Y.Z FILE          (in place)
//	stamp wininfo -version V -num X.Y.Z -in IN -out OUT
//
// V is the full version (e.g. 1.2.3, 1.2.3-rc.1 or 0.0.0-dev+abc1234) and
// X.Y.Z its numeric core, which is all macOS bundle versions and Windows
// VERSIONINFO accept. plist sets CFBundleShortVersionString and
// CFBundleVersion in an Info.plist; wininfo copies a Wails syso info.json
// (build/windows/info.json) with the version fields replaced.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
)

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "stamp:", err)
		os.Exit(1)
	}
}

var numericVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

func run(args []string, log io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: stamp plist|wininfo [flags]")
	}
	fs := flag.NewFlagSet("stamp "+args[0], flag.ContinueOnError)
	fs.SetOutput(log)
	version := fs.String("version", "", "full version, e.g. 1.2.3-rc.1")
	num := fs.String("num", "", "numeric version X.Y.Z")
	in := fs.String("in", "", "input file (wininfo)")
	out := fs.String("out", "", "output file (wininfo)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *version == "" {
		return errors.New("-version is required")
	}
	if !numericVersion.MatchString(*num) {
		return fmt.Errorf("-num %q is not X.Y.Z", *num)
	}
	switch args[0] {
	case "plist":
		if fs.NArg() != 1 {
			return errors.New("plist: exactly one Info.plist path is required")
		}
		return stampFile(fs.Arg(0), fs.Arg(0), func(b []byte) ([]byte, error) { return Plist(b, *num) })
	case "wininfo":
		if *in == "" || *out == "" {
			return errors.New("wininfo: -in and -out are required")
		}
		return stampFile(*in, *out, func(b []byte) ([]byte, error) { return WinInfo(b, *version, *num) })
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func stampFile(in, out string, fn func([]byte) ([]byte, error)) error {
	b, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	res, err := fn(b)
	if err != nil {
		return fmt.Errorf("%s: %w", in, err)
	}
	return os.WriteFile(out, res, 0o644)
}

// plistKey matches `<key>NAME</key> <string>…</string>`.
func plistKey(name string) *regexp.Regexp {
	return regexp.MustCompile(`(<key>` + regexp.QuoteMeta(name) + `</key>\s*<string>)[^<]*(</string>)`)
}

// Plist sets the bundle version keys of an Info.plist to num. Both keys must
// already exist, so a template change cannot silently drop the version.
func Plist(b []byte, num string) ([]byte, error) {
	for _, k := range []string{"CFBundleShortVersionString", "CFBundleVersion"} {
		re := plistKey(k)
		if !re.Match(b) {
			return nil, fmt.Errorf("key %s not found", k)
		}
		b = re.ReplaceAll(b, []byte("${1}"+num+"${2}"))
	}
	return b, nil
}

// WinInfo returns the Wails/winres info.json with fixed.file_version and
// fixed.product_version set to num, and FileVersion / ProductVersion of every
// language block set to num / version.
func WinInfo(b []byte, version, num string) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	fixed, _ := doc["fixed"].(map[string]any)
	if fixed == nil {
		fixed = map[string]any{}
		doc["fixed"] = fixed
	}
	fixed["file_version"] = num
	fixed["product_version"] = num
	info, _ := doc["info"].(map[string]any)
	if len(info) == 0 {
		return nil, errors.New(`no "info" language blocks`)
	}
	langs := make([]string, 0, len(info))
	for l := range info {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	for _, l := range langs {
		strs, ok := info[l].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("info.%s is not an object", l)
		}
		strs["FileVersion"] = num
		strs["ProductVersion"] = version
	}
	out, err := json.MarshalIndent(doc, "", "\t")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
