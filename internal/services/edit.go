package services

import (
	"context"
	"errors"

	"lontar/internal/edits"
	"lontar/internal/store"
)

// ApplyResult is the outcome of EditService.Apply. A change set that was
// refused or rolled back is not a Go error: Failure names the failing
// change so the UI can highlight it (nothing was saved then).
type ApplyResult struct {
	OK      bool               `json:"ok"`
	Result  *edits.Result      `json:"result"`
	Failure *edits.ChangeError `json:"failure"`
}

// EditService previews and commits the table grid's staged changes. The
// frontend sends a structured change set, never SQL (see package edits).
type EditService struct {
	sessions Sessions
	drivers  DriverGetter
}

// NewEditService returns an EditService.
func NewEditService(sessions Sessions, drivers DriverGetter) *EditService {
	return &EditService{sessions: sessions, drivers: drivers}
}

// Preview returns the statements Apply would run, as display SQL with
// literals. The display SQL is for reading (and copying) only; it is never
// executed.
func (s *EditService) Preview(ctx context.Context, sessionID string, cs edits.ChangeSet) ([]edits.PreviewStatement, error) {
	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		return nil, err
	}
	d, err := s.drivers.Get(sess.Kind)
	if err != nil {
		return nil, err
	}
	return edits.Preview(ctx, sess.DB, d, cs)
}

// Apply commits a change set in one transaction (see edits.Apply). It is
// refused on connections in read-only safe mode.
func (s *EditService) Apply(ctx context.Context, sessionID string, cs edits.ChangeSet) (*ApplyResult, error) {
	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		return nil, err
	}
	d, err := s.drivers.Get(sess.Kind)
	if err != nil {
		return nil, err
	}
	res, err := edits.Apply(ctx, sess.DB, d, cs, edits.Options{ReadOnly: sess.SafeMode == store.SafeReadOnly})
	var ce *edits.ChangeError
	switch {
	case errors.As(err, &ce):
		return &ApplyResult{Failure: ce}, nil
	case err != nil:
		return nil, err
	}
	return &ApplyResult{OK: true, Result: res}, nil
}

// ResultSource reports whether a SQL editor result can be edited like a
// table: sql is the statement that produced it and columns the number of
// result columns. Unqualified table names are looked up in the session's
// default schema. A result that cannot be edited is not an error: the
// Source says why.
func (s *EditService) ResultSource(ctx context.Context, sessionID, sql string, columns int) (*edits.Source, error) {
	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		return nil, err
	}
	d, err := s.drivers.Get(sess.Kind)
	if err != nil {
		return nil, err
	}
	schema, err := d.CurrentSchema(ctx, sess.DB)
	if err != nil {
		return nil, err
	}
	return edits.ResolveSource(ctx, sess.DB, d, sess.Kind, sql, schema, columns)
}
