package query

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultTimeout bounds a browse query (page or count).
const DefaultTimeout = 60 * time.Second

// ErrCanceled is returned when a request was cancelled with Cancel.
var ErrCanceled = errors.New("query cancelled")

// ErrDuplicateRequest is returned when a request ID is already running.
var ErrDuplicateRequest = errors.New("a request with this ID is already running")

// Runner runs cancellable queries with a timeout. Each call is registered
// under a caller-chosen request ID so the frontend can cancel it. It is
// safe for concurrent use.
//
// Cancelling the context stops the query: pgx sends a cancel request to
// the server; go-sql-driver/mysql and modernc SQLite abort the statement on
// the client side (MySQL closes the connection).
type Runner struct {
	// Timeout bounds each query; zero means DefaultTimeout and a negative
	// value means no timeout (the SQL editor: only Cancel stops a query).
	Timeout time.Duration

	mu      sync.Mutex
	cancels map[string]context.CancelCauseFunc
}

// NewRunner returns a Runner with the default timeout.
func NewRunner() *Runner { return &Runner{} }

func (r *Runner) timeout() time.Duration {
	if r.Timeout != 0 {
		return r.Timeout
	}
	return DefaultTimeout
}

// Run calls fn with a context derived from ctx that times out after the
// runner's timeout and is cancelled by Cancel(requestID). An empty
// requestID makes the call non-cancellable (but still bounded).
func (r *Runner) Run(ctx context.Context, requestID string, fn func(context.Context) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	if requestID != "" {
		r.mu.Lock()
		if r.cancels == nil {
			r.cancels = map[string]context.CancelCauseFunc{}
		}
		if _, dup := r.cancels[requestID]; dup {
			r.mu.Unlock()
			return ErrDuplicateRequest
		}
		r.cancels[requestID] = cancel
		r.mu.Unlock()
		defer func() {
			r.mu.Lock()
			delete(r.cancels, requestID)
			r.mu.Unlock()
		}()
	}
	tctx, tcancel := ctx, context.CancelFunc(func() {})
	if r.timeout() > 0 {
		tctx, tcancel = context.WithTimeout(ctx, r.timeout())
	}
	defer tcancel()

	err := fn(tctx)
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(context.Cause(ctx), ErrCanceled):
		return ErrCanceled
	case errors.Is(tctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		return fmt.Errorf("query timed out after %s: %w", r.timeout(), err)
	}
	return err
}

// Cancel cancels the running request; it reports whether one was found.
func (r *Runner) Cancel(requestID string) bool {
	r.mu.Lock()
	cancel, ok := r.cancels[requestID]
	r.mu.Unlock()
	if ok {
		cancel(ErrCanceled)
	}
	return ok
}

// Running returns the number of registered requests (for tests).
func (r *Runner) Running() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cancels)
}
