package services

import (
	"context"
	"sync"
	"time"

	"lontar/internal/query"
	"lontar/internal/sqlfile"
)

// EventSQLRunProgress is the progress event of SQLRunService.RunFile.
const EventSQLRunProgress = "sqlfile:progress"

// SQLRunProgress is the payload of "sqlfile:progress" events. Phase is
// "scan" (safe mode reads the file first) or "run".
type SQLRunProgress struct {
	JobID string `json:"jobId"`
	Phase string `json:"phase"`
	sqlfile.Progress
}

// ConfirmWrites is how many write statements a NeedsConfirm result lists.
const ConfirmWrites = 20

// SQLRunResult is the outcome of RunFile: either NeedsConfirm (nothing
// ran) or Result.
type SQLRunResult struct {
	// NeedsConfirm: the connection is in "confirm" safe mode and the file
	// has writes; call again with confirmed. Writes counts them and
	// WriteStatements lists the first ConfirmWrites.
	NeedsConfirm    bool                    `json:"needsConfirm"`
	Writes          int64                   `json:"writes"`
	WriteStatements []sqlfile.StatementInfo `json:"writeStatements"`
	Result          *sqlfile.Result         `json:"result"`
}

// SQLRunService runs SQL files (dumps) streaming, on one connection of a
// session (see package sqlfile). Safe mode is enforced here: on a
// read-only connection a file with any write is refused before anything
// runs; on a confirm connection the writes are listed for the user first.
type SQLRunService struct {
	sessions Sessions
	dialogs  Dialogs
	events   Emitter
	jobs     *query.Runner
}

// NewSQLRunService returns a SQLRunService.
func NewSQLRunService(sessions Sessions, dialogs Dialogs, events Emitter) *SQLRunService {
	return &SQLRunService{sessions: sessions, dialogs: dialogs, events: events, jobs: newJobRunner()}
}

// PickAndInspect asks for a SQL file and inspects it (nil when the dialog
// was cancelled).
func (s *SQLRunService) PickAndInspect(ctx context.Context, sessionID string) (*sqlfile.Info, error) {
	if s.dialogs == nil {
		return nil, ErrNoDialogs
	}
	path, err := s.dialogs.OpenFile("Run SQL file", sqlFilters)
	if err != nil || path == "" {
		return nil, err
	}
	return s.Inspect(ctx, sessionID, path)
}

// Inspect describes a file before running it: size, dump type and an
// estimated statement count.
func (s *SQLRunService) Inspect(ctx context.Context, sessionID, path string) (*sqlfile.Info, error) {
	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		return nil, err
	}
	return sqlfile.Inspect(ctx, sess.Kind, path)
}

// RunFile runs the file at path. jobID can be passed to Cancel (which
// rolls a single transaction back); progress is sent as
// "sqlfile:progress" events.
func (s *SQLRunService) RunFile(ctx context.Context, jobID, sessionID, path string, opts sqlfile.Options, confirmed bool) (*SQLRunResult, error) {
	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		return nil, err
	}
	if opts.SingleTransaction && opts.ContinueOnError {
		return nil, sqlfile.ErrOptions
	}
	out := &SQLRunResult{WriteStatements: []sqlfile.StatementInfo{}}
	err = s.jobs.Run(ctx, jobID, func(ctx context.Context) error {
		sc, err := sqlfile.CheckSafeMode(ctx, sess.Kind, path, sess.SafeMode, confirmed, ConfirmWrites, s.progress(jobID, "scan"))
		if err != nil {
			return err
		}
		if sc != nil {
			out.NeedsConfirm, out.Writes, out.WriteStatements = true, sc.Writes, sc.First
			return nil
		}
		res, err := sqlfile.Run(ctx, sess.DB, sess.Kind, path, opts, s.progress(jobID, "run"))
		out.Result = res
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Cancel stops a running file; in single transaction mode everything is
// rolled back.
func (s *SQLRunService) Cancel(jobID string) bool { return s.jobs.Cancel(jobID) }

// progress returns a progress callback emitting at most every 100 ms (the
// result carries the final counts).
func (s *SQLRunService) progress(jobID, phase string) sqlfile.ProgressFunc {
	if s.events == nil || jobID == "" {
		return nil
	}
	var mu sync.Mutex
	var last time.Time
	return func(p sqlfile.Progress) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(last) < 100*time.Millisecond {
			return
		}
		last = time.Now()
		s.events.Emit(EventSQLRunProgress, SQLRunProgress{JobID: jobID, Phase: phase, Progress: p})
	}
}
