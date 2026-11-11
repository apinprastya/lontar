# dbedit — TablePlus-style DB client (Go + Wails 3)

## Context
Build a fast, lightweight desktop database client modeled on TablePlus's workflow (connection list → table browser → spreadsheet-like grid with staged edits → SQL editor). Stack decided by user: **Go + Wails 3**. Databases for v1: **PostgreSQL, MySQL/MariaDB, SQLite**. Project dir `/mnt/monday/project/dbedit` is empty (greenfield). Toolchain present: Go 1.26.3, wails3 v3.0.0 (has `react` TS template), Node 24, Docker (for integration tests).

**Target platforms: Linux, Windows, macOS** — all three are first-class; every milestone must work on all three.

Constraints from global rules: never commit to git; new code ≥ 80% test coverage. Original product — no TablePlus name/branding/icons.

## Cross-platform requirements
| Concern | Linux | Windows | macOS |
|---|---|---|---|
| Webview | GTK4 + WebKitGTK 6.0 (`libgtk-4-dev`, `libwebkitgtk-6.0-dev` to build; GTK3/WebKit2GTK 4.1 only via legacy `-tags gtk3`) | WebView2 (bundle Evergreen bootstrapper in installer) | WKWebView (built in) |
| Package | AppImage + `.deb` (+ `.rpm`) | NSIS `.exe` installer (+ MSIX later) | `.app` in `.dmg`, universal (amd64+arm64) |
| Signing | — | Authenticode (optional cert) | Developer ID signing + notarization (needs Apple account) |
| Keychain (`go-keyring`) | Secret Service (gnome-keyring/KWallet) — **fallback** to AES-GCM-encrypted file when no Secret Service is running | Credential Manager | Keychain |
| Config dir (`os.UserConfigDir`) | `~/.config/dbedit` | `%AppData%\dbedit` | `~/Library/Application Support/dbedit` |
| SSH agent | `$SSH_AUTH_SOCK` unix socket | OpenSSH named pipe `\\.\pipe\openssh-ssh-agent` (+ Pageant optional) | `$SSH_AUTH_SOCK` |
| Shortcuts | Ctrl | Ctrl | Cmd — frontend uses a `Mod` abstraction (`navigator.platform`/Wails env) |
| Menus / chrome | Window menu | Window menu | Native app menu (About/Preferences/Quit), inset title bar |

Rules that keep it portable:
- **No CGO anywhere** (pure-Go SQLite via modernc) so Go code builds the same everywhere; only Wails' webview layer is platform-specific.
- File paths always via `filepath` + `os.UserConfigDir`/`os.UserHomeDir`; `~` expansion for SSH key paths handled in `sshtunnel`.
- Platform-specific code isolated behind build tags (`_windows.go`, `_darwin.go`, `_linux.go`) — only `sshtunnel/agent_*.go` and `store/secret_fallback_linux.go` expected.
- Line endings: export writes `\n` by default with a CRLF option; editor normalizes on paste.
- Fonts: system font stack + bundled monospace font (e.g. JetBrains Mono, OFL) for consistent grid/editor rendering.

## Tech choices
| Layer | Choice |
|---|---|
| Shell | Wails 3 (`wails3 init -n dbedit -t react`), services bound via `application.NewService`, TS bindings via `wails3 generate bindings` |
| Postgres | `github.com/jackc/pgx/v5` (via `pgx/v5/stdlib` for `database/sql`) |
| MySQL/MariaDB | `github.com/go-sql-driver/mysql` |
| SQLite | `modernc.org/sqlite` (pure Go → no CGO, easy cross-compile) |
| SSH tunnel | `golang.org/x/crypto/ssh` (password, key file, agent) |
| Secrets | `github.com/zalando/go-keyring` (OS keychain / Secret Service) |
| App config | JSON files in `os.UserConfigDir()/dbedit/` (connections, settings, query history as SQLite) |
| Frontend | React + TypeScript + Vite, Zustand (state), `@glideapps/glide-data-grid` (canvas grid), CodeMirror 6 + `@codemirror/lang-sql` (schema-aware autocomplete), `react-resizable-panels`, `cmdk` (quick-open palette) |
| Tests | `go test` + `testcontainers-go` (postgres, mysql, mariadb modules), Vitest + Testing Library, coverage thresholds 80% |

## Architecture

### Go backend layout
```
main.go                      // wails app, register services, window setup
internal/
  driver/                    // Driver interface + registry
    driver.go                //   type Driver interface { Open, Dialect() }
    dialect.go               //   QuoteIdent, Placeholder(n), LimitOffset, introspection SQL
    postgres/ mysql/ sqlite/ //   one impl each; MySQL impl handles MariaDB quirks via version detect
  conn/                      // session manager: id → *sql.DB + tunnel + current db/schema
  sshtunnel/                 // local listener forwarding to remote host via ssh client
  store/                     // saved connections (JSON) + keyring for passwords; query history
  schema/                    // introspection: databases, schemas, tables/views, columns, PKs, indexes, FKs
  query/                     // run SQL, cancel, result buffering & paging
  sqlsplit/                  // split scripts into statements (quotes, comments, PG $$ dollar quotes, MySQL DELIMITER)
  values/                    // driver value → JSON-safe cell (NULL, int64>2^53 → string, []byte → hex+flag, time → ISO)
  edits/                     // staged change set → INSERT/UPDATE/DELETE keyed by PK, preview SQL, apply in tx
  export/                    // CSV / JSON / SQL INSERT export of result or table
  services/                  // thin Wails-bound facades: ConnectionService, SchemaService, QueryService, EditService, ExportService
```
Services stay thin; logic lives in plain packages so it's unit-testable without Wails.

### Key backend contracts
- **Driver interface** — each DB implements: `ListDatabases`, `ListSchemas`, `ListTables(schema)`, `Columns(table)`, `PrimaryKey`, `Indexes`, `ForeignKeys`, `TableDDL`, plus dialect helpers (`QuoteIdent`, placeholders `$1` vs `?`, `LIMIT/OFFSET`). SQLite: "database" = file, single schema `main`. MySQL: database == schema.
- **Browsing tables**: server-side paging — `FetchTable(sessionID, table, filters, sort, offset, limit=300)` builds a parameterized SELECT (filters UI → WHERE with bound params, never string-concat values; identifiers quoted by dialect). Returns rows + `estimatedCount`.
- **Raw SQL**: `Execute(sessionID, sql) → queryID`; statements split by `sqlsplit`, each run with its own `context.WithCancel`; results buffered in Go up to a cap (default 1000 rows, setting), with `FetchMore(queryID)`. Progress/results pushed via Wails events (`query:result`, `query:error`, `query:done`); `Cancel(queryID)`.
- **Editing (TablePlus-style staging)**: frontend holds a change set (cell updates, inserted rows, deleted rows). `PreviewChanges` returns generated SQL; `CommitChanges` applies all in one transaction, rolls back on any error. Tables without PK → grid read-only (with a banner).
- **Safe mode** per connection: none / confirm writes / read-only (read-only sets `default_transaction_read_only` on PG, `SET SESSION TRANSACTION READ ONLY` on MySQL, `?mode=ro` on SQLite).
- **Connection colors/tags** (local/dev/staging/prod) shown in the window chrome, like TablePlus.

### Frontend layout
```
frontend/src/
  app/            // App shell, routing between Welcome (connection list) and Workspace
  features/
    connections/  // list, create/edit form (per-driver fields, SSH tab, SSL tab), test-connection
    sidebar/      // db/schema switcher, table/view list with search filter
    grid/         // Glide grid wrapper, cell editors per type, NULL/default rendering, row detail panel
    filters/      // filter bar (column / operator / value rows), sort by header click
    structure/    // columns / indexes / FKs view, DDL viewer
    editor/       // CodeMirror SQL tab, run current / run all, result tabs, history
    palette/      // Ctrl+P quick open tables, Ctrl+Shift+P commands
  stores/         // zustand: sessions, tabs, pendingChanges, settings
(frontend/bindings/ — generated wails bindings, Wails default location; do not hand-edit)
```
Workspace: left sidebar | main area with tabs (each tab = table data, structure, or SQL query) | optional right row-detail panel | bottom status bar (pending changes count, row count, query time). Keyboard: Ctrl+S commit, Ctrl+Enter run statement, Ctrl+R refresh, Ctrl+T new SQL tab, Delete mark row deleted, Ctrl+Z undo staged change.

## Milestones
1. **M0 Scaffold** — `wails3 init -t react`, add Go module deps, frontend deps, Taskfile targets `test`, `test:go`, `test:fe`, `cover`; testcontainers helper `internal/testutil` that starts PG 16, MySQL 8, MariaDB 11 once per package. CI workflow file (`.github/workflows/ci.yml`) with matrix `ubuntu-latest` / `windows-latest` / `macos-latest`: build + unit tests on all three; Docker integration tests on Linux only (hosted macOS/Windows runners lack Linux containers). Confirm empty app window launches on all three.
2. **M1 Connections** — `store` (JSON + keyring), `conn` session manager, three drivers' `Open` + ping, connection form + test button, connection list UI.
   *As built:* the `driver.Driver` interface currently has `Kind/DisplayName/DefaultPort/BuildDSN/Open/ServerVersion`. Introspection and dialect methods are added in M2. Drivers are registered explicitly via `internal/driver/all.Registry()` (no `init()` side effects). MariaDB is a separate kind served by the MySQL implementation, and the flavour is detected from `VERSION()`. SQLite uses `file:` URIs with `mode=ro|rw|rwc`. `store.Connection` has optional `SSH`/`SSL` structs (persisted only). `conn` refuses to connect when they are enabled, so it never silently bypasses them, until M6 implements them. connections.json carries `"version": 1`. It also stores a non-secret `hasPassword` flag, so listing connections never touches the keychain. Secret-store selection lives in `store/secret_fallback_linux.go` / `secret_fallback_other.go` (a build-tag constant); `FallbackSecretStore` reads from both stores.
3. **M2 Browse** — `schema` introspection for all 3 drivers, sidebar, table tab with paged grid, `values` encoding, filter bar + sorting.
   *As built:*
   - **Driver contract.** `driver.Driver` now embeds two interfaces from `driver/dialect.go`. `Dialect` holds the pure SQL helpers: `QuoteIdent` (embedded quotes doubled), `Placeholder(n)`, `LimitOffset`, `CaseInsensitiveLike` (`ILIKE` on PG; `LIKE` on MySQL and SQLite, where SQLite folds ASCII only) and `TextExpr` (`CAST(x AS TEXT)` on PG). `Introspector` covers `ListDatabases`, `CurrentDatabase`, `ListSchemas`, `CurrentSchema`, `ListTables`, `Columns` and `PrimaryKey`; each takes ctx and `*sql.DB`, and every value is a bound parameter. Each engine embeds a `driver.*Dialect` value and implements introspection in its own `introspect.go`. There is no separate `schema/` package, because introspection lives with the drivers.
   - **Per-engine introspection.** PG uses `pg_catalog`: `pg_attribute`, so materialized views are covered; `reltuples` as the estimate, with -1 mapped to nil; and templates and `pg_*`/`information_schema` hidden unless `includeSystem`. MySQL/MariaDB use `information_schema`. PK flags come from `KEY_COLUMN_USAGE`, because `COLUMN_KEY='PRI'` also marks NOT NULL UNIQUE columns. `ListSchemas` returns the current database, since database == schema. SQLite uses `pragma_database_list`/`pragma_table_list`/`pragma_table_info` with bound args, and an `INTEGER PRIMARY KEY` column is reported NOT NULL. `Column.PKPosition` gives the key order. Table order follows the server collation, and the UI sorts for display.
   - **Switching databases.** `conn.Manager.UseDatabase(ctx, sessionID, db)` checks the name against `ListDatabases(includeSystem=true)`. It then reopens a pool from the stored connection and secret with only `Database` changed, swaps the immutable `*Session` under the same ID, and closes the old pool. This is used on PG and on MySQL; MySQL reopens rather than issuing `USE`. SQLite returns `ErrUnsupported`. `Session.Database` records the current database.
   - **`values.Cell`.** A tagged union `{"k": null|str|num|bool|bytes|time|json, "v": …, "len"?, "trunc"?}`:
     - NULL has no `v`.
     - `num` is a JSON number, or a string for integers beyond ±2^53, exact decimals and NaN/Inf.
     - `bytes` is lowercase hex: the first 64 KiB, with `len` as the full size and `trunc` set when cut.
     - Text is capped at 1 MiB with `trunc`.
     - Dates are `YYYY-MM-DD`. Zone-less timestamps print without an offset; timestamptz uses RFC 3339.
     - PG arrays, UUID and interval are `str`.
     The conversion uses the wire type (`ColumnType.DatabaseTypeName`) and falls back to the declared type. MySQL `[]byte` becomes text only for textual types that are valid UTF-8.
   - **`query` package.** It provides `BuildSelect`/`BuildCount`, plus `FetchTable` and `CountRows`, which take ctx, `*sql.DB`, an `Engine` (= `Dialect` + `Introspector`) and the request.
     - The table must have introspected columns, otherwise `ErrUnknownTable`. Filter and sort columns must match exactly, otherwise `ErrUnknownColumn`.
     - Pages hold up to `limit+1` rows; the limit defaults to 300 with a maximum of 5000. Without an explicit sort they are ordered by PK, so paging is stable. A partial last page also returns `totalCount`.
     - LIKE operators use `ESCAPE '!'`, so `%`/`_` in values are literal. `in`/`notIn` split on commas, and each item gets its own placeholder.
     - `RawWhere` is the documented escape hatch: user SQL is wrapped in parentheses and ANDed. In the UI it is used instead of the filter rows.
     - `query.Runner` applies a 60 s timeout and keeps a requestID → cancel map (`Cancel`). Cancellation was verified on all four engines.
   - **Services.** `SchemaService` provides `Current`, `ListDatabases`, `UseDatabase`, `ListSchemas`, `ListTables` and `Columns`. `BrowseService` provides `FetchTable`, `CountRows` and `CancelFetch`. Both share the one `conn.Manager` (`app.go` `newBackend`).
   - **Frontend.**
     - `stores/workspace.ts` is a zustand vanilla store created per session and provided through React context. It holds the session, the current db/schema, the table list and the tabs. Per-tab state covers draft and applied filters, sort, offset, rows, loading, errors, count and selection.
     - Single-click opens an italic preview tab that the next single-click replaces. Double-clicking, or any sort, filter or page action, pins it.
     - **Paging uses explicit pages of 300**, like the TablePlus original, not infinite scroll. `CountRows` runs after the first page. The status bar shows the `reltuples`/`TABLE_ROWS` estimate (`~`) until the count arrives.
     - Errors show inline in the tab, with Retry; there are no toasts.
     - The grid (`features/grid/DataGrid.tsx`) wraps Glide read-only and owns all of its own logic: cell mapping, header sort clicks (Shift adds a key), column resize and TSV copy through Glide's `onKeyDown`. Tests mock `DataEditor`.
     - `#portal` lives in `index.html`. The layout uses `react-resizable-panels` v4 (`Group`/`Panel`/`Separator`).
     - When a session is open, App renders only the Workspace, which has its own sidebar, toolbar and status bar.
   - **Shortcuts.** Mod+W, Mod+1..9 (9 = last tab), Mod+R (refresh the tab and list), Mod+F (filter bar), Mod+I or Space in the grid (row detail), Mod+C (copy TSV).
   - **Tests.** `testutil.SeedBrowse` builds a shared fixture with every value type, a view, a PG materialized view, a no-PK table, a composite-PK table and a table named `Odd "Name" Tbl` with columns `order`, `Mixed Case` and `quote"col`. `testutil.CheckIntrospection` is the cross-engine assertion set run by each driver package.
4. **M3 SQL editor** — `sqlsplit`, `query` execute/cancel/fetch-more, CodeMirror with autocomplete fed from `schema`, multiple result tabs, query history.
5. **M4 Editing** — cell editors by type, insert/delete rows, staged change set, preview SQL dialog, commit in tx, safe mode.
6. **M5 Structure** — columns/indexes/FKs views, DDL view (PG via catalog query, MySQL `SHOW CREATE TABLE`, SQLite `sqlite_master`), FK click-through navigation.
7. **M6 Extras** — SSH tunnel, SSL options, export CSV/JSON/SQL, import CSV, quick-open palette, connection colors, dark/light theme.
8. **M7 Packaging** — `wails3 package` per OS, built natively on each OS in the CI matrix: Linux AppImage + `.deb`/`.rpm`; Windows NSIS installer with WebView2 bootstrapper; macOS universal `.dmg` with signing + notarization (when Apple credentials are available). App icon set (`.png`/`.ico`/`.icns`) and auto-update check (GitHub Releases) optional.

## Testing / coverage strategy (≥ 80%)
- Pure packages (`sqlsplit`, `values`, `edits`, `export`, dialect helpers, filter→SQL builder): table-driven unit tests, target 90%+.
- Drivers + `schema` + `query` + `edits` commit: integration tests against real DBs via testcontainers (skip with `-short` if Docker unavailable); SQLite uses temp files.
- `sshtunnel`: test against an in-process `x/crypto/ssh` server.
- `services`: tested with fake session manager.
- Frontend: Vitest + Testing Library for stores (change set logic, undo), filter bar, connection form; bindings mocked. `vitest --coverage` threshold 80.
- Platform-specific files (`agent_windows.go`, `secret_fallback_linux.go`, …) get tests with the same build tags, run by the matching CI runner.
- Gate: `go test ./... -coverprofile=cover.out && go tool cover -func=cover.out` total ≥ 80%, and Vitest coverage thresholds in `vite.config.ts`.

## Verification
1. `task test` (Go + frontend) passes with coverage ≥ 80% reported.
2. `wails3 dev` → create connections to the testcontainer PG/MySQL/MariaDB and a local SQLite file; test-connection succeeds.
3. Open a table with 100k seeded rows: grid scrolls smoothly, paging loads, filter + sort produce correct rows.
4. Edit cells, insert & delete rows → preview SQL is correct → Ctrl+S commits; verify in DB; failing commit rolls back fully.
5. SQL tab: run multi-statement script incl. PG `$$` function body; cancel a `SELECT pg_sleep(30)` / `SELECT SLEEP(30)`.
6. Connect PG through SSH tunnel (dockerised sshd) successfully.
7. `wails3 build` produces working binaries on Linux, Windows and macOS (CI matrix green); smoke-test each: launch, connect to SQLite, save a connection with password (stored in that OS's keychain), reopen app and reconnect.
8. Shortcuts show and work as Cmd on macOS and Ctrl on Windows/Linux; macOS app menu present.
