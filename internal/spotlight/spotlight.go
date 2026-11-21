// Package spotlight makes Lontar.app findable in macOS Spotlight by what it
// does, not only by its name: typing "db", "sql" or "query" should offer it.
//
// Info.plist has no key for search keywords, so Lontar writes Spotlight
// metadata as extended attributes on its own bundle when it starts
// (kMDItemKeywords, and a Finder comment, which Spotlight also searches).
// Attributes on the bundle directory are not part of the code signature.
// Doing it at start-up covers every way of installing: dragged from the
// .dmg, copied by hand or replaced by the self-updater.
package spotlight

import (
	"bytes"
	"fmt"
	"strings"
)

// Extended attribute names Spotlight reads.
const (
	KeywordsAttr = "com.apple.metadata:kMDItemKeywords"
	CommentAttr  = "com.apple.metadata:kMDItemFinderComment"
)

// Keywords Lontar.app is found by, besides its name.
var Keywords = []string{
	"database", "db", "database client", "db client",
	"sql", "sql client", "sql editor", "query",
	"postgresql", "postgres", "psql", "mysql", "mariadb", "sqlite",
}

// Comment is the Finder comment (Get Info → Comments) set when the bundle
// has none. A comment the user wrote is kept.
const Comment = "Database client and SQL editor (db, query) for PostgreSQL, MySQL, MariaDB and SQLite"

// Attrs reads and writes extended attributes. Get returns nil, nil when the
// attribute does not exist.
type Attrs interface {
	Get(path, name string) ([]byte, error)
	Set(path, name string, value []byte) error
}

// BundlePath returns the .app bundle that contains the executable, and false
// when the executable does not run from one (`wails3 dev`, a bare binary).
func BundlePath(executable string) (string, bool) {
	const marker = ".app/Contents/MacOS/"
	i := strings.Index(executable, marker)
	if i <= 0 {
		return "", false
	}
	return executable[:i+len(".app")], true
}

// Tag writes the keywords, and the comment when there is none, to bundle.
// Attributes that already hold the value are left alone, so the bundle is
// only touched once. It reports whether anything was written.
func Tag(a Attrs, bundle string) (bool, error) {
	keywords, err := EncodePlist(Keywords)
	if err != nil {
		return false, err
	}
	comment, err := EncodePlist(Comment)
	if err != nil {
		return false, err
	}
	changed := false
	cur, err := a.Get(bundle, KeywordsAttr)
	if err != nil {
		return false, fmt.Errorf("read Spotlight keywords: %w", err)
	}
	if !bytes.Equal(cur, keywords) {
		if err := a.Set(bundle, KeywordsAttr, keywords); err != nil {
			return false, fmt.Errorf("write Spotlight keywords: %w", err)
		}
		changed = true
	}
	cur, err = a.Get(bundle, CommentAttr)
	if err != nil {
		return changed, fmt.Errorf("read Finder comment: %w", err)
	}
	if len(cur) == 0 {
		if err := a.Set(bundle, CommentAttr, comment); err != nil {
			return changed, fmt.Errorf("write Finder comment: %w", err)
		}
		changed = true
	}
	return changed, nil
}

// Ensure tags the bundle the running executable lives in. It does nothing
// outside macOS or outside a bundle. A read-only or foreign-owned bundle
// (App Translocation, installed by another user) returns the error, which
// callers only log.
func Ensure(executable string) error {
	if !supported {
		return nil
	}
	return ensure(executable, systemAttrs{}, reindex)
}

func ensure(executable string, a Attrs, reindex func(bundle string) error) error {
	bundle, ok := BundlePath(executable)
	if !ok {
		return nil
	}
	changed, err := Tag(a, bundle)
	if err != nil {
		return err
	}
	if changed && reindex != nil {
		// Spotlight picks the change up on its own eventually; asking it
		// to re-import makes the keywords work right away.
		return reindex(bundle)
	}
	return nil
}
