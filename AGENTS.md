# AGENTS.md

Guidance for AI coding agents (and humans) working in this repository.
User-facing behaviour is documented in [README.md](README.md); keep it in sync
with what you change.

## What this is

Lontar is a desktop database client for PostgreSQL, MySQL/MariaDB and SQLite:
Go backend + [Wails 3](https://v3.wails.io) + React/TypeScript frontend. The Go
module is `lontar` (Go 1.26); the frontend lives in `frontend/` (Vite, Vitest,
zustand, CodeMirror 6, Glide Data Grid, cmdk).

## Layout

| Path | Contents |
|---|---|
| `main.go`, `app.go`, `wails.go` | App entry, Wails wiring (services, dialogs) |
| `internal/services/` | Wails-bound services (the only Go API the frontend sees). Keep them thin; logic lives in the packages below |
| `internal/conn/` | Session manager: open/close sessions, pools, TLS. Several sessions can be open at once |
| `internal/sshtunnel/` | Connecting through an SSH bastion, optionally via up to 3 jump hosts (host keys checked on every hop against the user's `known_hosts`, read-only, and Lontar's own) |
| `internal/driver/` (`postgres/`, `mysql/`, `sqlite/`, `all/`) | Per-engine dialect (quoting, placeholders) and introspection (tables, columns, structure, DDL). `objects.go`: the optional `ObjectLister` (functions, procedures, sequences, types, triggers, events for the sidebar); `ObjectDDL` must resolve the UI's `ObjectRef` with `FindObject` before touching SQL |
| `internal/query/` | Table browsing (`browse.go`), the SQL editor runtime (`editor.go`, `execute.go`), statement classification, single-table SELECT parsing (`source.go`) |
| `internal/ddl/` | Structure edits: validates a structured `TableChange` against fresh introspection (fingerprint stale check, `Caps` per engine/version) and builds the DDL per engine (`postgres.go`, `mysql.go`, `sqlite.go`); `Apply` runs it (one transaction on PostgreSQL/SQLite, in order on MySQL/MariaDB) |
| `internal/edits/` | Staged grid edits: validates a structured `ChangeSet` against introspected metadata and applies it in one transaction; `source.go` maps query results onto a table |
| `internal/sqlsplit/` | Client-side script splitting (quotes, comments, `$$`, `DELIMITER`, triggers, psql `COPY … FROM stdin` blocks and meta-commands); `Scanner` streams with a 64 MiB statement limit and `Split` runs on it. `Params` finds the editor's `:name` placeholders, which `query/params.go` binds |
| `internal/export/`, `internal/importer/` | CSV/JSON/SQL export; CSV, JSON and NDJSON import (formats share one `source` pipeline) |
| `internal/sqlfile/` | Run SQL file: streams a script or dump (pg_dump with COPY, mysqldump, sqlite3 .dump) on one dedicated connection, with safe-mode pre-scan, single-transaction / continue-on-error, progress and cancel |
| `internal/textfile/` | The SQL editor's `.sql` files: read (UTF-8, size limit, EOL), atomic save with a changed-on-disk check (`SQLFileService`) |
| `internal/mcpserver/`, `mcp.go` | MCP server for AI assistants (`lontar mcp`, stdio, no window): tools over connections with `AIAccess` read/write. Reads run on a `ConnectReadOnly` session in READ ONLY transactions that are rolled back; writes are DML only and need the user's approval (MCP multi round-trip input request, HMAC-bound to the SQL). Records history with source `mcp` |
| `internal/store/`, `internal/settings/`, `internal/history/`, `internal/migrate/` | Saved connections + secrets, settings, query history, data migration from the old `dbedit` name |
| `internal/appstate/`, `window.go` | `state.json`: main window bounds (tracked in `window.go`, wired in `main.go`) and UI values the frontend keeps via `UIStateService` (`frontend/src/lib/uiState.ts`, `panelLayout.ts`) |
| `internal/update/` | Self-update from GitHub Releases (on demand, or scheduled by `frontend/src/features/update/autoUpdate.ts` when the `autoUpdate` setting is on): ed25519-signed `SHA256SUMS`, per-install replacement (AppImage, exe, .app) |
| `internal/spotlight/` | macOS: writes Spotlight keywords and a Finder comment (binary-plist xattrs) onto the running `Lontar.app` at start-up, so "db"/"sql"/"query" find it |
| `internal/values/` | Cell value model shared by grid, edits and export |
| `internal/testutil/`, `internal/sshtest/`, `internal/tlstest/` | Test helpers (containers, in-process SSH servers that can be chained as jump hosts, certificates) |
| `tools/` | Build helpers (`stamp`, `iconrender`, `calver`, `updatesign`) — a **separate Go module** |
| `website/` | Marketing site (Astro, static): home and download pages. A **separate npm project**; download links come from the GitHub Releases API at runtime (`src/lib/releases.ts`, `src/scripts/download.ts`) with fallback links to the latest release |
| `build/` | Packaging per platform (nfpm, NSIS, plists, icons) |
| `frontend/bindings/` | **Generated** Wails bindings — never edit by hand |
| `frontend/src/features/<area>/` | UI by feature (connections, workspace, grid, editor, edits, structure, objects, palette, …) with their tests. `objects/` is the read-only object tab; `structure/DdlSection.tsx` is the DDL block it shares with the structure view; `structure/StructureEditor.tsx` (dialogs in `editDialogs.tsx`) is the staged structure editor |
| `frontend/src/stores/` | zustand stores: `connections.ts` (saved connections, open sessions), `workspace.ts` (one store per session: table, query and object tabs, sidebar objects, staging, commit), `queryTabs.ts`, `pending.ts` (pure staged-edit model), `structureEdits.ts` (pure structure draft → `TableChange`) and `structureTabs.ts` (structure editing actions) |
| `frontend/src/components/` | Shared components (`icons.tsx`, `ToolButton`, `EngineLogo`, `ConfirmDialog`) |
| `frontend/src/test/` | Test fakes (`mockBindings`, `fakeEditorApi`, structure fixtures) |

## Commands

Tasks run with Wails' bundled runner (`wails3 task <name>`; a standalone `task`
works too).

```sh
cd frontend && npm install && cd ..   # first time
wails3 dev                            # run the app with hot reload
wails3 build                          # version-stamped binary in bin/

wails3 task test                      # everything: Go + frontend + tools/
wails3 task test:go:short             # Go unit tests, no Docker
wails3 task test:go                   # Go incl. container integration tests (Docker/Podman)
wails3 task test:fe                   # vitest run --coverage (fails below 80%)
wails3 task cover                     # Go coverage gate (>= 80%, excluding main.go)

go vet ./...
cd frontend && npx tsc                # typecheck (also part of npm run build)
cd frontend && npx vitest run path/to/file.test.tsx   # one test file

wails3 generate bindings -clean=true -ts -i   # after changing a bound service
```

## Definition of done

A change is finished when all of these hold (CI enforces them):

- `go vet ./...`, `go test ./...` (or `-short` when Docker is unavailable, and
  say so), `npx tsc` and `npx vitest run --coverage` pass.
- New code is covered by tests; Go and frontend coverage stay at or above 80%.
- Bindings are regenerated when a service signature changed, and only the
  expected files under `frontend/bindings/` moved.
- README is updated for any user-visible change (features, shortcuts, limits).

## Rules that must not be broken

- **Grid edits never send SQL.** The frontend sends a structured `ChangeSet`
  (column names, original primary-key cells, typed values). Go checks the
  table and every column against introspected metadata, quotes identifiers
  with the dialect, and binds every value as a parameter. Preview SQL is for
  display only and is never executed. Query-result editing goes through the
  same path (`edits.ResolveSource` decides editability; anything unclear is
  read-only).
- **Structure edits never send DDL.** The frontend sends a `ddl.TableChange`
  (with the structure it was made from). Go re-introspects and refuses a
  stale change, checks every referenced name, quotes identifiers with the
  dialect and literals itself. The only user-written SQL fragments are types
  and expressions (defaults, checks, generated columns, index expressions,
  partial-index predicates), validated as one expression with balanced
  parentheses and no `;` or trailing comment.
- **Identifiers from the UI are validated, not trusted.** Table/column names
  are looked up in metadata before they reach generated SQL (`LookupColumns`,
  `LoadTable`, `FindTable`).
- **Safe mode is enforced in Go**, not only in the UI: `readonly` refuses
  writes, `confirm` returns `NeedsConfirm` until the user confirms.
- **Never edit `frontend/bindings/`**; regenerate it.
- **Secrets** (passwords, key passphrases) go through `internal/store`'s
  keychain/encrypted store, never into settings, logs, history or error
  messages.
- `wails3 task common:update:build-assets` overwrites `build/linux/nfpm/nfpm.yaml`
  and the darwin plists from Wails templates; re-apply Lontar's versions
  afterwards (see README "Build").

## Frontend conventions

- **State**: one workspace store per open session (`createWorkspaceStore`),
  created and disposed by `Workspace.tsx`. Backend adapters (`WorkspaceApi`,
  `EditorApi`, `EditApi`) are injected so tests use fakes instead of bindings.
- **Staged edits** are pure data (`stores/pending.ts`); the store's
  `EditTarget` adapter lets table tabs and editable query results share
  staging, preview, commit and the unsaved-changes guard.
- **Shortcuts**: `features/workspace/keymap.ts` is the single registry; the
  command palette is generated from it and a test fails on conflicts.
- **Styling**: all colours are CSS custom properties in `src/app/App.css` with
  light and dark sets (`data-theme` on `<html>`). Use tokens, not hex values.
  The data grid and SQL editor read the same variables.
- **Icons**: inline 16×16 SVGs in `components/icons.tsx` using `currentColor`
  and 1.4px round strokes. Toolbar actions use `ToolButton` (icon + tooltip +
  accessible name). Database logos come from `simple-icons` via `EngineLogo`.
- **Tests**: Vitest + Testing Library. Query elements by role and accessible
  name. The canvas grid (`@glideapps/glide-data-grid`) is mocked in component
  tests; bindings are mocked with `src/test/mockBindings`.
- The SQL editor's completion is `features/editor/sqlComplete.ts` (pure,
  tokenizer-based, unit-tested on `EditorState`).

## Go conventions

- Integration tests use `internal/testutil`: `SharedPostgres(t)`,
  `SharedMySQL(t)`, `SharedMariaDB(t)` (testcontainers, once per package via
  `testutil.Main` in `TestMain`) and `SQLiteTemp(t)`. They skip with `-short`
  or without Docker. Cover every engine; shared fixtures are
  `testutil.SeedBrowse`, `testutil.SeedStructure` and `testutil.SeedObjects`
  (checked with `CheckObjects`).
- With rootless Podman: `systemctl --user start podman.socket` and
  `export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/podman/podman.sock`.
- Errors meant for the UI are structured (e.g. `edits.ChangeError`,
  host-key errors) so the frontend can react without parsing messages.

## License

Apache-2.0 (see `LICENSE` and `NOTICE`). Only add dependencies with compatible
licenses.
