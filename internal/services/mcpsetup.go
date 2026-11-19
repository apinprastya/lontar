package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// MCPSetup tells the user how to connect an AI assistant to Lontar's MCP
// server (`lontar mcp`, see internal/mcpserver).
type MCPSetup struct {
	// Command is the Lontar executable that serves MCP with Args.
	Command string   `json:"command"`
	Args    []string `json:"args"`
	// ClaudeCode is the shell command that registers the server with
	// Claude Code.
	ClaudeCode string `json:"claudeCode"`
	// ClaudeDesktop is the "mcpServers" entry for Claude Desktop's
	// claude_desktop_config.json.
	ClaudeDesktop string `json:"claudeDesktop"`
	// Error explains why the executable could not be found ("" when
	// Command is set).
	Error string `json:"error"`
}

// mcpEnv is what MCPSetup reads from the environment (replaced in tests).
type mcpEnv struct {
	goos       string
	executable func() (string, error)
	getenv     func(string) string
}

func defaultMCPEnv(goos string) mcpEnv {
	return mcpEnv{goos: goos, executable: realExecutable, getenv: os.Getenv}
}

// realExecutable returns the running binary with symlinks resolved.
func realExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

// MCPSetup returns the commands that connect Claude to this installation.
func (s *AppService) MCPSetup() MCPSetup {
	env := s.mcp
	if env.executable == nil {
		env = defaultMCPEnv(s.Platform())
	}
	return mcpSetup(env)
}

func mcpSetup(env mcpEnv) MCPSetup {
	// An AppImage runs from a temporary mount; $APPIMAGE is the file the
	// user installed.
	exe := env.getenv("APPIMAGE")
	if exe == "" {
		var err error
		if exe, err = env.executable(); err != nil {
			return MCPSetup{Error: "could not locate the Lontar executable: " + err.Error()}
		}
	}
	args := []string{"mcp"}
	desktop, _ := json.MarshalIndent(map[string]any{
		"mcpServers": map[string]any{"lontar": map[string]any{"command": exe, "args": args}},
	}, "", "  ")
	return MCPSetup{
		Command:       exe,
		Args:          args,
		ClaudeCode:    "claude mcp add lontar -- " + shellQuote(exe, env.goos) + " mcp",
		ClaudeDesktop: string(desktop),
	}
}

// shellQuote quotes s for the user's shell when it needs quoting:
// PowerShell/cmd double quotes on Windows, POSIX single quotes elsewhere.
func shellQuote(s, goos string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`!*?&;|<>()[]{}#~%") {
		return s
	}
	if goos == "windows" {
		if !strings.ContainsAny(s, " \t\"&|<>()%^") {
			return s // backslashes are path separators there
		}
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
