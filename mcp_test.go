package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lontar/internal/history"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"
)

func TestIsMCP(t *testing.T) {
	if !isMCP([]string{"mcp"}) || isMCP(nil) || isMCP([]string{"--mcp"}) || isMCP([]string{"x", "mcp"}) {
		t.Fatal("isMCP")
	}
}

// runMCP serves a real client over stdio-like pipes, against the same
// config directory files the app uses.
func TestRunMCP(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "shop.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE t (x INTEGER); INSERT INTO t VALUES (41), (42)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	conns := fmt.Sprintf(`{"version":3,"connections":[
	  {"id":"c1","name":"Shop","kind":"sqlite","filePath":%q,"tag":"none","safeMode":"off","aiAccess":"read"},
	  {"id":"c2","name":"Hidden","kind":"sqlite","filePath":%q,"tag":"none","safeMode":"off"}]}`, dbPath, dbPath)
	if err := os.WriteFile(filepath.Join(dir, "connections.json"), []byte(conns), 0o600); err != nil {
		t.Fatal(err)
	}

	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runMCP(ctx, dir, serverIn, serverOut, &logs) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	cs, err := client.Connect(ctx, &mcp.IOTransport{Reader: clientIn, Writer: clientOut}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := cs.InitializeResult().ServerInfo.Name; got != "lontar" {
		t.Fatalf("server = %q", got)
	}
	text := func(name string, args map[string]any) string {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %+v, %v", name, res, err)
		}
		return res.Content[0].(*mcp.TextContent).Text
	}
	if out := text("list_connections", nil); !strings.Contains(out, "name: Shop") || strings.Contains(out, "Hidden") {
		t.Fatalf("connections:\n%s", out)
	}
	if out := text("run_query", map[string]any{"connection": "Shop", "sql": "SELECT max(x) AS m FROM t"}); !strings.Contains(out, "m\n42\n") {
		t.Fatalf("query:\n%s", out)
	}
	_ = cs.Close()
	_ = clientOut.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runMCP: %v (logs: %s)", err, logs.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runMCP did not return")
	}

	// The query is in the app's history, marked as coming from MCP.
	h := history.New(dir)
	defer h.Close()
	list, err := h.List(ctx, "c1", "", 10, 0)
	if err != nil || len(list) != 1 || list[0].Source != history.SourceMCP {
		t.Fatalf("history = %+v, %v", list, err)
	}
}

// A cancelled context ends the server without an error.
func TestRunMCPCancelled(t *testing.T) {
	serverIn, _ := io.Pipe()
	_, serverOut := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runMCP(ctx, t.TempDir(), serverIn, serverOut, io.Discard) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runMCP did not return")
	}
}

// A broken transport is reported.
func TestRunMCPError(t *testing.T) {
	err := runMCP(context.Background(), t.TempDir(), io.NopCloser(strings.NewReader("not json\n")), nopWriteCloser{io.Discard}, io.Discard)
	if err == nil || !strings.HasPrefix(err.Error(), "mcp: ") {
		t.Fatalf("err = %v", err)
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
