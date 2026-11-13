// Command calver checks and computes Lontar's calendar versions.
//
// Releases are numbered YY.MM.MICRO (tagged vYY.MM.MICRO): the two-digit year,
// the month without a leading zero (1-12), and a counter that starts at 1 for
// each month, e.g. v26.11.1, v26.11.2, v26.12.1. A pre-release appends
// -LABEL[.LABEL...] (e.g. v26.11.3-rc.1); it does not use up its number, so
// the final v26.11.3 follows it.
//
//	calver check [-date YYYY-MM-DD] VERSION   validate VERSION (leading "v"
//	    optional) and print it without "v"; warns on stderr when its
//	    YY.MM is not the month of -date (default: today, UTC)
//	calver next  [-date YYYY-MM-DD]           read existing tags from stdin
//	    (one per line, others ignored) and print the next version for the
//	    month of -date
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, time.Now); err != nil {
		fmt.Fprintln(os.Stderr, "calver:", err)
		os.Exit(1)
	}
}

// Version is a parsed YY.MM.MICRO[-PRE] version.
type Version struct {
	Year, Month, Micro int
	Pre                string
}

var pattern = regexp.MustCompile(`^v?([0-9]{2})\.([1-9]|1[0-2])\.([1-9][0-9]*)(?:-([0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*))?$`)

// Parse accepts "YY.MM.MICRO[-PRE]" with an optional leading "v".
func Parse(s string) (Version, error) {
	m := pattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("%q is not a calendar version: want vYY.MM.MICRO[-PRE], month 1-12 without a leading zero, MICRO from 1 (e.g. v26.11.1)", s)
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	mi, err := strconv.Atoi(m[3])
	if err != nil {
		return Version{}, fmt.Errorf("%q: MICRO out of range", s)
	}
	return Version{Year: y, Month: mo, Micro: mi, Pre: m[4]}, nil
}

// String formats v without the leading "v".
func (v Version) String() string {
	s := fmt.Sprintf("%02d.%d.%d", v.Year, v.Month, v.Micro)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Next returns the first unreleased version of now's month: one past the
// highest final (non pre-release) MICRO among tags for that month, or 1.
// Tags that are not calendar versions are ignored.
func Next(now time.Time, tags []string) Version {
	v := Version{Year: now.Year() % 100, Month: int(now.Month()), Micro: 1}
	for _, t := range tags {
		p, err := Parse(t)
		if err != nil || p.Pre != "" || p.Year != v.Year || p.Month != v.Month {
			continue
		}
		if p.Micro >= v.Micro {
			v.Micro = p.Micro + 1
		}
	}
	return v
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, now func() time.Time) error {
	if len(args) == 0 {
		return errors.New("usage: calver check|next [-date YYYY-MM-DD] [VERSION]")
	}
	fs := flag.NewFlagSet("calver "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	date := fs.String("date", "", "reference date YYYY-MM-DD (default: today, UTC)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	ref := now().UTC()
	if *date != "" {
		d, err := time.Parse(time.DateOnly, *date)
		if err != nil {
			return fmt.Errorf("-date: %w", err)
		}
		ref = d
	}
	switch args[0] {
	case "check":
		if fs.NArg() != 1 {
			return errors.New("check: exactly one VERSION is required")
		}
		v, err := Parse(fs.Arg(0))
		if err != nil {
			return err
		}
		if v.Year != ref.Year()%100 || v.Month != int(ref.Month()) {
			fmt.Fprintf(stderr, "calver: warning: %s is not a %s version\n", v, ref.Format("2006-01"))
		}
		_, err = fmt.Fprintln(stdout, v)
		return err
	case "next":
		if fs.NArg() != 0 {
			return errors.New("next: tags are read from stdin, not arguments")
		}
		var tags []string
		sc := bufio.NewScanner(stdin)
		for sc.Scan() {
			tags = append(tags, sc.Text())
		}
		if err := sc.Err(); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, Next(ref, tags))
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
