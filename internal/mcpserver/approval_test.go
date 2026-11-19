package mcpserver

import (
	"context"
	"testing"

	"lontar/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Clients on protocol versions before multi round-trip requests get a
// classic elicitation/create from the SDK's server middleware.
func TestExecuteWriteOlderProtocol(t *testing.T) {
	for _, version := range []string{"2025-06-18", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			e := newEnv(t, envOpts{protocol: version})
			if got := e.cs.InitializeResult().ProtocolVersion; got != version {
				t.Fatalf("negotiated %s", got)
			}
			c := e.sqlite("rw", store.AIWrite)
			wantContains(t, e.ok("execute_write", map[string]any{"connection": c.ID, "sql": "DELETE FROM orders WHERE total > 100"}),
				"Statement 1: 1 row affected")
			if len(e.asked) != 1 {
				t.Fatalf("asked %d times", len(e.asked))
			}
			e.approve = func(string) string { return "cancel" }
			e.fail("execute_write", map[string]any{"connection": c.ID, "sql": "DELETE FROM orders"}, "did not approve")
			if n := countRows(t, c, "SELECT count(*) FROM orders"); n != 2 {
				t.Fatalf("orders = %d", n)
			}
		})
	}
}

func TestExecuteWriteOlderProtocolWithoutElicitation(t *testing.T) {
	e := newEnv(t, envOpts{protocol: "2025-06-18", noElicitation: true})
	c := e.sqlite("rw", store.AIWrite)
	e.fail("execute_write", map[string]any{"connection": c.ID, "sql": "DELETE FROM users"}, "does not support approval prompts")
}

// An approval only runs the change it was given for.
func TestApprovalIsBoundToTheChange(t *testing.T) {
	e := newEnv(t, envOpts{manualRoundTrip: true})
	c := e.sqlite("rw", store.AIWrite)
	ctx := context.Background()
	call := func(sql, state string, answer mcp.InputResponse) *mcp.CallToolResult {
		t.Helper()
		p := &mcp.CallToolParams{Name: "execute_write", Arguments: map[string]any{"connection": c.ID, "sql": sql}, RequestState: state}
		if answer != nil {
			p.InputResponses = mcp.InputResponseMap{approvalID: answer}
		}
		res, err := e.cs.CallTool(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	first := call("DELETE FROM orders WHERE total > 100", "", nil)
	if !first.NeedsInput() || first.RequestState == "" {
		t.Fatalf("first call = %+v", first)
	}
	ep, ok := first.InputRequests[approvalID].(*mcp.ElicitParams)
	if !ok {
		t.Fatalf("input requests = %+v", first.InputRequests)
	}
	wantContains(t, ep.Message, "DELETE FROM orders WHERE total > 100")
	accept := &mcp.ElicitResult{Action: "accept"}

	// The approval of one statement does not run another.
	if res := call("DELETE FROM orders", first.RequestState, accept); !res.IsError {
		t.Fatalf("swapped SQL ran: %+v", res)
	}
	// A made-up state is refused.
	if res := call("DELETE FROM orders WHERE total > 100", "forged", accept); !res.IsError {
		t.Fatalf("forged state ran: %+v", res)
	}
	// A response of another type is not an approval.
	if res := call("DELETE FROM orders WHERE total > 100", first.RequestState, &mcp.ListRootsResult{}); !res.IsError {
		t.Fatalf("non-elicitation answer ran: %+v", res)
	}
	if n := countRows(t, c, "SELECT count(*) FROM orders"); n != 3 {
		t.Fatalf("orders = %d", n)
	}
	// The matching approval runs it.
	if res := call("DELETE FROM orders WHERE total > 100", first.RequestState, accept); res.IsError {
		t.Fatalf("approved call failed: %+v", res.Content)
	}
	if n := countRows(t, c, "SELECT count(*) FROM orders"); n != 2 {
		t.Fatalf("orders = %d", n)
	}
}
