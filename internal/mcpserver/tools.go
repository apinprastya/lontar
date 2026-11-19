package mcpserver

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"strings"
	"time"

	"lontar/internal/conn"
	"lontar/internal/driver"
	"lontar/internal/history"
	"lontar/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool arguments. Descriptions become the JSON schema the client sees.

type noArgs struct{}

type connArgs struct {
	Connection string `json:"connection" jsonschema:"connection id (or unique name) from list_connections"`
	Database   string `json:"database,omitempty" jsonschema:"database on the server; defaults to the connection's database. Not for SQLite"`
}

type schemaArgs struct {
	Connection string `json:"connection" jsonschema:"connection id (or unique name) from list_connections"`
	Database   string `json:"database,omitempty" jsonschema:"database on the server; defaults to the connection's database. Not for SQLite"`
	Schema     string `json:"schema,omitempty" jsonschema:"schema; defaults to the current one (PostgreSQL public, MySQL the database, SQLite main)"`
}

type tableArgs struct {
	Connection string `json:"connection" jsonschema:"connection id (or unique name) from list_connections"`
	Database   string `json:"database,omitempty" jsonschema:"database on the server; defaults to the connection's database. Not for SQLite"`
	Schema     string `json:"schema,omitempty" jsonschema:"schema; defaults to the current one"`
	Table      string `json:"table" jsonschema:"table or view name, unquoted, as listed by list_tables"`
}

type ddlArgs struct {
	Connection string `json:"connection" jsonschema:"connection id (or unique name) from list_connections"`
	Database   string `json:"database,omitempty" jsonschema:"database on the server; defaults to the connection's database. Not for SQLite"`
	Schema     string `json:"schema,omitempty" jsonschema:"schema; defaults to the current one"`
	Name       string `json:"name" jsonschema:"name of the table, view or object"`
	Kind       string `json:"kind,omitempty" jsonschema:"table (default; also views) or an object kind from list_objects: function, procedure, aggregate, sequence, enum, composite, domain, trigger, event"`
	Signature  string `json:"signature,omitempty" jsonschema:"argument signature, to pick one overload of a function"`
	Table      string `json:"table,omitempty" jsonschema:"table of a trigger, when several triggers share the name"`
}

type queryArgs struct {
	Connection string `json:"connection" jsonschema:"connection id (or unique name) from list_connections"`
	Database   string `json:"database,omitempty" jsonschema:"database on the server; defaults to the connection's database. Not for SQLite"`
	SQL        string `json:"sql" jsonschema:"one or more SQL statements separated by semicolons, in the engine's dialect"`
	MaxRows    int    `json:"max_rows,omitempty" jsonschema:"rows returned per statement (default 200, at most 1000)"`
}

type explainArgs struct {
	Connection string `json:"connection" jsonschema:"connection id (or unique name) from list_connections"`
	Database   string `json:"database,omitempty" jsonschema:"database on the server; defaults to the connection's database. Not for SQLite"`
	SQL        string `json:"sql" jsonschema:"exactly one statement to explain (it is not run)"`
}

type writeArgs struct {
	Connection string `json:"connection" jsonschema:"connection id (or unique name) from list_connections"`
	Database   string `json:"database,omitempty" jsonschema:"database on the server; defaults to the connection's database. Not for SQLite"`
	SQL        string `json:"sql" jsonschema:"INSERT, UPDATE, DELETE, MERGE or REPLACE statements, separated by semicolons; they run in one transaction after the user approves"`
}

func ptr[T any](v T) *T { return &v }

// readOnlyTool marks a tool that never changes anything.
func readOnlyTool(name, title, desc string) *mcp.Tool {
	return &mcp.Tool{
		Name: name, Title: title, Description: desc,
		Annotations: &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: ptr(false)},
	}
}

func (s *Server) addTools(srv *mcp.Server) {
	mcp.AddTool(srv, readOnlyTool("list_connections", "List connections",
		"Lists the saved database connections the user enabled for AI access: id, name, engine, default database, environment tag, access level (read or write) and safe mode."),
		s.listConnections)
	mcp.AddTool(srv, readOnlyTool("list_databases", "List databases",
		"Lists the databases on a connection's server (system databases excluded)."), s.listDatabases)
	mcp.AddTool(srv, readOnlyTool("list_schemas", "List schemas",
		"Lists the schemas of a database and marks the current one."), s.listSchemas)
	mcp.AddTool(srv, readOnlyTool("list_tables", "List tables",
		"Lists the tables and views of a schema with estimated row counts."), s.listTables)
	mcp.AddTool(srv, readOnlyTool("describe_table", "Describe table",
		"Shows a table's or view's columns (types, nullability, defaults, primary key), indexes, foreign keys in both directions, checks and triggers."),
		s.describeTable)
	mcp.AddTool(srv, readOnlyTool("list_objects", "List objects",
		"Lists a schema's functions, procedures, sequences, types, triggers and events (what the engine has)."), s.listObjects)
	mcp.AddTool(srv, readOnlyTool("get_ddl", "Get DDL",
		"Returns the CREATE statement of a table, view or object."), s.getDDL)
	mcp.AddTool(srv, readOnlyTool("run_query", "Run read-only query",
		"Runs read-only SQL (SELECT, WITH, SHOW, EXPLAIN, …) and returns each statement's rows as CSV; NULL is written as NULL. Every statement runs in a read-only transaction that is rolled back; writes, transaction control and USE are refused."),
		s.runQuery)
	mcp.AddTool(srv, readOnlyTool("explain_query", "Explain query",
		"Shows the query plan of one statement without running it (no ANALYZE)."), s.explainQuery)
	mcp.AddTool(srv, &mcp.Tool{
		Name:  "execute_write",
		Title: "Change data",
		Description: "Runs INSERT, UPDATE, DELETE, MERGE or REPLACE statements in one transaction, only on connections with write access. " +
			"The user sees the exact SQL and must approve it first; DDL and other statements are refused. Returns the rows affected.",
		Annotations: &mcp.ToolAnnotations{Title: "Change data", DestructiveHint: ptr(true), OpenWorldHint: ptr(false)},
	}, s.executeWrite)
}

// text is a successful tool result.
func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// failure is an error tool result that keeps partial output.
func failure(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// timeout bounds a call's database work.
func (s *Server) timeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.opts.QueryTimeout)
}

// readTarget resolves a connection and its read-only session and driver.
func (s *Server) readTarget(ctx context.Context, ref, database string) (store.Connection, *conn.Session, driver.Driver, error) {
	c, err := s.connection(ref)
	if err != nil {
		return c, nil, nil, err
	}
	sess, err := s.session(ctx, c, false, database)
	if err != nil {
		return c, nil, nil, err
	}
	d, err := s.opts.Drivers.Get(c.Kind)
	if err != nil {
		return c, nil, nil, err
	}
	return c, sess, d, nil
}

// schemaOf returns schema, or the session's current schema when empty.
func schemaOf(ctx context.Context, sess *conn.Session, d driver.Driver, schema string) (string, error) {
	if schema = strings.TrimSpace(schema); schema != "" {
		return schema, nil
	}
	return d.CurrentSchema(ctx, sess.DB)
}

// record adds an executed call to the query history (failures are logged).
func (s *Server) record(sess *conn.Session, sql string, start time.Time, rows, affected *int64, runErr error) {
	if s.opts.History == nil {
		return
	}
	e := history.Entry{
		ConnectionID: sess.ConnectionID, Database: sess.Database, SQL: sql, StartedAt: start,
		DurationMs: elapsed(start), RowCount: rows, RowsAffected: affected, Source: history.SourceMCP,
	}
	if runErr != nil {
		e.IsError, e.Error = true, runErr.Error()
	}
	if _, err := s.opts.History.Add(context.Background(), e); err != nil {
		s.opts.Logf("query history: %v", err)
	}
}

func (s *Server) listConnections(_ context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
	conns, err := s.visible()
	if err != nil {
		return nil, nil, err
	}
	if len(conns) == 0 {
		return text("No connections are enabled for AI access. The user can enable one in Lontar: edit the connection and set \"AI access (MCP)\" to Read or Read and write."), nil, nil
	}
	var b strings.Builder
	for _, c := range conns {
		fmt.Fprintf(&b, "- id: %s | name: %s | engine: %s", c.ID, c.Name, c.Kind)
		if c.Database != "" {
			fmt.Fprintf(&b, " | database: %s", c.Database)
		}
		if c.Tag != store.TagNone {
			fmt.Fprintf(&b, " | tag: %s", c.Tag)
		}
		fmt.Fprintf(&b, " | access: %s | safe mode: %s\n", c.EffectiveAIAccess(), c.SafeMode)
	}
	return text(b.String()), nil, nil
}

func (s *Server) listDatabases(ctx context.Context, _ *mcp.CallToolRequest, in connArgs) (*mcp.CallToolResult, any, error) {
	ctx, cancel := s.timeout(ctx)
	defer cancel()
	_, sess, d, err := s.readTarget(ctx, in.Connection, in.Database)
	if err != nil {
		return nil, nil, err
	}
	dbs, err := d.ListDatabases(ctx, sess.DB, false)
	if err != nil {
		return nil, nil, err
	}
	return text(markCurrent(dbs, sess.Database)), nil, nil
}

func (s *Server) listSchemas(ctx context.Context, _ *mcp.CallToolRequest, in connArgs) (*mcp.CallToolResult, any, error) {
	ctx, cancel := s.timeout(ctx)
	defer cancel()
	_, sess, d, err := s.readTarget(ctx, in.Connection, in.Database)
	if err != nil {
		return nil, nil, err
	}
	schemas, err := d.ListSchemas(ctx, sess.DB, false)
	if err != nil {
		return nil, nil, err
	}
	current, _ := d.CurrentSchema(ctx, sess.DB)
	return text(markCurrent(schemas, current)), nil, nil
}

// markCurrent lists names one per line, marking the current one.
func markCurrent(names []string, current string) string {
	if len(names) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		if n == current {
			b.WriteString(" (current)")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (s *Server) listTables(ctx context.Context, _ *mcp.CallToolRequest, in schemaArgs) (*mcp.CallToolResult, any, error) {
	ctx, cancel := s.timeout(ctx)
	defer cancel()
	_, sess, d, err := s.readTarget(ctx, in.Connection, in.Database)
	if err != nil {
		return nil, nil, err
	}
	schema, err := schemaOf(ctx, sess, d, in.Schema)
	if err != nil {
		return nil, nil, err
	}
	tables, err := d.ListTables(ctx, sess.DB, schema)
	if err != nil {
		return nil, nil, err
	}
	if len(tables) == 0 {
		return text(fmt.Sprintf("Schema %s has no tables or views.", schema)), nil, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Schema %s:\n", schema)
	bud := newBudget()
	for i, t := range tables {
		line := "- " + t.Name + " (" + string(t.Type)
		if t.EstimatedRows != nil {
			line += fmt.Sprintf(", ~%d rows", *t.EstimatedRows)
		}
		line += ")\n"
		if !bud.take(len(line)) {
			fmt.Fprintf(&b, "… %d more not shown\n", len(tables)-i)
			break
		}
		b.WriteString(line)
	}
	return text(b.String()), nil, nil
}

func (s *Server) describeTable(ctx context.Context, _ *mcp.CallToolRequest, in tableArgs) (*mcp.CallToolResult, any, error) {
	ctx, cancel := s.timeout(ctx)
	defer cancel()
	_, sess, d, err := s.readTarget(ctx, in.Connection, in.Database)
	if err != nil {
		return nil, nil, err
	}
	schema, err := schemaOf(ctx, sess, d, in.Schema)
	if err != nil {
		return nil, nil, err
	}
	if err := tableExists(ctx, sess, d, schema, in.Table); err != nil {
		return nil, nil, err
	}
	info, err := d.TableInfo(ctx, sess.DB, schema, in.Table)
	if err != nil {
		return nil, nil, err
	}
	return text(renderTable(info)), nil, nil
}

// tableExists checks a table name against the schema's metadata.
func tableExists(ctx context.Context, sess *conn.Session, d driver.Driver, schema, table string) error {
	cols, err := d.Columns(ctx, sess.DB, schema, table)
	if err != nil {
		return err
	}
	if len(cols) == 0 {
		return fmt.Errorf("table %q not found in schema %s (call list_tables)", table, schema)
	}
	return nil
}

func (s *Server) listObjects(ctx context.Context, _ *mcp.CallToolRequest, in schemaArgs) (*mcp.CallToolResult, any, error) {
	ctx, cancel := s.timeout(ctx)
	defer cancel()
	_, sess, d, err := s.readTarget(ctx, in.Connection, in.Database)
	if err != nil {
		return nil, nil, err
	}
	schema, err := schemaOf(ctx, sess, d, in.Schema)
	if err != nil {
		return nil, nil, err
	}
	l, ok := d.(driver.ObjectLister)
	if !ok {
		return text("This engine has no functions, sequences or similar objects to list."), nil, nil
	}
	objs, err := l.ListObjects(ctx, sess.DB, schema)
	if err != nil {
		return nil, nil, err
	}
	if len(objs) == 0 {
		return text(fmt.Sprintf("Schema %s has no functions, procedures, sequences, types, triggers or events.", schema)), nil, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Schema %s:\n", schema)
	bud := newBudget()
	for i, o := range objs {
		line := fmt.Sprintf("- %s %s", o.Kind, o.Name)
		if o.Signature != "" {
			line += "(" + o.Signature + ")"
		}
		if o.Table != "" {
			line += " on " + o.Table
		}
		if o.Detail != "" {
			line += " — " + o.Detail
		}
		line += "\n"
		if !bud.take(len(line)) {
			fmt.Fprintf(&b, "… %d more not shown\n", len(objs)-i)
			break
		}
		b.WriteString(line)
	}
	return text(b.String()), nil, nil
}

func (s *Server) getDDL(ctx context.Context, _ *mcp.CallToolRequest, in ddlArgs) (*mcp.CallToolResult, any, error) {
	ctx, cancel := s.timeout(ctx)
	defer cancel()
	_, sess, d, err := s.readTarget(ctx, in.Connection, in.Database)
	if err != nil {
		return nil, nil, err
	}
	schema, err := schemaOf(ctx, sess, d, in.Schema)
	if err != nil {
		return nil, nil, err
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind == "" || kind == "table" || kind == "view" {
		if err := tableExists(ctx, sess, d, schema, in.Name); err != nil {
			return nil, nil, err
		}
		ddl, err := d.TableDDL(ctx, sess.DB, schema, in.Name)
		if err != nil {
			return nil, nil, err
		}
		return text(ddl), nil, nil
	}
	l, ok := d.(driver.ObjectLister)
	if !ok {
		return nil, nil, fmt.Errorf("%s has no %s objects", d.DisplayName(), kind)
	}
	objs, err := l.ListObjects(ctx, sess.DB, schema)
	if err != nil {
		return nil, nil, err
	}
	obj, err := pickObject(objs, driver.ObjectKind(kind), in.Name, in.Signature, in.Table)
	if err != nil {
		return nil, nil, err
	}
	ddl, err := l.ObjectDDL(ctx, sess.DB, schema, obj.Ref())
	if err != nil {
		return nil, nil, err
	}
	return text(ddl), nil, nil
}

// pickObject finds the one object matching kind and name (and signature
// or table when given).
func pickObject(objs []driver.Object, kind driver.ObjectKind, name, signature, table string) (driver.Object, error) {
	var match []driver.Object
	for _, o := range objs {
		if o.Kind != kind || o.Name != name {
			continue
		}
		if signature != "" && o.Signature != signature {
			continue
		}
		if table != "" && o.Table != table {
			continue
		}
		match = append(match, o)
	}
	switch len(match) {
	case 0:
		return driver.Object{}, fmt.Errorf("%w: %s %q (call list_objects)", driver.ErrObjectNotFound, kind, name)
	case 1:
		return match[0], nil
	}
	var alts []string
	for _, o := range match {
		alts = append(alts, fmt.Sprintf("signature %q table %q", o.Signature, o.Table))
	}
	return driver.Object{}, fmt.Errorf("%d %s objects are named %q; pass signature or table: %s", len(match), kind, name, strings.Join(alts, "; "))
}

func (s *Server) runQuery(ctx context.Context, _ *mcp.CallToolRequest, in queryArgs) (*mcp.CallToolResult, any, error) {
	maxRows := in.MaxRows
	switch {
	case maxRows <= 0:
		maxRows = DefaultMaxRows
	case maxRows > MaxMaxRows:
		maxRows = MaxMaxRows
	}
	c, err := s.connection(in.Connection)
	if err != nil {
		return nil, nil, err
	}
	stmts, err := splitStatements(c.Kind, in.SQL)
	if err != nil {
		return nil, nil, err
	}
	for i, st := range stmts {
		if err := checkRead(c.Kind, i+1, st); err != nil {
			return nil, nil, err
		}
	}
	ctx, cancel := s.timeout(ctx)
	defer cancel()
	sess, err := s.session(ctx, c, false, in.Database)
	if err != nil {
		return nil, nil, err
	}
	start := time.Now()
	sets, failed, runErr := runRead(ctx, sess, stmts, maxRows)
	rows := rowCount(sets)
	s.record(sess, in.SQL, start, &rows, nil, runErr)
	out := renderResults(sets, len(stmts), maxRows)
	if runErr != nil {
		return failure(out + fmt.Sprintf("Statement %d of %d failed: %s\n", failed+1, len(stmts), describeErr(ctx, runErr, s.opts.QueryTimeout))), nil, nil
	}
	return text(out), nil, nil
}

// describeErr explains a timeout instead of a bare context error.
func describeErr(ctx context.Context, err error, timeout time.Duration) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Sprintf("the query did not finish within %s and was cancelled", timeout)
	}
	return err.Error()
}

func (s *Server) executeWrite(ctx context.Context, req *mcp.CallToolRequest, in writeArgs) (*mcp.CallToolResult, any, error) {
	// Validation runs on the first call and again on the approved retry:
	// access may have been revoked while the user was deciding.
	c, err := s.connection(in.Connection)
	if err != nil {
		return nil, nil, err
	}
	if c.EffectiveAIAccess() != store.AIWrite {
		return nil, nil, errNoWrite
	}
	if len(in.SQL) > MaxWriteSQLBytes {
		return nil, nil, fmt.Errorf("the SQL is %d bytes; execute_write takes at most %d so the user can review it. Split the change into smaller calls", len(in.SQL), MaxWriteSQLBytes)
	}
	stmts, err := splitStatements(c.Kind, in.SQL)
	if err != nil {
		return nil, nil, err
	}
	for i, st := range stmts {
		if err := checkWrite(c.Kind, i+1, st); err != nil {
			return nil, nil, err
		}
	}
	if !canElicit(req) {
		return nil, nil, errors.New("writes need the user's approval, but this MCP client does not support approval prompts (elicitation); nothing was changed")
	}
	// Connect first: a bad database name fails before the user is asked.
	cctx, cancel := s.timeout(ctx)
	sess, err := s.session(cctx, c, true, in.Database)
	cancel()
	if err != nil {
		return nil, nil, err
	}
	sql := strings.TrimSpace(in.SQL)
	state := s.approvalState(c.ID, sess.Database, sql)
	answer, answered := req.Params.InputResponses[approvalID]
	if !answered {
		return approvalRequest(approvalMessage(c, sess.Database, sql), state), nil, nil
	}
	if !hmac.Equal([]byte(req.Params.RequestState), []byte(state)) {
		return nil, nil, errors.New("the approval does not match this change; nothing was changed")
	}
	if r, ok := answer.(*mcp.ElicitResult); !ok || r.Action != "accept" {
		return failure("The user did not approve the change; nothing was changed."), nil, nil
	}

	ctx, cancel = s.timeout(ctx)
	defer cancel()
	start := time.Now()
	affected, failed, runErr := runWrite(ctx, sess, stmts)
	s.record(sess, in.SQL, start, nil, totalAffected(affected), runErr)
	if runErr != nil {
		msg := describeErr(ctx, runErr, s.opts.QueryTimeout)
		if failed >= 0 {
			return failure(fmt.Sprintf("Statement %d of %d failed: %s\nThe transaction was rolled back; nothing was changed.", failed+1, len(stmts), msg)), nil, nil
		}
		return failure(fmt.Sprintf("The change failed: %s\nThe transaction was rolled back; nothing was changed.", msg)), nil, nil
	}
	return text(renderWrite(affected)), nil, nil
}
