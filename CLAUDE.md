# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run

All commands run from `pgdba-cli/`:

```bash
# Build
go build -o pgdba-cli ./...

# Run (requires a running PostgreSQL)
go run . --host=localhost --user=postgres --password=postgres --dbname=mydb --sslmode=disable --port=5432

# Shortcut via Makefile (from repo root)
make build       # cross-compiles for linux/amd64
make run         # builds + runs against local docker-compose credentials
make docker-up   # starts postgres:13 + pgadmin on localhost:5432 / 8080
make docker-down

# Dependency hygiene
go mod tidy
```

## Tests

```bash
# Unit tests only (no Docker required)
go test ./... -run "^Test[^I]"

# All tests including integration (requires Docker)
go test ./... -v -timeout 120s

# Single test
go test ./util/ -run TestReplicationSlotsModel_PressD_ActivatesConfirm -v
```

Integration tests use `testcontainers-go` to spin up a real PostgreSQL container. The shared setup lives in `util/db_integration_test.go` (`TestMain`), which injects a live `*sql.DB` into `config.Config.DB` before any test runs.

## Architecture

### Shared Data Layer (fetch.go)

All SQL lives in `util/fetch.go`. Every diagnostic screen has a corresponding `Fetch*` function that:
- Takes `ctx context.Context`, `db *sql.DB`, and optional parameters
- Returns a typed struct or slice (e.g. `[]SlowQuery`, `ConnectionsResult`)
- Never does any display formatting

```
util/fetch.go      → Fetch* functions + typed result structs (all SQL here)
util/<screen>.go   → Check* builder calls Fetch*, converts to table.Row (display only)
mcpserver/tools.go → MCP handlers call Fetch*, marshal to JSON
```

**TUI flow:**
```
Check*(initialModel) → Fetch*(ctx, db, params) → []XxxResult → table.Row → tea.Model
```

**MCP flow:**
```
handleXxx(ctx, req) → Fetch*(ctx, db, params) → []XxxResult → json.Marshal → ToolResult
```

When adding a new diagnostic:
1. Add `FetchMyThing(ctx, db, params)` to `util/fetch.go` returning a typed struct.
2. Create `util/my_thing.go` with `CheckMyThing` calling `FetchMyThing`, converting to `table.Row`.
3. Add `handleCheckMyThing` to `mcpserver/tools.go` calling `FetchMyThing`, marshaling to JSON.
4. Register the tool in `mcpserver/server.go` with `s.AddTool(...)`.

### MCP Server Mode

`pgdba-cli --mcp [--mcp-port PORT]` starts an HTTP/SSE MCP server (default port 8811). The DB connection is established with the same flags as TUI mode. Credentials are **never** in the MCP config file.

```bash
# Start the server with DB credentials
pgdba-cli --mcp --url "postgres://user:pass@host/db"

# Claude Code .mcp.json (no credentials)
{"mcpServers": {"pgdba": {"url": "http://localhost:8811/sse"}}}
```

### TUI Model Pattern (Bubbletea)

Every screen is a Go struct that implements `tea.Model` (Init / Update / View). Navigation works by passing an `initialModel func() tea.Model` closure into each screen — pressing `q`/`esc` calls `m.initialModel()` to return to the main menu.

```
main.go          → root menu model + flag parsing + DB init
config/config.go → global AppConfig singleton (DB, Version, Host, User, …)
util/*.go        → one file per screen (SlowQueriesModel, RecordLocksModel, …)
mcpserver/       → MCP server registration (server.go) and tool handlers (tools.go)
```

### Adding a new screen

1. Add `FetchMyScreen(ctx, db, params)` to `util/fetch.go` with typed result struct.
2. Create `util/my_screen.go` with `CheckMyScreen` calling `FetchMyScreen`, building `[]table.Row`.
3. Implement `Init() tea.Cmd`, `Update(tea.Msg) (tea.Model, tea.Cmd)`, `View() string`.
4. Register in `main.go`: add a key binding in the navigator `Update` switch.
5. Add `handleCheckMyScreen` in `mcpserver/tools.go` and register with `s.AddTool` in `mcpserver/server.go`.

### Confirmation dialogs

Use two fields on the model (`confirmXxx bool` + `itemToXxx type`) and check them in `Update`:

```go
case "d":
    if m.confirmDelete { m.confirmDelete = false; return m, nil }
    m.slotToDelete = m.table.SelectedRow()[0]
    m.confirmDelete = true; return m, nil
case "y":
    if m.confirmDelete { /* execute action */ }
case "n":
    if m.confirmDelete { m.confirmDelete = false; return m, nil }
```

`View()` conditionally appends a prompt line when `confirmXxx` is true.

### DB access rules

- Always use parameterized queries (`$1`, `$2`, …) for DML: `config.Config.DB.Exec("SELECT pg_terminate_backend($1)", pid)`.
- `VACUUM` / DDL cannot use `$1`. Use `pq.QuoteIdentifier()` from `github.com/lib/pq` to safely escape identifiers.
- `sql.Open` does not dial the server — call `DB.Ping()` at startup to surface connection errors immediately.

### Key dependencies

| Package | Role |
|---|---|
| `github.com/charmbracelet/bubbletea` | TUI event loop (Model/Update/View) |
| `github.com/charmbracelet/bubbles` | `table` component used on every screen |
| `github.com/charmbracelet/lipgloss` | Styling (faint footer, colored summaries, error box border) |
| `github.com/lib/pq` | PostgreSQL driver + `pq.QuoteIdentifier` |
| `github.com/testcontainers/testcontainers-go` | Real PostgreSQL in integration tests |

### Connection flags & env vars

Flags take priority; env vars are used as defaults:

| Flag | Env var | Default |
|---|---|---|
| `--host` | `PGHOST` | `` |
| `--port` | `PGPORT` | `5432` |
| `--user` | `PGUSER` | `postgres` |
| `--password` | `PGPASSWORD` | `` |
| `--dbname` | `PGDATABASE` | `mydb` |
| `--sslmode` | `PGSSLMODE` | `disable` |

If password is still empty after flag parsing, `~/.pgpass` is consulted (`hostname:port:database:username:password`, wildcards `*` supported).

## Coding Standards

### Language
All code, comments, variable names, constants, and identifiers must be in **English**. No Portuguese or other languages anywhere in the codebase. This rule extends to all repository files including `README.md` — documentation must be written in English.

### Naming
Use descriptive names — single-letter or abbreviated identifiers are not acceptable. Prefer clarity over brevity:
- Style renderer functions: `renderKey`, `renderLabel`, `renderValue` (not `k`, `l`, `v`)
- Boolean helpers: `renderBoolFlag` (not `boolVal`)
- Visual dividers: `divider` (not `sep2`)
- Multi-line footer rows: `shortcutRow1`, `shortcutRow2` (not `line1`, `line2`)

### Comments
Add a comment when the WHY is non-obvious: a hidden constraint, a subtle invariant, a workaround for a specific bug, or behavior that would surprise a reader. Avoid comments that just restate what the code does — well-named identifiers already do that.

### Receivers
All model types use **value receivers** (`func (m MyModel) Method()`). Never use pointer receivers (`func (m *MyModel)`) on Bubbletea models — value semantics are required so that state updates return a new model value rather than mutating in place.

### Clean architecture
- Data fetching lives in `Fetch*` functions in `util/fetch.go` — they run SQL and return typed structs.
- `Check*` / `Identify*` builder functions call `Fetch*`, convert to `table.Row`, and return the model.
- Model state lives in the struct fields; Update returns a new value.
- Rendering lives in `View()` — no DB calls or side effects.
- Detail maps (`queryDetails map[string]string`) are keyed by a unique row identifier (never by row index, which breaks under filter).

### Bubbles table cell count constraint
`renderRow` in Bubbles iterates over row cells and accesses `m.cols[i]` for each cell. The row cell count **must exactly equal** the column count. Extra cells cause a panic. Store out-of-band data (e.g., full query text) in a `map[string]string` keyed by a unique identifier from the row, never as extra hidden cells.
