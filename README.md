# dbedit

A fast, lightweight desktop database client for PostgreSQL, MySQL/MariaDB and
SQLite, built with Go + [Wails 3](https://v3.wails.io) and React/TypeScript.
See [PLAN.md](PLAN.md) for the design and milestones.

## Prerequisites

All platforms:

- Go 1.26+
- Node.js 24+ and npm
- Wails 3 CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0`
- Docker (or Podman with the Docker API socket) — only for the database
  integration tests

Per OS:

| OS | Requirements |
|---|---|
| Linux | C toolchain + GTK4/WebKitGTK 6.0 dev packages. Debian/Ubuntu: `sudo apt install build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev`. (The legacy GTK3 stack, `libgtk-3-dev libwebkit2gtk-4.1-dev`, is used only when building with `-tags gtk3`.) |
| Windows | Microsoft Edge WebView2 runtime (preinstalled on Windows 10 21H2+/11; otherwise install the Evergreen runtime). |
| macOS | Xcode Command Line Tools: `xcode-select --install`. |

Run `wails3 doctor` to check your setup.

## Development

```sh
cd frontend && npm install && cd ..
wails3 dev            # hot-reloading app (Go + Vite)
```

Bindings for Go services live in `frontend/bindings/` and are generated — do
not edit them by hand. Regenerate after changing a bound service:

```sh
wails3 generate bindings -clean=true -ts -i
```

## Tests

Tasks are run with Wails' bundled task runner (`wails3 task <name>`; a
standalone `task` binary works too):

| Task | What it does |
|---|---|
| `wails3 task test` | Go + frontend tests |
| `wails3 task test:go` | `go test ./... -coverprofile=coverage.out`, including container integration tests (needs Docker) |
| `wails3 task test:go:short` | Go unit tests only (`-short`, no Docker) |
| `wails3 task test:fe` | `vitest run --coverage` (fails below 80%) |
| `wails3 task cover` | Prints Go coverage and fails if the total (excluding `main.go`) is below 80% |

Integration tests use `internal/testutil`, which starts PostgreSQL 16, MySQL 8
and MariaDB 11 with testcontainers-go (once per package via `SharedPostgres(t)`
etc. plus `testutil.Main` in `TestMain`) and provides `SQLiteTemp(t)` for
SQLite. They skip automatically with `-short` or when Docker is unreachable.

With rootless Podman, start the API socket first and point `DOCKER_HOST` at it:

```sh
systemctl --user start podman.socket
export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock
```

## Build

```sh
wails3 build          # production binary in bin/
wails3 package        # OS installer/package (see PLAN.md, milestone M7)
```
