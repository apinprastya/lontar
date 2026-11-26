package services

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"lontar/internal/conn"
	"lontar/internal/ddl"
	"lontar/internal/driver"
	"lontar/internal/history"
	"lontar/internal/store"
)

// StructurePreview is the outcome of StructureEditService.Preview: the
// statements Apply would run, or why the change set is refused (Failure
// names the change and field to highlight).
type StructurePreview struct {
	Statements []ddl.Statement  `json:"statements"`
	Failure    *ddl.ChangeError `json:"failure"`
}

// StructureApplyResult is the outcome of an Apply or DropTable. A refused
// or failed change is not a Go error: Failure describes it.
type StructureApplyResult struct {
	OK      bool             `json:"ok"`
	Result  *ddl.Result      `json:"result"`
	Failure *ddl.ChangeError `json:"failure"`
}

// StructureEditService changes table structure from the Structure view.
// The frontend sends a structured ddl.TableChange, never DDL (see package
// ddl). Applied statements are recorded in the query history.
type StructureEditService struct {
	sessions Sessions
	drivers  DriverGetter
	history  *history.Store
}

// NewStructureEditService returns a StructureEditService; hist may be nil.
func NewStructureEditService(sessions Sessions, drivers DriverGetter, hist *history.Store) *StructureEditService {
	return &StructureEditService{sessions: sessions, drivers: drivers, history: hist}
}

func (s *StructureEditService) session(sessionID string) (*conn.Session, driver.Driver, error) {
	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		return nil, nil, err
	}
	d, err := s.drivers.Get(sess.Kind)
	if err != nil {
		return nil, nil, err
	}
	return sess, d, nil
}

func options(sess *conn.Session) ddl.Options {
	return ddl.Options{ServerVersion: sess.ServerVersion, ReadOnly: sess.SafeMode == store.SafeReadOnly}
}

// Capabilities returns what the session's engine can change.
func (s *StructureEditService) Capabilities(sessionID string) (*ddl.Caps, error) {
	sess, _, err := s.session(sessionID)
	if err != nil {
		return nil, err
	}
	c := ddl.Capabilities(sess.Kind, sess.ServerVersion)
	return &c, nil
}

// failure splits a ddl error into a structured failure or a Go error.
func failure(err error) (*ddl.ChangeError, error) {
	var ce *ddl.ChangeError
	if errors.As(err, &ce) {
		return ce, nil
	}
	return nil, err
}

// Preview returns the statements Apply would run (exactly: DDL has no
// bound parameters).
func (s *StructureEditService) Preview(ctx context.Context, sessionID string, tc ddl.TableChange) (*StructurePreview, error) {
	sess, d, err := s.session(sessionID)
	if err != nil {
		return nil, err
	}
	stmts, err := ddl.Preview(ctx, sess.DB, d, tc, options(sess))
	if err != nil {
		ce, err := failure(err)
		if err != nil {
			return nil, err
		}
		return &StructurePreview{Statements: []ddl.Statement{}, Failure: ce}, nil
	}
	return &StructurePreview{Statements: stmts}, nil
}

// Apply applies a change set (refused in read-only safe mode).
func (s *StructureEditService) Apply(ctx context.Context, sessionID string, tc ddl.TableChange) (*StructureApplyResult, error) {
	sess, d, err := s.session(sessionID)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	res, err := ddl.Apply(ctx, sess.DB, d, tc, options(sess))
	return s.result(sess, started, res, err)
}

// DropTablePreview returns the DROP TABLE statement of a table.
func (s *StructureEditService) DropTablePreview(ctx context.Context, sessionID, schema, table string, cascade bool) (*StructurePreview, error) {
	sess, d, err := s.session(sessionID)
	if err != nil {
		return nil, err
	}
	st, err := ddl.DropTableStatement(ctx, sess.DB, d, schema, table, cascade, options(sess))
	if err != nil {
		ce, err := failure(err)
		if err != nil {
			return nil, err
		}
		return &StructurePreview{Statements: []ddl.Statement{}, Failure: ce}, nil
	}
	return &StructurePreview{Statements: []ddl.Statement{st}}, nil
}

// DropTable drops a table (cascade: PostgreSQL CASCADE).
func (s *StructureEditService) DropTable(ctx context.Context, sessionID, schema, table string, cascade bool) (*StructureApplyResult, error) {
	sess, d, err := s.session(sessionID)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	res, err := ddl.DropTable(ctx, sess.DB, d, schema, table, cascade, options(sess))
	return s.result(sess, started, res, err)
}

func (s *StructureEditService) result(sess *conn.Session, started time.Time, res *ddl.Result, err error) (*StructureApplyResult, error) {
	if err != nil {
		ce, err := failure(err)
		if err != nil {
			return nil, err
		}
		if ce.Kind == ddl.FailDatabase {
			s.record(sess, started, []string{ce.SQL}, ce.Message)
		}
		return &StructureApplyResult{Failure: ce}, nil
	}
	sqls := make([]string, len(res.Statements))
	for i, st := range res.Statements {
		sqls[i] = st.SQL
	}
	s.record(sess, started, sqls, "")
	return &StructureApplyResult{OK: true, Result: res}, nil
}

// record adds applied (or failed) DDL to the query history.
func (s *StructureEditService) record(sess *conn.Session, started time.Time, sqls []string, errMsg string) {
	if s.history == nil {
		return
	}
	e := history.Entry{
		ConnectionID: sess.ConnectionID, Database: sess.Database, SQL: strings.Join(sqls, ";\n") + ";",
		StartedAt: started, DurationMs: float64(time.Since(started).Microseconds()) / 1000,
		IsError: errMsg != "", Error: errMsg, Source: history.SourceStructure,
	}
	if _, err := s.history.Add(context.Background(), e); err != nil {
		log.Printf("query history: %v", err)
	}
}
