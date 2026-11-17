// Package textfile reads and writes the SQL editor's script files: UTF-8
// text up to MaxSize, with the line ending kept across a round trip and a
// check that the file did not change on disk since it was read.
//
// Errors never include file contents; the structured FileError tells the
// frontend what went wrong.
package textfile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"lontar/internal/export"
)

// MaxSize is the largest file that is opened (10 MiB).
const MaxSize = 10 << 20

// Line endings.
const (
	LF   = "\n"
	CRLF = "\r\n"
)

// File error codes.
const (
	CodeTooLarge      = "too_large"
	CodeNotText       = "not_text"
	CodeNotFound      = "not_found"
	CodeChangedOnDisk = "changed_on_disk"
)

// FileError is a failure the UI reacts to by its Code. It is returned
// unwrapped, so Wails sends its fields to the frontend.
type FileError struct {
	// Kind is always "fileError" (for the frontend).
	Kind    string `json:"kind"`
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (e *FileError) Error() string { return e.Message }

func fileError(code, path, msg string) *FileError {
	return &FileError{Kind: "fileError", Code: code, Path: path, Message: msg}
}

// File is a text file as the editor sees it.
type File struct {
	Path string `json:"path"`
	// Name is the base name (the tab title).
	Name string `json:"name"`
	// Text has "\n" line endings and no byte order mark.
	Text string `json:"text"`
	// ModTime is the modification time in Unix milliseconds.
	ModTime int64 `json:"modTime"`
	// EOL is the file's line ending: "\n" or "\r\n".
	EOL string `json:"eol"`
}

var bom = []byte{0xEF, 0xBB, 0xBF}

// Read reads a UTF-8 text file: a byte order mark is dropped and CRLF line
// endings become "\n" (EOL reports the original).
func Read(path string) (*File, error) {
	fi, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fileError(CodeNotFound, path, fmt.Sprintf("%s was not found", filepath.Base(path)))
	case err != nil:
		return nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	case fi.IsDir():
		return nil, fileError(CodeNotFound, path, fmt.Sprintf("%s is a folder, not a file", filepath.Base(path)))
	case fi.Size() > MaxSize:
		return nil, tooLarge(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if len(data) > MaxSize {
		return nil, tooLarge(path)
	}
	data = bytes.TrimPrefix(data, bom)
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, fileError(CodeNotText, path, fmt.Sprintf("%s is not a UTF-8 text file", filepath.Base(path)))
	}
	text := string(data)
	eol := DetectEOL(text)
	return &File{Path: path, Name: filepath.Base(path), Text: strings.ReplaceAll(text, CRLF, LF), ModTime: fi.ModTime().UnixMilli(), EOL: eol}, nil
}

func tooLarge(path string) *FileError {
	return fileError(CodeTooLarge, path, fmt.Sprintf("%s is larger than %d MiB", filepath.Base(path), MaxSize>>20))
}

// DetectEOL returns CRLF when the first line ending of text is "\r\n",
// otherwise LF.
func DetectEOL(text string) string {
	if i := strings.IndexByte(text, '\n'); i > 0 && text[i-1] == '\r' {
		return CRLF
	}
	return LF
}

// Write writes text to path atomically with the line ending eol ("\r\n";
// anything else means "\n"). With expectedModTime != 0 the write is refused
// (CodeChangedOnDisk) when the file on disk was modified after that time
// (Unix ms); a file that no longer exists is written.
func Write(path, text, eol string, expectedModTime int64) (*File, error) {
	// A replaced file keeps its permissions; a new one gets rw-r--r--.
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		if expectedModTime != 0 && fi.ModTime().UnixMilli() > expectedModTime {
			return nil, fileError(CodeChangedOnDisk, path, fmt.Sprintf("%s was changed on disk since it was opened", filepath.Base(path)))
		}
		mode = fi.Mode().Perm()
	}
	if eol != CRLF {
		eol = LF
	}
	text = strings.ReplaceAll(text, CRLF, LF)
	out := text
	if eol == CRLF {
		out = strings.ReplaceAll(text, LF, CRLF)
	}
	if err := export.ToFile(path, func(w io.Writer) error {
		if f, ok := w.(*os.File); ok {
			// Best effort: the temporary file is created rw-------.
			_ = f.Chmod(mode)
		}
		_, err := io.WriteString(w, out)
		return err
	}); err != nil {
		return nil, fmt.Errorf("save %s: %w", filepath.Base(path), err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("save %s: %w", filepath.Base(path), err)
	}
	return &File{Path: path, Name: filepath.Base(path), Text: text, ModTime: fi.ModTime().UnixMilli(), EOL: eol}, nil
}

// WithSQLExtension adds ".sql" to a path whose base name has no extension.
func WithSQLExtension(path string) string {
	if path == "" || filepath.Ext(path) != "" {
		return path
	}
	return path + ".sql"
}
