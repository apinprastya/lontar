package services

import (
	"context"
	"database/sql"

	"lontar/internal/conn"
	"lontar/internal/driver"
)

// Sessions is the subset of *conn.Manager used by SchemaService and
// BrowseService.
type Sessions interface {
	Get(sessionID string) (*conn.Session, error)
	UseDatabase(ctx context.Context, sessionID, database string) (*conn.Session, error)
}

// DriverGetter returns the driver for a kind (*driver.Registry).
type DriverGetter interface {
	Get(kind driver.Kind) (driver.Driver, error)
}

// Location is the database and default schema a session is browsing.
type Location struct {
	Database string `json:"database"`
	Schema   string `json:"schema"`
}

// SchemaService exposes database metadata (databases, schemas, tables,
// columns) and database switching to the frontend.
type SchemaService struct {
	sessions Sessions
	drivers  DriverGetter
}

// NewSchemaService returns a SchemaService.
func NewSchemaService(sessions Sessions, drivers DriverGetter) *SchemaService {
	return &SchemaService{sessions: sessions, drivers: drivers}
}

// resolve returns the session's pool and driver.
func resolve(sessions Sessions, drivers DriverGetter, sessionID string) (*sql.DB, driver.Driver, error) {
	s, err := sessions.Get(sessionID)
	if err != nil {
		return nil, nil, err
	}
	d, err := drivers.Get(s.Kind)
	if err != nil {
		return nil, nil, err
	}
	return s.DB, d, nil
}

func (s *SchemaService) resolve(sessionID string) (*sql.DB, driver.Driver, error) {
	return resolve(s.sessions, s.drivers, sessionID)
}

func location(ctx context.Context, db *sql.DB, d driver.Driver) (Location, error) {
	database, err := d.CurrentDatabase(ctx, db)
	if err != nil {
		return Location{}, err
	}
	schema, err := d.CurrentSchema(ctx, db)
	if err != nil {
		return Location{}, err
	}
	return Location{Database: database, Schema: schema}, nil
}

// Current returns the session's current database and default schema.
func (s *SchemaService) Current(ctx context.Context, sessionID string) (Location, error) {
	db, d, err := s.resolve(sessionID)
	if err != nil {
		return Location{}, err
	}
	return location(ctx, db, d)
}

// ListDatabases lists the server's databases (system ones only with
// includeSystem).
func (s *SchemaService) ListDatabases(ctx context.Context, sessionID string, includeSystem bool) ([]string, error) {
	db, d, err := s.resolve(sessionID)
	if err != nil {
		return nil, err
	}
	return d.ListDatabases(ctx, db, includeSystem)
}

// UseDatabase switches the session to another database (the session ID is
// unchanged) and returns the new location.
func (s *SchemaService) UseDatabase(ctx context.Context, sessionID, database string) (Location, error) {
	if _, err := s.sessions.UseDatabase(ctx, sessionID, database); err != nil {
		return Location{}, err
	}
	return s.Current(ctx, sessionID)
}

// ListSchemas lists the schemas of the current database.
func (s *SchemaService) ListSchemas(ctx context.Context, sessionID string, includeSystem bool) ([]string, error) {
	db, d, err := s.resolve(sessionID)
	if err != nil {
		return nil, err
	}
	return d.ListSchemas(ctx, db, includeSystem)
}

// ListTables lists the tables and views of a schema.
func (s *SchemaService) ListTables(ctx context.Context, sessionID, schema string) ([]driver.Table, error) {
	db, d, err := s.resolve(sessionID)
	if err != nil {
		return nil, err
	}
	return d.ListTables(ctx, db, schema)
}

// Columns lists the columns of a table or view.
func (s *SchemaService) Columns(ctx context.Context, sessionID, schema, table string) ([]driver.Column, error) {
	db, d, err := s.resolve(sessionID)
	if err != nil {
		return nil, err
	}
	return d.Columns(ctx, db, schema, table)
}

// TableInfo returns the structure of a table or view: detailed columns,
// indexes, foreign keys (both directions), checks, triggers and metadata.
func (s *SchemaService) TableInfo(ctx context.Context, sessionID, schema, table string) (*driver.TableDetail, error) {
	db, d, err := s.resolve(sessionID)
	if err != nil {
		return nil, err
	}
	return d.TableInfo(ctx, db, schema, table)
}

// TableDDL returns the CREATE statement(s) of a table or view.
func (s *SchemaService) TableDDL(ctx context.Context, sessionID, schema, table string) (string, error) {
	db, d, err := s.resolve(sessionID)
	if err != nil {
		return "", err
	}
	return d.TableDDL(ctx, db, schema, table)
}
