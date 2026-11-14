package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"lontar/internal/migrate"
	"lontar/internal/sshtunnel"
	"lontar/internal/update"
)

func TestAppOptions(t *testing.T) {
	b := newBackend(t.TempDir())
	opts := appOptions(fstest.MapFS{"index.html": {Data: []byte("<html></html>")}}, b)
	if opts.Name != "Lontar" {
		t.Fatalf("Name = %q, want Lontar", opts.Name)
	}
	if len(opts.Services) != 13 {
		t.Fatalf("expected 13 bound services, got %d", len(opts.Services))
	}
	if opts.Services[11].Instance() != b.update {
		t.Fatal("UpdateService not registered")
	}
	if opts.Services[12].Instance() != b.uiState || b.state == nil {
		t.Fatal("UIStateService not registered")
	}
	for i, inst := range []any{b.dialogs, b.export, b.importer, b.settings} {
		if opts.Services[7+i].Instance() != inst {
			t.Fatalf("service %d not registered", 7+i)
		}
	}
	if opts.Services[1].Instance() != b.connections {
		t.Fatal("ConnectionService not registered")
	}
	if opts.Services[2].Instance() != b.schema || opts.Services[3].Instance() != b.browse {
		t.Fatal("SchemaService/BrowseService not registered")
	}
	if opts.Services[6].Instance() != b.edit {
		t.Error("edit service not bound")
	}
	if opts.Services[4].Instance() != b.editor || opts.Services[5].Instance() != b.history {
		t.Fatal("EditorService/HistoryService not registered")
	}
	if opts.Assets.Handler == nil {
		t.Fatal("asset handler must be set")
	}
	if !opts.Mac.ApplicationShouldTerminateAfterLastWindowClosed {
		t.Fatal("app should quit when the last window closes on macOS")
	}
	if opts.Linux.ProgramName != "lontar" {
		t.Fatalf("Linux.ProgramName = %q, want lontar (StartupWMClass in build/linux/lontar.desktop)", opts.Linux.ProgramName)
	}
	if !bytes.HasPrefix(opts.Icon, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("Icon must be the embedded build/appicon.png")
	}
}

func TestNewBackend(t *testing.T) {
	b := newBackend(t.TempDir())
	if b.schema == nil || b.browse == nil || b.editor == nil || b.history == nil || b.connections.HostKeys == nil {
		t.Fatal("services missing")
	}
	if st := b.settings.Get(); st.Error != "" || st.Settings.GridPageSize != 300 {
		t.Fatalf("settings = %+v", st)
	}
	if len(userKnownHosts()) != 1 {
		t.Fatal("user known_hosts path")
	}
	svc := b.connections
	if got := svc.Drivers(); len(got) != 4 {
		t.Fatalf("Drivers() = %+v", got)
	}
	list, err := svc.List()
	if err != nil || len(list) != 0 {
		t.Fatalf("List() = %+v, %v", list, err)
	}
	if err := svc.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if err := b.editor.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
}

func TestWailsAdaptersWithoutApp(t *testing.T) {
	if _, err := (WailsDialogs{}).OpenFile("t", nil); err == nil {
		t.Fatal("OpenFile without an app")
	}
	if _, err := (WailsDialogs{}).SaveFile("t", "f", nil); err == nil {
		t.Fatal("SaveFile without an app")
	}
	WailsEmitter{}.Emit("x", 1) // no app: dropped, no panic
	quitApp()                    // no app: nothing to quit
	if err := openURL("https://example.com"); err == nil {
		t.Fatal("openURL without an app")
	}
}

func TestStartDetached(t *testing.T) {
	if err := startDetached("go", []string{"version"}); err != nil {
		t.Fatal(err)
	}
	if err := startDetached(filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Fatal("missing program started")
	}
}

func TestInstallation(t *testing.T) {
	// A test binary is never an installed Lontar.
	if in := installation(); in.Kind == update.KindAppImage && os.Getenv("APPIMAGE") == "" {
		t.Fatalf("installation() = %+v", in)
	}
	if newUpdateService(nil) == nil {
		t.Fatal("no update service")
	}
}

func TestConfigDir(t *testing.T) {
	base := t.TempDir()
	quiet := migrate.Options{Logf: func(string, ...any) {}}
	cfg := func() (string, error) { return base, nil }

	// Fresh profile: the new directory (not created yet).
	if d, err := configDir(cfg, quiet); err != nil || d != filepath.Join(base, "lontar") {
		t.Fatalf("configDir = %q, %v", d, err)
	}

	// A dbedit directory is moved before anything opens it.
	old := filepath.Join(base, "dbedit")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "settings.json"), []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := configDir(cfg, quiet)
	if err != nil || d != filepath.Join(base, "lontar") {
		t.Fatalf("configDir = %q, %v", d, err)
	}
	if _, err := os.Stat(filepath.Join(d, "settings.json")); err != nil {
		t.Fatal("settings.json not moved")
	}
	// known_hosts and the other files are resolved inside the new directory.
	b := newBackend(d)
	if hk, ok := b.connections.HostKeys.(*sshtunnel.HostKeys); !ok || hk.AppFile != filepath.Join(base, "lontar", KnownHostsFile) {
		t.Fatalf("known_hosts = %+v", b.connections.HostKeys)
	}
	_ = b.editor.ServiceShutdown()

	if _, err := configDir(func() (string, error) { return "", errors.New("no home") }, quiet); err == nil {
		t.Fatal("expected an error")
	}
}

func TestConfigDirFallsBackToLegacy(t *testing.T) {
	base := t.TempDir()
	cfg := func() (string, error) { return base, nil }
	if err := os.MkdirAll(filepath.Join(base, "dbedit"), 0o700); err != nil {
		t.Fatal(err)
	}
	var logged []string
	opts := migrate.Options{
		Rename: func(string, string) error { return errors.New("rename failed") },
		Logf:   func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) },
	}
	// The copy fallback fails too: base is read-only, so no staging dir.
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	if err := os.Chmod(base, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(base, 0o700) })
	d, err := configDir(cfg, opts)
	if err != nil || d != filepath.Join(base, "dbedit") {
		t.Fatalf("configDir = %q, %v (log %v)", d, err, logged)
	}
	if !strings.Contains(strings.Join(logged, "\n"), "for this run") {
		t.Fatalf("log = %v", logged)
	}

	// No legacy directory to fall back to: the new path, with the error logged.
	logged = nil
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if d, err := configDir(func() (string, error) { return filepath.Join(base, "f", "x"), nil }, opts); err != nil || d != filepath.Join(base, "f", "x", "lontar") {
		t.Fatalf("configDir = %q, %v", d, err)
	}
	if err := os.WriteFile(filepath.Join(base, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := configDir(func() (string, error) { return filepath.Join(base, "f", "x"), nil }, opts); err != nil {
		t.Fatal(err)
	}
	if len(logged) == 0 {
		t.Fatal("migration error not logged")
	}
	// The standard logger is used when Logf is nil.
	logf(migrate.Options{}, "test %d", 1)
}
