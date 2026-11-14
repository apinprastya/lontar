package main

import (
	_ "embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"lontar/internal/appstate"
	"lontar/internal/conn"
	"lontar/internal/driver/all"
	"lontar/internal/history"
	"lontar/internal/migrate"
	"lontar/internal/query"
	"lontar/internal/services"
	"lontar/internal/settings"
	"lontar/internal/sshtunnel"
	"lontar/internal/store"
	"lontar/internal/update"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// appName is the display name: the application name (menus, About) and the
// main window title.
const appName = "Lontar"

// programName is the lowercase identifier: the binary name and the Linux
// program name (WM_CLASS / Wayland app_id), which must match StartupWMClass
// and the name of build/linux/lontar.desktop. WebKitGTK also keeps the
// webview's data (localStorage) under $XDG_DATA_HOME/<programName>.
const programName = "lontar"

// appIcon is the application icon, rendered from build/appicon.svg. Wails
// uses it for the macOS/Linux message dialogs; the window/taskbar icons come
// from the platform packaging (Windows .syso, macOS .icns, Linux .desktop).
//
//go:embed build/appicon.png
var appIcon []byte

// backend holds the Wails-bound services.
type backend struct {
	connections *services.ConnectionService
	schema      *services.SchemaService
	browse      *services.BrowseService
	editor      *services.EditorService
	history     *services.HistoryService
	edit        *services.EditService
	dialogs     *services.DialogService
	export      *services.ExportService
	importer    *services.ImportService
	settings    *services.SettingsService
	update      *services.UpdateService
	uiState     *services.UIStateService

	// state is state.json: the window bounds (used by main) and UI values.
	state *appstate.Store
}

// KnownHostsFile is Lontar's own known_hosts in the config directory (the
// only known_hosts file Lontar writes).
const KnownHostsFile = "known_hosts"

// userKnownHosts returns ~/.ssh/known_hosts (read only), or nil without a
// home directory.
func userKnownHosts() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".ssh", "known_hosts")}
}

// configDir returns the per-user config directory (<user config
// dir>/lontar), first moving the directory left by the app's former name,
// dbedit, there (see migrate.ConfigDir). It must run before any store opens.
//
// If moving fails, the legacy directory is complete and still in place, so
// this run uses it as is rather than starting empty; the next start retries
// the move. Starting with an empty lontar directory instead would strand the
// old data, because the move never overwrites an existing directory.
func configDir(userConfigDir func() (string, error), opts migrate.Options) (string, error) {
	base, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate config directory: %w", err)
	}
	if _, err := migrate.ConfigDir(base, store.LegacyAppDirName, store.AppDirName, opts); err != nil {
		legacy := filepath.Join(base, store.LegacyAppDirName)
		if fi, serr := os.Stat(legacy); serr == nil && fi.IsDir() {
			logf(opts, "migrate: %v; using %s for this run", err, legacy)
			return legacy, nil
		}
		logf(opts, "migrate: %v", err)
	}
	return filepath.Join(base, store.AppDirName), nil
}

// logf logs through opts.Logf, or the standard logger when it is nil.
func logf(opts migrate.Options, format string, args ...any) {
	if opts.Logf != nil {
		opts.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// newBackend wires the connection store (JSON + OS keychain, with the
// encrypted-file fallback on Linux), the session manager with its SSH
// tunnels (host keys checked against ~/.ssh/known_hosts and Lontar's own
// known_hosts), the driver registry, the query history (history.db) and the
// settings (settings.json) and the app state (state.json), keeping everything under configDir. All
// services share one session manager.
func newBackend(configDir string) backend {
	st := store.New(configDir, store.NewSecretStore(configDir))
	drivers := all.Registry()
	sessions := conn.NewManager(st, drivers)
	hostKeys := &sshtunnel.HostKeys{UserFiles: userKnownHosts(), AppFile: filepath.Join(configDir, KnownHostsFile)}
	sessions.Tunnels = conn.SSHTunnels(sshtunnel.New(sshtunnel.Options{HostKeys: hostKeys}))
	hist := history.New(configDir)
	connections := services.NewConnectionService(st, sessions, drivers)
	connections.HostKeys = hostKeys
	events := WailsEmitter{}
	state := appstate.New(configDir)
	return backend{
		connections: connections,
		schema:      services.NewSchemaService(sessions, drivers),
		browse:      services.NewBrowseService(sessions, drivers, query.NewRunner()),
		editor:      services.NewEditorService(sessions, drivers, hist),
		history:     services.NewHistoryService(hist),
		edit:        services.NewEditService(sessions, drivers),
		dialogs:     services.NewDialogService(WailsDialogs{}),
		export:      services.NewExportService(sessions, drivers, events),
		importer:    services.NewImportService(sessions, drivers, events),
		settings:    services.NewSettingsService(settings.New(configDir)),
		update:      newUpdateService(events),
		uiState:     services.NewUIStateService(state),
		state:       state,
	}
}

// installation describes how the running binary was installed (see
// update.Detect), with the executable's symlinks resolved.
func installation() update.Installation {
	exe, err := os.Executable()
	if err == nil {
		if real, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = real
		}
	}
	return update.Detect(update.Env{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Executable: exe, Getenv: os.Getenv})
}

// newUpdateService wires the on-demand updater (GitHub Releases, verified
// with the compiled-in update.PublicKey), removes what a previous update
// left behind, and lets it restart and open the browser through Wails.
func newUpdateService(events services.Emitter) *services.UpdateService {
	inst := installation()
	inst.Cleanup()
	svc := services.NewUpdateService(&update.Updater{PublicKey: update.PublicKey, Install: inst}, events)
	svc.Start = startDetached
	svc.Quit = quitApp
	svc.Open = openURL
	return svc
}

// appOptions builds the Wails application options. It is kept separate from
// main() so it can be unit tested without starting a webview. Bound
// services implementing ServiceShutdown (ConnectionService closes all open
// sessions) are shut down by Wails when the app quits.
func appOptions(assets fs.FS, b backend) application.Options {
	return application.Options{
		Name:        appName,
		Description: "Fast, lightweight database client for PostgreSQL, MySQL/MariaDB and SQLite",
		Icon:        appIcon,
		Services: []application.Service{
			application.NewService(services.NewAppService()),
			application.NewService(b.connections),
			application.NewService(b.schema),
			application.NewService(b.browse),
			application.NewService(b.editor),
			application.NewService(b.history),
			application.NewService(b.edit),
			application.NewService(b.dialogs),
			application.NewService(b.export),
			application.NewService(b.importer),
			application.NewService(b.settings),
			application.NewService(b.update),
			application.NewService(b.uiState),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Linux: application.LinuxOptions{
			ProgramName: programName,
		},
	}
}
