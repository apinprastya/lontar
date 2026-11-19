package services

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func fakeMCPEnv(goos, exe, appimage string, err error) mcpEnv {
	return mcpEnv{
		goos:       goos,
		executable: func() (string, error) { return exe, err },
		getenv: func(k string) string {
			if k == "APPIMAGE" {
				return appimage
			}
			return ""
		},
	}
}

func TestMCPSetup(t *testing.T) {
	cases := []struct {
		name string
		env  mcpEnv
		cmd  string
		code string
	}{
		{"linux", fakeMCPEnv("linux", "/usr/bin/lontar", "", nil), "/usr/bin/lontar", "claude mcp add lontar -- /usr/bin/lontar mcp"},
		{"appimage", fakeMCPEnv("linux", "/tmp/.mount_x/lontar", "/home/me/Apps/Lontar.AppImage", nil),
			"/home/me/Apps/Lontar.AppImage", "claude mcp add lontar -- /home/me/Apps/Lontar.AppImage mcp"},
		{"mac bundle with space", fakeMCPEnv("darwin", "/Applications/My Apps/Lontar.app/Contents/MacOS/Lontar", "", nil),
			"/Applications/My Apps/Lontar.app/Contents/MacOS/Lontar", "claude mcp add lontar -- '/Applications/My Apps/Lontar.app/Contents/MacOS/Lontar' mcp"},
		{"quote in path", fakeMCPEnv("linux", "/opt/it's/lontar", "", nil), "/opt/it's/lontar", `claude mcp add lontar -- '/opt/it'\''s/lontar' mcp`},
		{"windows", fakeMCPEnv("windows", `C:\Program Files\Lontar\lontar.exe`, "", nil),
			`C:\Program Files\Lontar\lontar.exe`, `claude mcp add lontar -- "C:\Program Files\Lontar\lontar.exe" mcp`},
		{"windows no space", fakeMCPEnv("windows", `C:\Lontar\lontar.exe`, "", nil), `C:\Lontar\lontar.exe`, `claude mcp add lontar -- C:\Lontar\lontar.exe mcp`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &AppService{goos: tc.env.goos, mcp: tc.env}
			got := s.MCPSetup()
			if got.Command != tc.cmd || got.ClaudeCode != tc.code || got.Error != "" || strings.Join(got.Args, " ") != "mcp" {
				t.Fatalf("got %+v", got)
			}
			var desktop struct {
				MCPServers map[string]struct {
					Command string   `json:"command"`
					Args    []string `json:"args"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal([]byte(got.ClaudeDesktop), &desktop); err != nil {
				t.Fatal(err)
			}
			if d := desktop.MCPServers["lontar"]; d.Command != tc.cmd || len(d.Args) != 1 || d.Args[0] != "mcp" {
				t.Fatalf("desktop = %+v", desktop)
			}
		})
	}
}

func TestMCPSetupError(t *testing.T) {
	s := &AppService{mcp: fakeMCPEnv("linux", "", "", errors.New("no exe"))}
	if got := s.MCPSetup(); got.Command != "" || !strings.Contains(got.Error, "no exe") {
		t.Fatalf("got %+v", got)
	}
}

func TestMCPSetupReal(t *testing.T) {
	got := NewAppService().MCPSetup()
	if got.Command == "" || got.Error != "" || !strings.HasSuffix(got.ClaudeCode, " mcp") {
		t.Fatalf("got %+v", got)
	}
}
