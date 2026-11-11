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

The SQL splitter has a fuzz test. Normal runs use only its seed corpus; to fuzz
locally, run `go test -run XXX -fuzz FuzzSplit -fuzztime 60s ./internal/sqlsplit`.

Integration tests share a fixture, `testutil.SeedBrowse`. It holds every
value type, a view, tables with no or a composite primary key, and a table
and columns with awkward names. Run it against each engine (SQLite always;
PostgreSQL, MySQL and MariaDB when Docker is available). The structure tests
add `testutil.SeedStructure`: composite and self-referencing foreign keys, a
cross-schema key (PostgreSQL), partial, expression and GIN indexes, checks,
generated and identity columns, comments, triggers and views. On PostgreSQL,
the reconstructed DDL is executed in a fresh schema and must give the same
structure back.

## Browsing tables

After connecting, the sidebar lists the tables and views of the current
schema. Switch databases (PostgreSQL, MySQL/MariaDB) or schemas
(PostgreSQL, attached SQLite databases) at the top. A single click previews
a table in a reusable tab, and a double click keeps it open. Rows load in
pages of 300. The status bar shows the server's row estimate until the
exact count is known.

| Shortcut | Action |
|---|---|
| Mod+R | Refresh the table list and the current tab |
| Mod+F | Toggle the filter bar (Enter applies) |
| Mod+I / Space | Toggle the row detail panel |
| Mod+C | Copy the selected cells as TSV |
| Mod+W | Close the tab |
| Mod+1 … Mod+9 | Switch tabs (Mod+9 = last) |

Click a column header to sort it (ascending, then descending, then off).
Shift-click adds a secondary sort. Filter values are always sent as bound
parameters. The filter bar's **Raw SQL** mode instead takes a WHERE
condition you type, and uses it verbatim against your database.

## SQL editor

**Mod+T** (or **New query**) opens an SQL tab next to the table tabs. Double-click
a tab to rename it. Each tab has its own database connection, kept from the first
run until the tab is closed. That means `BEGIN`/`COMMIT`, `SET`, `USE`,
`SET search_path` and temporary tables carry over from one run to the next. The
connection is also released when you disconnect or switch databases in the
sidebar, and after 30 minutes without a run. An open transaction is rolled back
whenever the connection is released. The toolbar shows **Transaction open**
while one is active. On MySQL, MariaDB and SQLite this is inferred from the
statements you run, so it is marked "(guess)".

| Shortcut | Action |
|---|---|
| Mod+Enter | Run the selection, or the statement under the cursor (highlighted) |
| Mod+Shift+Enter | Run the whole script |
| Esc | Stop the running query |
| Mod+F | Search in the editor (in a table tab it opens the filter bar) |
| Ctrl+Space / typing | Autocomplete tables, `table.column`, `alias.column` and keywords |

Scripts are split client-side the way each engine's CLI does it: quotes,
comments, PostgreSQL `$$` bodies, the MySQL `DELIMITER` command and SQLite
trigger bodies are all handled. Each statement that returns rows gets a result tab, and
**Messages** lists every statement with its status, row count, time and any
error. Click a message to select that statement, or jump to the error
position. A run stops at the first error unless **Continue on error** is on.
Results show the first *Max rows* rows (1,000 by default, up to 50,000).
**Load more** fetches the next rows of the last statement's result from the
still-open cursor, so the query is never run twice.

Cancelling a PostgreSQL query keeps the tab's session. MySQL and MariaDB close
the connection when a query is cancelled. The next run then reconnects and tells
you that the session state (transaction, temporary tables, variables) is gone.

The text of each tab is saved per connection in the app's local storage and
comes back the next time you open that connection. Query history (every run, with time,
duration, rows and errors, but never passwords) is stored in `history.db`
next to `connections.json`, with the newest 10,000 runs kept per connection. Open
it with **History**: click an entry to insert it, double-click to open it in a
new tab.

## Editing data

Tables with a primary key can be edited in the grid. Views, tables without a
primary key and connections in read-only safe mode are read-only, and a banner
says why. Changes are staged, not written: modified cells are highlighted,
new rows are green, and rows marked for deletion are red and struck through.
The status bar shows "N pending changes" with **Discard**, **Preview SQL** and
**Commit**.

| Shortcut | Action |
|---|---|
| Double-click / Enter / typing | Edit the cell (type-aware editor: numbers, booleans, dates, JSON, enums, hex bytes) |
| Mod+S | Commit the staged changes |
| Mod+Z / Mod+Shift+Z | Undo / redo a staged change |
| Mod+N | Insert a row |
| Mod+D | Duplicate the selected row |
| Mod+Shift+N | Set the selected cells to NULL |
| Delete / Backspace | Mark the selected rows for deletion (again to unmark) |
| Right-click | Edit, Set NULL, Set DEFAULT, Set empty string, Insert, Duplicate, Delete |

Pasting tab-separated text fills a range of cells. Values cut for display
(text over 1 MiB, binary over 64 KiB) show a padlock and cannot be edited.
The row detail panel (Mod+I) edits the selected row too.

A commit runs every change in one transaction: deletes, then updates, then
inserts. Every value is sent as a bound parameter; the UI never sends SQL.
Each update and delete must hit exactly one row, found by its original
primary key. If a row was deleted or re-keyed elsewhere, or anything fails,
nothing is saved. The failing row is highlighted, and your changes stay staged.
**Preview SQL** shows a readable rendering of the statements, with a copy
button. Changing the page, sort or filter, refreshing, closing the tab,
switching database or disconnecting asks you to commit or discard first.

## Table structure

Every table tab has a **Data | Structure** switch at the bottom (**Mod+E**).
The structure view is read-only. Its header shows the table comment, the
estimated row count and the size (plus the engine and collation on
MySQL/MariaDB). It has these sections:

- **Columns**: type, nullable, default, auto-increment/identity/generated, collation, comment
- **Indexes**, including partial and expression indexes
- **Foreign keys** and **Referenced by**: click a table name to open it
- **Checks** (not reported by SQLite) and **Triggers**
- **DDL**: read-only, with **Copy** and **Open in new query tab**

Mod+R reloads the structure.

The DDL comes from `SHOW CREATE TABLE`/`VIEW` on MySQL/MariaDB and from
`sqlite_master` on SQLite. PostgreSQL has no built-in equivalent, so the
statement is rebuilt from the catalog. It covers columns, defaults,
identity, generated columns, serial columns, constraints, indexes, triggers
and comments. It leaves out ownership, grants, policies, storage options and
partitions; see PLAN.md for the full list.

**Foreign keys in the grid.** Foreign key columns have a link icon in their
header, and every non-NULL key cell shows a **→**. To open the referenced
row, click the arrow, press **Mod+O**, or choose **Open referenced row** from
the right-click menu. The referenced table opens in a tab, filtered by
`column = value` for each column of the key. The values are bound
parameters, like any filter. If that table's tab is already open, it is
reused, and you are asked to commit or discard first if it has staged
changes. A table in another schema opens as `schema.table`.

Right-click a table in the sidebar for **Open data**, **Open structure**,
**Copy name** and **Copy DDL**.

## Safe mode

Each connection has a safe mode, set in the connection form:

| Mode | Effect |
|---|---|
| Off | No restrictions |
| Confirm writes | The SQL editor shows the statements that write and asks before running them. Committing grid edits always opens the preview first. Choosing the Production tag suggests this mode. |
| Read-only | The session is read-only in the database itself (PostgreSQL `default_transaction_read_only`, MySQL `transaction_read_only`, MariaDB `tx_read_only`, SQLite `mode=ro`). Grid editing is disabled and the editor's writes are rejected. |

The active mode is shown next to the connection name and applies from the
next connect. "Confirm" decides by each statement's leading keyword and
treats anything not clearly a read as a write, including `SET`. A `SELECT`
that calls a function with side effects is not detected. Read-only mode is a
guard against accidents, not a permission system: use database privileges
for that.

## Saved connections and passwords

Connections are stored as JSON (schema-versioned, written atomically, file
mode `0600` in a `0700` directory) in the per-user config directory:

| OS | Location |
|---|---|
| Linux | `$XDG_CONFIG_HOME/dbedit/connections.json` (default `~/.config/dbedit/`) |
| Windows | `%AppData%\dbedit\connections.json` |
| macOS | `~/Library/Application Support/dbedit/connections.json` |

**Passwords are never written to `connections.json`** and are never sent back
to the UI (it only learns whether one is saved). They are kept in the OS
keychain via [go-keyring](https://github.com/zalando/go-keyring), under the
service `dbedit` with the connection ID as the account:

- macOS: Keychain
- Windows: Credential Manager
- Linux: Secret Service (gnome-keyring, KWallet, KeePassXC, ...)

**Linux fallback.** If no Secret Service is reachable (e.g. a minimal window
manager or a headless session), passwords are stored in `secrets.enc` in the
same directory, encrypted with AES-256-GCM. The key lives next to it in
`secrets.key` (mode `0600`). **This is weaker than an OS keychain:** anyone
who can read your files can decrypt it. It only keeps passwords out of
`connections.json` and out of casual view. Start a Secret Service to avoid
the fallback; passwords saved later go to the keychain, and the stale
fallback copy is then removed. On macOS and Windows there is no fallback, and
keychain errors are reported.

Deleting a connection also deletes its saved password.

## Build

```sh
wails3 build          # production binary in bin/
wails3 package        # OS installer/package (see PLAN.md, milestone M7)
```
