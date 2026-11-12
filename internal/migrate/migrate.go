// Package migrate moves the data left by the app's former name, dbedit, to
// Lontar's locations. It runs once at startup, before any store opens.
//
// Only the config directory is moved here. Keychain entries move lazily on
// first read (store.MigratingSecretStore), and the frontend moves its own
// localStorage keys (frontend/src/lib/legacyStorage.ts).
package migrate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Result says what ConfigDir did.
type Result int

const (
	// NothingToDo: there is no legacy directory.
	NothingToDo Result = iota
	// AlreadyMigrated: only the new directory exists.
	AlreadyMigrated
	// BothExist: both directories exist; the legacy one is left untouched.
	BothExist
	// Renamed: the legacy directory was renamed to the new name.
	Renamed
	// Copied: the rename failed (e.g. across devices), so the legacy
	// directory was copied and then removed.
	Copied
)

func (r Result) String() string {
	switch r {
	case NothingToDo:
		return "nothing to do"
	case AlreadyMigrated:
		return "already migrated"
	case BothExist:
		return "both directories exist"
	case Renamed:
		return "renamed"
	case Copied:
		return "copied"
	}
	return fmt.Sprintf("Result(%d)", int(r))
}

// Options are the injectable parts of ConfigDir. Zero values use the real
// implementations.
type Options struct {
	// Rename moves the legacy directory in one step (default os.Rename).
	// Tests inject a failure to exercise the copy fallback.
	Rename func(oldpath, newpath string) error
	// Logf reports what happened (default log.Printf).
	Logf func(format string, args ...any)
}

func (o Options) rename(oldpath, newpath string) error {
	if o.Rename != nil {
		return o.Rename(oldpath, newpath)
	}
	return os.Rename(oldpath, newpath)
}

func (o Options) logf(format string, args ...any) {
	if o.Logf != nil {
		o.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// tempPrefix names the staging directories of an interrupted copy.
const tempPrefix = ".lontar-migrate-"

// ConfigDir moves base/oldName to base/newName when only the old one
// exists. It first tries a rename; if that fails (e.g. the two paths are on
// different devices) it copies the tree into a staging directory next to the
// target, keeping file and directory permissions, renames that into place
// and only then removes the old directory.
//
// It never overwrites: if base/newName exists it does nothing (and logs when
// base/oldName exists as well). Running it again after a migration is a
// no-op. On error the old directory is still complete and base/newName does
// not exist, so the next start tries again.
func ConfigDir(base, oldName, newName string, opts Options) (Result, error) {
	oldDir, newDir := filepath.Join(base, oldName), filepath.Join(base, newName)
	removeStaleStaging(base, opts)

	newExists, err := exists(newDir)
	if err != nil {
		return NothingToDo, err
	}
	oldInfo, err := os.Stat(oldDir)
	oldExists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return NothingToDo, fmt.Errorf("migrate: %w", err)
	}
	switch {
	case newExists && oldExists:
		opts.logf("migrate: both %s and %s exist; using %s and leaving %s untouched", oldDir, newDir, newDir, oldDir)
		return BothExist, nil
	case newExists:
		return AlreadyMigrated, nil
	case !oldExists:
		return NothingToDo, nil
	case !oldInfo.IsDir():
		opts.logf("migrate: %s is not a directory; not moving it", oldDir)
		return NothingToDo, nil
	}

	rerr := opts.rename(oldDir, newDir)
	if rerr == nil {
		opts.logf("migrate: moved %s to %s", oldDir, newDir)
		return Renamed, nil
	}
	opts.logf("migrate: renaming %s to %s failed (%v); copying instead", oldDir, newDir, rerr)

	staging, err := os.MkdirTemp(base, tempPrefix)
	if err != nil {
		return NothingToDo, fmt.Errorf("migrate: %w", err)
	}
	if err := copyTree(oldDir, staging, opts); err != nil {
		_ = os.RemoveAll(staging)
		return NothingToDo, fmt.Errorf("migrate: copy %s: %w", oldDir, err)
	}
	// MkdirTemp creates 0700; give the directory the old one's mode.
	if err := os.Chmod(staging, oldInfo.Mode().Perm()); err != nil {
		_ = os.RemoveAll(staging)
		return NothingToDo, fmt.Errorf("migrate: %w", err)
	}
	if err := os.Rename(staging, newDir); err != nil {
		_ = os.RemoveAll(staging)
		return NothingToDo, fmt.Errorf("migrate: %w", err)
	}
	if err := os.RemoveAll(oldDir); err != nil {
		opts.logf("migrate: copied %s to %s but could not remove the old directory: %v", oldDir, newDir, err)
		return Copied, nil
	}
	opts.logf("migrate: copied %s to %s and removed the old directory", oldDir, newDir)
	return Copied, nil
}

// exists reports whether path exists (without following a final symlink).
func exists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("migrate: %w", err)
}

// removeStaleStaging deletes staging directories left by a copy that was
// interrupted (e.g. the app was killed). They are never the only copy.
func removeStaleStaging(base string, opts Options) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), tempPrefix) {
			p := filepath.Join(base, e.Name())
			if err := os.RemoveAll(p); err != nil {
				opts.logf("migrate: could not remove %s: %v", p, err)
			}
		}
	}
}

// copyTree copies the contents of src into the existing directory dst.
// Regular files and directories keep their permission bits, symlinks are
// recreated, and anything else (sockets, devices) is skipped with a log line.
func copyTree(src, dst string, opts Options) error {
	type dirMode struct {
		path string
		mode fs.FileMode
	}
	var dirs []dirMode
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch mode := info.Mode(); {
		case mode.IsDir():
			// Writable while copying; the real mode is applied afterwards.
			if err := os.Mkdir(target, 0o700); err != nil {
				return err
			}
			dirs = append(dirs, dirMode{target, mode.Perm()})
		case mode.IsRegular():
			return copyFile(path, target, mode.Perm())
		case mode&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			opts.logf("migrate: skipping %s (not a regular file)", path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Deepest first, so a read-only parent never blocks a child.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i].path, dirs[i].mode); err != nil {
			return err
		}
	}
	return nil
}

// copyFile copies src to the new file dst with permission bits perm,
// syncing it to disk before returning.
func copyFile(src, dst string, perm fs.FileMode) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	// OpenFile's mode is reduced by the umask; set it exactly.
	return out.Chmod(perm)
}
