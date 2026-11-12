package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// seedLegacy creates base/dbedit with the files a real config dir holds:
// connections.json, the Linux fallback secrets, history.db with its WAL
// files, settings.json, known_hosts and a nested directory.
func seedLegacy(t *testing.T, base string) string {
	t.Helper()
	old := filepath.Join(base, "dbedit")
	if err := os.MkdirAll(filepath.Join(old, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range legacyFiles {
		if err := os.WriteFile(filepath.Join(old, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(old, 0o700); err != nil {
		t.Fatal(err)
	}
	return old
}

var legacyFiles = map[string]string{
	"connections.json":  `{"version":3,"connections":[]}`,
	"secrets.enc":       "enc",
	"secrets.key":       "0123456789abcdef0123456789abcdef",
	"history.db":        "db",
	"history.db-wal":    "wal",
	"history.db-shm":    "shm",
	"settings.json":     `{"version":1}`,
	"known_hosts":       "host ssh-ed25519 AAAA\n",
	"sub/nested.txt":    "nested",
	"sub/readonly.conf": "ro",
}

type logger struct{ lines []string }

func (l *logger) logf(format string, args ...any) { l.lines = append(l.lines, fmt.Sprintf(format, args...)) }

func (l *logger) has(sub string) bool {
	for _, s := range l.lines {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func checkMigrated(t *testing.T, base string) {
	t.Helper()
	dir := filepath.Join(base, "lontar")
	for name, body := range legacyFiles {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != body {
			t.Fatalf("%s = %q, %v", name, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "dbedit")); !os.IsNotExist(err) {
		t.Fatalf("old directory still exists: %v", err)
	}
}

func TestConfigDirRename(t *testing.T) {
	base := t.TempDir()
	seedLegacy(t, base)
	var l logger
	res, err := ConfigDir(base, "dbedit", "lontar", Options{Logf: l.logf})
	if err != nil || res != Renamed {
		t.Fatalf("ConfigDir = %v, %v", res, err)
	}
	checkMigrated(t, base)
	if !l.has("moved") {
		t.Fatalf("log = %v", l.lines)
	}

	// Idempotent: a second run changes nothing.
	res, err = ConfigDir(base, "dbedit", "lontar", Options{Logf: l.logf})
	if err != nil || res != AlreadyMigrated {
		t.Fatalf("second run = %v, %v", res, err)
	}
	checkMigrated(t, base)
}

func TestConfigDirCopyFallback(t *testing.T) {
	base := t.TempDir()
	old := seedLegacy(t, base)
	if runtime.GOOS != "windows" {
		// Distinct modes, to check they are preserved.
		_ = os.Chmod(filepath.Join(old, "sub", "readonly.conf"), 0o400)
		_ = os.Chmod(filepath.Join(old, "sub"), 0o750)
		_ = os.Symlink("connections.json", filepath.Join(old, "link"))
	}
	var l logger
	crossDevice := func(o, n string) error {
		return &os.LinkError{Op: "rename", Old: o, New: n, Err: syscall.EXDEV}
	}
	res, err := ConfigDir(base, "dbedit", "lontar", Options{Rename: crossDevice, Logf: l.logf})
	if err != nil || res != Copied {
		t.Fatalf("ConfigDir = %v, %v", res, err)
	}
	checkMigrated(t, base)
	if !l.has("copying instead") || !l.has("removed the old directory") {
		t.Fatalf("log = %v", l.lines)
	}
	if runtime.GOOS != "windows" {
		dir := filepath.Join(base, "lontar")
		for path, want := range map[string]os.FileMode{
			"":                  0o700,
			"connections.json":  0o600,
			"secrets.key":       0o600,
			"sub":               0o750,
			"sub/readonly.conf": 0o400,
		} {
			fi, err := os.Stat(filepath.Join(dir, path))
			if err != nil || fi.Mode().Perm() != want {
				t.Errorf("%q mode = %v, want %v (%v)", path, fi.Mode().Perm(), want, err)
			}
		}
		if link, err := os.Readlink(filepath.Join(dir, "link")); err != nil || link != "connections.json" {
			t.Errorf("symlink = %q, %v", link, err)
		}
	}
	// No staging directory is left behind.
	entries, _ := os.ReadDir(base)
	if len(entries) != 1 || entries[0].Name() != "lontar" {
		t.Fatalf("base holds %v", entries)
	}

	// Idempotent.
	if res, err := ConfigDir(base, "dbedit", "lontar", Options{Rename: crossDevice}); err != nil || res != AlreadyMigrated {
		t.Fatalf("second run = %v, %v", res, err)
	}
}

func TestConfigDirBothExist(t *testing.T) {
	base := t.TempDir()
	seedLegacy(t, base)
	newDir := filepath.Join(base, "lontar")
	if err := os.Mkdir(newDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "connections.json"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	var l logger
	renamed := false
	res, err := ConfigDir(base, "dbedit", "lontar", Options{Logf: l.logf, Rename: func(string, string) error { renamed = true; return nil }})
	if err != nil || res != BothExist || renamed {
		t.Fatalf("ConfigDir = %v, %v (renamed %v)", res, err, renamed)
	}
	if !l.has("leaving") {
		t.Fatalf("log = %v", l.lines)
	}
	// Nothing was overwritten or removed.
	if b, _ := os.ReadFile(filepath.Join(newDir, "connections.json")); string(b) != "new" {
		t.Fatalf("new data overwritten: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "dbedit", "connections.json")); string(b) != legacyFiles["connections.json"] {
		t.Fatalf("old data changed: %q", b)
	}
}

func TestConfigDirNothingToDo(t *testing.T) {
	base := t.TempDir()
	if res, err := ConfigDir(base, "dbedit", "lontar", Options{}); err != nil || res != NothingToDo {
		t.Fatalf("ConfigDir = %v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(base, "lontar")); !os.IsNotExist(err) {
		t.Fatal("nothing should be created")
	}
	// The base directory itself may not exist yet (fresh profile).
	if res, err := ConfigDir(filepath.Join(base, "missing"), "dbedit", "lontar", Options{}); err != nil || res != NothingToDo {
		t.Fatalf("ConfigDir = %v, %v", res, err)
	}
	// A plain file with the old name is not moved.
	if err := os.WriteFile(filepath.Join(base, "dbedit"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var l logger
	if res, err := ConfigDir(base, "dbedit", "lontar", Options{Logf: l.logf}); err != nil || res != NothingToDo || !l.has("not a directory") {
		t.Fatalf("ConfigDir = %v, %v, %v", res, err, l.lines)
	}
}

func TestConfigDirCopyFailureKeepsOld(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	base := t.TempDir()
	old := seedLegacy(t, base)
	// An unreadable file makes the copy fail half-way.
	unreadable := filepath.Join(old, "sub", "nested.txt")
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) })
	fail := func(string, string) error { return errors.New("EXDEV") }
	res, err := ConfigDir(base, "dbedit", "lontar", Options{Rename: fail, Logf: func(string, ...any) {}})
	if err == nil || res != NothingToDo {
		t.Fatalf("ConfigDir = %v, %v", res, err)
	}
	// The old directory is intact, and neither the target nor a staging
	// directory exists, so the next start can retry.
	if _, err := os.Stat(filepath.Join(old, "connections.json")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(base)
	if len(entries) != 1 || entries[0].Name() != "dbedit" {
		t.Fatalf("base holds %v", entries)
	}
}

func TestConfigDirRemovesStaleStaging(t *testing.T) {
	base := t.TempDir()
	stale := filepath.Join(base, tempPrefix+"123")
	if err := os.MkdirAll(filepath.Join(stale, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(base, "other")
	if err := os.Mkdir(keep, 0o700); err != nil {
		t.Fatal(err)
	}
	seedLegacy(t, base)
	if res, err := ConfigDir(base, "dbedit", "lontar", Options{Logf: func(string, ...any) {}}); err != nil || res != Renamed {
		t.Fatalf("ConfigDir = %v, %v", res, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale staging directory not removed")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("unrelated directory removed")
	}
}

func TestConfigDirStatError(t *testing.T) {
	// A path component that is a file makes Lstat fail with ENOTDIR, which
	// is not "does not exist".
	base := t.TempDir()
	file := filepath.Join(base, "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("ENOTDIR semantics differ on Windows")
	}
	if _, err := exists(filepath.Join(file, "x")); err == nil {
		t.Fatal("expected an error")
	}
}

func TestResultString(t *testing.T) {
	for r, want := range map[Result]string{
		NothingToDo: "nothing to do", AlreadyMigrated: "already migrated", BothExist: "both directories exist",
		Renamed: "renamed", Copied: "copied", Result(42): "Result(42)",
	} {
		if r.String() != want {
			t.Errorf("%d = %q", int(r), r.String())
		}
	}
}

func TestDefaultOptions(t *testing.T) {
	// The zero Options use os.Rename and log.Printf.
	base := t.TempDir()
	seedLegacy(t, base)
	if res, err := ConfigDir(base, "dbedit", "lontar", Options{}); err != nil || res != Renamed {
		t.Fatalf("ConfigDir = %v, %v", res, err)
	}
}
