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
   *As built:*
   - **`internal/sqlsplit`.** One byte scanner, with the dialect picked by `driver.Kind`. It returns `Statement{Text, Start, End, DelimEnd}` byte offsets; `Text` is `script[Start:End]`, with leading comments and the delimiter excluded.
     - It handles `''` and `""` doubling, MySQL backslash escapes (also in `"…"` strings), PG `E'…'`, backticks (MySQL, SQLite), `[…]` (SQLite), `--` (MySQL needs a blank after it), `#` (MySQL), `/* */` (nested on PG), MySQL `/*! */` and `/*+ */` (kept as code), PG `$tag$` quoting, the MySQL client `DELIMITER` line (a custom delimiter also ends a statement mid-word, as in `END$$`), SQLite `CREATE TRIGGER … BEGIN … END` (with `CASE … END` nesting) and PG `BEGIN ATOMIC … END`.
     - Unterminated tokens run to the end; the scanner never fails.
     - `StatementAt(kind, script, cursor)` picks the statement containing the cursor (delimiter included), otherwise the one before it, otherwise the first. `Words` returns the bare keywords, used for classification and transaction tracking.
     - `OffsetMap`/`ByteOffset` convert to and from UTF-16 offsets. **Every offset the frontend sees is UTF-16** (JS string index).
     - Tests: table tests on all dialects plus `FuzzSplit` (seed corpus in normal runs; checks no panic, offsets in range and monotonic, `Text` matches, and UTF-8 is never split). A 90 s local `-fuzz` run found one bad test invariant and no scanner bug.
   - **Pinned connections (`query.Editors`, `editor.go`).**
     - `OpenEditor` creates an editor with no connection. The first run takes a `*sql.Conn` from the session pool and keeps it, so `BEGIN`/`COMMIT`, `SET`, `USE`, `search_path` and temp tables persist across runs.
     - Pinned connections do not starve browsing: the pool's `MaxOpenConns` is raised to `conn.MaxOpenConns + pinned` and lowered again on release.
     - **A pinned conn is never returned to the pool.** When it is released, an open transaction is rolled back (on PG only if `TxStatus != 'I'`; on the other engines a `ROLLBACK` is always sent, which is harmless), and the conn is discarded via `Raw` returning `driver.ErrBadConn`. Closing the conn rolls back server-side as well.
     - Release happens on `CloseEditor`; on disconnect, through a new `conn.Manager.Subscribe` event `SessionClosed` that closes the session's editors; and on a database switch (`DatabaseChanged`), where the editors stay but reconnect, and the next run says so. A **janitor** (every minute) releases conns idle ≥ 30 min and cursors idle ≥ 5 min.
     - A run on an editor whose pool changed underneath also reconnects. Runs on one editor are serialised; a concurrent run gets `ErrEditorBusy`.
   - **Execute.**
     - It splits, then runs the statements in order. Each statement runs under its own context, detached from the call but cancelled with it, so a kept cursor survives the call.
     - **Row vs non-row statements: classified by keyword, not by `QueryContext` for everything.** `database/sql` cannot report rows affected for a `Query`, and pgx/MySQL drop the command tag. So `SELECT/WITH/VALUES/TABLE/SHOW/EXPLAIN/DESC(RIBE)/PRAGMA/CALL/FETCH/EXECUTE/HELP/CHECK/ANALYZE/OPTIMIZE/REPAIR/CHECKSUM/XA`, and anything containing `RETURNING`, run as `QueryContext` and walk `NextResultSet`; this covers MySQL `CALL` with several result sets. Everything else runs as `ExecContext`, and `RowsAffected` is reported for DML keywords only.
     - A wrong guess never runs anything twice. A row statement run as Exec loses its rows; a Query that returns no columns shows "OK".
     - PG statements use `QueryExecModeExec`: an unnamed statement, no statement cache, so there are no "cached plan" errors after DDL.
     - Results: columns (name, lower-cased wire type), `values.Cell` rows capped at `maxRows` (default 1000, max 50,000; one row is read ahead to set `truncated`), duration, and UTF-16 statement offsets plus `BaseOffset`. Statement status is ok/error/cancelled/skipped.
     - The run stops at the first error unless `continueOnError`. A cancel always stops it.
     - **Errors.** PG `PgError` gives SQLSTATE, detail, hint, and position (characters → bytes → UTF-16). MySQL gives the error number and SQLSTATE; the position comes from "near '…' at line N". SQLite gives the result-code name; the position comes from `near "x"` or `no such column/table/function: x`.
   - **Cancel.** It goes through a `query.Runner` with `Timeout: -1` (new: a negative timeout means none, so the editor has no 60 s limit).
     - **PG driver change:** `postgres.OpenConfig` sets pgx's `CancelRequestContextWatcherHandler`, which sends a server cancel request and only closes the conn after 5 s. pgx's default closed the connection. Verified: cancelling `pg_sleep(30)` takes ~0.1 s and the session survives. Inside a transaction, PG then reports `failed`, which is correct server behaviour.
     - On MySQL/MariaDB the driver kills the conn. After any cancel the conn is pinged; if it is dead it is dropped, the cancelled run carries a notice, and the next run re-pins with `sessionReset: true` and a notice. A conn that died while idle (`ErrBadConn`/`ErrConnDone`, nothing sent) is re-pinned and the statement retried once, with a notice.
   - **Load more: option (a), an open cursor.** The rows of a truncated result stay open on the pinned conn, with the read-ahead row held, and `FetchMore(editorID, resultID, n)` continues them.
     - Re-running is unsafe for non-idempotent queries, and raising the cap re-executes.
     - Only the *last* statement's result can stay open, since later statements need the conn, and never a `CALL`'s. Earlier truncated results report `canLoadMore: false`, and the UI says "run again with a higher row limit".
     - The cursor closes on the next run, at exhaustion, on cursor idle (5 min) or when the editor is released.
     - **Closing early:** draining a huge result would read everything. So on PG outside a transaction the rest is cancelled server-side. Inside a transaction (a cancel would abort it), and on MySQL (where a cancel kills the conn) and SQLite (instant), it is drained.
   - **Transaction state.** On PG it is exact: pgx `PgConn().TxStatus()` via `conn.Raw` after every run, with no round trip (`idle`/`active`/`failed`). On MySQL/MariaDB/SQLite it is a **heuristic** (`source: "heuristic"`, shown as "(guess)"):
     - `BEGIN`/`START TRANSACTION` (and SQLite `SAVEPOINT`) open a transaction.
     - `COMMIT`/`END`/`ROLLBACK` (but not `ROLLBACK TO`) close it.
     - On MySQL, DDL/`LOCK`/`GRANT`… commit implicitly (`… TEMPORARY` does not), `SET autocommit=0` makes later statements open one, and error 1213 (deadlock) closes it.
     - `TxState(editorID)` returns the cached value, so it never waits on a running query.
   - **History (`internal/history`).** It lives in `history.db` (modernc SQLite, WAL, created 0600) in the config dir; the dir is injected (`history.New(dir)`, `newBackend(configDir)`).
     - Each `Execute` call with at least one statement is recorded **once per run (the script as run)**, not per statement. The entry holds the connection ID, current database, SQL (capped at 256 KiB, `truncated`), start, duration, rows returned, summed rows affected, and the first error. Never credentials.
     - `List(connectionID, search, limit, offset)` uses a bound `LIKE … ESCAPE '!'`; an empty connection ID lists all. There are also `Delete(id)` and `Clear(connectionID)`.
     - Each connection is pruned to the newest 10,000 after every insert.
   - **Services.** `EditorService` provides `OpenEditor`, `CloseEditor`, `Execute` (also records history), `Cancel`, `FetchMore`, `TxState`, `Split`, `StatementAt` (UTF-16 in and out), `CompletionSchema(sessionID, schema, refresh)` and `CompletionColumns`. `HistoryService` provides `List`, `Delete` and `Clear`. Both are registered in `app.go`. `ServiceShutdown` closes all editors (rolling back) and the history DB.
     - `CompletionSchema` returns the schema names plus the current schema's tables with columns, using a new bulk `Introspector.SchemaColumns(ctx, db, schema)` (one query per engine). Above 2,000 tables it returns names only, and columns come per table on demand.
     - The completion cache is per session+schema. It is invalidated by `refresh`, a database switch and disconnect.
   - **Frontend.**
     - `stores/queryTabs.ts` holds the query-tab state and actions, merged into the workspace store; tabs are now a `TabState | QueryTabState` union. `features/editor/` holds `SqlEditor` (CodeMirror 6), `cm.ts` (dialect mapping, `toSqlNamespace`, lazy-columns source, current-statement and error decorations, run keymap, theme), `QueryTab`, `ResultsPanel`, `Messages`, `HistoryPanel` and `TxIndicator`. `grid/sort.ts` holds `cycleSort` (moved) and client-side `sortRows`.
     - Tabs are named "Query N" (one more than the highest in use) and are renamed by double-click. Table tabs are closed on a database switch, but query tabs stay.
     - **Drafts:** `localStorage["dbedit.queryDrafts.<connectionId>"]` holds `[{title, sql}]`, written on every change inside try/catch, and is restored when the workspace for that connection is created. Results are not persisted.
     - **Autocomplete:** lang-sql's own schema completion is fed a namespace `{schema: {table: {self, children: columns}}, otherSchema: …}` with `defaultSchema`, so plain tables, `schema.table`, `table.col` and alias `a.col` all complete, as do keywords (upper case). Columns of big schemas load when "t." is typed.
     - **Current statement:** `StatementAt` is called from Go, debounced 120 ms after each cursor or text change, and highlighted. Run-current also asks Go, so there is one splitter.
     - **Keys:** CodeMirror's `Prec.highest` keymap handles Mod+Enter, Mod+Shift+Enter and Esc when the editor is focused. The window handler does the same when it is not, and it skips events that are already `defaultPrevented`. Mod+F opens the CodeMirror search panel in a query tab and the filter bar in a table tab. Mod+T opens a new tab.
     - **Results:** one tab per result set, plus Messages, which shows notices, ✓/✗/⊘/–, the first line, rows, duration, and `[code] message` with detail and hint. Clicking a message selects the statement, or puts the cursor on the error position, which gets a wavy mark. A run that has an error opens Messages and jumps to the first error position.
     - Results reuse `DataGrid` with client-side sort and per-result column widths. Row detail and the status bar (rows of the active result, plus the run's total time) work for query results too.
     - **History panel:** it debounces search, reloads after every run, and has delete, and clear with confirmation. Click waits 250 ms so that a double-click (open in a new tab) does not also insert.
   - **Deviations.**
     - Statements are classified by keyword rather than with `QueryContext` for everything (see above).
     - History has one entry per run, not per statement.
     - The MySQL/MariaDB/SQLite tx state is heuristic. `@@in_transaction` (MariaDB) is not used.
     - There is no explicit "close result" call; a cursor closes on the next run or when idle.
     - `CompletionSchema` takes the schema from the UI rather than asking the server.
5. **M4 Editing** — cell editors by type, insert/delete rows, staged change set, preview SQL dialog, commit in tx, safe mode.
   *As built:*
   - **`internal/edits`.** It takes a structured `ChangeSet{schema, table, updates[{pk, set, old}], inserts[{values}], deletes[{pk}]}`. Keys are the original `values.Cell`s from FetchTable. New values are `EditValue{kind: null|default|value|bytes, text, hex}`. The frontend never sends SQL.
     - `LoadTable` introspects the table. It refuses unknown tables and views (checked with `ListTables`). `Build` then validates every column, the full primary key (no missing or extra key columns), truncated cells and duplicate changes to one row, and generates the statements in execution order: deletes, updates, inserts. Identifiers are dialect-quoted; every value is a bound parameter. `NULL` and `DEFAULT` are keywords.
     - **`Update.old` is required** for every `set` column. It is the original cell, and Go refuses `trunc: true` originals and truncated key cells (`ErrTruncated`), so the UI is not the only guard.
     - `Preview` returns *display SQL* with dialect literals, for the dialog only; it is never executed. Literal rules: PG `'…'` with quotes doubled, or `E'…'` with escapes when the text has control characters; MySQL backslash escapes (`\0 \' \\ \n \r \Z`); SQLite `'…'`, or `CAST(X'…' AS TEXT)` when the text contains NUL. Bytes are `'\x…'::bytea` or `X'…'`.
     - `Apply` runs in one `db.BeginTx` on a pooled connection. Each UPDATE/DELETE must affect exactly one row. 0 rows gives a `stale` failure ("changed or deleted since it was loaded"); more than one gives `duplicate`. A database error gives `database`. Any failure rolls everything back and returns a `*ChangeError{op, index, kind, column, message, sql}`. Inserts return the new key where it is cheap: PG `RETURNING <pk>`, MySQL `LastInsertId` (single integer key), SQLite `last_insert_rowid` (INTEGER PRIMARY KEY only).
     - **Stale detection is key-based.** The WHERE uses the primary key only, so a row deleted or re-keyed elsewhere is detected. A concurrent change to a non-key column is last-writer-wins. Comparing old values is unreliable across types (json has no `=`, floats), so it is not done.
   - **Type handling.** PG uses **option (b), pgx inference**. Every text value is bound as a Go `string` with `pgx.QueryExecModeDescribeExec`. The server infers each parameter's type from its context (SET target, VALUES column, WHERE key column), and pgx always sends `string` args in text format, so the column type's own input function parses them like a typed literal. No type name is ever written into SQL. DescribeExec also avoids the statement cache, so a commit never fails with "cached plan" after DDL. Bytes are bound as `[]byte`. This was verified on int, bigint > 2^53, numeric(30,10), bool, date, timestamp(6), timestamptz, json, jsonb, uuid, bytea, text[], enum, interval and DEFAULT.
     - MySQL/MariaDB: integer and bool columns bind `int64`/`uint64`. A string compared with an integer column is compared as a double, which cannot tell 2^53+1 from 2^53; a test covers that neighbour. `true`/`false` become 1/0, date-times get a space separator, DECIMAL keys compare via `CAST(? AS DECIMAL(65,30))` (a fixed type, not user input), and DOUBLE keys bind `float64`. Everything else binds as a string.
     - SQLite: integer and bool columns bind `int64`; keys bind native int, float, text or blob. Text-declared decimals stay exact; NUMERIC-declared ones are stored as REAL by SQLite itself.
     - Key round trip (fetch → key → exactly that row) is tested per engine for big ints, unsigned max, decimals, timestamptz, datetime(6)/timestamp(3), dates, uuid, bytes, text with quotes, booleans and doubles.
   - **`clientFoundRows`.** The MySQL driver config always sets `ClientFoundRows=true`, after Options, so it cannot be turned off. An UPDATE that writes the current value then counts as one matched row, as on PG and SQLite. The SQL editor's "rows affected" for UPDATE on MySQL/MariaDB is now matched rows rather than changed rows, consistent with the other engines; the M2/M3 test suites are unaffected.
   - **Safe mode.** `store.Connection.SafeMode` is `off|confirm|readonly` (default `off`). **connections.json is now version 2.** A v1 file is migrated in memory on load (`safeMode: "off"`) and written as v2 on the next save. Newer versions are still refused.
     - `conn.Session.SafeMode` is fixed at connect time; `UseDatabase` keeps it even if the saved connection changed. `SessionInfo.safeMode` goes to the UI.
     - Read-only is enforced by the database: `Connection.DriverConfig().ReadOnly` is true in readonly mode.
       - PG: runtime parameter `default_transaction_read_only=on`, set after Options.
       - MySQL: session variable `transaction_read_only=1` via DSN params. go-sql-driver runs it as `SET` on every new connection.
       - MariaDB: `tx_read_only=1`, which every MariaDB version has; `transaction_read_only` exists only from 11.1. The choice follows the connection's kind, so a "mysql" connection pointing at MariaDB < 11.1 fails to connect in readonly mode.
       - SQLite: `mode=ro`.
       - Writes are rejected on all four engines (tested). A user could still turn it off from the SQL editor (`SET … = off`); the database enforcement is a safety net, not a security boundary.
     - App level: `edits.Apply` refuses readonly sessions (`FailReadOnly`). `EditorService.Execute` in readonly mode runs everything; the database rejects writes, and a notice explains why.
     - Confirm mode: `Execute` classifies statements with `query.IsWrite`, the leading-keyword classifier. If any statement writes and `ExecOptions.confirmed` is false, nothing runs: it returns `needsConfirm` and `writeStatements`, and the UI re-calls with `confirmed: true`. Grid commits in confirm mode always go through the preview dialog (enforced in the UI).
     - `IsWrite` is conservative: anything that is not a known read is a write, including SET, CALL/DO and DDL. Also writes: `SELECT … INTO`, a `WITH` containing DML, `EXPLAIN ANALYZE` with DML, `START` other than `START TRANSACTION`, and `PRAGMA x = y`. Transaction control and USE are reads. Known limit: a SELECT calling a function with side effects counts as a read.
   - **Enums.** `driver.Column.EnumValues` comes from PG `pg_enum` (in the same Columns query) and MySQL `COLUMN_TYPE` (`mysql.ParseEnum`). `query.ResultColumn` gains `enumValues` and `hasDefault` for table pages.
   - **Services.** `EditService.Preview(sessionID, cs)` and `Apply(sessionID, cs)` are registered in `app.go`. `Apply` returns `ApplyResult{ok, result, failure}`; a refused or rolled-back set is a structured `failure`, not a Go error.
   - **Frontend.**
     - `stores/pending.ts` holds immutable, pure staged changes per table tab: edits keyed by the original PK cells (JSON), inserts with local IDs, and deletes. It has snapshot undo/redo (200 steps), no-op detection (editing back to the original drops the change), "deletes win over edits", `buildChangeSet` with refs back to the staged change for failure highlighting, and `displayRows` (cells plus modified/default/locked/state per row).
     - The workspace store adds `stageCells`, `addRow`, `duplicateRow`, `markDeleted`, `undoEdit`, `redoEdit`, `discardChanges`, `openPreview`, `commit` and `requestCommit` (confirm mode → preview; otherwise commit directly), plus `guarded()`/`resolveGuard()`. Commit, Discard or Cancel is asked before page, sort, filter, refresh, closing the tab, switching database or disconnecting.
     - `DataGrid` takes an optional `editing` prop, so query results stay read-only. It owns its own overlay editor, opened by double-click, Enter or typing, and positioned with `getBounds`. The editor is rendered with `flushSync` and focused in a layout effect. Keys that still reach the grid while it opens (Glide reclaims focus after a click) are forwarded to it through a small handle, so no keystroke is lost.
     - Visual states come from a pure `cellLook`: modified amber, inserted green, deleted red with a strikethrough drawn in `drawCell`, failed row strong red, truncated cells muted with a padlock, DEFAULT italic. The context menu has Edit, Set NULL, Set DEFAULT, Set empty string, Insert, Duplicate and Delete. TSV paste is supported.
     - `features/edits/editors.tsx` has type-aware editors: text (single line, expanding to multi-line for newlines or long text), integer and number with validation, a bool select, date/datetime/time with validation, a JSON textarea with parse validation, an enum select, and hex bytes. Each has NULL and Default buttons. DEFAULT is not offered for SQLite updates, which Go refuses too.
     - The row detail panel edits through the same editors and store, and shows staged values and inserted rows.
     - The status bar shows "N pending changes" with Discard, Preview SQL and Commit. The preview dialog has Copy SQL. A failed commit shows an inline banner, keeps the changes and highlights the failing row.
     - Safe mode UI: a selector in the connection form (choosing tag Production turns `off` into `confirm`, a UI default only); a shield badge with the mode next to the connection name; a read-only banner explaining why (safe mode, view, no primary key); no edit affordances in readonly mode; and a confirm dialog listing the write statements for SQL editor runs.
     - **Shortcuts** (`features/workspace/keymap.ts` registry with a conflict test):
       - Mod+S commit; Mod+Z / Mod+Shift+Z undo/redo staged edits (not in text fields or the SQL editor).
       - Mod+N insert row. Mod+I stays row detail, and Mod+Shift+I was avoided because it opens the webview inspector.
       - In the grid: Mod+Shift+N set NULL, Mod+D duplicate row, Delete/Backspace mark rows for deletion (pressing again unmarks), Enter/typing edit.
   - **Tests.**
     - Go unit tests cover SQL and args per dialect (composite key, awkward identifiers, every EditValue kind), validation errors, key and text conversion tables, literal escaping, store migration v1→v2, DSN params (read-only, `clientFoundRows`), `ParseEnum`, `IsWrite`, and the services (preview/apply, readonly refusal, confirm gating, readonly notice).
     - Integration tests run on PG 16, MySQL 8, MariaDB 11 and SQLite: type round trip (insert → fetch → update with fetched keys → delete), key round trip, composite key plus key-column edit plus awkward names plus preview, stale rollback, unchanged update = 1 row, DB error rollback with index, inserted keys, refusals (no PK, view, unknown, truncated, readonly), read-only sessions rejecting writes, and the injection string stored literally.
     - Vitest covers the pending store, editors, dialogs, grid states/keys/context menu/paste, TableTab wiring, the store commit/preview/guards, the Workspace flows, the confirm-run dialog, the safe-mode form and the shortcut conflicts.
   - **Deviations.**
     - SQLite rowid tables without a declared primary key stay read-only; `rowid` is not exposed.
     - In confirm mode, grid commits require the preview in the UI only; `EditService.Apply` keeps the specified signature without a confirmation token.
     - A safe-mode change applies from the next connect.
     - SQLite timestamp keys: modernc returns DATE/DATETIME text as `time.Time`, so a key stored as `'2024-01-02 03:04:05'` comes back as `…T…` and would not match. Such rows report `stale` instead of being changed wrongly.
6. **M5 Structure** — columns/indexes/FKs views, DDL view (PG via catalog query, MySQL `SHOW CREATE TABLE`, SQLite `sqlite_master`), FK click-through navigation.
   *As built:*
   - **Driver contract.** `driver/structure.go` adds a `Structure` interface, embedded in `Introspector`, with two methods: `TableInfo(ctx, db, schema, table) (*TableDetail, error)` and `TableDDL(ctx, db, schema, table) (string, error)`. Both return `driver.ErrTableNotFound` for unknown tables.
     - `TableDetail` holds the type; the comment; the MySQL engine and collation; `estimatedRows`; `sizeBytes`; detailed `columns`; `indexes`; `foreignKeys`; `referencedBy`; `checks`; `triggers`; and `checksSupported`. Slices are never nil.
     - `ColumnDetail` adds the following to `Column`: `autoIncrement`, `identity` (ALWAYS / BY DEFAULT), `generated` (STORED / VIRTUAL) with `generationExpr`, `onUpdate` (MySQL), `collation` and `comment`.
     - `Index` holds the name; `columns`, where expressions are shown in parentheses and prefix lengths as `name(n)`; `unique`; `primary`; `method`; `predicate`; and `definition`.
     - `ForeignKey` has the source schema, table and columns; the referenced schema, table and columns, in key order; and the referential actions.
     - Helpers: `FindTable`/`LookupTable` (exact-name check against `ListTables`) and `ReferentialAction`.
     - The name is `TableDetail` rather than `TableInfo`, because the frontend already has a `TableInfo` type (the sidebar list item).
   - **PostgreSQL** (`postgres/structure.go`). The relation is looked up once by bound `nspname`/`relname`; every later query binds its OID (`$1::int8::oid`).
     - Columns: `format_type`, `pg_attrdef`, `attidentity`, `attgenerated`, the collation (only when it differs from the type's), `col_description`, the PK position and enum labels. A column counts as serial when its `nextval` default is on a sequence it owns (`pg_depend` with `deptype = 'a'`).
     - Indexes: `pg_index` with `pg_am`. Columns or expressions come from `pg_get_indexdef(oid, n, true)`, the predicate from `pg_get_expr(indpred)`, and the definition from `pg_get_indexdef`. It also records whether a constraint owns the index.
     - FKs come from `pg_constraint` in both directions. Incoming keys skip the clones on partitions (`conparentid = 0`).
     - Checks use `pg_get_expr(conbin)`. Triggers use `pg_get_triggerdef`, with timing and event decoded from `tgtype`.
     - Metadata: `obj_description`, `reltuples` (-1 → nil) and `pg_total_relation_size`.
   - **PG DDL reconstruction** (`postgres/ddl.go`, the pure `buildDDL`, unit-tested with fixture catalog rows). Identifiers are quoted by the server with `quote_ident`, so they are only quoted when needed.
     - Tables become `CREATE [UNLOGGED] TABLE schema.name (...)`. Each column gets its `format_type`, `COLLATE` when it is not the default, then one of `GENERATED ALWAYS AS (…) STORED|VIRTUAL`, `GENERATED ALWAYS|BY DEFAULT AS IDENTITY` or `DEFAULT …`, then `NOT NULL`.
     - **Serial columns are emitted as `smallserial/serial/bigserial` without the `nextval` default**, so the DDL creates its own sequence.
     - Next come the local constraints as `CONSTRAINT name <pg_get_constraintdef>`, in the order PK, UNIQUE, EXCLUDE, CHECK, FK, with FKs inline. Partitioned tables get `PARTITION BY` from `pg_get_partkeydef`.
     - Views become `CREATE VIEW … AS <pg_get_viewdef>`. Materialized views become `CREATE MATERIALIZED VIEW … AS … WITH DATA`.
     - After that come the non-constraint indexes (`pg_get_indexdef`), the triggers (`pg_get_triggerdef`), and `COMMENT ON TABLE/VIEW/MATERIALIZED VIEW/COLUMN`. Comment literals are quoted the way `quote_literal` does it, using `E''` when the text has a backslash.
     - **Limits (not emitted):** ownership; GRANTs; RLS policies; tablespaces; storage parameters; identity sequence options (start, increment); inheritance; partitions, which are rendered as plain tables without `PARTITION OF`; foreign-table servers and options; view options (`security_barrier`, `CHECK OPTION`); index comments; and the definitions of referenced types, sequences and functions.
     - Because FKs are inline, the referenced tables must exist first. Names are schema-qualified wherever PG prints them that way: in the `CREATE` and `COMMENT` lines, and in types, functions and FK targets outside the `search_path`.
     - **Round trip, tested.** `TestDDLRoundTrip` takes the DDL of every fixture relation, swaps `shop.` for `shop_rt.`, executes it in a fresh schema, and asserts that `TableInfo` of each copy equals the original (after normalising the schema name, without rows and size). It also asserts that the copy's DDL is identical.
   - **MySQL/MariaDB** (`mysql/structure.go`). Everything comes from `information_schema` with bound names:
     - Metadata from `TABLES`, columns from `COLUMNS` (`EXTRA` gives auto_increment, VIRTUAL/STORED/PERSISTENT GENERATED and `on update …`), indexes from `STATISTICS`, FKs from `KEY_COLUMN_USAGE` joined with `REFERENTIAL_CONSTRAINTS` in both directions, and triggers from `TRIGGERS`.
     - Feature probes look at `information_schema.COLUMNS` instead of the version: `STATISTICS.EXPRESSION` (MySQL 8.0.13+ functional indexes) and `CHECK_CONSTRAINTS`. On MariaDB that table has `TABLE_NAME`, because check names are per table there. MySQL 8.0.16+ joins `TABLE_CONSTRAINTS`, and older servers report `checksSupported: false`.
     - The index `definition` is synthesised in `SHOW CREATE TABLE` style.
     - `TableDDL` first checks the name with `FindTable`, then quotes it with the dialect and runs `SHOW CREATE TABLE` or `SHOW CREATE VIEW`. Each trigger, named from `information_schema`, follows with `SHOW CREATE TRIGGER`; a body containing `;` is wrapped in `DELIMITER ;;`.
   - **SQLite** (`sqlite/structure.go`). It uses table-valued pragmas with bound arguments: `pragma_table_xinfo` (hidden 2/3 = virtual/stored generated), `pragma_index_list`/`pragma_index_xinfo` (cid -2 = expression, shown as `(expression)`) and `pragma_foreign_key_list`.
     - Incoming FKs come from `pragma_table_list × pragma_foreign_key_list`, matched with `COLLATE NOCASE`. `REFERENCES parent` without columns resolves to the parent's PK, and the parent's name is mapped to its real case.
     - `sqlite_master` supplies the index and trigger SQL. Its schema prefix can't be bound, so the schema is first checked against `pragma_database_list` and then dialect-quoted. Partial-index predicates and trigger timing/event are parsed from that SQL.
     - A single `INTEGER PRIMARY KEY` in a rowid table is auto-increment and NOT NULL.
     - Not reported: checks (`checksSupported: false`; they appear in the DDL), comments, size and estimate.
     - `TableDDL` is the `sqlite_master.sql` of the table or view, then its indexes and triggers.
   - **Services.** `SchemaService.TableInfo` and `SchemaService.TableDDL`; the bindings are regenerated.
   - **Frontend.**
     - Table tabs gain `view: 'data' | 'structure'`, `section`, a cached `structure`, a lazy `ddl` and their loading/error states.
       - `openTable` loads the structure alongside the first page, so the grid knows its foreign keys. The DDL loads the first time the DDL section opens.
       - Mod+R in the structure view reloads the structure and any loaded DDL, without the unsaved-changes guard, since no rows are touched. In the data view it also reloads the structure and drops the cached DDL.
     - The **Data | Structure toggle** is a segmented control at the bottom of every table tab. **Mod+E** (`toggleStructure`, global) switches it. In the structure view, Mod+F switches to data and opens the filter bar, and Mod+N does nothing.
     - `features/structure/StructureView` has a header (qualified name, type, ~rows, size, engine, collation, comment) and sections:
       - Columns: #, name with key and link icons, type, nullable, default, extra, collation, comment.
       - Indexes: including the predicate, unique, method and definition.
       - Foreign keys and Referenced by: the table names are links that open that table's tab, qualified when they are in another schema.
       - Checks (shown as "not reported" on SQLite) and Triggers.
       - DDL: a read-only CodeMirror (`DdlView`: dialect highlighting, line numbers, `EditorState.readOnly`) with **Copy** and **Open in new query tab**.
     - **FK navigation.** `structure/fk.ts`: `fkByColumn` and `referenceFilters(fk, columns, row)` build one `refCol = value` filter per key column, in key order. They return null when any key value is NULL (MATCH SIMPLE), binary, truncated or missing.
       - The grid gets a `links` prop. FK headers show a link icon (`fk`, or `pkfk` for key columns that are also FKs).
       - Followable cells draw a "→" at the right edge and are left-aligned, so values stay clear of it. Clicking the arrow, pressing **Mod+O** (`openReference`, grid scope) or choosing **Open referenced row** from the context menu follows the key. The context menu now exists in read-only grids too, with just that item.
       - `followReference` opens a new pinned tab with the filters applied and shown in the filter bar; no guard is needed. If the target table's tab is already open, it reuses it after `guarded([target])`.
       - The values go through the normal filter model, so they are bound parameters.
       - A referenced table in another schema (PG) opens with its own `schema` and is titled `schema.table` in the tab bar.
     - The **sidebar context menu** on a table offers Open data, Open structure, Copy name and Copy DDL. Copy DDL errors go to the sidebar error line.
   - **Tests.**
     - `testutil.SeedStructure` and `CheckStructure` (shared, like `CheckIntrospection`) run on PG 16, MySQL 8, MariaDB 11 and SQLite. The fixture includes:
       - a composite PK, a composite FK with ON DELETE CASCADE / ON UPDATE SET NULL, and a self-referencing FK
       - a cross-schema FK (PG `shop` → `geo`)
       - unique, partial, expression (PG, SQLite, MySQL 8 functional) and GIN indexes
       - checks, generated columns, identity, serial and auto_increment
       - column and table comments, a non-default collation, an enum, triggers, views and a PG materialized view
       - awkward names: `Weird "Q" Tbl`, `select`, `Mixed Col`, `Line Note`, and a comment with a quote and a backslash
       - injection-shaped names, which are rejected with `ErrTableNotFound`
     - PG adds the DDL round trip. MySQL/MariaDB add a `SHOW CREATE` sanity check: the `order_lines` DDL is re-executed under a new name and gives equivalent columns and FK.
     - The MySQL seed sets `log_bin_trust_function_creators` as root, so the test user can create the trigger.
     - Unit tests cover `buildDDL`, `triggerType`, `literal`, column detail mapping, `applyExtra`, `delimited`, `indexDefinition`, the SQLite trigger and predicate parsing, `FindTable` (exact names only), and the services.
     - Vitest covers the store (lazy loading, errors, refresh, follow in a new tab, composite and cross-schema keys, NULL keys, tab reuse with the guard's cancel and discard), the structure view sections, the DDL copy and open-in-query actions, the toggle and its shortcuts, the grid links (icons, arrow, click, Mod+O, context menu), the TableTab wiring, the sidebar menu and the qualified tab titles. The keymap conflict test still passes.
   - **Deviations.**
     - SQLite reports no check constraints (they stay in the DDL) and no size.
     - Column comments are not shown as grid header tooltips (optional; Glide has no header tooltip hook).
     - When FK navigation reuses an open tab, its filters and page are replaced and it switches to the data view (after the guard when it has staged changes).
     - The MySQL index `definition` is synthesised rather than taken from `SHOW CREATE`.
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
