package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"lontar/internal/export"
	"lontar/internal/importer"
	"lontar/internal/query"
	"lontar/internal/settings"
	"lontar/internal/store"
	"lontar/internal/values"
)

// Event names sent to the frontend.
const (
	EventExportProgress = "export:progress"
	EventImportProgress = "import:progress"
)

// JobProgress is the payload of the progress events.
type JobProgress struct {
	JobID string `json:"jobId"`
	Rows  int64  `json:"rows"`
}

// Emitter sends events to the frontend (Wails' app.Event.Emit).
type Emitter interface {
	Emit(name string, data any)
}

// EmitterFunc adapts a function to Emitter.
type EmitterFunc func(name string, data any)

// Emit implements Emitter.
func (f EmitterFunc) Emit(name string, data any) { f(name, data) }

// FileFilter restricts a file dialog ("CSV files", "*.csv;*.tsv").
type FileFilter struct {
	DisplayName string `json:"displayName"`
	Pattern     string `json:"pattern"`
}

// Dialogs shows native file dialogs. Both return "" when cancelled.
type Dialogs interface {
	OpenFile(title string, filters []FileFilter) (string, error)
	SaveFile(title, filename string, filters []FileFilter) (string, error)
}

// DialogService opens native file pickers for the frontend (SSH keys, TLS
// certificates, export targets, import files). In Wails server mode there
// is no native dialog; the UI then lets the user type a path.
type DialogService struct {
	dialogs Dialogs
}

// NewDialogService returns a DialogService.
func NewDialogService(d Dialogs) *DialogService { return &DialogService{dialogs: d} }

// ErrNoDialogs is returned when no native dialogs are available.
var ErrNoDialogs = errors.New("file dialogs are not available; type the path instead")

// OpenFile asks for an existing file and returns its path ("" if cancelled).
func (s *DialogService) OpenFile(title string, filters []FileFilter) (string, error) {
	if s.dialogs == nil {
		return "", ErrNoDialogs
	}
	return s.dialogs.OpenFile(title, filters)
}

// SaveFile asks for a file to write and returns its path ("" if cancelled).
func (s *DialogService) SaveFile(title, filename string, filters []FileFilter) (string, error) {
	if s.dialogs == nil {
		return "", ErrNoDialogs
	}
	return s.dialogs.SaveFile(title, filename, filters)
}

// jobs runs cancellable long jobs keyed by a frontend-chosen ID.
func newJobRunner() *query.Runner { return &query.Runner{Timeout: -1} }

// progress returns a throttled progress callback emitting name.
func progress(em Emitter, name, jobID string) func(int64) {
	if em == nil || jobID == "" {
		return nil
	}
	var mu sync.Mutex
	var last time.Time
	return func(n int64) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(last) < 100*time.Millisecond {
			return
		}
		last = time.Now()
		em.Emit(name, JobProgress{JobID: jobID, Rows: n})
	}
}

// ---- Export ----------------------------------------------------------------

// Export sources.
const (
	SourceTable = "table"
	SourceQuery = "query"
	SourceRows  = "rows"
)

// ExportRequest describes an export.
type ExportRequest struct {
	JobID     string         `json:"jobId"`
	SessionID string         `json:"sessionId"`
	Path      string         `json:"path"`
	Options   export.Options `json:"options"`
	// Source is "table" (Table: every row with the tab's filters and
	// sort), "query" (SQL run again; read-only statements only) or "rows"
	// (Columns/Rows already loaded in the UI).
	Source  string          `json:"source"`
	Table   query.Request   `json:"table"`
	SQL     string          `json:"sql"`
	Columns []export.Column `json:"columns"`
	Rows    [][]values.Cell `json:"rows"`
}

// ExportResult reports a finished export.
type ExportResult struct {
	Rows int64  `json:"rows"`
	Path string `json:"path"`
}

// ExportService writes CSV, JSON and SQL INSERT files (see package
// export). Progress is sent as "export:progress" events; Cancel stops a
// running export and removes its temporary file.
type ExportService struct {
	sessions Sessions
	drivers  DriverGetter
	events   Emitter
	jobs     *query.Runner
}

// NewExportService returns an ExportService.
func NewExportService(sessions Sessions, drivers DriverGetter, events Emitter) *ExportService {
	return &ExportService{sessions: sessions, drivers: drivers, events: events, jobs: newJobRunner()}
}

// Export runs an export to req.Path (written atomically).
func (s *ExportService) Export(ctx context.Context, req ExportRequest) (*ExportResult, error) {
	db, d, err := resolve(s.sessions, s.drivers, req.SessionID)
	if err != nil {
		return nil, err
	}
	opts := req.Options
	prog := progress(s.events, EventExportProgress, req.JobID)
	var n int64
	err = s.jobs.Run(ctx, req.JobID, func(ctx context.Context) error {
		switch req.Source {
		case SourceTable:
			if opts.Format == export.FormatSQL {
				if err := s.tableSQLOptions(ctx, req, &opts); err != nil {
					return err
				}
			}
		case SourceQuery:
			if _, err := export.CheckRerunnable(d.Kind(), req.SQL); err != nil {
				return err
			}
		case SourceRows:
		default:
			return fmt.Errorf("%w: unknown source %q", export.ErrOptions, req.Source)
		}
		return export.ToFile(req.Path, func(w io.Writer) error {
			var err error
			switch req.Source {
			case SourceTable:
				n, err = export.Table(ctx, db, d, req.Table, w, opts, prog)
			case SourceQuery:
				n, err = export.Query(ctx, db, d, req.SQL, w, opts, prog)
			default:
				n, err = export.Write(ctx, &export.CellSource{Cols: req.Columns, Rows: req.Rows}, w, d.Kind(), d, opts, prog)
			}
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	if s.events != nil && req.JobID != "" {
		s.events.Emit(EventExportProgress, JobProgress{JobID: req.JobID, Rows: n})
	}
	return &ExportResult{Rows: n, Path: req.Path}, nil
}

// tableSQLOptions fills the SQL INSERT options of a table export: the
// DDL (when asked), OVERRIDING SYSTEM VALUE for PostgreSQL identity
// ALWAYS columns, and generated columns to leave out.
func (s *ExportService) tableSQLOptions(ctx context.Context, req ExportRequest, opts *export.Options) error {
	db, d, err := resolve(s.sessions, s.drivers, req.SessionID)
	if err != nil {
		return err
	}
	if opts.SQL.Table == "" {
		opts.SQL.Table = req.Table.Table
	}
	info, err := d.TableInfo(ctx, db, req.Table.Schema, req.Table.Table)
	if err != nil {
		return err
	}
	for _, c := range info.Columns {
		if c.Generated != "" {
			opts.SQL.SkipColumns = append(opts.SQL.SkipColumns, c.Name)
		}
		if c.Identity == "ALWAYS" {
			opts.SQL.OverridingSystemValue = true
		}
	}
	if opts.SQL.CreateTable {
		ddl, err := d.TableDDL(ctx, db, req.Table.Schema, req.Table.Table)
		if err != nil {
			return err
		}
		opts.SQL.DDL = ddl
	}
	return nil
}

// Cancel stops a running export; its temporary file is removed.
func (s *ExportService) Cancel(jobID string) bool { return s.jobs.Cancel(jobID) }

// CheckQuery reports whether sql can be run again for "export all rows"
// (a single read-only statement that returns rows); the error says why
// not.
func (s *ExportService) CheckQuery(sessionID, sql string) error {
	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		return err
	}
	_, err = export.CheckRerunnable(sess.Kind, sql)
	return err
}

// SaveText writes text to path atomically (the grid selection, rendered
// as CSV/TSV by the frontend).
func (s *ExportService) SaveText(path, text string) error {
	return export.ToFile(path, func(w io.Writer) error {
		_, err := w.Write([]byte(text))
		return err
	})
}

// ---- Import ----------------------------------------------------------------

// ImportResult is the outcome of ImportService.Import. A failed import is
// not a Go error when it names a line: Failure then says where (nothing was
// imported).
type ImportResult struct {
	OK   bool  `json:"ok"`
	Rows int64 `json:"rows"`
	// NeedsConfirm: the connection is in "confirm" safe mode and the
	// import was not confirmed; nothing ran.
	NeedsConfirm bool               `json:"needsConfirm"`
	Failure      *importer.RowError `json:"failure"`
}

// ImportService loads CSV files into tables (see package importer).
// Progress is sent as "import:progress" events; Cancel rolls back.
type ImportService struct {
	sessions Sessions
	drivers  DriverGetter
	events   Emitter
	jobs     *query.Runner
}

// NewImportService returns an ImportService.
func NewImportService(sessions Sessions, drivers DriverGetter, events Emitter) *ImportService {
	return &ImportService{sessions: sessions, drivers: drivers, events: events, jobs: newJobRunner()}
}

// Preview reads the first rows of path and suggests a column mapping.
func (s *ImportService) Preview(ctx context.Context, sessionID, path string, opts importer.Options, schema, table string) (*importer.Preview, error) {
	db, d, err := resolve(s.sessions, s.drivers, sessionID)
	if err != nil {
		return nil, err
	}
	return importer.PreviewFile(ctx, db, d, path, opts, schema, table)
}

// Import runs an import in one transaction. Read-only safe mode refuses
// it; confirm mode needs confirmed.
func (s *ImportService) Import(ctx context.Context, jobID, sessionID string, req importer.Request, confirmed bool) (*ImportResult, error) {
	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		return nil, err
	}
	d, err := s.drivers.Get(sess.Kind)
	if err != nil {
		return nil, err
	}
	if sess.SafeMode == store.SafeConfirm && !confirmed {
		return &ImportResult{NeedsConfirm: true}, nil
	}
	var res *importer.Result
	err = s.jobs.Run(ctx, jobID, func(ctx context.Context) error {
		var err error
		res, err = importer.Run(ctx, sess.DB, d, req, importer.RunOptions{ReadOnly: sess.SafeMode == store.SafeReadOnly},
			progress(s.events, EventImportProgress, jobID))
		return err
	})
	var re *importer.RowError
	switch {
	case errors.As(err, &re):
		return &ImportResult{Failure: re}, nil
	case err != nil:
		return nil, err
	}
	return &ImportResult{OK: true, Rows: res.Rows}, nil
}

// Cancel stops a running import; everything is rolled back.
func (s *ImportService) Cancel(jobID string) bool { return s.jobs.Cancel(jobID) }

// ---- Settings ----------------------------------------------------------------

// SettingsState is what SettingsService.Get returns: the settings and, when
// settings.json could not be read, why (the defaults are used then).
type SettingsState struct {
	Settings settings.Settings `json:"settings"`
	Error    string            `json:"error"`
}

// SettingsService reads and writes the application settings.
type SettingsService struct {
	store *settings.Store
}

// NewSettingsService returns a SettingsService.
func NewSettingsService(st *settings.Store) *SettingsService { return &SettingsService{store: st} }

// Get returns the settings (the defaults if the file is missing or broken).
func (s *SettingsService) Get() SettingsState {
	v, err := s.store.Load()
	if err != nil {
		return SettingsState{Settings: v, Error: err.Error()}
	}
	return SettingsState{Settings: v}
}

// Defaults returns the default settings.
func (s *SettingsService) Defaults() settings.Settings { return settings.Defaults() }

// Save validates (clamps) and stores v, and returns what was stored.
func (s *SettingsService) Save(v settings.Settings) (settings.Settings, error) {
	return s.store.Save(v)
}
