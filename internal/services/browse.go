package services

import (
	"context"

	"dbedit/internal/query"
)

// BrowseService pages through tables for the grid. Every call takes a
// frontend-chosen requestID that CancelFetch can cancel; queries are also
// bounded by the runner's timeout and cancelled when the frontend cancels
// the binding call.
type BrowseService struct {
	sessions Sessions
	drivers  DriverGetter
	runner   *query.Runner
}

// NewBrowseService returns a BrowseService.
func NewBrowseService(sessions Sessions, drivers DriverGetter, runner *query.Runner) *BrowseService {
	if runner == nil {
		runner = query.NewRunner()
	}
	return &BrowseService{sessions: sessions, drivers: drivers, runner: runner}
}

// FetchTable returns one page of a table. See query.Request.
func (s *BrowseService) FetchTable(ctx context.Context, sessionID, requestID string, req query.Request) (*query.Page, error) {
	db, d, err := resolve(s.sessions, s.drivers, sessionID)
	if err != nil {
		return nil, err
	}
	var page *query.Page
	err = s.runner.Run(ctx, requestID, func(ctx context.Context) error {
		var err error
		page, err = query.FetchTable(ctx, db, d, req)
		return err
	})
	return page, err
}

// CountRows returns the exact row count for req's filters (Offset, Limit
// and Sort are ignored).
func (s *BrowseService) CountRows(ctx context.Context, sessionID, requestID string, req query.Request) (int64, error) {
	db, d, err := resolve(s.sessions, s.drivers, sessionID)
	if err != nil {
		return 0, err
	}
	var n int64
	err = s.runner.Run(ctx, requestID, func(ctx context.Context) error {
		var err error
		n, err = query.CountRows(ctx, db, d, req)
		return err
	})
	return n, err
}

// CancelFetch cancels a running FetchTable or CountRows call. It reports
// whether the request was still running.
func (s *BrowseService) CancelFetch(requestID string) bool {
	return s.runner.Cancel(requestID)
}
