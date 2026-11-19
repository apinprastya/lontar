package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"

	"lontar/internal/conn"
	"lontar/internal/driver/all"
	"lontar/internal/history"
	"lontar/internal/mcpserver"
	"lontar/internal/services"
	"lontar/internal/sshtunnel"
	"lontar/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpCommand is the first argument that starts the MCP server instead of
// the app: `lontar mcp`.
const mcpCommand = "mcp"

// isMCP reports whether args (os.Args[1:]) ask for the MCP server.
func isMCP(args []string) bool {
	return len(args) > 0 && args[0] == mcpCommand
}

// newMCPServer wires the MCP server to the same connection store (and
// keychain), SSH host keys and query history as the app. It opens no
// window and binds no Wails service.
func newMCPServer(configDir string, logger *log.Logger) (*mcpserver.Server, func()) {
	st := store.New(configDir, store.NewSecretStore(configDir))
	drivers := all.Registry()
	sessions := conn.NewManager(st, drivers)
	hostKeys := &sshtunnel.HostKeys{UserFiles: userKnownHosts(), AppFile: filepath.Join(configDir, KnownHostsFile)}
	sessions.Tunnels = conn.SSHTunnels(sshtunnel.New(sshtunnel.Options{HostKeys: hostKeys}))
	hist := history.New(configDir)
	srv := mcpserver.New(mcpserver.Options{
		Connections: st,
		Sessions:    sessions,
		Drivers:     drivers,
		History:     hist,
		Version:     services.Version,
		Logf:        logger.Printf,
	})
	cleanup := func() {
		_ = sessions.DisconnectAll()
		_ = hist.Close()
	}
	return srv, cleanup
}

// runMCP serves the MCP protocol on in/out (stdin/stdout) until the client
// disconnects or ctx ends. Diagnostics go to logw (stderr), never to out.
func runMCP(ctx context.Context, configDir string, in io.ReadCloser, out io.WriteCloser, logw io.Writer) error {
	logger := log.New(logw, "lontar mcp: ", log.LstdFlags)
	srv, cleanup := newMCPServer(configDir, logger)
	defer cleanup()
	err := srv.Run(ctx, &mcp.IOTransport{Reader: in, Writer: out})
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("mcp: %w", err)
	}
	return nil
}
