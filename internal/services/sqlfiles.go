package services

import (
	"strings"

	"lontar/internal/textfile"
)

// sqlFilters are the filters of the SQL file dialogs.
var sqlFilters = []FileFilter{
	{DisplayName: "SQL files (*.sql)", Pattern: "*.sql"},
	{DisplayName: "All files", Pattern: "*"},
}

// SQLFileService opens and saves the SQL editor's script files (see
// package textfile). Failures the UI reacts to are *textfile.FileError
// (codes too_large, not_text, not_found, changed_on_disk).
type SQLFileService struct {
	dialogs Dialogs
}

// NewSQLFileService returns a SQLFileService.
func NewSQLFileService(d Dialogs) *SQLFileService { return &SQLFileService{dialogs: d} }

// OpenSQLFile asks for a file and reads it; nil when the dialog was
// cancelled.
func (s *SQLFileService) OpenSQLFile() (*textfile.File, error) {
	if s.dialogs == nil {
		return nil, ErrNoDialogs
	}
	path, err := s.dialogs.OpenFile("Open SQL file", sqlFilters)
	if err != nil || path == "" {
		return nil, err
	}
	return s.ReadSQLFile(path)
}

// ReadSQLFile reads a file by path (dropped files, reopening).
func (s *SQLFileService) ReadSQLFile(path string) (*textfile.File, error) {
	f, err := textfile.Read(path)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// SaveSQLFile writes text to path with the line ending eol. An empty path
// first asks where to save (Save as, suggesting name + ".sql"; ".sql" is
// added when the chosen name has no extension) and returns nil when that
// dialog is cancelled. With expectedModTime != 0, a file changed on disk
// since then is not overwritten (changed_on_disk); 0 overwrites.
func (s *SQLFileService) SaveSQLFile(path, name, text, eol string, expectedModTime int64) (*textfile.File, error) {
	if path == "" {
		if s.dialogs == nil {
			return nil, ErrNoDialogs
		}
		picked, err := s.dialogs.SaveFile("Save SQL file", suggestedFileName(name), sqlFilters)
		if err != nil || picked == "" {
			return nil, err
		}
		path = textfile.WithSQLExtension(picked)
		// The user chose the target (and confirmed any overwrite).
		expectedModTime = 0
	}
	f, err := textfile.Write(path, text, eol, expectedModTime)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// suggestedFileName turns a tab title into a file name: characters that
// are not allowed in file names become "_", and ".sql" is appended.
func suggestedFileName(title string) string {
	name := strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(title))
	name = strings.Trim(name, ". ")
	if name == "" {
		name = "query"
	}
	if strings.HasSuffix(strings.ToLower(name), ".sql") {
		return name
	}
	return name + ".sql"
}
