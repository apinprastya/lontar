package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lontar/internal/driver"
	"lontar/internal/query"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// renderTable describes a table's structure as compact text.
func renderTable(t *driver.TableDetail) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s.%s", t.Type, t.Schema, t.Name)
	if t.EstimatedRows != nil {
		fmt.Fprintf(&b, " (~%d rows)", *t.EstimatedRows)
	}
	b.WriteString("\n")
	if t.Comment != "" {
		fmt.Fprintf(&b, "Comment: %s\n", t.Comment)
	}
	b.WriteString("Columns:\n")
	for _, c := range t.Columns {
		fmt.Fprintf(&b, "- %s %s", c.Name, c.DataType)
		if !c.Nullable {
			b.WriteString(" NOT NULL")
		}
		if c.IsPrimaryKey {
			fmt.Fprintf(&b, " PRIMARY KEY(%d)", c.PKPosition)
		}
		if c.AutoIncrement {
			b.WriteString(" auto-increment")
		}
		if c.Identity != "" {
			fmt.Fprintf(&b, " GENERATED %s AS IDENTITY", c.Identity)
		}
		if c.Generated != "" {
			fmt.Fprintf(&b, " GENERATED %s", c.Generated)
			if c.GenerationExpr != nil {
				fmt.Fprintf(&b, " AS (%s)", *c.GenerationExpr)
			}
		}
		if c.Default != nil {
			fmt.Fprintf(&b, " DEFAULT %s", *c.Default)
		}
		if len(c.EnumValues) > 0 {
			fmt.Fprintf(&b, " values: %s", strings.Join(c.EnumValues, ", "))
		}
		if c.Comment != "" {
			fmt.Fprintf(&b, " -- %s", c.Comment)
		}
		b.WriteString("\n")
	}
	if len(t.Indexes) > 0 {
		b.WriteString("Indexes:\n")
		for _, ix := range t.Indexes {
			kind := "INDEX"
			switch {
			case ix.Primary:
				kind = "PRIMARY KEY"
			case ix.Unique:
				kind = "UNIQUE"
			}
			fmt.Fprintf(&b, "- %s %s (%s)", ix.Name, kind, strings.Join(ix.Columns, ", "))
			if ix.Predicate != "" {
				fmt.Fprintf(&b, " WHERE %s", ix.Predicate)
			}
			b.WriteString("\n")
		}
	}
	fks := func(title string, list []driver.ForeignKey) {
		if len(list) == 0 {
			return
		}
		b.WriteString(title + ":\n")
		for _, fk := range list {
			b.WriteString("- ")
			if fk.Name != "" {
				b.WriteString(fk.Name + ": ")
			}
			fmt.Fprintf(&b, "%s.%s (%s) -> %s.%s (%s)", fk.Schema, fk.Table, strings.Join(fk.Columns, ", "),
				fk.RefSchema, fk.RefTable, strings.Join(fk.RefColumns, ", "))
			if fk.OnDelete != "" {
				fmt.Fprintf(&b, " ON DELETE %s", fk.OnDelete)
			}
			if fk.OnUpdate != "" {
				fmt.Fprintf(&b, " ON UPDATE %s", fk.OnUpdate)
			}
			b.WriteString("\n")
		}
	}
	fks("Foreign keys", t.ForeignKeys)
	fks("Referenced by", t.ReferencedBy)
	if len(t.Checks) > 0 {
		b.WriteString("Checks:\n")
		for _, c := range t.Checks {
			fmt.Fprintf(&b, "- %s: %s\n", c.Name, c.Expression)
		}
	}
	if len(t.Triggers) > 0 {
		b.WriteString("Triggers:\n")
		for _, tr := range t.Triggers {
			fmt.Fprintf(&b, "- %s %s %s\n", tr.Name, tr.Timing, tr.Event)
		}
	}
	return b.String()
}

func (s *Server) explainQuery(ctx context.Context, _ *mcp.CallToolRequest, in explainArgs) (*mcp.CallToolResult, any, error) {
	c, err := s.connection(in.Connection)
	if err != nil {
		return nil, nil, err
	}
	stmts, err := splitStatements(c.Kind, in.SQL)
	if err != nil {
		return nil, nil, err
	}
	if len(stmts) != 1 {
		return nil, nil, fmt.Errorf("explain_query takes exactly one statement, got %d", len(stmts))
	}
	ctx, cancel := s.timeout(ctx)
	defer cancel()
	sess, err := s.session(ctx, c, false, in.Database)
	if err != nil {
		return nil, nil, err
	}
	editorID, err := s.editors.Open(sess.ID)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = s.editors.Close(editorID) }()
	start := time.Now()
	plan, err := s.editors.Explain(ctx, editorID, "", stmts[0], query.ExplainOptions{})
	if err != nil {
		return nil, nil, err
	}
	if plan.Error != nil {
		s.record(sess, plan.SQL, start, nil, nil, fmt.Errorf("%s", plan.Error.Message))
		return failure("EXPLAIN failed: " + plan.Error.Message), nil, nil
	}
	s.record(sess, plan.SQL, start, nil, nil, nil)
	return text(renderPlan(plan)), nil, nil
}

// renderPlan prints the plan tree, one node per line.
func renderPlan(p *query.Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s plan (%s):\n", p.Engine, p.SQL)
	var walk func(nodes []query.PlanNode, depth int)
	walk = func(nodes []query.PlanNode, depth int) {
		for _, n := range nodes {
			b.WriteString(strings.Repeat("  ", depth))
			b.WriteString("-> " + n.Op)
			if n.Object != "" {
				b.WriteString(" on " + n.Object)
			}
			var nums []string
			if n.EstRows != nil {
				nums = append(nums, fmt.Sprintf("rows=%g", *n.EstRows))
			}
			if n.Cost != nil {
				nums = append(nums, fmt.Sprintf("cost=%g", *n.Cost))
			}
			if len(nums) > 0 {
				b.WriteString(" (" + strings.Join(nums, " ") + ")")
			}
			if n.Detail != "" {
				b.WriteString(" " + n.Detail)
			}
			b.WriteString("\n")
			walk(n.Children, depth+1)
		}
	}
	walk(p.Nodes, 0)
	if len(p.Nodes) == 0 && p.Raw != "" {
		b.WriteString(clip(p.Raw, MaxOutputBytes))
		b.WriteString("\n")
	}
	return b.String()
}
