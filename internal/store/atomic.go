package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// File and directory permissions for everything Lontar writes to its config
// directory. On Windows only the read-only bit is meaningful; the per-user
// %AppData% ACLs provide the equivalent protection.
const (
	FilePerm os.FileMode = 0o600
	DirPerm  os.FileMode = 0o700
)

// Hooks replaced by tests to simulate I/O failures.
var (
	osRename    = os.Rename
	osWriteFile = func(f *os.File, data []byte) error { _, err := f.Write(data); return err }
)

// ensureDir creates dir with DirPerm, tightening the mode if it already
// exists.
func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, DirPerm); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.Chmod(dir, DirPerm); err != nil {
		return fmt.Errorf("secure config directory: %w", err)
	}
	return nil
}

// WriteFileAtomic writes data to path atomically with FilePerm, creating
// the directory with DirPerm (see writeFileAtomic). Used for other files in
// the config directory (settings.json).
func WriteFileAtomic(path string, data []byte) error { return writeFileAtomic(path, data) }

// writeFileAtomic writes data to path via a temporary file in the same
// directory followed by a rename, so readers see either the old or the new
// content, never a partial file. The file gets FilePerm.
func writeFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err := ensureDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(FilePerm); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err = osWriteFile(tmp, data); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err = osRename(tmpName, path); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}
