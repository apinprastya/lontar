package main

import (
	_ "embed"
	"io/fs"
	"os"
	"path/filepath"

	"dbedit/internal/conn"
	"dbedit/internal/driver/all"
	"dbedit/internal/history"
	"dbedit/internal/query"
	"dbedit/internal/services"
	"dbedit/internal/settings"
	"dbedit/internal/sshtunnel"
	"dbedit/internal/store"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// appName is used for the application name and the main window title. It
// is also the Linux program name (WM_CLASS / Wayland app_id), which must
// match StartupWMClass and the name of build/linux/dbedit.desktop.
const appName = "dbedit"

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
}

// KnownHostsFile is dbedit's own known_hosts in the config directory (the
// only known_hosts file dbedit writes).
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

// newBackend wires the connection store (JSON + OS keychain, with the
// encrypted-file fallback on Linux), the session manager with its SSH
// tunnels (host keys checked against ~/.ssh/known_hosts and dbedit's own
// known_hosts), the driver registry, the query history (history.db) and the
// settings (settings.json), keeping everything under configDir. All
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
	}
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
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Linux: application.LinuxOptions{
			ProgramName: appName,
		},
	}
}

// mainWindowOptions describes the main application window.
func mainWindowOptions() application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Title:     appName,
		Width:     1200,
		Height:    760,
		MinWidth:  800,
		MinHeight: 500,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 38,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(24, 25, 30),
		URL:              "/",
	}
}
