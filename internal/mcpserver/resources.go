package mcpserver

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// schemaURIPrefix starts the schema overview resource's URI:
// lontar://{connection}/schema.
const schemaURIPrefix = "lontar://"

func (s *Server) addResources(srv *mcp.Server) {
	srv.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "schema",
		Title:       "Database schema overview",
		URITemplate: "lontar://{connection}/schema",
		Description: "Every table and view of a connection's current schema with its columns and types, in one compact document. {connection} is an id from list_connections.",
		MIMEType:    "text/plain",
	}, s.readSchema)
}

// connectionFromURI extracts {connection} from lontar://{connection}/schema.
func connectionFromURI(uri string) (string, bool) {
	rest, ok := strings.CutPrefix(uri, schemaURIPrefix)
	if !ok {
		return "", false
	}
	ref, ok := strings.CutSuffix(rest, "/schema")
	if !ok || ref == "" || strings.Contains(ref, "/") {
		return "", false
	}
	ref, err := url.PathUnescape(ref)
	return ref, err == nil
}

func (s *Server) readSchema(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	ref, ok := connectionFromURI(uri)
	if !ok {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	ctx, cancel := s.timeout(ctx)
	defer cancel()
	c, sess, d, err := s.readTarget(ctx, ref, "")
	if err != nil {
		return nil, err
	}
	schema, err := d.CurrentSchema(ctx, sess.DB)
	if err != nil {
		return nil, err
	}
	tables, err := d.ListTables(ctx, sess.DB, schema)
	if err != nil {
		return nil, err
	}
	cols, err := d.SchemaColumns(ctx, sess.DB, schema)
	if err != nil {
		return nil, err
	}
	byTable := map[string][]string{}
	for _, col := range cols {
		byTable[col.Table] = append(byTable[col.Table], col.Name+" "+col.DataType)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "-- %s (%s), database %q, schema %q: %d tables and views\n", c.Name, c.Kind, sess.Database, schema, len(tables))
	bud := newBudget()
	for i, t := range tables {
		line := fmt.Sprintf("%s [%s](%s)\n", t.Name, t.Type, strings.Join(byTable[t.Name], ", "))
		if !bud.take(len(line)) {
			fmt.Fprintf(&b, "… %d more tables not shown; use list_tables and describe_table\n", len(tables)-i)
			break
		}
		b.WriteString(line)
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/plain", Text: b.String()}}}, nil
}
