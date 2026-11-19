package mcpserver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"lontar/internal/conn"
	"lontar/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// approvalID is the input request ID of a write's approval prompt.
const approvalID = "approve"

// Approval of writes uses MCP multi round-trip requests (SEP-2322): the
// first execute_write call returns an input request (an elicitation
// showing the SQL) instead of running anything; the client asks the user
// and calls again with the answer and the request state. For clients on
// older protocol versions the SDK's server middleware sends a classic
// elicitation/create and re-invokes the handler the same way. The request
// state is an HMAC over the connection, database and SQL, so an answer
// only ever approves the exact change that was shown.

// approvalState binds an approval to one change.
func (s *Server) approvalState(connectionID, database, sql string) string {
	m := hmac.New(sha256.New, s.key)
	for _, part := range []string{connectionID, database, sql} {
		m.Write([]byte(part))
		m.Write([]byte{0})
	}
	return hex.EncodeToString(m.Sum(nil))
}

// canElicit reports whether the client declared elicitation support.
func canElicit(req *mcp.CallToolRequest) bool {
	if req == nil || req.Session == nil {
		return false
	}
	p := req.Session.InitializeParams()
	return p != nil && p.Capabilities != nil && p.Capabilities.Elicitation != nil
}

// approvalRequest asks the user to approve message.
func approvalRequest(message, state string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{approvalID: &mcp.ElicitParams{
			Mode:            "form",
			Message:         message,
			RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		}},
		RequestState: state,
	}
}

// approvalMessage is what the user sees before a write runs.
func approvalMessage(c store.Connection, database string, sql string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Claude wants to change data on %q (%s", c.Name, c.Kind)
	if database != "" {
		fmt.Fprintf(&b, ", database %s", database)
	}
	if c.Tag != "" && c.Tag != store.TagNone {
		fmt.Fprintf(&b, ", tagged %s", c.Tag)
	}
	b.WriteString("). It will run in one transaction:\n\n")
	b.WriteString(sql)
	b.WriteString("\n\nApprove to run it, decline to cancel.")
	return b.String()
}

// runWrite runs the statements in one transaction on the write session
// and commits. It returns the rows affected per statement (nil when the
// driver cannot tell) and, on error, the failing statement's index.
func runWrite(ctx context.Context, sess *conn.Session, stmts []string) ([]*int64, int, error) {
	tx, err := sess.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, -1, err
	}
	defer func() { _ = tx.Rollback() }()
	affected := make([]*int64, 0, len(stmts))
	for i, text := range stmts {
		res, err := tx.ExecContext(ctx, text)
		if err != nil {
			return affected, i, err
		}
		var n *int64
		if v, err := res.RowsAffected(); err == nil {
			n = &v
		}
		affected = append(affected, n)
	}
	if err := tx.Commit(); err != nil {
		return affected, -1, fmt.Errorf("commit: %w", err)
	}
	return affected, -1, nil
}

// renderWrite describes a committed write.
func renderWrite(affected []*int64) string {
	var b strings.Builder
	b.WriteString("Committed.\n")
	for i, n := range affected {
		if n == nil {
			fmt.Fprintf(&b, "Statement %d: done\n", i+1)
			continue
		}
		fmt.Fprintf(&b, "Statement %d: %s affected\n", i+1, plural(*n, "row", "rows"))
	}
	return b.String()
}

// totalAffected sums the known rows affected (nil when none is known).
func totalAffected(affected []*int64) *int64 {
	var sum int64
	known := false
	for _, n := range affected {
		if n != nil {
			sum += *n
			known = true
		}
	}
	if !known {
		return nil
	}
	return &sum
}
